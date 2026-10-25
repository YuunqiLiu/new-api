#!/usr/bin/env python3
"""Isolated New API integration checks; Python 3.6 stdlib only.

Never uses production credentials or hosts. The only writable database target is
the dedicated new-api-routing-test-db container / routing_test database.
"""
from __future__ import print_function
import argparse
import datetime
import http.client
import ipaddress
import json
import os
import subprocess
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

DB_CONTAINER = 'new-api-routing-test-db'
FIXTURE_IDS = [3, 4, 6, 15, 19, 31]
MODELS = ['glm-5.3', 'glm-5.3-flash', 'deepseek-v4.1-flash', 'deepseek-v4-pro', 'gpt-4o-mini']
RESULTS = []


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def request(base, path, data=None, method=None, headers=None):
    encoded = None if data is None else json.dumps(data).encode('utf-8')
    head = {'Content-Type': 'application/json'}
    head.update(headers or {})
    req = urllib.request.Request(base.rstrip('/') + path, data=encoded, headers=head,
                                 method=method or ('POST' if data is not None else 'GET'))
    try:
        with urllib.request.urlopen(req, timeout=150) as response:
            status = response.status
            try:
                raw = response.read()
            except http.client.IncompleteRead as exc:
                raw = exc.partial
    except urllib.error.HTTPError as exc:
        status, raw = exc.code, exc.read()
    text = raw.decode('utf-8', 'replace')
    try:
        value = json.loads(text)
    except ValueError:
        value = None
    return status, text, value


def sql(statement):
    result = subprocess.check_output([
        'docker', 'exec', '-i', DB_CONTAINER, 'psql', '-X', '-U', 'routing_test',
        '-d', 'routing_test', '-v', 'ON_ERROR_STOP=1', '-t', '-A'],
        input=statement.encode('utf-8'), stderr=subprocess.STDOUT)
    return result.decode('utf-8').strip()


def quote(value):
    return "'" + str(value).replace("'", "''") + "'"


class Validation(object):
    def __init__(self, config):
        self.app = config['app_base'].rstrip('/')
        self.mock = config['mock_base'].rstrip('/')
        self.admin = config['admin_token']
        self.token = config['relay_token'].replace('sk-', '', 1)
        self.user = int(config.get('user_id', 1))
        self.token_id = int(config.get('token_id', 1))
        parsed = urllib.parse.urlparse(self.app)
        check(parsed.hostname in ('127.0.0.1', 'localhost') and parsed.port == 3018,
              'refusing non-validation application address')
        check(ipaddress.ip_address(urllib.parse.urlparse(self.mock).hostname).is_private,
              'mock must use a private numeric address')
        check(sql('SELECT current_database();') == 'routing_test', 'wrong database')

    def admin_call(self, path, data=None, method=None):
        status, raw, value = request(self.app, path, data, method, {
            'Authorization': 'Bearer ' + self.admin, 'New-Api-User': str(self.user)})
        check(status == 200 and isinstance(value, dict) and value.get('success') is True,
              'management API failed: {} HTTP {}'.format(path, status))
        return value

    def control(self, channels=None, reset=True, release=None):
        request(self.mock, '/__control', {'reset': reset, 'channels': channels or {}, 'release': release or []})

    def state(self):
        return request(self.mock, '/__state')[2]

    def relay(self, model='gpt-4o-mini', stream=False, pin=None, tool=False, responses=False):
        key = 'sk-' + self.token + (('-' + str(pin)) if pin else '')
        if responses:
            payload = {'model': model, 'input': 'Return a short validation response.', 'stream': stream, 'max_output_tokens': 64}
            if tool:
                payload['tools'] = [{'type': 'function', 'name': 'get_time', 'description': 'Read time',
                                     'parameters': {'type': 'object', 'properties': {'timezone': {'type': 'string'}}}}]
        else:
            payload = {'model': model, 'messages': [{'role': 'user', 'content': 'Return a short validation response.'}],
                       'stream': stream, 'max_tokens': 64}
            if tool:
                payload['tools'] = [{'type': 'function', 'function': {'name': 'get_time', 'description': 'Read time',
                                     'parameters': {'type': 'object', 'properties': {'timezone': {'type': 'string'}}}}}]
        return request(self.app, '/v1/responses' if responses else '/v1/chat/completions', payload,
                       headers={'Authorization': 'Bearer ' + key})

    def set_status(self, channel, status):
        return self.admin_call('/api/channel/{}/status'.format(channel), {'status': status})

    def channel(self, channel):
        return self.admin_call('/api/channel/{}'.format(channel))['data']

    def setup(self):
        # All fixture credentials are inert mock strings. Advanced Custom
        # settings exercise the real Responses -> Chat converter.
        routes = {'advanced_custom': {'advanced_routes': [
            {'incoming_path': '/v1/chat/completions', 'upstream_path': '/v1/chat/completions',
             'models': MODELS, 'auth': {'type': 'header', 'name': 'Authorization', 'value': 'Bearer {api_key}'}},
            {'incoming_path': '/v1/responses', 'upstream_path': '/v1/chat/completions',
             'converter': 'openai_responses_to_openai_chat_completions', 'models': MODELS,
             'auth': {'type': 'header', 'name': 'Authorization', 'value': 'Bearer {api_key}'}}]}}
        specs = [(3, 58, 100, 3, MODELS[:4]), (19, 58, 100, 3, MODELS[:4]),
                 (15, 1, 100, 4, ['glm-5.3', 'deepseek-v4.1-flash', 'deepseek-v4-pro']),
                 (4, 1, 10, 1, ['deepseek-v4.1-flash', 'deepseek-v4-pro']),
                 (6, 1, 200, 1, ['glm-5.3', 'glm-5.3-flash']), (31, 1, 100, 1, ['gpt-4o-mini'])]
        statements = ['BEGIN;', 'DELETE FROM abilities WHERE channel_id IN (3,4,6,15,19,31);']
        for cid, ctype, priority, weight, models in specs:
            settings = json.dumps(routes) if ctype == 58 else '{}'
            values = [str(cid), str(ctype), quote('mock-key-{}'.format(cid)), '1', '0', quote('validation-{}'.format(cid)),
                      str(weight), quote(self.mock + '/c' + str(cid)), quote(','.join(models)), quote('default'),
                      str(priority), '1', quote('{}'), quote(settings), quote('{}'), quote(models[0]), quote('{}')]
            statements.append('INSERT INTO channels (id,type,key,status,status_version,name,weight,base_url,models,"group",priority,auto_ban,other_info,settings,channel_info,test_model,setting) VALUES (' + ','.join(values) + ') ON CONFLICT (id) DO UPDATE SET type=EXCLUDED.type,key=EXCLUDED.key,status=1,status_version=0,name=EXCLUDED.name,weight=EXCLUDED.weight,base_url=EXCLUDED.base_url,models=EXCLUDED.models,"group"=EXCLUDED."group",priority=EXCLUDED.priority,auto_ban=1,other_info=EXCLUDED.other_info,settings=EXCLUDED.settings,channel_info=EXCLUDED.channel_info,test_model=EXCLUDED.test_model,setting=EXCLUDED.setting;')
            for model in models:
                statements.append('INSERT INTO abilities ("group",model,channel_id,enabled,priority,weight) VALUES (' + ','.join([quote('default'), quote(model), str(cid), 'true', str(priority), str(weight)]) + ');')
        statements.append('COMMIT;')
        sql('\n'.join(statements))
        self.admin_call('/api/channel/fix', {})
        for key, value in {
            'SameChannelRetryTimes': '1', 'CrossChannelRetryTimes': '6', 'RetryTimeBudgetSeconds': '90',
            'AutomaticRetryStatusCodes': '100-199,300-399,401-407,409-599',
            'AutomaticEnableChannelEnabled': 'true', 'AutomaticDisableChannelEnabled': 'false',
            'ModelRatio': json.dumps(dict((m, 1) for m in MODELS)),
            'CompletionRatio': json.dumps(dict((m, 1) for m in MODELS)), 'GroupRatio': '{"default":1}'
        }.items():
            self.admin_call('/api/option/', {'key': key, 'value': value}, 'PUT')

    def text_stream_and_tools(self):
        for stream, tool in [(False, False), (True, False), (False, True), (True, True)]:
            self.control({'31': {'mode': 'tool' if tool else 'ok'}})
            status, raw, value = self.relay(stream=stream, tool=tool, pin=31)
            check(status == 200, 'chat HTTP failure')
            if stream:
                check('[DONE]' in raw, 'chat stream did not finish')
                check('get_time' in raw if tool else ('validation-' in raw and 'ok' in raw), 'stream content missing')
            else:
                message = value['choices'][0]['message']
                check(message.get('tool_calls') if tool else message.get('content') == 'validation-ok', 'chat result mismatch')
            check(len(self.state()['calls']) == 1, 'successful chat unexpectedly replayed')

    def baidu_responses_conversion(self):
        for cid in (3, 19):
            for stream, tool in [(False, False), (True, False), (False, True), (True, True)]:
                self.control({str(cid): {'mode': 'tool' if tool else 'ok'}})
                status, raw, value = self.relay('glm-5.3-flash', stream, cid, tool, True)
                check(status == 200, 'Responses conversion HTTP failure, channel {}'.format(cid))
                check('get_time' in raw if tool else 'validation' in raw, 'Responses output missing')
                check('response.completed' in raw if stream else value.get('object') == 'response', 'Responses did not complete')
                calls = self.state()['calls']
                check(len(calls) == 1 and calls[0]['path'].endswith('/chat/completions'), 'Responses bypassed Chat converter')

    def partial_stream_not_replayed(self):
        for cid in (3, 19, 15, 4):
            self.set_status(cid, 1)
        self.control(dict((str(cid), {'mode': 'partial'}) for cid in (3, 19, 15, 4)))
        status, raw, value = self.relay('deepseek-v4.1-flash', True)
        check('partial-visible' in raw, 'partial output never reached client')
        check(len(self.state()['calls']) == 1, 'partial stream was replayed upstream')
        check(raw.count('partial-visible') == 1, 'partial output duplicated')

    def fallback_and_exclusion(self):
        for cid in (3, 19, 15, 4):
            self.set_status(cid, 1)
        self.control({'3': {'mode': 'quota'}, '19': {'mode': 'quota'}, '15': {'mode': 'quota'}, '4': {'mode': 'ok'}})
        status, raw, value = self.relay('deepseek-v4.1-flash')
        check(status == 200 and value['choices'][0]['message']['content'] == 'validation-ok', 'fallback failed')
        calls = [c['channel'] for c in self.state()['calls']]
        check(len(calls) == 4 and set(calls[:-1]) == set((3, 19, 15)) and calls[-1] == 4,
              'fallback did not try each subscription once before PAYG: {}'.format(calls))

    def peak_fixed_channel_blocked(self):
        now = datetime.datetime.utcnow() + datetime.timedelta(hours=8)
        minute = now.hour * 60 + now.minute
        if not (810 <= minute < 1080):
            return 'SKIP: outside Shanghai 13:30-18:00; boundary covered by Go unit tests'
        self.control()
        self.set_status(6, 1)
        status, raw, value = self.relay('glm-5.3', pin=6)
        check(status != 200, 'peak fixed GLM request was accepted')
        check(not self.state()['calls'], 'peak GLM request reached upstream')
        return 'PASS'

    def accounting_snapshot(self):
        return sql('SELECT quota,used_quota,request_count FROM users WHERE id={}; SELECT remain_quota,used_quota FROM tokens WHERE id={};'.format(self.user, self.token_id))

    def failed_request_refund(self):
        interval = max(1, int(os.getenv('TEST_BATCH_SECONDS', '5')))
        self.set_status(31, 1)
        self.control({'31': {'mode': 'invalid'}})
        time.sleep(interval * 2 + 1)
        before = self.accounting_snapshot()
        status, raw, value = self.relay(pin=31)
        check(status >= 400, 'invalid mock request unexpectedly succeeded')
        time.sleep(interval * 2 + 1)
        check(self.accounting_snapshot() == before, 'failed request did not refund quota or modified usage')
        check(len(self.state()['calls']) == 1, 'fixed invalid request replayed')

    def recovery_manual_disable_race(self):
        previous = sql("SELECT value FROM options WHERE key='AutomaticDisableChannelEnabled';") or 'false'
        self.admin_call('/api/option/', {'key': 'AutomaticDisableChannelEnabled', 'value': 'true'}, 'PUT')
        try:
            self._recovery_manual_disable_race()
        finally:
            self.admin_call('/api/option/', {'key': 'AutomaticDisableChannelEnabled', 'value': previous}, 'PUT')

    def _recovery_manual_disable_race(self):
        self.set_status(31, 1)
        self.control({'31': {'mode': 'quota'}})
        self.relay(pin=31)
        deadline = time.time() + 10
        channel = self.channel(31)
        while channel['status'] != 3 and time.time() < deadline:
            time.sleep(0.1)
            channel = self.channel(31)
        check(channel['status'] == 3, 'quota failure did not create an automatic-disable generation')
        version = channel['status_version']
        self.control({'31': {'mode': 'ok', 'hold': 'cas-probe'}})
        result = {}

        def recover():
            try:
                result['value'] = self.admin_call('/api/channel/31/recovery', {'expected_version': version})
            except Exception as exc:
                result['error'] = type(exc).__name__

        thread = threading.Thread(target=recover)
        thread.daemon = True
        thread.start()
        deadline = time.time() + 15
        try:
            while 'cas-probe' not in self.state()['waiting'] and time.time() < deadline:
                time.sleep(0.1)
            check('cas-probe' in self.state()['waiting'], 'recovery never entered upstream probe')
            self.set_status(31, 2)
        finally:
            self.control(reset=False, release=['cas-probe'])
        thread.join(30)
        check(not thread.is_alive() and 'error' not in result, 'recovery request failed to finish')
        current = self.channel(31)
        check(current['status'] == 2 and current['status_version'] > version,
              'probe overwrote administrator disable')
        check(result['value']['data']['outcome'] == 'stale', 'recovery did not report stale generation')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--credentials', default=os.path.join(os.path.dirname(__file__), 'test-credentials.json'))
    parser.add_argument('--skip-seed', action='store_true')
    parser.add_argument('--only', default='')
    parser.add_argument('--report', default=os.path.join(os.path.dirname(__file__), 'integration-results.json'))
    args = parser.parse_args()
    with open(args.credentials) as source:
        config = json.load(source)
    for env, key in [('APP_BASE', 'app_base'), ('MOCK_BASE', 'mock_base'), ('TEST_TOKEN', 'relay_token'), ('TEST_ADMIN_TOKEN', 'admin_token')]:
        if os.getenv(env):
            config[key] = os.environ[env]
    test = Validation(config)
    if not args.skip_seed:
        test.setup()
    names = ['text_stream_and_tools', 'baidu_responses_conversion', 'partial_stream_not_replayed',
             'fallback_and_exclusion', 'peak_fixed_channel_blocked', 'failed_request_refund', 'recovery_manual_disable_race']
    if args.only:
        names = [name for name in names if name in args.only.split(',')]
    for name in names:
        try:
            outcome = getattr(test, name)() or 'PASS'
        except Exception as exc:
            # Assertions contain fixture state only. Suppress arbitrary upstream
            # response bodies and HTTP authentication headers from output.
            outcome = 'FAIL: ' + (str(exc) if isinstance(exc, AssertionError) else type(exc).__name__)
        RESULTS.append({'test': name, 'result': outcome})
        print(name + ': ' + outcome, flush=True)
    with open(args.report, 'w') as target:
        json.dump(RESULTS, target, indent=2)
    return 1 if any(row['result'].startswith('FAIL') for row in RESULTS) else 0


if __name__ == '__main__':
    raise SystemExit(main())

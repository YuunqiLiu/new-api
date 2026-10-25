#!/usr/bin/env python3
"""Bounded production canaries; dry plan by default, --execute sends requests.

Python 3.6 standard library. Uses a short-lived, model-limited temporary token,
pins every request to an approved subscription channel, and deletes the token in
finally. No GLM Coding or official DeepSeek channel is ever a canary target.
"""
from __future__ import print_function
import argparse
import http.client
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ENV_FILE = '/home/deploy/new_api_channel_scheduler/scheduler.env'
APPLY_REPORT = '/home/deploy/deploy_new_api/backups/routing-20261018/config/apply-config-state.json'
FORBIDDEN_CHANNELS = set((1, 2, 4, 6, 7, 14, 16, 17, 18))


class CanaryError(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        return None


def load_env(path):
    values = {}
    with open(path) as source:
        for line in source:
            line = line.strip()
            if not line or line.startswith('#') or '=' not in line:
                continue
            key, value = line.split('=', 1)
            values[key.strip()] = value.strip().strip('"').strip("'")
    if not values.get('API_TOKEN'):
        raise CanaryError('scheduler credential is unavailable')
    return values


def validate_base(base):
    parsed = urllib.parse.urlparse(base)
    local = parsed.scheme == 'http' and parsed.hostname == '127.0.0.1' and parsed.port == 3019
    public = parsed.scheme == 'https' and parsed.hostname == 'api.nexevic.com' and parsed.port in (None, 443)
    if not (local or public) or parsed.username or parsed.password or parsed.path not in ('', '/') or parsed.query or parsed.fragment:
        raise CanaryError('canary API base must be the reviewed candidate or api.nexevic.com')
    return base.rstrip('/')


class API(object):
    def __init__(self, base, token):
        self.base = validate_base(base)
        self.token = token
        self.user_id = None
        self.opener = urllib.request.build_opener(NoRedirect())

    def management(self, path, data=None, method=None):
        headers = {'Authorization': 'Bearer ' + self.token, 'Content-Type': 'application/json'}
        if self.user_id is not None:
            headers['New-Api-User'] = str(self.user_id)
        payload = None if data is None else json.dumps(data).encode('utf-8')
        request = urllib.request.Request(self.base + path, data=payload, headers=headers,
                                         method=method or ('POST' if data is not None else 'GET'))
        try:
            with self.opener.open(request, timeout=30) as response:
                result = json.loads(response.read().decode('utf-8'))
        except Exception as error:
            raise CanaryError('management request failed: {} ({})'.format(path.split('?')[0], type(error).__name__))
        if not isinstance(result, dict) or result.get('success') is not True:
            raise CanaryError('management request rejected: ' + path.split('?')[0])
        return result.get('data')

    def find_tokens(self, name):
        result = self.management('/api/token/search?' + urllib.parse.urlencode({'keyword': name, 'p': 0, 'page_size': 100}))
        items = result.get('items', []) if isinstance(result, dict) else result
        if not isinstance(items, list):
            raise CanaryError('token search returned an unsupported shape')
        return [row for row in items if row.get('name') == name]


def build_cases(args):
    if args.stage == 'pre':
        cases = [{'channel': 3, 'model': 'deepseek-v4.1-flash', 'stream': True, 'tool': False}]
        if args.with_tools:
            cases.append({'channel': 3, 'model': 'deepseek-v4.1-flash', 'stream': True, 'tool': True})
    elif args.stage == 'qwen':
        cases = [{'channel': 15, 'model': 'qwen3.7-max', 'stream': False, 'tool': False}]
    else:
        baidu2 = args.baidu2_id
        if baidu2 is None:
            with open(args.apply_report) as source:
                report = json.load(source)
            baidu2 = report.get('baidu2_id')
        if not isinstance(baidu2, int) or baidu2 <= 0 or baidu2 in FORBIDDEN_CHANNELS or baidu2 in (3, 15):
            raise CanaryError('new independent Baidu channel ID is not verified')
        # Six short requests cover both GLM variants, both DeepSeek variants and
        # all three subscription channels. This is a minimal canary, not proof
        # of every model/channel combination.
        cases = [
            {'channel': 3, 'model': 'glm-5.3', 'stream': True, 'tool': False},
            {'channel': baidu2, 'model': 'glm-5.3-flash', 'stream': True, 'tool': False},
            {'channel': 15, 'model': 'glm-5.3', 'stream': False, 'tool': False},
            {'channel': 3, 'model': 'deepseek-v4-pro', 'stream': False, 'tool': False},
            {'channel': baidu2, 'model': 'deepseek-v4.1-flash', 'stream': False, 'tool': False},
            {'channel': 15, 'model': 'deepseek-v4.1-flash', 'stream': False, 'tool': False},
        ]
    if any(case['channel'] in FORBIDDEN_CHANNELS for case in cases):
        raise CanaryError('forbidden official/fallback canary target')
    return cases


def make_payload(case):
    payload = {
        'model': case['model'], 'stream': case['stream'],
        'messages': [{'role': 'user', 'content': 'Reply with exactly OK.'}],
        'max_tokens': 64 if case['tool'] or case['model'] == 'deepseek-v4-pro' else 32,
    }
    if case['channel'] == 15 and case['model'] == 'glm-5.3':
        payload['enable_thinking'] = True
        payload['max_tokens'] = 512
    elif case['model'] == 'glm-5.3-flash':
        # This upstream model always reasons and rejects thinking.disabled.
        # Use the verified low-effort setting and allow enough tokens for a
        # short reasoning phase plus the requested visible acknowledgement.
        payload['reasoning_effort'] = 'low'
        payload['max_tokens'] = 512
    elif case['model'].startswith('qwen'):
        payload['enable_thinking'] = False
    else:
        payload['thinking'] = {'type': 'disabled'}
    if case['tool']:
        payload['messages'][0]['content'] = 'Call canary_ack once with ok true. Do not write other text.'
        payload['tools'] = [{'type': 'function', 'function': {
            'name': 'canary_ack', 'description': 'Acknowledge this connection check.',
            'parameters': {'type': 'object', 'properties': {'ok': {'type': 'boolean'}},
                           'required': ['ok'], 'additionalProperties': False}}}]
        payload['tool_choice'] = {'type': 'function', 'function': {'name': 'canary_ack'}}
    return payload


def validate_tool(calls):
    for call in calls:
        function = call.get('function', {})
        if function.get('name') == 'canary_ack':
            try:
                return json.loads(function.get('arguments', '')) == {'ok': True}
            except (ValueError, TypeError):
                return False
    return False


def check_stream(response, wants_tool):
    done, finished, text_seen, error_seen = False, False, False, False
    tools, event_lines = {}, []
    for raw_line in response:
        line = raw_line.decode('utf-8', 'replace').rstrip('\r\n')
        if line.startswith('data:'):
            event_lines.append(line[5:].lstrip())
        if line != '' or not event_lines:
            continue
        data = '\n'.join(event_lines)
        event_lines = []
        if data == '[DONE]':
            done = True
            continue
        try:
            event = json.loads(data)
        except ValueError:
            error_seen = True
            continue
        if not isinstance(event, dict) or event.get('error'):
            error_seen = True
            continue
        for choice in event.get('choices', []):
            delta = choice.get('delta') or {}
            text_seen = text_seen or bool(delta.get('content'))
            finished = finished or choice.get('finish_reason') is not None
            for call in delta.get('tool_calls') or []:
                index = call.get('index', 0)
                accumulated = tools.setdefault(index, {'function': {'name': '', 'arguments': ''}})
                function = call.get('function') or {}
                accumulated['function']['name'] += function.get('name') or ''
                accumulated['function']['arguments'] += function.get('arguments') or ''
    # Do not print generated content or function arguments.
    return done and finished and not error_seen and (validate_tool(list(tools.values())) if wants_tool else text_seen)


def relay(api, relay_key, case):
    result = {'channel': case['channel'], 'model': case['model'], 'http_status': None, 'pass': False}
    key = relay_key[3:] if relay_key.startswith('sk-') else relay_key
    request = urllib.request.Request(api.base + '/v1/chat/completions',
        data=json.dumps(make_payload(case)).encode('utf-8'),
        headers={'Content-Type': 'application/json',
                 'Authorization': 'Bearer sk-' + key + '-' + str(case['channel'])}, method='POST')
    try:
        with api.opener.open(request, timeout=90) as response:
            result['http_status'] = response.status
            if case['stream']:
                result['pass'] = response.status == 200 and check_stream(response, case['tool'])
            else:
                value = json.loads(response.read().decode('utf-8'))
                choices = value.get('choices') or []
                message = choices[0].get('message') if choices else {}
                result['pass'] = response.status == 200 and not value.get('error') and bool(message) and (
                    validate_tool(message.get('tool_calls') or []) if case['tool'] else bool(message.get('content')))
    except urllib.error.HTTPError as error:
        result['http_status'] = error.code
        error.close()  # Upstream error bodies can be sensitive; do not output.
    except Exception:
        pass  # No automatic request retry or parameter fallback.
    return result


def execute(api, args, cases):
    user = api.management('/api/user/self')
    if not isinstance(user, dict) or user.get('role', 0) < 10 or not user.get('id'):
        raise CanaryError('management identity cannot pin canary channels')
    api.user_id = user['id']
    channels = {}
    for case in cases:
        cid = case['channel']
        if cid not in channels:
            channels[cid] = api.management('/api/channel/' + str(cid))
        channel = channels[cid]
        expected_type = 17 if cid == 15 else 58
        if channel.get('status') != 1 or channel.get('type') != expected_type:
            raise CanaryError('canary subscription channel is disabled or has unexpected type: ' + str(cid))
        if case['model'] not in [model.strip() for model in channel.get('models', '').split(',')]:
            raise CanaryError('canary model is not configured on subscription channel: ' + str(cid))
        if cid not in (3, 15) and channel.get('name') != '百度千帆 #2':
            raise CanaryError('new Baidu channel identity does not match reviewed configuration')

    name = 'routing-canary-' + args.stage + '-' + uuid.uuid4().hex[:16]
    token_id = None
    relay_key = None
    created_attempted = False
    results = []
    cleanup_ok = True
    try:
        created_attempted = True
        api.management('/api/token/', {
            'name': name, 'expired_time': int(time.time()) + 900,
            'remain_quota': 50000, 'unlimited_quota': False,
            'model_limits_enabled': True, 'model_limits': ','.join(sorted(set(case['model'] for case in cases))),
            'group': 'default', 'cross_group_retry': False, 'auto_groups': [], 'allow_ips': '',
        })
        found = api.find_tokens(name)
        if len(found) != 1:
            raise CanaryError('temporary canary token could not be uniquely located')
        token_id = int(found[0]['id'])
        key_response = api.management('/api/token/{}/key'.format(token_id), {})
        relay_key = key_response.get('key') if isinstance(key_response, dict) else None
        if not relay_key or '*' in relay_key:
            raise CanaryError('temporary canary key retrieval failed')
        for case in cases:
            result = relay(api, relay_key, case)
            results.append(result)
            print(json.dumps(result, ensure_ascii=False), flush=True)
            if not result['pass']:
                break
    finally:
        relay_key = None
        if created_attempted:
            try:
                # Also resolves a lost create acknowledgement without creating
                # a second token. Cleanup is restricted to this exact UUID name.
                ids = [token_id] if token_id else [int(row['id']) for row in api.find_tokens(name)]
                for ident in ids:
                    api.management('/api/token/' + str(ident), method='DELETE')
                cleanup_ok = not api.find_tokens(name)
            except Exception:
                cleanup_ok = False
            if cleanup_ok:
                print(json.dumps({'temporary_token_deleted': True}), flush=True)
            else:
                # This random name lets the operator remove only this token;
                # it carries no credential material and expires after 15 minutes.
                print(json.dumps({'temporary_token_deleted': False, 'cleanup_token_name': name}), flush=True)
    if not cleanup_ok:
        raise CanaryError('temporary token cleanup needs operator review')
    return len(results) == len(cases) and all(result['pass'] for result in results)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage', required=True, choices=['pre', 'post', 'qwen'])
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--api-base', default='http://127.0.0.1:3019')
    parser.add_argument('--env-file', default=ENV_FILE)
    parser.add_argument('--apply-report', default=APPLY_REPORT)
    parser.add_argument('--baidu2-id', type=int)
    parser.add_argument('--start-index', type=int, default=0,
                        help='Resume at this zero-based case index; earlier cases are not sent again')
    parser.add_argument('--with-tools', action='store_true', help='Add one forced-tool SSE request to the pre canary')
    args = parser.parse_args()
    try:
        validate_base(args.api_base)
        if args.with_tools and args.stage != 'pre':
            raise CanaryError('--with-tools is only valid for the pre stage')
        cases = build_cases(args)
        if args.start_index < 0 or args.start_index >= len(cases):
            raise CanaryError('--start-index is outside the selected stage cases')
        cases = cases[args.start_index:]
        if not args.execute:
            print(json.dumps({'dry_run': True, 'start_index': args.start_index, 'cases': cases, 'token_ttl_seconds': 900,
                              'token_quota_limit': 50000, 'automatic_canary_retries': 0}, ensure_ascii=False, indent=2))
            return 0
        env = load_env(args.env_file)
        return 0 if execute(API(args.api_base, env['API_TOKEN']), args, cases) else 1
    except CanaryError as error:
        print('STOP: ' + str(error), file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        print('STOP: interrupted; check the cleanup result above', file=sys.stderr)
        return 130
    except Exception as error:
        print('STOP: {} (details suppressed)'.format(type(error).__name__), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())

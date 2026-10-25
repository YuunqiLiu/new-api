#!/usr/bin/env python3
"""One held mock SSE through a separately administered loopback Caddy."""
import json
import os
import pathlib
import socket
import subprocess
import threading
import time
import urllib.request

from integration_test import Validation, check, request

ROOT = pathlib.Path(__file__).parent
APP = 'new-api-routing-validation-app'
UNIT = 'new-api-routing-caddy-reload-validation'
PROXY = 'http://127.0.0.1:19180'
ADMIN = '127.0.0.1:19181'
HOLD = 'isolated-caddy-reload-stream'
CONFIG = ROOT / 'Caddyfile.reload-validation'
LOG = ROOT / 'caddy-reload-validation.log'
RESULT = ROOT / 'caddy-reload-results.json'


def write_config(reloaded):
    header = '    header X-Isolated-Reload v2\n' if reloaded else ''
    CONFIG.write_text('{\n    admin 127.0.0.1:19181\n    auto_https off\n    persist_config off\n}\nhttp://127.0.0.1:19180 {\n' + header + '    reverse_proxy 127.0.0.1:3018\n}\n')


def main():
    info = json.loads(subprocess.check_output(['docker', 'inspect', APP]).decode())[0]
    check(set(info['NetworkSettings']['Networks']) == {'new-api-routing-validation'}, 'app is not isolated')
    values = dict(item.split('=', 1) for item in info['Config']['Env'] if '=' in item)
    check('routing_test' in values.get('SQL_DSN', ''), 'app database is not isolated')
    check(not info['State']['Running'], 'test app must initially be stopped')
    for port in (19180, 19181):
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', port))
    check(subprocess.call(['systemctl', 'is-active', '--quiet', UNIT]) != 0, 'test Caddy already running')
    write_config(False)
    stream = None
    v = None
    started_caddy = False
    result = {'passed': False, 'proxy': PROXY, 'admin': ADMIN}
    try:
        subprocess.check_call(['docker', 'start', APP], stdout=subprocess.DEVNULL)
        for _ in range(100):
            try:
                if request('http://127.0.0.1:3018', '/api/status')[0] == 200:
                    break
            except Exception:
                pass
            time.sleep(0.1)
        else:
            raise RuntimeError('isolated app did not become healthy')
        v = Validation(json.loads((ROOT / 'test-credentials.json').read_text()))
        v.set_status(31, 1)
        v.control({'31': {'mode': 'ok', 'hold': HOLD}})
        with LOG.open('w') as log:
            subprocess.check_call(['systemd-run', '--unit=' + UNIT,
                '--property=MemoryMax=128M', '--property=CPUQuota=30%',
                '--setenv=XDG_CONFIG_HOME=' + str(ROOT / 'caddy-test-config'),
                '--setenv=XDG_DATA_HOME=' + str(ROOT / 'caddy-test-data'),
                '/usr/bin/caddy', 'run', '--config', str(CONFIG), '--adapter', 'caddyfile'], stdout=log, stderr=log)
        started_caddy = True
        for _ in range(100):
            try:
                if request(PROXY, '/api/status')[0] == 200:
                    break
            except Exception:
                pass
            time.sleep(0.1)
        else:
            raise RuntimeError('isolated Caddy did not become healthy')
        captured = {}
        def generate():
            try:
                captured['response'] = request(PROXY, '/v1/chat/completions',
                    {'model': 'gpt-4o-mini', 'messages': [{'role': 'user', 'content': 'Isolated reload validation'}], 'stream': True, 'max_tokens': 64},
                    headers={'Authorization': 'Bearer sk-' + v.token + '-31'})
            except Exception as exc:
                captured['error'] = type(exc).__name__
        stream = threading.Thread(target=generate)
        stream.start()
        for _ in range(100):
            if HOLD in v.state()['waiting']:
                break
            time.sleep(0.1)
        else:
            raise RuntimeError('mock did not receive held request through test proxy')
        check(stream.is_alive(), 'held request ended before reload')
        write_config(True)
        with LOG.open('a') as log:
            subprocess.check_call(['/usr/bin/caddy', 'reload', '--config', str(CONFIG),
                '--adapter', 'caddyfile', '--address', ADMIN], stdout=log, stderr=log)
        with urllib.request.urlopen(PROXY + '/api/status', timeout=5) as response:
            check(response.getheader('X-Isolated-Reload') == 'v2', 'new requests did not see reloaded configuration')
        check(stream.is_alive(), 'existing held request ended at reload')
        v.control(reset=False, release=[HOLD])
        stream.join(30)
        check(not stream.is_alive() and 'error' not in captured, 'held SSE failed after reload')
        status, raw, _ = captured['response']
        check(status == 200 and '[DONE]' in raw and 'validation-' in raw, 'SSE completion/content missing after reload')
        check(len(v.state()['calls']) == 1, 'reload replayed the upstream generation')
        result.update({'passed': True, 'http_status': status, 'sse_done': True,
            'upstream_calls': 1, 'new_configuration_seen': True, 'request_held_across_reload': True})
    finally:
        if v is not None:
            v.control(reset=False, release=[HOLD])
        if stream is not None:
            stream.join(30)
        if started_caddy:
            subprocess.check_call(['systemctl', 'stop', UNIT])
        subprocess.check_call(['docker', 'kill', '--signal=TERM', APP], stdout=subprocess.DEVNULL)
        for _ in range(100):
            state = json.loads(subprocess.check_output(['docker', 'inspect', APP]).decode())[0]['State']
            if not state['Running']:
                check(state['ExitCode'] == 0, 'isolated app exit was not clean')
                result['isolated_app_stopped_cleanly'] = True
                break
            time.sleep(0.2)
        else:
            raise RuntimeError('isolated app still draining; do not force-stop')
        result['test_caddy_stopped'] = subprocess.call(['systemctl', 'is-active', '--quiet', UNIT]) != 0
        RESULT.write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()

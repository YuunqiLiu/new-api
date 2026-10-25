#!/usr/bin/env python3
"""Four bounded relay checks, using a short-lived token on Baidu channel #3.

No retry, fallback, credential printing, shell/tool execution, or configuration
changes. A function call is answered with fixed data, never executed locally.
"""
import argparse
import json
import pathlib
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

MODEL = 'deepseek-v4.1-flash'


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, newurl):
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', choices=['http://127.0.0.1:3020', 'https://api.nexevic.com'], required=True)
    parser.add_argument('--env-file', default='/srv/new-api/config/scheduler.env')
    args = parser.parse_args()
    environment = {}
    for line in pathlib.Path(args.env_file).read_text().splitlines():
        if '=' in line and not line.startswith('#'):
            key, value = line.split('=', 1)
            environment[key] = value.strip().strip("'").strip('"')
    opener = urllib.request.build_opener(NoRedirect())
    credential = environment['API_TOKEN']
    token_id, token_name, attempted = None, 'layout-check-' + uuid.uuid4().hex[:16], False
    results = []

    def management(path, body=None, method=None):
        request = urllib.request.Request(args.base + path,
            data=None if body is None else json.dumps(body).encode(),
            headers={'Authorization': 'Bearer ' + credential, 'New-Api-User': '1',
                     'Content-Type': 'application/json'},
            method=method or ('POST' if body is not None else 'GET'))
        with opener.open(request, timeout=15) as response:
            value = json.loads(response.read().decode())
        if value.get('success') is not True:
            raise RuntimeError('Management operation rejected; details withheld')
        return value.get('data')

    def find_token():
        value = management('/api/token/search?' + urllib.parse.urlencode({'keyword': token_name, 'p': 0, 'page_size': 100}))
        items = value.get('items', []) if isinstance(value, dict) else value
        return [item for item in items if item.get('name') == token_name]

    try:
        channel = management('/api/channel/3')
        if channel.get('status') != 1 or channel.get('type') != 58 or MODEL not in channel.get('models', '').split(','):
            raise RuntimeError('Reviewed Baidu subscription route is not available')
        attempted = True
        management('/api/token/', {'name': token_name, 'expired_time': int(time.time()) + 900,
            'remain_quota': 50000, 'unlimited_quota': False, 'group': 'default',
            'model_limits_enabled': True, 'model_limits': MODEL, 'cross_group_retry': False,
            'auto_groups': [], 'allow_ips': ''})
        found = find_token()
        if len(found) != 1:
            raise RuntimeError('Cannot locate unique temporary token')
        token_id = found[0]['id']
        relay_key = management('/api/token/{}/key'.format(token_id), {})['key']
        if not relay_key.startswith('sk-'):
            relay_key = 'sk-' + relay_key

        def relay(path, body, stream=False):
            body['model'] = MODEL
            body['stream'] = stream
            request = urllib.request.Request(args.base + path, data=json.dumps(body).encode(),
                headers={'Authorization': 'Bearer ' + relay_key + '-3', 'Content-Type': 'application/json'}, method='POST')
            with opener.open(request, timeout=90) as response:
                if response.status != 200:
                    raise RuntimeError('Relay did not return HTTP 200')
                if not stream:
                    value = json.loads(response.read().decode())
                    if value.get('error'):
                        raise RuntimeError('Relay error; details withheld')
                    return value
                completed, text, data_lines = False, '', []
                for raw in response:
                    line = raw.decode('utf-8', 'replace').rstrip('\r\n')
                    if line.startswith('data:'):
                        data_lines.append(line[5:].lstrip())
                    if line or not data_lines:
                        continue
                    data = '\n'.join(data_lines)
                    data_lines = []
                    if data == '[DONE]':
                        continue
                    item = json.loads(data)
                    if item.get('type') in ('error', 'response.failed') or item.get('error'):
                        raise RuntimeError('Responses SSE failed; details withheld')
                    if item.get('type') == 'response.output_text.delta':
                        text += item.get('delta', '')
                    if item.get('type') == 'response.completed':
                        completed = item.get('response', {}).get('status') == 'completed'
                if not completed or not text.strip():
                    raise RuntimeError('Responses SSE completion/text missing')
                return {'completed': completed}

        value = relay('/v1/chat/completions', {'messages': [{'role': 'user', 'content': 'Reply exactly OK.'}],
            'thinking': {'type': 'disabled'}, 'max_tokens': 128})
        if not value.get('choices', [{}])[0].get('message', {}).get('content'):
            raise RuntimeError('Chat response text missing')
        results.append({'check': 'chat', 'http': 200, 'passed': True})
        print(json.dumps(results[-1]), flush=True)
        relay('/v1/responses', {'input': [{'role': 'developer', 'content': 'Follow the user instruction.'},
            {'role': 'user', 'content': 'Reply exactly OK.'}], 'max_output_tokens': 512}, stream=True)
        results.append({'check': 'responses_sse_developer_role', 'http': 200, 'passed': True})
        print(json.dumps(results[-1]), flush=True)
        tool = {'type': 'function', 'name': 'layout_ack', 'description': 'Acknowledge the migration check.',
                'parameters': {'type': 'object', 'properties': {'ok': {'type': 'boolean'}},
                               'required': ['ok'], 'additionalProperties': False}}
        history = [{'role': 'user', 'content': 'Call layout_ack once with ok true.'}]
        first = relay('/v1/responses', {'input': history, 'tools': [tool],
            'tool_choice': {'type': 'function', 'name': 'layout_ack'}, 'max_output_tokens': 512})
        calls = [item for item in first.get('output', []) if item.get('type') == 'function_call']
        if len(calls) != 1 or calls[0].get('name') != 'layout_ack' or not calls[0].get('call_id'):
            raise RuntimeError('Expected function call missing')
        if json.loads(calls[0].get('arguments', '{}')) != {'ok': True}:
            raise RuntimeError('Function arguments incorrect')
        results.append({'check': 'responses_function_call', 'http': 200, 'passed': True})
        print(json.dumps(results[-1]), flush=True)
        history += first['output'] + [{'type': 'function_call_output', 'call_id': calls[0]['call_id'], 'output': '{"ok":true}'},
                                     {'role': 'user', 'content': 'The tool succeeded. Reply exactly OK without calling tools.'}]
        second = relay('/v1/responses', {'input': history, 'tools': [tool], 'tool_choice': 'none', 'max_output_tokens': 512})
        text = ''.join(content.get('text', '') for item in second.get('output', [])
                       if item.get('type') == 'message' for content in item.get('content', [])
                       if content.get('type') == 'output_text')
        if not text.strip() or second.get('status') != 'completed':
            raise RuntimeError('Tool output roundtrip did not complete')
        results.append({'check': 'responses_call_id_roundtrip', 'http': 200, 'passed': True})
        print(json.dumps(results[-1]), flush=True)
    finally:
        if attempted:
            ids = [token_id] if token_id else [row['id'] for row in find_token()]
            for ident in ids:
                management('/api/token/' + str(ident), method='DELETE')
            if find_token():
                raise RuntimeError('Temporary token cleanup failed; token expires after 15 minutes')
            print(json.dumps({'temporary_token_deleted': True}), flush=True)
    if len(results) != 4:
        raise RuntimeError('Incomplete canary')


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('Canary stopped: ' + type(error).__name__ + ' (details withheld)', file=sys.stderr)
        sys.exit(1)

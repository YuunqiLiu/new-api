#!/usr/bin/env python3
"""Python 3.6 stdlib mock; bind only to the isolated validation network."""
from __future__ import print_function
import argparse
import json
import re
import socket
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from socketserver import ThreadingMixIn

LOCK = threading.Lock()
CONFIG = {}
CALLS = []
HOLDS = {}
WAITING = set()


class Server(ThreadingMixIn, HTTPServer):
    daemon_threads = True
    allow_reuse_address = True


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *args):
        pass

    def json_response(self, status, value):
        payload = json.dumps(value, separators=(',', ':')).encode('utf-8')
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)
        self.wfile.flush()

    def do_GET(self):
        if self.path == '/__state':
            with LOCK:
                result = {'calls': list(CALLS), 'waiting': sorted(WAITING)}
            self.json_response(200, result)
        elif self.path.endswith('/models'):
            self.json_response(200, {'object': 'list', 'data': [
                {'id': m, 'object': 'model', 'owned_by': 'validation'}
                for m in ['glm-5.3', 'glm-5.3-flash', 'deepseek-v4.1-flash', 'deepseek-v4-pro', 'gpt-4o-mini']]})
        else:
            self.json_response(404, {'error': 'not found'})

    def do_POST(self):
        try:
            size = int(self.headers.get('Content-Length', '0'))
            body = json.loads(self.rfile.read(size).decode('utf-8')) if size else {}
            if self.path == '/__control':
                with LOCK:
                    if body.get('reset'):
                        for event in HOLDS.values():
                            event.set()
                        CONFIG.clear()
                        CALLS[:] = []
                        HOLDS.clear()
                        WAITING.clear()
                    CONFIG.update(body.get('channels', {}))
                    for name in body.get('release', []):
                        HOLDS.setdefault(name, threading.Event()).set()
                self.json_response(200, {'ok': True})
                return
            match = re.match(r'^/c([0-9]+)/', self.path)
            if not match:
                self.json_response(404, {'error': 'channel prefix required'})
                return
            channel = int(match.group(1))
            with LOCK:
                config = dict(CONFIG.get(str(channel), {}))
                call = {'channel': channel, 'path': self.path, 'stream': bool(body.get('stream')),
                        'model': body.get('model'), 'mode': config.get('mode', 'ok')}
                CALLS.append(call)
                hold = config.get('hold')
                if hold:
                    WAITING.add(hold)
                    event = HOLDS.setdefault(hold, threading.Event())
            if hold:
                event.wait(120)
                with LOCK:
                    WAITING.discard(hold)
            if config.get('delay'):
                time.sleep(min(60, float(config['delay'])))
            mode = config.get('mode', 'ok')
            failures = {
                'quota': (429, 'insufficient_quota', 'subscription quota exhausted'),
                'rate_limit': (429, 'rate_limit_exceeded', 'requests per minute exceeded'),
                'error': (502, 'server_error', 'temporary upstream failure'),
                'invalid': (400, 'invalid_request_error', 'validation rejected request'),
            }
            if mode in failures:
                status, code, message = failures[mode]
                self.json_response(status, {'error': {'code': code, 'type': code, 'message': message}})
                return
            model = body.get('model', 'gpt-4o-mini')
            tool = mode == 'tool'
            if body.get('stream'):
                self.stream_response(model, tool, mode == 'partial')
                return
            message = {'role': 'assistant', 'content': 'validation-ok'}
            finish = 'stop'
            if tool:
                message = {'role': 'assistant', 'content': None, 'tool_calls': [{
                    'id': 'call_validation', 'type': 'function',
                    'function': {'name': 'get_time', 'arguments': '{"timezone":"UTC"}'}}]}
                finish = 'tool_calls'
            self.json_response(200, {'id': 'chatcmpl-validation', 'object': 'chat.completion',
                'created': int(time.time()), 'model': model,
                'choices': [{'index': 0, 'message': message, 'finish_reason': finish}],
                'usage': {'prompt_tokens': 5, 'completion_tokens': 7, 'total_tokens': 12}})
        except (BrokenPipeError, ConnectionResetError, socket.error):
            self.close_connection = True
        except Exception:
            # Do not log request bodies, headers, or credentials.
            self.close_connection = True
            try:
                self.json_response(500, {'error': {'type': 'mock_error', 'message': 'mock request failed'}})
            except Exception:
                pass

    def stream_response(self, model, tool, partial):
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Cache-Control', 'no-cache')
        self.send_header('Connection', 'close')
        self.end_headers()
        self.close_connection = True

        def emit(delta, finish=None, usage=None):
            chunk = {'id': 'chatcmpl-validation', 'object': 'chat.completion.chunk',
                     'created': int(time.time()), 'model': model,
                     'choices': [{'index': 0, 'delta': delta, 'finish_reason': finish}]}
            if usage:
                chunk['usage'] = usage
            self.wfile.write(('data: ' + json.dumps(chunk) + '\n\n').encode('utf-8'))
            self.wfile.flush()

        emit({'role': 'assistant'})
        if partial:
            emit({'content': 'partial-visible'})
            # Give the gateway a chance to flush the first content before the
            # upstream emits a real error; this is not a throughput test.
            time.sleep(0.3)
            self.wfile.write(b'data: {"error":{"message":"stream interrupted","type":"server_error","code":"stream_error"}}\n\n')
            self.wfile.flush()
            return
        if tool:
            emit({'tool_calls': [{'index': 0, 'id': 'call_validation', 'type': 'function',
                                 'function': {'name': 'get_time', 'arguments': ''}}]})
            emit({'tool_calls': [{'index': 0, 'function': {'arguments': '{"timezone":"UTC"}'}}]})
        else:
            emit({'content': 'validation-'})
            emit({'content': 'ok'})
        emit({}, 'tool_calls' if tool else 'stop', {'prompt_tokens': 5, 'completion_tokens': 7, 'total_tokens': 12})
        self.wfile.write(b'data: [DONE]\n\n')
        self.wfile.flush()


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--host', default='127.0.0.1')
    parser.add_argument('--port', type=int, default=19081)
    args = parser.parse_args()
    print('Mock upstream ready on isolated listener', flush=True)
    Server((args.host, args.port), Handler).serve_forever()

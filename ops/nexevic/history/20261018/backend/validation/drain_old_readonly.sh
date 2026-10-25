#!/bin/sh
# Read-only evidence collection. Never signals/stops containers or changes DB,
# scheduler, ingress, channel status or application configuration.
set -eu
python3 - <<'PY'
from __future__ import print_function
import collections
import datetime
import json
import os
import pathlib
import re
import subprocess
import time

CONTAINER = 'new-api'
POSTGRES = 'postgres'
PORT = 3000


def run(args, timeout=15):
    return subprocess.check_output(args, stderr=subprocess.STDOUT, timeout=timeout).decode('utf-8', 'replace')


def db(statement):
    # Environment expansion occurs only inside the DB container; values are
    # never included in command arguments, returned records or stdout.
    command = ['docker', 'exec', '-i', POSTGRES, 'sh', '-c',
               'exec psql -X -q -U "$POSTGRES_USER" -d "${POSTGRES_DB:-$POSTGRES_USER}" -v ON_ERROR_STOP=1 -t -A']
    wrapped = "BEGIN READ ONLY; SET LOCAL statement_timeout='5s'; SET LOCAL lock_timeout='1s';\n" + statement + '\nROLLBACK;'
    process = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    output, error = process.communicate(wrapped.encode('utf-8'), timeout=12)
    if process.returncode:
        return {'available': False, 'reason': 'read-only query failed; raw database error withheld'}
    rows = []
    for line in output.decode('utf-8', 'replace').splitlines():
        if line.strip():
            try:
                rows.append(json.loads(line))
            except ValueError:
                pass
    return {'available': True, 'rows': rows}


def connection_snapshot(pid):
    states = {'01': 'established', '02': 'syn_sent', '03': 'syn_received', '04': 'fin_wait_1',
              '05': 'fin_wait_2', '06': 'time_wait', '07': 'closed', '08': 'close_wait',
              '09': 'last_ack', '0A': 'listen', '0B': 'closing'}
    inbound, outbound = collections.Counter(), collections.Counter()
    readable = 0
    for family in ('tcp', 'tcp6'):
        path = pathlib.Path('/proc/{}/net/{}'.format(pid, family))
        try:
            rows = path.read_text().splitlines()[1:]
            readable += 1
        except (IOError, OSError):
            continue
        for row in rows:
            fields = row.split()
            local_port = int(fields[1].rsplit(':', 1)[1], 16)
            peer_port = int(fields[2].rsplit(':', 1)[1], 16)
            state = states.get(fields[3], fields[3])
            if local_port == PORT:
                inbound[state] += 1
            elif state in ('established', 'close_wait', 'syn_sent'):
                category = {5432: 'postgres_5432', 6379: 'redis_6379',
                            443: 'remote_443', 80: 'remote_80'}.get(peer_port, 'other_remote_ports')
                outbound[category + ':' + state] += 1
    return {'available': bool(readable), 'inbound_local_port_3000': dict(inbound),
            'outbound_by_remote_port': dict(outbound),
            'interpretation': 'Connections are not request counters. Established connections may be idle; zero connections does not rule out handlers continuing after client disconnect.'}


def log_snapshot():
    # Raw logs may contain user/request data. Read them only in memory and emit
    # category counts and timestamps, never text lines, bodies, IDs or headers.
    try:
        output = run(['docker', 'logs', '--since', '10m', '--tail', '20000', '--timestamps', CONTAINER])
    except Exception:
        return {'available': False}
    patterns = {
        'batch_started': re.compile(r'batch update started', re.I),
        'batch_finished': re.compile(r'batch update finished', re.I),
        'refund_started': re.compile(r'返还预扣费|退款|refund(?:ing)? (?:billing|token|subscription)', re.I),
        'precharge_or_settlement': re.compile(r'需要预扣费|信任且不需要预扣费|settling billing|funding settled|结算|post.consume', re.I),
        'suspected_accounting_or_db_error': re.compile(r'failed to (?:batch update|update (?:channel used|user.*quota)|increase.*quota|decrease.*quota)|error (?:refunding|settling|adjusting token quota|rolling back token quota)|SQLSTATE|database is locked|deadlock detected|connection.*refused', re.I),
        'completed_http_access_log': re.compile(r'\[GIN\]|\|\s*(?:GET|POST|PUT|DELETE|PATCH)\s*\||"(?:POST|GET)\s+/(?:v1|api|pg)/', re.I),
    }
    counts, last = collections.Counter(), {}
    unmatched_starts = 0
    for line in output.splitlines():
        stamp = line.split(' ', 1)[0]
        if not re.match(r'^\d{4}-\d{2}-\d{2}T', stamp):
            stamp = 'timestamp unavailable'
        for name, pattern in patterns.items():
            if pattern.search(line):
                counts[name] += 1
                last[name] = stamp
                if name == 'batch_started':
                    unmatched_starts += 1
                elif name == 'batch_finished' and unmatched_starts:
                    unmatched_starts -= 1
    return {'available': True, 'window': 'last 10 minutes, at most 20000 lines',
            'counts': dict(counts), 'latest_utc': last,
            'batch_starts_without_later_finish_in_window': unmatched_starts,
            'interpretation': 'Original batch finish logging is not an exact queue metric or a transaction receipt. Absence of matching error text is not proof of successful accounting.'}


try:
    metadata = json.loads(run(['docker', 'inspect', CONTAINER]))[0]
except Exception:
    print(json.dumps({'container': CONTAINER, 'available': False, 'safe_to_stop': 'not established'}))
    raise SystemExit(1)
state = metadata.get('State', {})
pid = int(state.get('Pid', 0))
if not state.get('Running') or pid <= 0:
    print(json.dumps({'container': CONTAINER, 'running': False, 'safe_to_stop': 'already not running; no drain proof'}))
    raise SystemExit(0)

env = dict(item.split('=', 1) for item in metadata['Config'].get('Env', []) if '=' in item)
try:
    batch_interval = max(1, int(env.get('BATCH_UPDATE_INTERVAL', '5')))
except ValueError:
    batch_interval = 5
node = env.get('NODE_NAME', '')
if not re.match(r'^[A-Za-z0-9_.-]{1,128}$', node):
    node = ''
gap = min(20, max(10, batch_interval * 2 + 2))
report = {
    'container': CONTAINER, 'started_at': state.get('StartedAt'),
    'batch_update_enabled': env.get('BATCH_UPDATE_ENABLED', '').lower() == 'true',
    'batch_interval_seconds': batch_interval,
    'node_name': node or 'unset/unsupported characters; node attribution unavailable',
    'background_handover_note': 'Old master maintenance can start more work while this process remains running. Keep candidate maintenance marker absent until handover.',
    'safe_to_stop': 'not established by this script; old binary has no exact HTTP/refund/batch drain metric',
    'sample_gap_seconds': gap,
    'samples': [],
}

for index in range(2):
    sample = {'sample': index + 1, 'utc': datetime.datetime.utcnow().isoformat() + 'Z',
              'connections': connection_snapshot(pid), 'logs': log_snapshot()}
    sample['system_tasks'] = db("""
SELECT json_build_object('kind','system_task_counts','type',type,'status',status,'count',count(*),'latest_update',max(updated_at))
FROM system_tasks WHERE status IN ('pending','running') GROUP BY type,status ORDER BY type,status;
SELECT json_build_object('kind','unexpired_system_task_lease','type',type,'runner',locked_by,'locked_until',locked_until)
FROM system_task_locks WHERE locked_until > extract(epoch FROM now()) ORDER BY type;
SELECT json_build_object('kind','async_task_counts','status',status,'count',count(*))
FROM tasks WHERE status NOT IN ('SUCCESS','FAILURE') GROUP BY status ORDER BY status;
""")
    # Attribute consume logs only by explicit safe node name; do not inspect
    # prompts, API keys, user names, payloads or raw log metadata.
    if node:
        escaped_node = re.escape(node).replace("'", "''")
        expression = '"node_name"[[:space:]]*:[[:space:]]*"' + escaped_node + '"'
        sample['node_consume_logs'] = db("SELECT json_build_object('scope','node-name attribution; may include another instance using same name','count_last_10m',count(*),'latest_log_id',max(id),'latest_created_at',max(created_at)) FROM logs WHERE type=2 AND created_at >= extract(epoch FROM now())-600 AND other ~ '" + expression + "';")
    report['samples'].append(sample)
    if index == 0:
        time.sleep(gap)

report['review_before_term'] = [
    'Confirm ingress and direct internal callers no longer send work to old instance; this script does not change them.',
    'Confirm old scheduler is paused separately and no probe invocation remains running; this script does not stop it.',
    'Compare inbound socket state, latest old-node consume log, refund/precharge markers and batch completion across samples.',
    'Inspect running task/lease counts and any accounting/DB errors. Pending jobs are not automatically proof of active old-process work.',
    'No finite quiet interval proves the old in-memory queues are empty. If evidence is insufficient, keep the old container running.',
    'Do not use a fixed docker-stop timeout as proof of drain; this script sends no signal and performs no writes.',
]
print(json.dumps(report, ensure_ascii=False, indent=2))
PY

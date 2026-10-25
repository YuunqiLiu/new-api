#!/usr/bin/env python3
"""Explicit, root-only adoption of the existing NexEvic production release.

No builds, database migration, credential rotation, or automatic deletion.
Generated private material is written only beneath /srv/new-api/backups.
Commands never echo subprocess output containing runtime credentials.
"""
import argparse
import datetime
import hashlib
import json
import os
import pathlib
import shutil
import stat
import subprocess
import sys
import time
import urllib.request

ROOT = pathlib.Path('/srv/new-api')
LEGACY = pathlib.Path('/home/deploy/deploy_new_api')
SOURCE = pathlib.Path('/home/deploy/projects/new-api')
OLD = 'new-api-routing'
NEW = 'new-api-production'
IMAGE = 'sha256:cf8f9b44d8cb535e214706e39efdddc9d2c3445f7d71b9b052fd5bf59c66ad2c'
OLD_ID = '8a52b93ddaf2f0e8bf62e5abbe37b759bcd5bb18877c06ebf0e912b6986e750f'
CADDY = pathlib.Path('/etc/caddy/Caddyfile')
OLD_RELEASE = pathlib.Path('/srv/new-api-frontend/releases/dashboard-20261018')
NEW_RELEASE = ROOT / 'frontend/releases/dashboard-20261018'
UNIT = pathlib.Path('/etc/systemd/system/new-api-channel-scheduler.service')


def run(argv, timeout=60, stdin=None):
    result = subprocess.run(argv, input=stdin, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, timeout=timeout)
    if result.returncode:
        raise RuntimeError('Command failed (details withheld): ' + argv[0])
    return result.stdout


def inspect(name):
    return json.loads(run(['docker', 'inspect', name]))[0]


def digest(data):
    return hashlib.sha256(data).hexdigest()


def file_digest(path):
    hasher = hashlib.sha256()
    with path.open('rb') as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b''):
            hasher.update(chunk)
    return hasher.hexdigest()


def private_write(path, data):
    if isinstance(data, str):
        data = data.encode('utf-8')
    fd = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'wb') as handle:
        handle.write(data)
        handle.flush()
        os.fsync(handle.fileno())


def files_manifest(directory):
    result = {}
    for path in sorted(directory.rglob('*')):
        if path.is_symlink():
            raise RuntimeError('Unexpected release symlink')
        if path.is_file():
            result[str(path.relative_to(directory))] = file_digest(path)
    return result


def state(path):
    path = pathlib.Path(path)
    if path.resolve().parent.parent != (ROOT / 'backups').resolve():
        raise RuntimeError('State must be inside a migration backup directory')
    if stat.S_IMODE(path.stat().st_mode) != 0o600 or path.stat().st_uid != 0:
        raise RuntimeError('State file is not root-only')
    return json.loads(path.read_text()), path.parent


def save_result(directory, name, result):
    private_write(directory / name, json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, sort_keys=True))


def git_backup(repo, directory, label):
    safe = ['git', '-c', 'safe.directory=' + str(repo), '-C', str(repo)]
    run(safe + ['bundle', 'create', str(directory / (label + '.bundle')), '--all'], timeout=180)
    run(safe + ['bundle', 'verify', str(directory / (label + '.bundle'))])
    private_write(directory / (label + '.patch'), run(safe + ['diff', '--binary', 'HEAD']))
    private_write(directory / (label + '-status.txt'),
                  run(safe + ['status', '--porcelain=v1', '--untracked-files=normal']))


def backup():
    if ROOT.exists():
        raise RuntimeError('Production root already exists; inspect, do not overwrite')
    old = inspect(OLD)
    if old['Id'] != OLD_ID or old['Image'] != IMAGE or not old['State']['Running']:
        raise RuntimeError('Observed production baseline changed')
    if any((LEGACY / 'routing-20261018/accounting-failures').iterdir()):
        raise RuntimeError('Unreconciled accounting files exist; migration blocked')
    ROOT.mkdir(mode=0o755)
    os.chmod(str(ROOT), 0o755)
    (ROOT / 'backups').mkdir(mode=0o700)
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    directory = ROOT / 'backups' / ('layout-' + stamp)
    directory.mkdir(mode=0o700)
    for label, name in [('old-app-inspect', OLD), ('postgres-inspect', 'postgres'), ('redis-inspect', 'redis')]:
        private_write(directory / (label + '.json'), json.dumps(inspect(name), indent=2))
    for filename, original in [
        ('compose.original.yaml', LEGACY / 'docker-compose.yml'),
        ('Caddyfile.original', CADDY), ('scheduler.service.original', UNIT),
        ('scheduler.timer.original', pathlib.Path('/etc/systemd/system/new-api-channel-scheduler.timer')),
        ('scheduler.env.original', pathlib.Path('/home/deploy/new_api_channel_scheduler/scheduler.env')),
    ]:
        private_write(directory / filename, original.read_bytes())
    archive = directory / 'database.dump'
    with archive.open('xb') as output:
        proc = subprocess.run(['docker', 'exec', 'postgres', 'pg_dump', '-U', 'root',
                               '-d', 'new-api', '-Fc'], stdout=output, stderr=subprocess.PIPE, timeout=180)
        if proc.returncode:
            raise RuntimeError('Database backup failed; details withheld')
    with archive.open('rb') as source:
        check = subprocess.run(['docker', 'exec', '-i', 'postgres', 'pg_restore',
                                '--file=/dev/null'], stdin=source, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, timeout=180)
        if check.returncode:
            raise RuntimeError('Database archive read-through failed; stop migration')
    git_backup(LEGACY, directory, 'legacy-all-refs')
    git_backup(pathlib.Path('/home/deploy/worktrees/new-api-routing-20261018'), directory, 'routing-all-refs')
    for repo in ['/home/deploy/worktrees/new-api-mainline',
                 '/home/deploy/worktrees/new-api-baidu-responses-code-mode',
                 '/home/deploy/worktrees/new-api-responses-toolfix',
                 '/home/deploy/worktrees/new-api-dashboard-20261018']:
        repo = pathlib.Path(repo)
        safe = ['git', '-c', 'safe.directory=' + str(repo), '-C', str(repo)]
        private_write(directory / (repo.name + '.patch'), run(safe + ['diff', '--binary', 'HEAD']))
        private_write(directory / (repo.name + '-status.txt'),
                      run(safe + ['status', '--porcelain=v1', '--untracked-files=normal']))
    # Meaningful historical source/private release records, not dependency trees
    # or multi-gigabyte binaries. Old directories remain intact after migration.
    archive_paths = [
        '/home/deploy/worktrees/new-api-routing-20261018/deployment',
        '/home/deploy/worktrees/new-api-routing-20261018/validation',
        '/home/deploy/worktrees/new-api-routing-20261018/scheduler',
        '/home/deploy/worktrees/new-api-dashboard-20261018/deployment',
        '/home/deploy/worktrees/new-api-dashboard-20261018/validation',
        '/home/deploy/worktrees/new-api-dashboard-20261018/web/src/features/dashboard',
        '/home/deploy/worktrees/new-api-baidu-responses-code-mode/relay/channel/advancedcustom',
        '/home/deploy/new_api_channel_scheduler',
        '/var/lib/new-api-channel-scheduler',
    ]
    run(['tar', '-czf', str(directory / 'source-and-private-operations.tar.gz'), '--'] + archive_paths, timeout=180)
    result = {
        'schema': 1, 'backup_directory': str(directory), 'old_container_id': OLD_ID,
        'image_id': IMAGE, 'postgres_id': inspect('postgres')['Id'],
        'redis_id': inspect('redis')['Id'], 'caddy_sha256': digest(CADDY.read_bytes()),
        'scheduler_unit_sha256': digest(UNIT.read_bytes()),
        'database_archive_bytes': archive.stat().st_size,
        'database_archive_sha256': file_digest(archive),
        'database_archive_readthrough_passed': True,
        'database_restore_into_server_performed': False,
        'created_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
        'source_commit': run(['git', '-c', 'safe.directory=' + str(SOURCE), '-C', str(SOURCE), 'rev-parse', 'HEAD']).decode().strip(),
    }
    save_result(directory, 'state.json', result)


def env_file(path, values):
    rows = []
    for key, value in sorted(values.items()):
        value = str(value)
        if any(char in value for char in ("'", '\n', '\r', '\x00')):
            raise RuntimeError('Unsupported environment value; do not alter or expose it')
        rows.append(key + "='" + value + "'")
    private_write(path, '\n'.join(rows) + '\n')


def prepare(state_path):
    recorded, directory = state(state_path)
    if inspect(OLD)['Id'] != recorded['old_container_id']:
        raise RuntimeError('Old production container changed')
    for folder, mode in [('config', 0o700), ('bin', 0o755), ('runtime', 0o755),
                         ('runtime/data', 0o700), ('runtime/logs', 0o700),
                         ('runtime/handover', 0o700), ('runtime/accounting-failures', 0o700),
                         ('runtime/scheduler-state', 0o700), ('frontend', 0o755),
                         ('frontend/releases', 0o755)]:
        (ROOT / folder).mkdir(mode=mode)
        os.chmod(str(ROOT / folder), mode)
    old_config = json.loads(run(['docker', 'compose', '-f', str(LEGACY / 'docker-compose.yml'),
                                 'config', '--format', 'json']))
    services = old_config['services']
    app = services.pop('new-api-routing')
    values = app.pop('environment')
    running_values = dict(item.split('=', 1) for item in inspect(OLD)['Config']['Env'] if '=' in item)
    if any(str(value) != running_values.get(key) for key, value in values.items()):
        raise RuntimeError('Saved configuration differs from running environment')
    # Keep migration-only variables that the older compose file omitted.
    # The running process, not an incomplete saved file, is the baseline.
    for key, value in running_values.items():
        if key != 'PATH' and key not in values:
            values[key] = value
    values['NODE_NAME'] = NEW
    env_file(ROOT / 'config/app.env', values)
    app.update({'container_name': NEW, 'env_file': [str(ROOT / 'config/app.env')],
                'ports': [{'target': 3000, 'published': '3020', 'host_ip': '127.0.0.1', 'protocol': 'tcp'}],
                'volumes': [{'type': 'bind', 'source': str(ROOT / src), 'target': dst} for src, dst in [
                    ('runtime/data', '/data'), ('runtime/logs', '/app/logs'),
                    ('runtime/handover', '/handover'), ('runtime/accounting-failures', '/accounting-failures')]],
                'stop_grace_period': '24h', 'pull_policy': 'never'})
    services[NEW] = app
    # Redis is NOT restarted; make its future definition refer to a private env.
    redis = services['redis']
    command = redis.get('command', [])
    if '--requirepass' not in command:
        raise RuntimeError('Unexpected Redis command; stop preparation')
    password = command[command.index('--requirepass') + 1]
    env_file(ROOT / 'config/redis.env', {'REDIS_PASSWORD': password})
    redis['env_file'] = [str(ROOT / 'config/redis.env')]
    redis['command'] = ['sh', '-c', 'exec redis-server --requirepass "$$REDIS_PASSWORD"']
    postgres = services['postgres']
    env_file(ROOT / 'config/postgres.env', postgres.pop('environment'))
    postgres['env_file'] = [str(ROOT / 'config/postgres.env')]
    old_config['name'] = 'deploy_new_api'
    old_config['volumes'] = {'pg_data': {'name': 'deploy_new_api_pg_data', 'external': True}}
    old_config['networks'] = {'new-api-network': {'name': 'deploy_new_api_new-api-network', 'external': True}}
    private_write(ROOT / 'compose.yaml', json.dumps(old_config, indent=2) + '\n')
    run(['docker', 'compose', '-p', 'deploy_new_api', '-f', str(ROOT / 'compose.yaml'), 'config', '--quiet'])
    for source, target in [(LEGACY / 'data', ROOT / 'runtime/data'),
                            (LEGACY / 'routing-20261018/logs', ROOT / 'runtime/logs'),
                            (pathlib.Path('/var/lib/new-api-channel-scheduler'), ROOT / 'runtime/scheduler-state')]:
        run(['rsync', '-a', str(source) + '/', str(target) + '/'], timeout=120)
    # Do not copy /handover/start while the old master remains alive.
    shutil.copytree(str(OLD_RELEASE), str(NEW_RELEASE))
    if files_manifest(OLD_RELEASE) != files_manifest(NEW_RELEASE):
        raise RuntimeError('Frontend copy hash mismatch')
    os.symlink('releases/dashboard-20261018', str(ROOT / 'frontend/current'))
    scheduler = pathlib.Path('/home/deploy/new_api_channel_scheduler/scheduler.py')
    shutil.copy2(str(scheduler), str(ROOT / 'bin/channel_scheduler.py'))
    os.chmod(str(ROOT / 'bin/channel_scheduler.py'), 0o755)
    original_env = (directory / 'scheduler.env.original').read_text()
    old_base = 'API_BASE=http://127.0.0.1:3019'
    if original_env.count(old_base) != 1:
        raise RuntimeError('Unexpected scheduler endpoint')
    private_write(ROOT / 'config/scheduler.env', original_env.replace(old_base, 'API_BASE=http://127.0.0.1:3020'))
    owner = pathlib.Path('/var/lib/new-api-channel-scheduler').stat()
    os.chown(str(ROOT / 'runtime/scheduler-state'), owner.st_uid, owner.st_gid)
    original_unit = (directory / 'scheduler.service.original').read_text()
    updated_unit = original_unit.replace('/home/deploy/new_api_channel_scheduler/scheduler.py', str(ROOT / 'bin/channel_scheduler.py'))
    updated_unit = updated_unit.replace('/home/deploy/new_api_channel_scheduler/scheduler.env', str(ROOT / 'config/scheduler.env'))
    updated_unit = updated_unit.replace('WorkingDirectory=/home/deploy/new_api_channel_scheduler', 'WorkingDirectory=' + str(ROOT / 'bin'))
    updated_unit = updated_unit.replace('/var/lib/new-api-channel-scheduler', str(ROOT / 'runtime/scheduler-state'))
    updated_unit = '\n'.join(line for line in updated_unit.splitlines() if not line.startswith('StateDirectory')) + '\n'
    private_write(directory / 'scheduler.service.candidate', updated_unit)
    original_caddy = (directory / 'Caddyfile.original').read_text()
    old_root = 'root * ' + str(OLD_RELEASE)
    if original_caddy.count(old_root) != 1 or original_caddy.count('reverse_proxy 127.0.0.1:3019') != 1:
        raise RuntimeError('Unexpected Caddy API site; do not overwrite')
    updated_caddy = original_caddy.replace(old_root, 'root * ' + str(ROOT / 'frontend/current'))
    updated_caddy = updated_caddy.replace('reverse_proxy 127.0.0.1:3019', 'reverse_proxy 127.0.0.1:3020')
    private_write(directory / 'Caddyfile.candidate', updated_caddy)
    run(['caddy', 'validate', '--config', str(directory / 'Caddyfile.candidate'), '--adapter', 'caddyfile'])
    run(['runuser', '-u', 'caddy', '--', 'test', '-r', str(ROOT / 'frontend/current/index.html')])
    save_result(directory, 'prepared.json', {'prepared': True, 'production_switched': False,
                'frontend_files': len(files_manifest(NEW_RELEASE)), 'image_unchanged': True,
                'handover_marker_present': (ROOT / 'runtime/handover/start').exists()})


def launch(state_path):
    recorded, directory = state(state_path)
    if (ROOT / 'runtime/handover/start').exists():
        raise RuntimeError('New background marker exists before old master drain')
    if inspect(OLD)['Id'] != recorded['old_container_id']:
        raise RuntimeError('Old production baseline changed')
    current_image = json.loads(run(['docker', 'image', 'inspect', 'localhost/new-api:nexevic-routing-20261018']))[0]
    if current_image['Id'] != IMAGE:
        raise RuntimeError('Image tag changed; never pull/build during relocation')
    run(['docker', 'compose', '-p', 'deploy_new_api', '-f', str(ROOT / 'compose.yaml'),
         'up', '-d', '--no-deps', '--no-build', NEW], timeout=90)
    for unused in range(60):
        try:
            with urllib.request.urlopen('http://127.0.0.1:3020/api/status', timeout=2) as response:
                status_value = json.loads(response.read().decode())
            container = inspect(NEW)
            if status_value.get('success') and container['State'].get('Health', {}).get('Status') == 'healthy':
                if container['Image'] != IMAGE:
                    raise RuntimeError('Unexpected running image')
                result_name = 'launched.json' if not (directory / 'launched.json').exists() else 'relaunched-' + container['Id'][:12] + '.json'
                save_result(directory, result_name, {'candidate_healthy': True,
                    'container_id': container['Id'], 'production_switched': False,
                    'background_tasks_still_blocked': not (ROOT / 'runtime/handover/start').exists()})
                return
        except (OSError, ValueError):
            pass
        time.sleep(1)
    raise RuntimeError('Candidate did not become healthy; old production remains unchanged')


def atomic_replace(path, data, expected_sha):
    if path.is_symlink() or digest(path.read_bytes()) != expected_sha:
        raise RuntimeError('Concurrent configuration change; refusing overwrite')
    metadata = path.stat()
    temporary = path.with_name(path.name + '.nexevic-layout-tmp')
    private_write(temporary, data)
    os.chmod(str(temporary), stat.S_IMODE(metadata.st_mode))
    os.chown(str(temporary), metadata.st_uid, metadata.st_gid)
    if digest(path.read_bytes()) != expected_sha:
        raise RuntimeError('Configuration changed before replace')
    os.replace(str(temporary), str(path))


def synchronize_environment(state_path):
    recorded, directory = state(state_path)
    if digest(CADDY.read_bytes()) != recorded['caddy_sha256'] or (directory / 'switched.json').exists():
        raise RuntimeError('Environment reconciliation is allowed only before cutover')
    if inspect(OLD)['Id'] != OLD_ID or not inspect(OLD)['State']['Running']:
        raise RuntimeError('Original process is not the observed live baseline')
    values = dict(item.split('=', 1) for item in inspect(OLD)['Config']['Env'] if '=' in item)
    values.pop('PATH', None)
    values['NODE_NAME'] = NEW
    temporary = ROOT / 'config/app.env.corrected'
    env_file(temporary, values)
    destination = ROOT / 'config/app.env'
    atomic_replace(destination, temporary.read_bytes(), digest(destination.read_bytes()))
    temporary.unlink()
    save_result(directory, 'environment-synchronized.json', {
        'effective_environment_preserved_except': ['NODE_NAME'], 'cutover_performed': False})


def switch(state_path):
    recorded, directory = state(state_path)
    old, new = inspect(OLD), inspect(NEW)
    if old['Id'] != OLD_ID or not old['State']['Running'] or new['Image'] != IMAGE:
        raise RuntimeError('Runtime baseline changed')
    if new['State'].get('Health', {}).get('Status') != 'healthy':
        raise RuntimeError('New app is not healthy')
    if (ROOT / 'runtime/handover/start').exists():
        raise RuntimeError('Double-master risk; stop migration')
    if digest(CADDY.read_bytes()) != recorded['caddy_sha256'] or digest(UNIT.read_bytes()) != recorded['scheduler_unit_sha256']:
        raise RuntimeError('Configuration baseline changed')
    run(['systemctl', 'stop', 'new-api-channel-scheduler.timer'])
    active = run(['systemctl', 'show', 'new-api-channel-scheduler.service', '-p', 'ActiveState', '--value']).decode().strip()
    if active in ('active', 'activating', 'deactivating'):
        run(['systemctl', 'start', 'new-api-channel-scheduler.timer'])
        raise RuntimeError('Scheduler still executing; postpone switch, do not kill it')
    run(['rsync', '-a', '/var/lib/new-api-channel-scheduler/', str(ROOT / 'runtime/scheduler-state') + '/'])
    atomic_replace(CADDY, (directory / 'Caddyfile.candidate').read_bytes(), recorded['caddy_sha256'])
    try:
        run(['systemctl', 'reload', 'caddy'])
    except Exception:
        atomic_replace(CADDY, (directory / 'Caddyfile.original').read_bytes(),
                       digest((directory / 'Caddyfile.candidate').read_bytes()))
        run(['systemctl', 'reload', 'caddy'])
        run(['systemctl', 'start', 'new-api-channel-scheduler.timer'])
        raise
    save_result(directory, 'switched.json', {'new_requests_routed_to': '127.0.0.1:3020',
                'old_container_still_running': inspect(OLD)['State']['Running'],
                'scheduler_timer_paused': True, 'old_requests_not_signalled_yet': True})


def drain(state_path):
    recorded, directory = state(state_path)
    if not (directory / 'switched.json').exists():
        raise RuntimeError('Do not drain before traffic switch')
    if inspect(OLD)['Id'] != recorded['old_container_id']:
        raise RuntimeError('Old container changed')
    expected = digest((directory / 'Caddyfile.candidate').read_bytes())
    if digest(CADDY.read_bytes()) != expected:
        raise RuntimeError('Ingress does not match reviewed candidate')
    candidate = inspect(NEW)
    if candidate['Image'] != IMAGE or candidate['State'].get('Health', {}).get('Status') != 'healthy':
        raise RuntimeError('Candidate is not healthy; do not drain the old app')
    if inspect(OLD)['State']['Running']:
        # Disable restart before TERM. Never docker stop/rm -f or SIGKILL.
        run(['docker', 'update', '--restart=no', OLD])
        run(['docker', 'kill', '--signal=TERM', OLD])
    save_result(directory, 'drain-requested.json', {'signal': 'SIGTERM', 'force_kill': False,
                'new_app_accepting_requests': True, 'old_app_restart_disabled': True})


def finalize(state_path):
    recorded, directory = state(state_path)
    old = inspect(OLD)
    if old['State']['Running']:
        raise RuntimeError('Old requests/accounting still draining; do not finalize')
    if old['State']['ExitCode'] != 0 or old['State'].get('OOMKilled'):
        raise RuntimeError('Old app did not exit cleanly; manual reconciliation required')
    log_result = subprocess.run(['docker', 'logs', '--tail', '1200', OLD],
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=30)
    if log_result.returncode:
        raise RuntimeError('Could not read graceful drain evidence')
    logs = log_result.stdout.decode('utf-8', 'replace')
    for marker in ['HTTP requests drained', 'maintenance tasks drained', 'accounting tasks drained',
                   'batch accounting drained', 'server exited']:
        if marker not in logs:
            raise RuntimeError('Missing graceful drain evidence: ' + marker)
    if any((LEGACY / 'routing-20261018/accounting-failures').iterdir()):
        raise RuntimeError('Accounting failures require reconciliation, not replay')
    for name, key in [('postgres', 'postgres_id'), ('redis', 'redis_id')]:
        if inspect(name)['Id'] != recorded[key] or not inspect(name)['State']['Running']:
            raise RuntimeError('Infrastructure unexpectedly changed')
    run(['rsync', '-a', str(LEGACY / 'routing-20261018/logs') + '/', str(ROOT / 'runtime/logs') + '/'], timeout=120)
    marker = ROOT / 'runtime/handover/start'
    if not marker.exists():
        private_write(marker, 'old master drained; enable maintenance\n')
    atomic_replace(UNIT, (directory / 'scheduler.service.candidate').read_bytes(), recorded['scheduler_unit_sha256'])
    run(['systemctl', 'daemon-reload'])
    run(['systemctl', 'start', 'new-api-channel-scheduler.timer'])
    save_result(directory, 'finalized.json', {'old_exit_code': 0, 'all_drain_stages_verified': True,
                'background_handover_enabled': True, 'scheduler_timer_restored': True,
                'postgres_and_redis_unchanged': True, 'old_files_deleted': False})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['backup', 'prepare', 'launch', 'synchronize_environment', 'switch', 'drain', 'finalize'])
    parser.add_argument('--state')
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('root required; no new sudo rights are installed')
    os.umask(0o077)
    if args.action == 'backup':
        backup()
    else:
        if not args.state:
            parser.error('--state is required')
        globals()[args.action](args.state)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        # No raw subprocess/environment output: it may contain credentials.
        print('Migration stopped safely: ' + str(error), file=sys.stderr)
        sys.exit(1)

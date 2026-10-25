#!/usr/bin/env python3
"""Frontend release preparation/application. Import through the two entrypoints.

Production paths are deliberately fixed. No container or service restart/stop is
implemented. Application requires the explicitly named prepared state file.
Python 3.6 compatible; all command output is retained in protected local logs.
"""
import difflib
import fcntl
import hashlib
import json
import os
import pathlib
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
import uuid

WORK = pathlib.Path('/home/deploy/worktrees/new-api-dashboard-20261018')
DIST = WORK / 'web/dist'
RELEASE = pathlib.Path('/srv/new-api-frontend/releases/dashboard-20261018')
CADDY = pathlib.Path('/etc/caddy/Caddyfile')
CONTAINER = 'new-api-routing'
BASE = 'https://api.nexevic.com'
GENERATOR = pathlib.Path(__file__).with_name('prepare_frontend_overlay.py')


def sha(data):
    return hashlib.sha256(data).hexdigest()


def private_write(path, data):
    if isinstance(data, str):
        data = data.encode('utf-8')
    descriptor = os.open(str(path), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'wb') as handle:
        handle.write(data)
        handle.flush()
        os.fsync(handle.fileno())


def command(argv, log, timeout=30):
    proc = subprocess.run(argv, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
    private_write(log, proc.stdout)
    if proc.returncode:
        raise RuntimeError('Command failed; inspect protected log ' + str(log))


def fetch(path, base=BASE):
    request = urllib.request.Request(base + path, headers={'Accept-Encoding': 'identity',
                                     'Cache-Control': 'no-cache'})
    with urllib.request.urlopen(request, timeout=15) as response:
        if response.status != 200:
            raise RuntimeError('Unexpected HTTP status')
        return response.read()


def version(base):
    status = json.loads(fetch('/api/status', base).decode())
    if status.get('success') is not True or not isinstance(status.get('data'), dict):
        raise RuntimeError('API status did not succeed')
    value = status['data'].get('version')
    if not isinstance(value, str) or not value:
        raise RuntimeError('API version is missing')
    return value


def backend():
    container = json.loads(subprocess.check_output(['docker', 'inspect', CONTAINER]))[0]
    if not container['State']['Running']:
        raise RuntimeError('Routing container is not running')
    analytics = ('UMAMI_WEBSITE_ID=', 'GOOGLE_ANALYTICS_ID=')
    if any(value.startswith(analytics) and value.partition('=')[2]
           for value in container['Config'].get('Env', [])):
        raise RuntimeError('Analytics injection now configured; static HTML needs review')
    binary = subprocess.check_output(['docker', 'exec', CONTAINER, 'sha256sum', '/new-api'])
    return {'id': container['Id'], 'pid': container['State']['Pid'],
            'started_at': container['State']['StartedAt'],
            'binary_sha256': binary.decode().split()[0]}


def manifest(directory):
    if directory.is_symlink() or not directory.is_dir():
        raise RuntimeError('Release is not a real directory')
    result = {}
    for path in sorted(directory.rglob('*')):
        if path.is_symlink() or path.name.startswith('.'):
            raise RuntimeError('Unexpected symlink or hidden build file')
        if path.is_file():
            result[path.relative_to(directory).as_posix()] = sha(path.read_bytes())
        elif not path.is_dir():
            raise RuntimeError('Unexpected special build file')
    if 'index.html' not in result or not any(p.startswith('static/js/') for p in result):
        raise RuntimeError('Build output is incomplete')
    return result


def validate_html(directory, files):
    text = (directory / 'index.html').read_text()
    refs = re.findall(r'(?:src|href)="([^"]+)"', text)
    js_paths = []
    for ref in refs:
        parsed = urllib.parse.urlsplit(ref)
        if parsed.scheme or parsed.netloc or not parsed.path.startswith('/'):
            raise RuntimeError('Unexpected non-origin HTML asset reference')
        relative = parsed.path.lstrip('/')
        if relative not in files:
            raise RuntimeError('HTML references a missing build file')
        if relative.startswith('static/js/') and relative.endswith('.js'):
            js_paths.append('/' + relative)
    if not js_paths:
        raise RuntimeError('No JavaScript entrypoint in HTML')
    return js_paths[0]


def check_permissions(log):
    code = ('import os,pathlib; p=pathlib.Path(%r); '
            'assert os.access(str(p),os.R_OK|os.X_OK); '
            'assert all(os.access(str(f),os.R_OK|(os.X_OK if f.is_dir() else 0)) '
            'for f in p.rglob("*"))') % str(RELEASE)
    command(['runuser', '-u', 'caddy', '--', 'python3', '-c', code], log)


def prepare():
    if os.geteuid() != 0:
        raise RuntimeError('Preparation requires root for release ownership')
    if CADDY.is_symlink() or CADDY.resolve() != CADDY or not CADDY.is_file():
        raise RuntimeError('Unexpected active Caddyfile path')
    if RELEASE.exists() or RELEASE.is_symlink():
        raise RuntimeError('Immutable release name already exists; do not overwrite it')
    source_files = manifest(DIST)
    probe_asset = validate_html(DIST, source_files)
    output = WORK / 'deployment' / ('frontend-prepared-' + uuid.uuid4().hex[:12])
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    original = CADDY.read_bytes()
    original_meta = CADDY.stat()
    private_write(output / 'source.Caddyfile', original)
    baseline = backend()
    api_version = version('http://127.0.0.1:3019')
    if version(BASE) != api_version:
        raise RuntimeError('Public origin does not match routing backend version')
    old_html_sha = sha(fetch('/dashboard/overview'))
    # Generator rejects unexpected root files and any drift in the reviewed API site.
    command([sys.executable, str(GENERATOR), '--caddy-source', str(output / 'source.Caddyfile'),
             '--route-tree', str(WORK / 'web/src/routeTree.gen.ts'), '--dist', str(DIST),
             '--release-root', str(RELEASE), '--output-dir', str(output / 'candidates')],
            output / 'generator.log')
    RELEASE.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
    if RELEASE.parent.resolve() != RELEASE.parent:
        raise RuntimeError('Release parent must not traverse a symlink')
    staging = RELEASE.parent / (RELEASE.name + '-staging-' + uuid.uuid4().hex[:12])
    shutil.copytree(str(DIST), str(staging))
    if manifest(staging) != source_files or manifest(DIST) != source_files:
        raise RuntimeError('Build output changed while preparing release')
    for path in [staging] + list(staging.rglob('*')):
        os.chown(str(path), 0, 0)
        os.chmod(str(path), 0o755 if path.is_dir() else 0o644)
    # Never overwrite a completed release. The directory stays immutable after rename.
    if RELEASE.exists() or RELEASE.is_symlink():
        raise RuntimeError('Release was created concurrently')
    os.rename(str(staging), str(RELEASE))
    check_permissions(output / 'permissions.log')
    candidates = output / 'candidates'
    for name in ['candidate', 'rollback']:
        command(['caddy', 'validate', '--config', str(candidates / ('Caddyfile.frontend-' + name)),
                 '--adapter', 'caddyfile'], output / ('validate-' + name + '.log'))
    if CADDY.read_bytes() != original or backend() != baseline:
        raise RuntimeError('Production baseline changed during preparation')
    state = {'schema': 1, 'prepared_at_unix': int(time.time()), 'release': str(RELEASE), 'files': source_files,
             'original_sha256': sha(original), 'caddy_mode': stat.S_IMODE(original_meta.st_mode),
             'caddy_uid': original_meta.st_uid, 'caddy_gid': original_meta.st_gid,
             'backend': baseline, 'api_version': api_version, 'old_html_sha256': old_html_sha,
             'new_html_sha256': source_files['index.html'], 'probe_asset': probe_asset,
             'candidate': str(candidates / 'Caddyfile.frontend-candidate'),
             'rollback': str(candidates / 'Caddyfile.frontend-rollback')}
    for name in ['candidate', 'rollback']:
        state[name + '_sha256'] = sha(pathlib.Path(state[name]).read_bytes())
    private_write(output / 'state.json', json.dumps(state, indent=2))
    changed = pathlib.Path(state['candidate']).read_text()
    # Context-free diff only exposes the changed, reviewed API site, not adjacent sites.
    print(''.join(difflib.unified_diff(original.decode().splitlines(True),
                                     changed.splitlines(True), fromfile='original API site',
                                     tofile='frontend overlay', n=0)))
    print(json.dumps({'prepared': True, 'production_reloaded': False,
                      'state_file': str(output / 'state.json'), 'file_count': len(source_files)}))


def atomic_install(data, expected_hash, state):
    if CADDY.is_symlink() or sha(CADDY.read_bytes()) != expected_hash:
        raise RuntimeError('Caddyfile changed concurrently; refusing overwrite')
    meta = CADDY.stat()
    if (stat.S_IMODE(meta.st_mode), meta.st_uid, meta.st_gid) != (
            state['caddy_mode'], state['caddy_uid'], state['caddy_gid']):
        raise RuntimeError('Caddyfile ownership or mode changed; refusing overwrite')
    descriptor, temp = tempfile.mkstemp(prefix='.Caddyfile.frontend-', dir=str(CADDY.parent))
    try:
        with os.fdopen(descriptor, 'wb') as handle:
            handle.write(data)
            handle.flush()
            os.fchown(handle.fileno(), state['caddy_uid'], state['caddy_gid'])
            os.fchmod(handle.fileno(), state['caddy_mode'])
            os.fsync(handle.fileno())
        if CADDY.is_symlink() or sha(CADDY.read_bytes()) != expected_hash:
            raise RuntimeError('Caddyfile changed before atomic replacement')
        os.replace(temp, str(CADDY))
        dir_descriptor = os.open(str(CADDY.parent), os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(dir_descriptor)
        finally:
            os.close(dir_descriptor)
    finally:
        if os.path.exists(temp):
            os.unlink(temp)


def apply(state_path):
    if os.geteuid() != 0:
        raise RuntimeError('Application requires root')
    state_path = pathlib.Path(state_path).resolve()
    if not str(state_path).startswith(str(WORK / 'deployment') + '/') or state_path.name != 'state.json':
        raise RuntimeError('Use the state file created by this preparation script')
    state = json.loads(state_path.read_text())
    if state.get('schema') != 1 or state.get('release') != str(RELEASE):
        raise RuntimeError('Unexpected prepared state')
    output = state_path.parent / ('apply-' + uuid.uuid4().hex[:12])
    output.mkdir(mode=0o700)
    lock_fd = os.open('/run/lock/new-api-frontend-deploy.lock', os.O_CREAT | os.O_RDWR, 0o600)
    with os.fdopen(lock_fd, 'w') as lock:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        if sha(CADDY.read_bytes()) != state['original_sha256']:
            raise RuntimeError('Active Caddyfile differs from prepared original')
        if manifest(RELEASE) != state['files'] or backend() != state['backend']:
            raise RuntimeError('Prepared assets or backend process changed')
        check_permissions(output / 'permissions.log')
        configs = {}
        for name in ['candidate', 'rollback']:
            path = pathlib.Path(state[name]).resolve()
            if path.parent != state_path.parent / 'candidates':
                raise RuntimeError('Candidate escaped prepared directory')
            configs[name] = path.read_bytes()
            if sha(configs[name]) != state[name + '_sha256']:
                raise RuntimeError('Prepared Caddy candidate changed')
            command(['caddy', 'validate', '--config', str(path), '--adapter', 'caddyfile'],
                    output / ('validate-' + name + '.log'))
        result = {'applied': False, 'rollback': None, 'backend_restart_requested': False}
        installed = False
        try:
            atomic_install(configs['candidate'], state['original_sha256'], state)
            installed = True
            command(['systemctl', 'reload', 'caddy'], output / 'reload.log')
            last_error = None
            for attempt in range(5):
                try:
                    if sha(fetch('/dashboard/overview?frontend_check=' + str(int(time.time())))) != state['new_html_sha256']:
                        raise RuntimeError('Public HTML hash differs from prepared release')
                    if version(BASE) != state['api_version'] or version('http://127.0.0.1:3019') != state['api_version']:
                        raise RuntimeError('API version changed')
                    if backend() != state['backend']:
                        raise RuntimeError('Routing container identity, PID, start time or binary changed')
                    if sha(fetch(state['probe_asset'])) != state['files'][state['probe_asset'].lstrip('/')]:
                        raise RuntimeError('Published entry asset hash mismatch')
                    last_error = None
                    break
                except Exception as exc:
                    last_error = exc
                    time.sleep(1)
            if last_error:
                raise last_error
            result.update({'applied': True, 'public_html_sha256': state['new_html_sha256'],
                           'api_version': state['api_version'], 'backend_identity_unchanged': True})
        except Exception as exc:
            result['error'] = type(exc).__name__ + ': ' + str(exc)
            # An fsync failure after rename must still trigger staged rollback.
            if installed or sha(CADDY.read_bytes()) == state['candidate_sha256']:
                try:
                    # Refuse to overwrite an intervening administrator edit even during rollback.
                    atomic_install(configs['rollback'], state['candidate_sha256'], state)
                    command(['systemctl', 'reload', 'caddy'], output / 'rollback-reload.log')
                    if sha(fetch('/dashboard/overview?frontend_rollback=' + str(int(time.time())))) != state['old_html_sha256']:
                        raise RuntimeError('Rollback old HTML hash verification failed')
                    if sha(fetch(state['probe_asset'])) != state['files'][state['probe_asset'].lstrip('/')]:
                        raise RuntimeError('Rollback new chunk availability check failed')
                    if backend() != state['backend'] or version(BASE) != state['api_version']:
                        raise RuntimeError('Backend invariant failed after rollback')
                    result['rollback'] = 'staged rollback verified; old HTML and new assets available'
                except Exception as rollback_error:
                    result['rollback'] = 'FAILED: ' + type(rollback_error).__name__ + ': ' + str(rollback_error)
        private_write(output / 'result.json', json.dumps(result, indent=2))
        print(json.dumps(result, indent=2))
        if not result['applied']:
            raise RuntimeError('Frontend application failed; inspect saved result. No services were killed or restarted.')

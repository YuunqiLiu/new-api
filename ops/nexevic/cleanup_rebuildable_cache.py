#!/usr/bin/env python3
"""Remove only an explicit set of reconstructible New API build material.

Dry-run by default. Requires a successful fixed-layout handover. Does not remove
any production data, source worktree, logs, backups, or application image.
"""
import argparse
import importlib.util
import json
import os
import pathlib
import shutil
import subprocess
import sys

spec = importlib.util.spec_from_file_location('layout', str(pathlib.Path(__file__).with_name('production_layout.py')))
layout = importlib.util.module_from_spec(spec)
spec.loader.exec_module(layout)

DIRECTORIES = [
    '/home/deploy/deploy_new_api/web/node_modules',
    '/home/deploy/worktrees/new-api-dashboard-20261018/web/node_modules',
    '/home/deploy/worktrees/new-api-dashboard-20261018/deps-partial',
]
CONTAINERS = [
    'new-api-dashboard-build-20261018', 'new-api-dashboard-validate3-20261018',
    'new-api-dashboard-validate2-20261018', 'new-api-dashboard-validate-20261018',
    'new-api-dashboard-red-20261018', 'new-api-dashboard-deps-verify-20261018',
    'new-api-dashboard-deps-20261018',
]
VOLUMES = ['new-api-go-cache', 'new-api-go-mod']
ARCHIVES = [
    'new-api-channel-plan-quota-20260823.tar.gz',
    'new-api-channel-plan-quota-v2-20260823.tar.gz',
    'new-api-quota-card-v3.tar.gz', 'new-api-deepseek-cny-v4.tar.gz',
    'new-api-nexevic-kimi-baidu-plan-v1.tar.gz',
    'new-api-nexevic-zhipu-responses-v1.tar.gz',
    'new-api-nexevic-all-responses-v1.tar.gz',
    'new-api-nexevic-all-responses-v2.tar.gz',
    'new-api-nexevic-all-responses-v3.tar.gz',
    'new-api-glm-effort-ds-bridge-v1.tar.gz',
    'new-api-glm-effort-ds-bridge-v2.tar.gz',
]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state', required=True)
    parser.add_argument('--execute', action='store_true')
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('root required; no new privilege grants are installed')
    os.umask(0o077)
    recorded, directory = layout.state(args.state)
    if not (directory / 'finalized.json').exists():
        raise RuntimeError('Migration is not finalized')
    current = layout.inspect(layout.NEW)
    if current['Image'] != layout.IMAGE or current['State'].get('Health', {}).get('Status') != 'healthy':
        raise RuntimeError('Production is not the expected healthy image')
    old = layout.inspect(layout.OLD)
    if old['State']['Running'] or old['State']['ExitCode'] != 0:
        raise RuntimeError('Old process is not cleanly stopped')
    for name, key in [('postgres', 'postgres_id'), ('redis', 'redis_id')]:
        if layout.inspect(name)['Id'] != recorded[key]:
            raise RuntimeError('Infrastructure baseline changed')
    running_ids = layout.run(['docker', 'ps', '-q']).decode().split()
    running = json.loads(layout.run(['docker', 'inspect'] + running_ids))
    for target in DIRECTORIES:
        path = pathlib.Path(target)
        if not path.is_dir() or path.is_symlink() or str(path.resolve()) != target:
            raise RuntimeError('Cleanup directory is missing or not its exact original path')
        for container in running:
            for mount in container['Mounts']:
                source = mount['Source']
                if source == target or source.startswith(target + '/') or target.startswith(source + '/'):
                    raise RuntimeError('A running container references a cleanup directory')
    removable = []
    for name in CONTAINERS:
        container = layout.inspect(name)
        if container['State']['Status'] != 'exited' or container['Config']['Image'] != 'oven/bun:1':
            raise RuntimeError('A build container is no longer the stopped baseline')
        removable.append(container['Id'])
    for volume in VOLUMES:
        if layout.run(['docker', 'ps', '-aq', '--filter', 'volume=' + volume]).strip():
            raise RuntimeError('A build-cache volume is still referenced')
    archives = [layout.LEGACY / 'artifacts' / name for name in ARCHIVES]
    for path in archives:
        if not path.is_file() or path.is_symlink() or str(path.resolve()) != str(path):
            raise RuntimeError('Archive target is not an exact regular file')
    plan = {'dry_run': not args.execute, 'directories': DIRECTORIES,
            'stopped_build_containers': CONTAINERS, 'cache_volumes': VOLUMES,
            'old_archives': [str(path) for path in archives],
            'archive_bytes': sum(path.stat().st_size for path in archives),
            'production_images_and_data_preserved': True}
    print(json.dumps(plan, sort_keys=True), flush=True)
    if not args.execute:
        return
    layout.private_write(directory / 'cleanup-plan.json', json.dumps(plan, indent=2))
    before = shutil.disk_usage('/').free
    # No --force and no -v: Docker rejects any container that started meanwhile.
    layout.run(['docker', 'rm'] + removable, timeout=120)
    for volume in VOLUMES:
        layout.run(['docker', 'volume', 'rm', volume], timeout=120)
    for target in DIRECTORIES:
        shutil.rmtree(target)
    for path in archives:
        path.unlink()
    after = shutil.disk_usage('/').free
    layout.save_result(directory, 'cleanup-result.json', {
        'removed_build_containers': len(removable), 'removed_cache_volumes': len(VOLUMES),
        'removed_dependency_directories': len(DIRECTORIES), 'removed_old_archives': len(archives),
        'free_bytes_before': before, 'free_bytes_after': after,
        'approximate_freed_bytes': after - before,
        'source_worktrees_and_rollback_images_deleted': False,
        'production_data_or_logs_deleted': False})


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('Cleanup stopped: ' + str(error), file=sys.stderr)
        sys.exit(1)

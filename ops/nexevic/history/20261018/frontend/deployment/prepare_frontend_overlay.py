#!/usr/bin/env python3
"""Render reviewable Caddy candidates only; never reload Caddy or change services.

Run after the completed dist is copied to an immutable, publicly readable
/srv/new-api-frontend/releases/<release> directory. This program only reads
inputs and writes new output files in --output-dir. Python 3.6 compatible.
"""
import argparse
import hashlib
import json
import os
import pathlib
import re


API_BLOCK = re.compile(
    r"(?m)^api\.nexevic\.com\s*\{\s*"
    r"encode zstd gzip\s*reverse_proxy 127\.0\.0\.1:3019\s*\}"
)
BACKEND_PREFIXES = ('/api', '/v1', '/v1beta', '/pg', '/mj', '/suno', '/kling', '/jimeng')
PUBLIC_ROOT_FILES = frozenset(('favicon.ico', 'logo.png', 'pay-apple.png',
                               'pay-card.png', 'pay-google.png',
                               'waffo-logo-dark.svg', 'waffo-logo-light.svg'))


def frontend_pattern(route_source):
    match = re.search(r'export interface FileRoutesByFullPath\s*\{(.*?)\n\}',
                      route_source, re.S)
    if not match:
        raise ValueError('Expected generated FileRoutesByFullPath block')
    routes = sorted(set(re.findall(r"^\s*'([^']+)':", match.group(1), re.M)))
    if not routes or '/' not in routes or '/dashboard/$section' not in routes:
        raise ValueError('Missing expected frontend routes')
    patterns = []
    for path in routes + ['/index.html']:
        if path == '/index.html':
            patterns.append('/index[.]html')
            continue
        if not re.fullmatch(r'/[A-Za-z0-9_/$-]*', path):
            raise ValueError('Unexpected route syntax: ' + path)
        if any(path == p or path.startswith(p + '/') for p in BACKEND_PREFIXES):
            raise ValueError('API prefix unexpectedly present in frontend route list')
        if path == '/':
            patterns.append('/')
            continue
        parts = path.rstrip('/').split('/')[1:]
        # Parameters match one segment only, not arbitrary path descendants.
        patterns.append('/' + '/'.join('[^/]+' if p.startswith('$') else p
                                       for p in parts) + '/?')
    return '^(' + '|'.join(patterns) + ')$', routes


def render_block(release_root, route_pattern, assets, serve_new_html):
    lines = [
        'api.nexevic.com {',
        '\tencode zstd gzip',
        '\t@frontend_static {',
        '\t\tmethod GET HEAD',
        '\t\tpath ' + ' '.join(assets),
        '\t}',
    ]
    if serve_new_html:
        lines.extend([
            '\t@frontend_ui {',
            '\t\tmethod GET HEAD',
            '\t\tpath_regexp frontend_ui ' + route_pattern,
            '\t}',
        ])
    lines.extend(['\troute {', '\t\troot * ' + release_root])
    if serve_new_html:
        lines.extend([
            '\t\thandle @frontend_ui {',
            '\t\t\theader Cache-Control "no-cache"',
            '\t\t\trewrite * /index.html',
            '\t\t\tfile_server',
            '\t\t}',
        ])
    lines.extend([
        '\t\troute @frontend_static {',
        '\t\t\tfile_server {',
        '\t\t\t\tpass_thru',
        '\t\t\t}',
        '\t\t}',
        '\t\treverse_proxy 127.0.0.1:3019',
        '\t}',
        '}',
    ])
    return '\n'.join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--caddy-source', required=True)
    parser.add_argument('--route-tree', required=True)
    parser.add_argument('--dist', required=True)
    parser.add_argument('--release-root', required=True)
    parser.add_argument('--output-dir', required=True)
    args = parser.parse_args()
    source = pathlib.Path(args.caddy_source).read_text(encoding='utf-8-sig')
    matches = list(API_BLOCK.finditer(source))
    if len(matches) != 1 or source.count('api.nexevic.com') != 1:
        raise ValueError('API site differs from reviewed baseline; review new configuration')
    if not re.fullmatch(r'/srv/new-api-frontend/releases/[A-Za-z0-9_-]+', args.release_root):
        raise ValueError('Use one immutable /srv/new-api-frontend/releases/<name> directory')
    dist = pathlib.Path(args.dist).resolve()
    if not (dist / 'index.html').is_file() or not (dist / 'static').is_dir():
        raise ValueError('A completed frontend dist with index.html and static/ is required')
    entries = sorted(dist.rglob('*'))
    for entry in entries:
        if entry.is_symlink() or entry.name.startswith('.'):
            raise ValueError('Release must contain only public build files, without symlinks/dotfiles')
    root_files = sorted('/' + p.name for p in dist.iterdir()
                        if p.is_file() and p.name != 'index.html')
    if any(p[1:] not in PUBLIC_ROOT_FILES for p in root_files):
        raise ValueError('Unexpected public root file; review the explicit public asset allowlist')
    root_dirs = [p.name for p in dist.iterdir() if p.is_dir()]
    if root_dirs != ['static']:
        raise ValueError('Unexpected public directory; review asset routing')
    pattern, routes = frontend_pattern(pathlib.Path(args.route_tree).read_text(encoding='utf-8-sig'))
    assets = ['/static/*'] + root_files
    replacements = {
        'Caddyfile.frontend-candidate': render_block(args.release_root, pattern, assets, True),
        # First rollback restores embedded old HTML but keeps new hashed chunks
        # reachable for browser tabs already running the new frontend.
        'Caddyfile.frontend-rollback': render_block(args.release_root, pattern, assets, False),
    }
    output = pathlib.Path(args.output_dir).resolve()
    if output == dist or dist in output.parents:
        raise ValueError('Private candidate files must not be inside the public release')
    bodies = dict((name, source[:matches[0].start()] + block + source[matches[0].end():])
                  for name, block in replacements.items())
    bodies['Caddyfile.frontend-original'] = source
    report = {
        'production_changed': False,
        'backend': '127.0.0.1:3019',
        'release_root': args.release_root,
        'ui_routes': routes,
        'public_asset_matchers': assets,
        'original_config_sha256': hashlib.sha256(source.encode()).hexdigest(),
        'files': dict((p.relative_to(dist).as_posix(), hashlib.sha256(p.read_bytes()).hexdigest())
                      for p in entries if p.is_file()),
        'checks_required': ['caddy validate candidate and rollback',
                            'isolated Caddy API method/body/query passthrough',
                            'isolated in-flight SSE across reload',
                            'old and new lazy chunks remain reachable',
                            'logged-in browser 29-day and 7-day query/date/total checks'],
    }
    bodies['frontend-overlay-report.json'] = json.dumps(report, indent=2)
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    for name, body in bodies.items():
        p = output / name
        descriptor = os.open(str(p), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, 'w', encoding='utf-8') as handle:
            handle.write(body)
    print('Prepared candidate, staged rollback, original backup, and manifest; no reload or deployment performed.')


if __name__ == '__main__':
    main()

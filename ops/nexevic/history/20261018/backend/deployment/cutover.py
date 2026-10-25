#!/usr/bin/env python3
import pathlib,json,subprocess,urllib.request,time,re,os
ROOT=pathlib.Path('/home/deploy/worktrees/new-api-routing-20261018')
BACKUP=pathlib.Path('/home/deploy/deploy_new_api/backups/routing-20261018')
s=json.loads(urllib.request.urlopen('http://127.0.0.1:3019/api/status',timeout=5).read().decode())
assert s['success'] and s['data']['version']=='b5bedec2d-routing-20261018'
subprocess.check_call(['docker','image','inspect','localhost/new-api:nexevic-routing-compatible-rollback-20261018'],stdout=subprocess.DEVNULL)
subprocess.check_call(['systemctl','stop','new-api-channel-scheduler.timer'])
for n in range(60):
 state=subprocess.check_output(['systemctl','show','new-api-channel-scheduler.service','--property=ActiveState','--value']).decode().strip()
 if state in ('inactive','failed'):break
 time.sleep(1)
else:raise RuntimeError('Old scheduler still running; ingress left unchanged')
path=pathlib.Path('/etc/caddy/Caddyfile')
source=path.read_text();info=path.stat()
assert source.count('reverse_proxy 127.0.0.1:3000')==1,'unexpected proxy layout'
assert re.search(r'api\.nexevic\.com\s*\{\s*encode zstd gzip\s*reverse_proxy 127\.0\.0\.1:3000\s*\}',source),'unexpected API block'
saved=BACKUP/'Caddyfile.at-cutover';saved.write_text(source);saved.chmod(0o600)
prepared=path.with_name('Caddyfile.routing-prepared')
prepared.write_text(source.replace('reverse_proxy 127.0.0.1:3000','reverse_proxy 127.0.0.1:3019'))
os.chmod(str(prepared),info.st_mode&0o777);os.chown(str(prepared),info.st_uid,info.st_gid)
subprocess.check_call(['caddy','validate','--config',str(prepared),'--adapter','caddyfile'],stdout=subprocess.DEVNULL)
os.replace(str(prepared),str(path))
subprocess.check_call(['systemctl','reload','caddy'])
subprocess.check_call(['docker','update','--restart=no','new-api'],stdout=subprocess.DEVNULL)
(ROOT/'deployment/cutover.json').write_text(json.dumps({'time':int(time.time()),'active_port':3019,'old_container':'new-api','old_background_still_owner':True},indent=2))
print('Ingress reloaded onto validated new instance; old accepted requests/background tasks can finish')

#!/usr/bin/env python3
import pathlib,subprocess,os,json,time
BASE=pathlib.Path('/home/deploy/deploy_new_api')
ROOT=pathlib.Path('/home/deploy/worktrees/new-api-routing-20261018')
old=json.loads(subprocess.check_output(['docker','inspect','new-api']).decode())[0]
assert not old['State']['Running'],'old master must exit before background handover'
assert old['State']['ExitCode']==0,'old master must have exited cleanly'
assert (ROOT/'deployment/config-applied.json').exists(),'final routing verification record missing'
# Keep future compose/reboot behavior aligned with this deployment.
canonical=BASE/'docker-compose.yml'
backup=BASE/'backups/routing-20261018/docker-compose.at-handover.yml'
backup.write_bytes(canonical.read_bytes());backup.chmod(0o600)
tmp=canonical.with_name('docker-compose.routing-next.yml')
tmp.write_bytes((ROOT/'deployment/compose-final.json').read_bytes());tmp.chmod(0o600)
os.replace(str(tmp),str(canonical))
scheduler=pathlib.Path('/home/deploy/new_api_channel_scheduler')
p=scheduler/'scheduler.py';p.write_bytes((ROOT/'scheduler/scheduler.py').read_bytes());p.chmod(0o755)
subprocess.check_call(['chown','deploy:deploy',str(p)])
envfile=scheduler/'scheduler.env'
values=[]
for line in envfile.read_text().splitlines():
 if line.startswith(('API_BASE=','NEXEVIC_GLM_PEAK_CHANNEL_IDS=','NEXEVIC_LEGACY_SCHEDULER_CHANNEL_IDS=')):continue
 values.append(line)
values.extend(['API_BASE=http://127.0.0.1:3019','NEXEVIC_GLM_PEAK_CHANNEL_IDS=6,7,14,18','NEXEVIC_LEGACY_SCHEDULER_CHANNEL_IDS=6,7'])
envfile.write_text('\n'.join(values)+'\n');envfile.chmod(0o600)
subprocess.check_call(['chown','deploy:deploy',str(envfile)])
unit=pathlib.Path('/etc/systemd/system/new-api-channel-scheduler.service')
unit.write_text('''[Unit]
Description=Recover system-disabled New API channels with version checks
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=oneshot
User=deploy
Group=deploy
WorkingDirectory=/home/deploy/new_api_channel_scheduler
EnvironmentFile=/home/deploy/new_api_channel_scheduler/scheduler.env
Environment=STATE_PATH=/var/lib/new-api-channel-scheduler/state.json
Environment=LOCK_PATH=/var/lib/new-api-channel-scheduler/scheduler.lock
Environment=TZ=Asia/Shanghai
ExecStart=/usr/bin/python3 /home/deploy/new_api_channel_scheduler/scheduler.py
StateDirectory=new-api-channel-scheduler
StateDirectoryMode=0700
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=/var/lib/new-api-channel-scheduler
''')
unit.chmod(0o644)
subprocess.check_call(['systemctl','daemon-reload'])
# Old instance has exited. Only this master may begin periodic maintenance.
marker=BASE/'routing-20261018/handover/start'
marker.write_text('ownership transferred '+str(int(time.time()))+'\n');marker.chmod(0o600)
subprocess.check_call(['systemctl','start','new-api-channel-scheduler.service'])
state=json.loads(pathlib.Path('/var/lib/new-api-channel-scheduler/state.json').read_text())
assert not any(str(i) in state.get('legacy_managed',{}) for i in (6,7)),'legacy ownership adoption incomplete'
# Disable one-time legacy adoption from the scheduler after verified success.
envfile.write_text('\n'.join(x for x in envfile.read_text().splitlines() if not x.startswith('NEXEVIC_LEGACY_SCHEDULER_CHANNEL_IDS='))+'\n')
subprocess.check_call(['systemctl','enable','--now','new-api-channel-scheduler.timer'])
(ROOT/'deployment/handover.json').write_text(json.dumps({'time':int(time.time()),'old_exit_code':0,'maintenance_owner':'new-api-routing','recovery_timer_active':True},indent=2))
print('Canonical configuration updated; maintenance/recovery ownership transferred; verified legacy quota records adopted')

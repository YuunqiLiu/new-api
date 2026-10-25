#!/usr/bin/env python3
"""Prepare isolated/final-master runtime descriptions without starting production."""
import json, pathlib, subprocess, os
ROOT=pathlib.Path('/home/deploy/worktrees/new-api-routing-20261018')
BASE=pathlib.Path('/home/deploy/deploy_new_api')
OUT=ROOT/'deployment'
OUT.mkdir(parents=True,exist_ok=True)
os.chmod(str(OUT),0o700)
original=json.loads(subprocess.check_output(['docker','compose','-f',str(BASE/'docker-compose.yml'),'config','--format','json']).decode())
service=original['services']['new-api']
app=json.loads(json.dumps(service))
app['image']='localhost/new-api:nexevic-routing-20261018'
app['container_name']='new-api-routing'
app['ports']=[{'target':3000,'published':'3019','host_ip':'127.0.0.1','protocol':'tcp'}]
app['restart']='always'
app['stop_grace_period']='24h'
app['environment'].update({'NODE_TYPE':'master','NODE_NAME':'new-api-routing-20261018','SKIP_DB_MIGRATION':'true','BACKGROUND_TASKS_START_FILE':'/handover/start','ACCOUNTING_FAILURE_DIR':'/accounting-failures','CHANNEL_TEST_ENABLED':'false','NEXEVIC_GLM_PEAK_CHANNEL_IDS':'6,7,14,18','NEXEVIC_LEGACY_SCHEDULER_CHANNEL_IDS':'6,7','SHUTDOWN_TIMEOUT_SECONDS':'600'})
app['environment'].pop('CHANNEL_TEST_FREQUENCY',None)
for mount in app.get('volumes',[]):
 if mount.get('target')=='/app/logs':mount['source']=str(BASE/'routing-20261018/logs')
for folder in ('logs','handover','accounting-failures'):
 p=BASE/'routing-20261018'/folder;p.mkdir(parents=True,exist_ok=True);p.chmod(0o700)
app['volumes'].extend([{'type':'bind','source':str(BASE/'routing-20261018/handover'),'target':'/handover'},{'type':'bind','source':str(BASE/'routing-20261018/accounting-failures'),'target':'/accounting-failures'}])
original['services']['new-api-routing']=app
# Only this service is started explicitly; the old master is left untouched.
prepared=OUT/'compose-prepared.json';prepared.write_text(json.dumps(original,indent=2));prepared.chmod(0o600)
final=json.loads(json.dumps(original));del final['services']['new-api']
canonical=OUT/'compose-final.json';canonical.write_text(json.dumps(final,indent=2));canonical.chmod(0o600)
# Build only replaces /new-api. The existing compatible frontend and base stay.
(ROOT/'Dockerfile.routing').write_text('FROM localhost/new-api:nexevic-baidu-responses-20261014\nCOPY artifacts/new-api-routing /new-api\n')
print('Prepared protected runtime descriptions; production not started or changed')

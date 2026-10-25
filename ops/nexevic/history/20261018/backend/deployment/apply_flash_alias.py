#!/usr/bin/env python3
import pathlib,json,sys,datetime
sys.path.insert(0,str(pathlib.Path(__file__).parent))
from apply_config import API,load_env,ENV_FILE,get_channel,list_channels
api=API(load_env(ENV_FILE)['API_TOKEN'])
legacy='deepseek-v4-flash';latest='deepseek-v4.1-flash'
backup=pathlib.Path('/home/deploy/deploy_new_api/backups/routing-20261018/config/legacy-flash-alias.json')
assert not backup.exists(),'Alias update already journaled; inspect before rerun'
record={'authorized_alias':{legacy:latest},'started_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'before':{},'after':{}}
for cid in (3,19,15,4):
 c=get_channel(api,cid)
 record['before'][str(cid)]={k:c.get(k) for k in ('id','status','status_version','models','model_mapping','priority')}
backup.write_text(json.dumps(record,ensure_ascii=False,indent=2));backup.chmod(0o600)
for cid in (3,19,15,4):
 c=get_channel(api,cid)
 models=c['models'].split(',')
 if legacy not in models:models.append(legacy)
 mapping=json.loads(c.get('model_mapping') or '{}')
 mapping[legacy]=mapping.get(latest,latest)
 patch={'id':cid,'models':','.join(models),'model_mapping':json.dumps(mapping,ensure_ascii=False)}
 if cid in (3,19):
  settings=json.loads(c.get('settings') or '{}')
  for route in settings['advanced_custom']['advanced_routes']:
   if route.get('models') and latest in route['models'] and legacy not in route['models']:route['models'].append(legacy)
  patch['settings']=json.dumps(settings,ensure_ascii=False)
 if cid==4:
  # An existing enabled team subscription at priority 0 remains available.
  # Paid official fallback must rank below every currently eligible subscription.
  all_channels=list_channels(api)
  other=[x['priority'] for i,x in all_channels.items() if i!=4 and any(m in x.get('models','').split(',') for m in (legacy,latest,'deepseek-v4-pro'))]
  patch['priority']=min([-1]+other)-1
 api.request('PUT','/api/channel/',patch)
 updated=get_channel(api,cid)
 assert all(updated.get(k)==v for k,v in patch.items() if k!='id'),'read-back mismatch'
 assert updated['status']==c['status'],'channel status changed during alias update'
 record['after'][str(cid)]={k:updated.get(k) for k in ('id','status','status_version','models','model_mapping','priority')}
 backup.write_text(json.dumps(record,ensure_ascii=False,indent=2))
 print('Verified legacy Flash alias on channel',cid,'priority',updated['priority'])
record['completed_at']=datetime.datetime.now(datetime.timezone.utc).isoformat();backup.write_text(json.dumps(record,ensure_ascii=False,indent=2))
print('Legacy client model now uses latest Flash across all subscription routes; official remains final fallback')

#!/usr/bin/env python3
import pathlib,json,time,threading,subprocess,sys
sys.path.insert(0,str(pathlib.Path(__file__).parent))
from integration_test import Validation,sql,check
root=pathlib.Path(__file__).parent
v=Validation(json.loads((root/'test-credentials.json').read_text()))
v.set_status(31,1)
v.control({'31':{'mode':'ok','hold':'shutdown-stream'}})
time.sleep(3)
before=[int(i) for i in sql('SELECT quota,used_quota,request_count FROM users WHERE id=1;').split('|')]
result={}
def run():
 try:result['response']=v.relay(stream=True,pin=31)
 except Exception as e:result['error']=type(e).__name__
t=threading.Thread(target=run);t.start()
for i in range(100):
 if 'shutdown-stream' in v.state()['waiting']:break
 time.sleep(.1)
else:raise RuntimeError('mock did not receive draining request')
subprocess.check_call(['docker','kill','--signal=TERM','new-api-routing-validation-app'],stdout=subprocess.DEVNULL)
time.sleep(2)
running=subprocess.check_output(['docker','inspect','-f','{{.State.Running}}','new-api-routing-validation-app']).decode().strip()
check(running=='true','candidate exited while generation was held')
v.control(reset=False,release=['shutdown-stream'])
t.join(30)
check(not t.is_alive() and 'error' not in result,'draining request did not finish')
check(result['response'][0]==200 and '[DONE]' in result['response'][1],'SSE did not finish during drain')
for i in range(40):
 running=subprocess.check_output(['docker','inspect','-f','{{.State.Running}}','new-api-routing-validation-app']).decode().strip()
 if running=='false':break
 time.sleep(1)
else:raise RuntimeError('candidate did not finish drain')
exitcode=subprocess.check_output(['docker','inspect','-f','{{.State.ExitCode}}','new-api-routing-validation-app']).decode().strip()
check(exitcode=='0','candidate exit was not clean')
after=[int(i) for i in sql('SELECT quota,used_quota,request_count FROM users WHERE id=1;').split('|')]
check(after[0]<before[0] and after[1]>before[1] and after[2]==before[2]+1,'inflight request accounting not flushed exactly once')
print('Graceful shutdown: held SSE completed, accounting persisted once, clean exit')
(root/'shutdown-results.json').write_text(json.dumps({'passed':True,'stream_completed':True,'accounting_flushed_once':True,'exit_code':0}))

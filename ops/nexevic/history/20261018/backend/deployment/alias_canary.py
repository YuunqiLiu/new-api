#!/usr/bin/env python3
import pathlib,sys,argparse
sys.path.insert(0,str(pathlib.Path(__file__).parent))
from canary import API,load_env,ENV_FILE,execute
api=API('http://127.0.0.1:3019',load_env(ENV_FILE)['API_TOKEN'])
cases=[{'channel':cid,'model':'deepseek-v4-flash','stream':True,'tool':False} for cid in (3,19,15)]
result=execute(api,argparse.Namespace(stage='alias'),cases)
if result is False:raise SystemExit(1)

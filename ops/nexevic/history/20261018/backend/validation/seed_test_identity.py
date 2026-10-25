#!/usr/bin/env python3
"""Seed only the dedicated empty routing_test validation database."""
from __future__ import print_function
import argparse
import json
import os
import re
import subprocess
import time
import urllib.parse

CONTAINER = 'new-api-routing-test-db'


def sql(statement):
    command = ['docker', 'exec', '-i', CONTAINER, 'psql', '-X', '-U', 'routing_test',
               '-d', 'routing_test', '-v', 'ON_ERROR_STOP=1', '-t', '-A']
    process = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    output, error = process.communicate(statement.encode('utf-8'))
    if process.returncode:
        # A PostgreSQL error can quote SQL literals. Never echo it because the
        # INSERT contains the private test tokens.
        raise RuntimeError('isolated identity seed SQL failed (details suppressed to protect test credentials)')
    return output.decode('utf-8').strip()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--credentials', default=os.path.join(os.path.dirname(__file__), 'test-credentials.json'))
    args = parser.parse_args()
    with open(args.credentials) as source:
        config = json.load(source)
    app = urllib.parse.urlparse(config['app_base'])
    if app.hostname not in ('127.0.0.1', 'localhost') or app.port != 3018:
        raise RuntimeError('refusing credentials for a non-validation app')
    admin = config['admin_token']
    relay = config['relay_token']
    if not re.match(r'^[0-9a-fA-F]{32}$', admin) or not re.match(r'^[0-9a-fA-F]{48}$', relay):
        raise RuntimeError('test tokens must be fresh random 32/48-hex values')
    if sql('SELECT current_database();') != 'routing_test':
        raise RuntimeError('wrong database')
    existing = sql("SELECT count(*) FROM users WHERE id <> 1 OR username NOT IN ('root','validation-root');")
    if existing != '0':
        raise RuntimeError('refusing to seed database containing non-fixture users')
    statement = """
BEGIN;
INSERT INTO users (id,username,password,display_name,role,status,access_token,quota,used_quota,request_count,"group",aff_code,setting,auth_version,created_at)
VALUES (1,'validation-root','!password-login-disabled!','Validation Root',100,1,'%s',100000000,0,0,'default','validation-root','{}',1,%d)
ON CONFLICT (id) DO UPDATE SET username=EXCLUDED.username,password=EXCLUDED.password,display_name=EXCLUDED.display_name,role=100,status=1,access_token=EXCLUDED.access_token,quota=100000000,used_quota=0,request_count=0,"group"='default',setting='{}',auth_version=1,deleted_at=NULL;
INSERT INTO tokens (id,user_id,key,status,name,created_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,"group",cross_group_retry,auto_groups)
VALUES (1,1,'%s',1,'validation-token',%d,-1,100000000,true,false,'','',0,'default',false,'[]')
ON CONFLICT (id) DO UPDATE SET user_id=1,key=EXCLUDED.key,status=1,name=EXCLUDED.name,expired_time=-1,remain_quota=100000000,unlimited_quota=true,model_limits_enabled=false,model_limits='',allow_ips='',used_quota=0,"group"='default',cross_group_retry=false,auto_groups='[]',deleted_at=NULL;
SELECT setval(pg_get_serial_sequence('users','id'), GREATEST((SELECT max(id) FROM users),1), true);
SELECT setval(pg_get_serial_sequence('tokens','id'), GREATEST((SELECT max(id) FROM tokens),1), true);
COMMIT;
""" % (admin, int(time.time()), relay, int(time.time()))
    sql(statement)
    print('Seeded isolated validation user/token id 1; credentials not printed')


if __name__ == '__main__':
    main()

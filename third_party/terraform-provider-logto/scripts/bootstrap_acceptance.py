#!/usr/bin/env python3
"""Bootstrap or verify credentials for an explicitly disposable loopback Logto."""
import argparse
import base64
import json
import os
from pathlib import Path
import secrets
import subprocess
import urllib.parse
import urllib.request
import ipaddress


def loopback(url):
    parsed = urllib.parse.urlparse(url)
    try:
        allowed = ipaddress.ip_address(parsed.hostname).is_loopback
    except (ValueError, TypeError):
        allowed = False
    if not allowed:
        raise SystemExit('Acceptance endpoints must use explicit loopback IP addresses')
    return parsed


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def verify(data):
    request = urllib.request.Request(
        data['endpoint'] + '/oidc/token',
        data=urllib.parse.urlencode({'grant_type': 'client_credentials', 'resource': data['resource'], 'scope': 'all'}).encode(),
        headers={
            'Authorization': 'Basic ' + base64.b64encode((data['application_id'] + ':' + data['application_secret']).encode()).decode(),
            'Content-Type': 'application/x-www-form-urlencoded',
        },
    )
    with urllib.request.build_opener(NoRedirect()).open(request, timeout=15) as response:
        token = json.load(response)
    if not token.get('access_token') or token.get('token_type') != 'Bearer':
        raise SystemExit('Invalid management token response')
    print('Disposable Logto management credentials verified')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage', choices=['bootstrap', 'verify'], default='bootstrap')
    parser.add_argument('--credentials', type=Path, default=Path('.work/logto-credentials.json'))
    args = parser.parse_args()
    endpoint = os.environ.get('LOGTO_TEST_ENDPOINT', 'http://127.0.0.1:13001')
    parsed = loopback(endpoint)
    destination = args.credentials.resolve()
    if not destination.is_relative_to(Path.cwd().resolve()):
        parser.error('credential output must stay inside the current workspace')
    if destination.exists():
        data = json.loads(destination.read_text())
        loopback(data['endpoint'])
        verify(data)
        return
    if args.stage == 'verify':
        parser.error('bootstrap credentials first')
    database = os.environ.get('LOGTO_TEST_DB_URL', '')
    if not database:
        parser.error('LOGTO_TEST_DB_URL must reference a disposable local database')
    loopback(database)
    data = {
        'endpoint': endpoint.rstrip('/'), 'hostname': parsed.netloc,
        'application_id': 'acceptance-manager', 'application_secret': secrets.token_urlsafe(32),
        'resource': 'https://default.logto.app/api',
    }
    psql = os.environ.get('PSQL', 'psql')
    name = 'Disposable acceptance manager'
    query = "SELECT name FROM applications WHERE id='acceptance-manager';"
    existing = subprocess.run([psql, database, '-At', '-v', 'ON_ERROR_STOP=1'], input=query, text=True, capture_output=True, check=True).stdout.strip()
    if existing and existing != name:
        raise SystemExit('Refusing to modify an application not owned by the acceptance harness')
    # Generated URL-safe secret and constant IDs are sent over stdin, never argv.
    sql = "BEGIN; INSERT INTO applications (tenant_id,id,name,secret,type,oidc_client_metadata) VALUES ('default','acceptance-manager','" + name + "','" + data['application_secret'] + "','MachineToMachine','{\"redirectUris\":[],\"postLogoutRedirectUris\":[]}') ON CONFLICT (id) DO UPDATE SET secret=EXCLUDED.secret; INSERT INTO applications_roles (tenant_id,id,application_id,role_id) VALUES ('default','acceptance-role','acceptance-manager','admin-role') ON CONFLICT DO NOTHING; COMMIT;"
    subprocess.run([psql, database, '-v', 'ON_ERROR_STOP=1'], input=sql, text=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, check=True)
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_suffix('.partial')
    with os.fdopen(os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), 'w') as output:
        json.dump(data, output)
    temporary.replace(destination)
    verify(data)


if __name__ == '__main__':
    main()

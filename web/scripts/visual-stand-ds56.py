"""Build/start the real API on loopback with ephemeral memory data and synthetic roles."""
import argparse
import json
import os
from pathlib import Path
import secrets
import signal
import subprocess
import time
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--temp-dir', required=True)
parser.add_argument('--port', type=int, default=18057)
args = parser.parse_args()
repo = Path(__file__).resolve().parents[2]
temp = Path(args.temp_dir).resolve()
temp.mkdir(mode=0o700, parents=True, exist_ok=True)
temp.chmod(0o700)
for command in ['api', 'dev-token']:
    subprocess.run(['go', 'build', '-o', str(temp / command), './cmd/' + command], cwd=repo, check=True)
env = {key: value for key, value in os.environ.items() if not key.startswith('METIS_')}
env.update(METIS_AUTH_MODE='hmac', METIS_HMAC_SECRET=secrets.token_hex(32),
           METIS_HMAC_ISSUER='metis-ds56-visual', METIS_STORAGE='memory', METIS_SEED='true',
           METIS_HTTP_ADDR=f'127.0.0.1:{args.port}', METIS_OTEL_EXPORTER='none',
           METIS_WEB_DIR=str(repo / 'web/dist'))
profiles = [('admin', 'admin', '', 'full'), ('cpo', 'cpo', '', 'full'),
            ('pm', 'pm', 'soar', 'none'), ('pm-other', 'pm', 'edr', 'none'),
            ('dev_lead', 'dev_lead', '', 'none'), ('marketing', 'marketing', '', 'none'),
            ('finance', 'finance', '', 'full'), ('finance-aggregates', 'finance', '', 'aggregates'),
            ('finance-none', 'finance', '', 'none'), ('compliance', 'compliance', '', 'none'),
            ('presale', 'presale', '', 'none')]
tokens = {}
for name, role, products, finance in profiles:
    tokens[name] = subprocess.check_output([str(temp / 'dev-token'), '-subject', 'visual-' + name,
                                           '-roles', role, '-products', products, '-finance', finance,
                                           '-ttl', '2h'], env=env, text=True).strip()
token_file = temp / 'tokens.json'
with open(token_file, 'w', opener=lambda name, flags: os.open(name, flags, 0o600)) as handle:
    json.dump(tokens, handle)
base = f'http://127.0.0.1:{args.port}'
with (temp / 'api.log').open('w') as log:
    server = subprocess.Popen([str(temp / 'api')], env=env, stdout=log, stderr=subprocess.STDOUT)
    signal.signal(signal.SIGTERM, lambda *_: server.send_signal(signal.SIGINT))
    signal.signal(signal.SIGINT, lambda *_: server.send_signal(signal.SIGINT))
    try:
        for _ in range(100):
            if server.poll() is not None:
                raise RuntimeError('API did not start; inspect the temporary api.log')
            try:
                urllib.request.urlopen(urllib.request.Request(base + '/api/v1/me', headers={'Authorization': 'Bearer ' + tokens['admin']}), timeout=1).close()
                break
            except OSError:
                time.sleep(0.1)
        else:
            raise RuntimeError('API startup timed out')
        fixture_env = dict(os.environ, METIS_ACCEPTANCE_URL=base, METIS_ACCEPTANCE_TEMP=str(temp))
        for fixture in ['seed-visual-ds56.py', 'seed-finance-visual-ds56.py']:
            subprocess.run(['python3', str(repo / 'web/scripts' / fixture)], env=fixture_env, check=True)
        print(f'DS56 stand ready at {base}; private role credentials in {temp}. Rebuild dist BEFORE starting the stand.', flush=True)
        server.wait()
    finally:
        if server.poll() is None:
            server.send_signal(signal.SIGINT)
            server.wait(timeout=15)
        token_file.unlink(missing_ok=True)

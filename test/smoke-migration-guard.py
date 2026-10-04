#!/usr/bin/env python3
"""Real-binary migration/daemon exclusion; retain a repeatable JSON artifact."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--bin-dir', default=str(Path(__file__).resolve().parents[1] / 'bin'))
parser.add_argument('--artifact-dir')
args = parser.parse_args()
bins = Path(args.bin_dir)
root = Path(args.artifact_dir or tempfile.mkdtemp(prefix='wrkq-migration-guard-'))
root.mkdir(parents=True, exist_ok=True)
path = root / 'scratch.db'
catalog = root / 'hook-catalog.json'
catalog.write_text('{"schemaVersion":"wrkf.hook-catalog.v0","hooks":{}}')
env = {k: v for k, v in os.environ.items() if not k.startswith(('WRKQ', 'WRKF', 'HRC_'))}
env['WRKF_HOOK_CATALOG'] = str(catalog)
results = []

def adm(*flags, db=path):
    p = subprocess.run([str(bins / 'wrkqadm'), '--db', str(db), 'migrate', *flags],
                       env=env, capture_output=True, text=True, timeout=15)
    results.append({'command': p.args, 'exit': p.returncode, 'stdout': p.stdout, 'stderr': p.stderr})
    return p

def state():
    return {str(p): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in [path, Path(str(path) + '-wal'), Path(str(path) + '-shm')] if p.exists()}

assert adm().returncode == 0
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    port = sock.getsockname()[1]
log = open(root / 'daemon.log', 'w')
daemon = subprocess.Popen([str(bins / 'wrkqd'), '--db', str(path), '--addr', f'127.0.0.1:{port}'],
                          env=env, stdout=log, stderr=log)
try:
    for _ in range(100):
        try:
            with urllib.request.urlopen(f'http://127.0.0.1:{port}/v1/health', timeout=1) as r:
                assert json.load(r)['ok']
            break
        except OSError:
            assert daemon.poll() is None, (root / 'daemon.log').read_text()
            time.sleep(.1)
    else:
        raise AssertionError('daemon did not become healthy')
    before = state()
    alias = root / 'symlink.db'
    alias.symlink_to(path)
    for db in [path, alias, Path(os.path.relpath(path))]:
        p = adm(db=db)
        assert p.returncode != 0 and 'MIGRATION_DATABASE_SERVING' in p.stderr, p
        assert str(daemon.pid) in p.stderr and 'bootout' in p.stderr and 'bootstrap' in p.stderr, p.stderr
        assert state() == before, 'refusal changed SQLite main/WAL/SHM'
    for flags in [('--dry-run',), ('--status',)]:
        p = adm(*flags)
        assert p.returncode == 0, p
    before_init = state()
    p = subprocess.run([str(bins / 'wrkqadm'), '--db', str(path), 'init'],
                       env=env, capture_output=True, text=True)
    assert p.returncode != 0 and 'MIGRATION_DATABASE_SERVING' in p.stderr, p
    assert state() == before_init, 'init refusal changed SQLite main/WAL/SHM'
    results.append({'init_refusal': p.stderr})
    hardlink = root / 'hardlink.db'
    os.link(path, hardlink)
    p = adm(db=hardlink)
    assert p.returncode != 0 and 'DATABASE_HARDLINK_UNSUPPORTED' in p.stderr, p
    hardlink.unlink()
finally:
    daemon.terminate()
    daemon.wait(timeout=15)
    log.close()
assert adm('--dry-run').returncode == 0
assert adm().returncode == 0
# Simulate a migrator holding its lease before SQLite is opened. Startup must fail.
with open(str(path) + '.migration-lock', 'a+') as lease:
    fcntl.flock(lease, fcntl.LOCK_EX | fcntl.LOCK_NB)
    p = subprocess.run([str(bins / 'wrkqd'), '--db', str(path), '--addr', f'127.0.0.1:{port}'],
                       env=env, capture_output=True, text=True, timeout=10)
    assert p.returncode != 0 and 'DATABASE_LIFECYCLE_BUSY' in p.stderr, p
    results.append({'startup_during_migration': p.stderr})
(root / 'result.json').write_text(json.dumps({'passed': True, 'results': results}, indent=2))
print(f'PASS migration guard; artifact: {root}/result.json')

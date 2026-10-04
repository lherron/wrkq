#!/usr/bin/env python3
"""Isolated real-shell hook runs; no publisher can reach the project ledger."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time

SOURCE = Path(__file__).resolve().parents[1]
BASE_ENV = {k: v for k, v in os.environ.items() if not k.startswith('GIT_')}
REFS = 'refs/heads/main abc refs/heads/main def\nrefs/heads/other 123 refs/heads/other 456\n'


def run_case(hook, gate_rc=0, publisher='ok', skip=None, leak_rc=0, measurement=True):
    with tempfile.TemporaryDirectory(prefix='wrkq-hook-e2e-') as tmp:
        root = Path(tmp)
        bins = root / 'bin'
        bins.mkdir()
        # An allowlist PATH guarantees the missing-publisher case cannot find
        # a real wrkp installation elsewhere on this host.
        for tool in ('git', 'sh', 'python3', 'cat'):
            (bins / tool).symlink_to(shutil.which(tool))
        shutil.copytree(SOURCE, root / 'tools/hooks')
        env = dict(BASE_ENV, PATH=str(bins),
                   TRACE=str(root / 'trace'), POSTS=str(root / 'posts'),
                   REFS=str(root / 'refs'), GATE_RC=str(gate_rc), LEAK_RC=str(leak_rc),
                   PUBLISHER=publisher, LEFTHOOK='', WRKQ_SKIP_VERIFY_HOOK='')
        # Explicit env also isolates a caller launched under Bun. Remove GIT_*
        # BEFORE init, even if this runner was invoked from a live git hook.
        subprocess.run(['git', 'init', '-q', str(root)], env=env, check=True)
        for name in ('just', 'gitleaks', 'golangci-lint', 'wrkp'):
            if name == 'wrkp' and publisher == 'missing':
                continue
            script = '''#!/usr/bin/env python3
import json, os, sys, time
from pathlib import Path
name = Path(sys.argv[0]).name
args = sys.argv[1:]
if name == 'wrkp' and args[0] == 'post':
    with open(os.environ['POSTS'], 'a') as f: f.write(json.dumps(args) + '\\n')
    if os.environ['PUBLISHER'] == 'hang': time.sleep(10)
    sys.exit(19 if os.environ['PUBLISHER'] == 'fail' else 0)
with open(os.environ['TRACE'], 'a') as f: f.write(name + ' ' + ' '.join(args) + '\\n')
if name == 'wrkp':
    Path(os.environ['REFS']).write_text(sys.stdin.read())
    sys.exit(19 if os.environ['PUBLISHER'] == 'fail' else 0)
if name == 'gitleaks': sys.exit(int(os.environ['LEAK_RC']))
sys.exit(int(os.environ['GATE_RC']))
'''
            path = bins / name
            path.write_text(script)
            path.chmod(0o755)
        assert subprocess.check_output(['git', 'config', '--get', 'core.bare'], cwd=root, env=env).strip() == b'false'
        if not measurement:
            (root / 'tools/hooks/hook-duration.py').unlink()
        if skip:
            env[skip[0]] = skip[1]
        start = time.monotonic()
        proc = subprocess.run(['sh', str(root / 'tools/hooks' / hook), 'origin', 'remote'],
                              cwd=root, env=env, input=REFS, text=True, capture_output=True, timeout=5)
        elapsed = time.monotonic() - start
        # pre-push no longer runs a gate (verify is post-push, T-10161): it always passes.
        expected_rc = 0 if skip or hook == 'pre-push' else (leak_rc or gate_rc)
        assert proc.returncode == expected_rc, (hook, proc.returncode, expected_rc, proc.stderr)
        trace = (root / 'trace').read_text().splitlines() if (root / 'trace').exists() else []
        if hook == 'pre-commit':
            expected = ['gitleaks protect --staged --redact']
            if not leak_rc:
                expected += ['golangci-lint run']
        else:
            expected = []
            if publisher != 'missing':
                expected += ['wrkp git push origin remote']
                assert (root / 'refs').read_text() == REFS
        assert trace == expected, (trace, expected)
        if not measurement:
            assert not (root / '.git/praesidium/hook-timings.jsonl').exists()
            return {'hook': hook, 'rc': expected_rc, 'measurement': 'unavailable'}
        records = [json.loads(line) for line in (root / '.git/praesidium/hook-timings.jsonl').read_text().splitlines()]
        assert len(records) == 1, records
        record = records[0]
        assert record['exit_code'] == expected_rc
        assert record['result'] == ('skipped' if skip else 'failed' if expected_rc else 'passed')
        assert record['change_kind'] == 'unclassified'
        assert record.get('file_count') == (0 if hook == 'pre-commit' else None)
        assert record['duration_ms'] >= 0 and record['finished_at'] >= record['started_at']
        assert 'head' not in record and all(v != '' for v in record.values())
        if publisher != 'missing':
            deadline = time.monotonic() + 2
            while not (root / 'posts').exists() and time.monotonic() < deadline:
                time.sleep(.02)
            posts = [json.loads(line) for line in (root / 'posts').read_text().splitlines()]
            assert len(posts) == 1, posts
            args = posts[0]
            assert args[:2] == ['post', 'wrkq']
            assert args[args.index('--key') + 1] == 'hook:' + record['run_id']
            assert args[args.index('--occurred-at') + 1] == record['finished_at']
        if publisher == 'ok':
            replay = subprocess.run(['python3', 'tools/hooks/hook-duration.py', '--backfill'],
                                    cwd=root, env=env, capture_output=True, text=True, check=True)
            assert json.loads(replay.stdout) == {'records': 1, 'posted': 1, 'invalid_or_failed': 0}
            replay_args = json.loads((root / 'posts').read_text().splitlines()[-1])
            assert replay_args == args  # same fact key, occurrence and attributes
        if publisher == 'hang':
            assert elapsed < 2, elapsed
        return {'hook': hook, 'rc': expected_rc, 'publisher': publisher, 'skip': skip, 'trace': trace}


results = []
for publisher in ('ok', 'missing', 'fail', 'hang'):
    for rc in (0, 7):
        results.append(run_case('pre-commit', gate_rc=rc, publisher=publisher))
    results.append(run_case('pre-commit', leak_rc=9, publisher=publisher))
    results.append(run_case('pre-push', publisher=publisher))
for rc in (0, 7):
    results.append(run_case('pre-commit', gate_rc=rc, measurement=False))
results.append(run_case('pre-commit', leak_rc=9, measurement=False))
results.append(run_case('pre-push', measurement=False))
print(json.dumps({'passed': len(results), 'cases': results}, indent=2))

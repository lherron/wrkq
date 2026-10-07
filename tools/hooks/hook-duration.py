#!/usr/bin/env python3
"""Best-effort whole-hook journal and detached hook.settled publisher."""
import datetime
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import time
import uuid


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat(timespec='milliseconds').replace('+00:00', 'Z')


def git(*args):
    child = subprocess.run(['git', *args], stdin=subprocess.DEVNULL, capture_output=True,
                           text=True, timeout=2, env=dict(os.environ))
    return child.stdout.strip() if child.returncode == 0 else None


def journal():
    common = git('rev-parse', '--git-common-dir')
    if not common:
        raise ValueError('not in a git repository')
    return Path(common) / 'praesidium/hook-timings.jsonl'


def load():
    """load1/ncpu sampled before the hook body runs; neither when unreadable."""
    try:
        load1, ncpu = os.getloadavg()[0], os.cpu_count()
    except (OSError, AttributeError):
        return {}
    return dict(load1=f'{load1:.2f}', ncpu=str(ncpu)) if ncpu else {}


def start(hook):
    sampled = load()
    started_ns = time.monotonic_ns()
    record = dict(source='wrkq-git-hook', node=socket.gethostname(), hook=hook,
                  run_id=str(uuid.uuid4()), started_at=now(), change_kind='unclassified',
                  **sampled)
    for key, value in [('head', git('rev-parse', '--verify', 'HEAD')),
                       ('branch', git('branch', '--show-current'))]:
        if value:
            record[key] = value
    # Count staged paths for commit; compare committed tree with upstream for
    # push when available. No classifier inferred from extensions or ref stdin.
    args = ('diff', '--cached', '--name-only', '-z') if hook == 'pre-commit' else (
        'diff', '--name-only', '-z', '@{upstream}', 'HEAD')
    names = git(*args)
    if names is not None:
        record['file_count'] = len([name for name in names.split('\0') if name])
    record['_monotonic_ns'] = started_ns
    record['_journal'] = str(journal().resolve())
    return record


def post(record):
    args = ['wrkp', 'post', 'wrkq', '--type', 'hook.settled', '--key', 'hook:' + record['run_id'],
            '--occurred-at', record['finished_at'], '--message',
            f"{record['hook']} {record['result']} in {record['duration_ms']}ms"]
    for key, value in record.items():
        if key != 'finished_at' and value is not None and value != '':
            args.extend(['--attr', f'{key}={value}'])
    return subprocess.run(args, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                          stderr=subprocess.DEVNULL, timeout=2, env=dict(os.environ)).returncode == 0


def finish(state, code, result):
    finished_at, finished_ns = now(), time.monotonic_ns()
    record = json.loads(state)
    started_ns = record.pop('_monotonic_ns')
    path = Path(record.pop('_journal'))
    record.update(finished_at=finished_at, duration_ms=max(0, (finished_ns - started_ns) // 1_000_000),
                  exit_code=code, result=result if result == 'skipped' else 'passed' if code == 0 else 'failed')
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open('a') as stream:
            stream.write(json.dumps(record, separators=(',', ':')) + '\n')
    except OSError:
        pass
    # A hung/missing/failing publisher cannot delay the gate. Explicit env and
    # null descriptors prevent Bun autoload and inherited hook stdin surprises.
    subprocess.Popen([sys.executable, str(Path(__file__).resolve()), '--post', json.dumps(record)],
                     stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                     start_new_session=True, env=dict(os.environ))


def backfill():
    path = journal()
    lines = path.read_text().splitlines() if path.exists() else []
    posted = invalid = 0
    for line in lines:
        try:
            record = json.loads(line)
            if (not record.get('run_id') or not record.get('finished_at') or
                    record.get('source') != 'wrkq-git-hook' or
                    record.get('result') not in ('passed', 'failed', 'skipped')):
                raise ValueError('not a genuine whole-hook record')
            if post(record):
                posted += 1
            else:
                invalid += 1
        except (ValueError, OSError, subprocess.SubprocessError):
            invalid += 1
    print(json.dumps(dict(records=len(lines), posted=posted, invalid_or_failed=invalid)))


if __name__ == '__main__':
    try:
        mode = sys.argv[1]
        if mode == '--start':
            print(json.dumps(start(sys.argv[2])))
        elif mode == '--finish':
            finish(sys.argv[2], int(sys.argv[3]), sys.argv[4])
        elif mode == '--post':
            sys.exit(0 if post(json.loads(sys.argv[2])) else 1)
        elif mode == '--backfill':
            backfill()
    except (OSError, ValueError, subprocess.SubprocessError):
        # Hook instrumentation has no authority over gate results.
        sys.exit(1)

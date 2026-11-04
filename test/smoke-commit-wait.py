#!/usr/bin/env python3
"""Drive repo-local binaries against an isolated wrkqd; count real RPC calls.
Usage: python3 test/smoke-commit-wait.py --bin bin --scratch /scratch/path --out /artifact/path
No installed binaries, canonical data, or services are changed.
"""
import argparse
import collections
import http.server
import json
import os
import pathlib
import signal
import socket
import subprocess
import threading
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bin', required=True, type=pathlib.Path)
    parser.add_argument('--scratch', required=True, type=pathlib.Path)
    parser.add_argument('--out', required=True, type=pathlib.Path)
    args = parser.parse_args()
    bins = args.bin.resolve()
    scratch = args.scratch.resolve()
    scratch.mkdir(parents=True, exist_ok=True)
    args.out.mkdir(parents=True, exist_ok=True)
    database = scratch / 'wait.db'
    if database.exists():
        raise RuntimeError('use a fresh scratch directory')
    env = {'HOME': str(scratch), 'PATH': os.environ['PATH'],
           'WRKQ_PRINCIPAL_REF': 'agent:cody', 'WRKQ_SEARCH_ENABLED': '0'}
    catalog = scratch / 'hooks.json'
    catalog.write_text('{"schemaVersion":"wrkf.hook-catalog.v0","hooks":{}}')
    env['WRKF_HOOK_CATALOG'] = str(catalog)
    drives = []

    def cli(name, *argv, remote=False):
        command = [str(bins / name), *argv]
        result = subprocess.run(command, cwd=scratch, env=remote_env if remote else env,
                                capture_output=True, text=True, timeout=15)
        drives.append({'command': command, 'stdout': result.stdout,
                       'stderr': result.stderr, 'exit': result.returncode})
        if result.returncode:
            raise RuntimeError(drives[-1])
        return result.stdout

    cli('wrkqadm', '--db', str(database), 'init')
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    addr = '127.0.0.1:' + str(port)
    counts = collections.Counter()
    lock = threading.Lock()
    initial_read = threading.Event()
    parked = threading.Event()

    class Proxy(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            data = self.rfile.read(int(self.headers['Content-Length']))
            frame = json.loads(data)
            with lock:
                counts[frame['method']] += 1
            if frame['method'] == 'wrkq.monitor.stateView':
                initial_read.set()
            if frame['method'] == 'wrkq.commit.wait' and frame.get('params', {}).get('since'):
                parked.set()
            request = urllib.request.Request('http://' + addr + self.path, data,
                                             headers=dict(self.headers))
            try:
                with urllib.request.urlopen(request, timeout=35) as response:
                    body = response.read()
                    self.send_response(response.status)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
            except (BrokenPipeError, ConnectionResetError):
                pass  # The short-clock smoke cancels its HTTP request.

    proxy = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Proxy)
    proxy.daemon_threads = True
    threading.Thread(target=proxy.serve_forever, daemon=True).start()
    remote_env = dict(env, WRKQ_DB='rpc://127.0.0.1:' + str(proxy.server_port),
                      WRKQD_TOKEN='isolated-commit-wait')
    log = open(args.out / 'daemon.log', 'w')
    daemon = subprocess.Popen([str(bins / 'wrkqd'), '--db', str(database), '--addr', addr,
                               '--token', 'isolated-commit-wait'], cwd=scratch,
                              env=env, stdout=log, stderr=log)
    waiter = None
    try:
        for _ in range(100):
            try:
                request = urllib.request.Request('http://' + addr + '/v1/health',
                                                 headers={'Authorization': 'Bearer isolated-commit-wait'})
                urllib.request.urlopen(request, timeout=.2).close()
                break
            except Exception:
                if daemon.poll() is not None:
                    raise RuntimeError('scratch daemon exited')
                time.sleep(.05)
        cli('wrkq', 'whoami', '--json', remote=True)
        cli('wrkq', 'mkdir', '/wait-test', '--kind', 'project', remote=True)
        cli('wrkq', 'touch', '/wait-test/target', '-t', 'Wake target', remote=True)
        with lock:
            counts.clear()
        command = [str(bins / 'wrkq'), 'monitor', 'wait', 'T-00001',
                   '--until', 'state=completed', '--timeout', '10s']
        waiter = subprocess.Popen(command, cwd=scratch, env=remote_env,
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        assert initial_read.wait(3), 'monitor did not read state'
        # The old loop must fail on its stateView count, not a missing wait call.
        parked.wait(.3)
        time.sleep(1.2)
        with lock:
            idle_counts = dict(counts)
        assert idle_counts.get('wrkq.monitor.stateView') == 1, idle_counts
        assert idle_counts.get('wrkq.commit.wait') == 2, idle_counts
        begin = time.monotonic()
        cli('wrkq', 'set', 'T-00001', '--state', 'completed', remote=True)
        stdout, stderr = waiter.communicate(timeout=2)
        elapsed = time.monotonic() - begin
        assert waiter.returncode == 0, (stdout, stderr, waiter.returncode)
        lines = [json.loads(line) for line in stdout.splitlines() if line.strip()]
        assert len(lines) == 1 and lines[0]['result'] == 'met', lines
        assert elapsed < 1.0, elapsed
        with lock:
            final_counts = dict(counts)
        assert final_counts['wrkq.monitor.stateView'] == 2, final_counts
        assert final_counts['wrkq.commit.wait'] == 2, final_counts
        # Own clocks remain subsecond, below the server's 1s minimum wait.
        clocks = []
        for argv in [
            ['monitor', 'wait', 'T-00001', '--until', 'state=blocked', '--timeout', '250ms'],
            ['monitor', 'watch', 'T-00001', '--timeout', '250ms'],
            ['monitor', 'watch', '--raw', '--timeout', '250ms'],
            ['monitor', 'watch', 'T-00001', '--stall-after', '250ms'],
        ]:
            started = time.monotonic()
            p = subprocess.run([str(bins / 'wrkq'), *argv], cwd=scratch, env=remote_env,
                               text=True, capture_output=True, timeout=2)
            duration = time.monotonic() - started
            terminal = json.loads(p.stdout.splitlines()[-1])
            expected = 1 if argv[1] == 'wait' else 0
            assert p.returncode == expected, (argv, p.stdout, p.stderr)
            assert terminal['result'] in ('timeout', 'stall'), terminal
            assert .25 <= duration < .8, (argv, duration)
            clocks.append({'argv': argv, 'elapsedSeconds': duration, 'terminal': terminal,
                           'exit': p.returncode})
        result = {'idleSeconds': 1.2, 'idleCalls': idle_counts, 'finalCalls': final_counts,
                  'mutationToMetSeconds': elapsed, 'stdout': stdout, 'stderr': stderr,
                  'exit': waiter.returncode, 'clocks': clocks}
        (args.out / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result, indent=2))
    finally:
        if waiter is not None and waiter.poll() is None:
            waiter.terminate()
            waiter.wait(timeout=3)
        daemon.send_signal(signal.SIGINT)
        daemon.wait(timeout=10)
        proxy.shutdown()
        proxy.server_close()
        log.close()
        (args.out / 'drive.json').write_text(json.dumps(drives, indent=2) + '\n')


if __name__ == '__main__':
    main()

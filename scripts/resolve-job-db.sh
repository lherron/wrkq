#!/usr/bin/env bash
# Resolve the database a launchd wrkqd job opens, from `launchctl print` text on
# stdin (T-08927). Prints the path on stdout; exits 1 with the reason on stderr
# when the job's own argv/environment does not name one.
#
# Precedence mirrors the daemon: a --db program argument (any Go flag form:
# --db X, --db=X, -db X, -db=X) wins over the job's environment, where WRKQ_DB
# (local path) outranks WRKQ_DB_PATH. Only the job's `environment = {}` block
# counts; the caller's shell environment is never consulted, because the probe
# exists to answer for the job, not for whoever runs `just install`.
set -euo pipefail

print="$(cat)"

# Lines of one top-level block (`\t<name> = {` ... `\t}`), inner indent stripped.
block() {
  printf '%s\n' "$print" | awk -v name="$1" '
    $0 == "\t" name " = {" { inside = 1; next }
    inside && $0 == "\t}" { exit }
    inside { sub(/^\t+/, ""); print }
  '
}

env_value() {
  block environment | awk -v key="$1" -F' => ' '$1 == key { sub(/^[^=]*=> /, ""); print; exit }'
}

fail() {
  echo "resolve-job-db: $*" >&2
  exit 1
}

if [ -z "$print" ]; then
  fail "empty launchctl print output (job not loaded?)"
fi

arg_db=""
take_next=""
while IFS= read -r arg; do
  if [ -n "$take_next" ]; then
    arg_db="$arg"
    take_next=""
    continue
  fi
  case "$arg" in
    --db | -db) take_next=1 ;;
    --db=* | -db=*) arg_db="${arg#*=}" ;;
  esac
done < <(block arguments)
if [ -n "$take_next" ]; then
  fail "job arguments end with a bare --db"
fi
if [ -n "$arg_db" ]; then
  printf '%s\n' "$arg_db"
  exit 0
fi

env_db="$(env_value WRKQ_DB)"
env_db_path="$(env_value WRKQ_DB_PATH)"
case "$env_db" in
  rpc://*) fail "job environment sets WRKQ_DB=$env_db (remote locator); a daemon needs a local database" ;;
esac
if [ -n "$env_db" ] && [ -n "$env_db_path" ] && [ "$env_db" != "$env_db_path" ]; then
  fail "job environment sets WRKQ_DB=$env_db and WRKQ_DB_PATH=$env_db_path; the daemon refuses that conflict (T-08302)"
fi
if [ -n "$env_db" ]; then
  printf '%s\n' "$env_db"
  exit 0
fi
if [ -n "$env_db_path" ]; then
  printf '%s\n' "$env_db_path"
  exit 0
fi
if [ -n "$(env_value WRKQ_DB_PATH_FILE)" ]; then
  fail "job environment names its database only through WRKQ_DB_PATH_FILE; resolve it by hand"
fi
fail "job has no --db argument and no WRKQ_DB / WRKQ_DB_PATH in its environment (it may rely on .env.local or config.yaml)"

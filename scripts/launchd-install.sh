#!/usr/bin/env bash
# Install a launchd plist without silently replacing a different job (T-08044).
#
#   scripts/launchd-install.sh <src.plist> <dst.plist> [--force]
#
# On mini the installed com.praesidium.wrkq-server job is the canonical tailnet
# wrkqd (node tokens, WRKF_HOOK_CATALOG, its own attachment dir); the repo plist
# is the loopback `wrkq server serve` dev variant. Copying one over the other
# takes the ledger away from every remote node. So the only silent outcomes are
# "nothing installed yet" and "already the same plist" (compared after plutil
# normalization, so formatting and comments do not count). Any other difference
# refuses, prints the normalized diff and what the installed job would lose, and
# needs --force. A destination plutil cannot read is refused the same way.
set -euo pipefail

usage="usage: launchd-install.sh <src.plist> <dst.plist> [--force]"
force=""
positional=()
for arg in "$@"; do
  case "$arg" in
    --force) force=1 ;;
    -*) echo "launchd-install: unknown flag $arg" >&2; echo "$usage" >&2; exit 2 ;;
    *) positional+=("$arg") ;;
  esac
done
if [ "${#positional[@]}" -ne 2 ]; then
  echo "$usage" >&2
  exit 2
fi
src="${positional[0]}"
dst="${positional[1]}"
# The command an operator reruns to override; the justfile passes its recipe name.
override="${LAUNCHD_INSTALL_OVERRIDE:-scripts/launchd-install.sh $src $dst --force}"

# Key-sorted, pretty JSON of a plist; non-zero when plutil cannot read it.
normalize() {
  plutil -convert json -o - "$1" 2>/dev/null | jq -S .
}

install_it() {
  mkdir -p "$(dirname "$dst")"
  cp "$src" "$dst"
  echo "✓ Installed $dst"
}

src_json="$(normalize "$src")" || { echo "launchd-install: cannot read $src" >&2; exit 1; }

if [ ! -e "$dst" ]; then
  install_it
  exit 0
fi

if ! dst_json="$(normalize "$dst")"; then
  if [ -z "$force" ]; then
    {
      echo "✗ NOT installing: $dst exists and plutil cannot read it, so what it runs is unknown."
      echo "  Inspect it by hand; overwrite anyway with:  $override"
    } >&2
    exit 1
  fi
  echo "⚠️  --force: replacing unreadable $dst"
  install_it
  exit 0
fi

if [ "$src_json" = "$dst_json" ]; then
  echo "✓ $dst already matches $src"
  exit 0
fi

if [ -z "$force" ]; then
  # What the installed job has that the repo plist does not: an overwrite destroys these.
  lost="$(jq -rn --argjson dst "$dst_json" --argjson src "$src_json" '
    (($dst | keys) - ($src | keys) | .[] | "key \(.)"),
    (($dst.ProgramArguments // []) - ($src.ProgramArguments // []) | .[] | "arg \(.)"),
    (($dst.EnvironmentVariables // {}) as $de | ($src.EnvironmentVariables // {}) as $se
      | $de | keys[] as $k
      | if $se | has($k) | not then "env \($k)=\($de[$k])"
        elif $se[$k] != $de[$k] then "env \($k)=\($de[$k])  (repo: \($se[$k]))"
        else empty end)
  ')" || lost="(could not list them: jq failed; read the diff)"
  {
    echo "✗ NOT installing: $dst differs from $src."
    if [ -n "$lost" ]; then
      echo "  The installed job has, and an overwrite would lose:"
      printf '%s\n' "$lost" | sed 's/^/    /'
    fi
    echo "  Normalized diff (installed → repo):"
    diff -u --label "installed $dst" --label "repo $src" <(printf '%s\n' "$dst_json") <(printf '%s\n' "$src_json") | sed 's/^/    /' || true
    echo "  Overwrite anyway with:  $override"
  } >&2
  exit 1
fi
echo "⚠️  --force: replacing $dst, which differed from $src"
install_it

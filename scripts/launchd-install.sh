#!/usr/bin/env bash
# Install a launchd plist without silently replacing a different job (T-08044).
#
#   scripts/launchd-install.sh <src.plist> <dst.plist> [--force] [--check]
#
# On mini the installed com.praesidium.wrkq-server job is the canonical tailnet
# wrkqd (node tokens, WRKF_HOOK_CATALOG, its own attachment dir); the repo plist
# is the loopback `wrkq server serve` dev variant. Copying one over the other
# takes the ledger away from every remote node. So the only silent outcomes are
# "nothing installed yet" and "already the same plist" (compared after plutil
# normalization, so formatting and comments do not count). Any other difference
# refuses, prints the normalized diff and what the installed job would lose, and
# needs --force. A destination plutil cannot read is refused the same way.
#
# Host adoption (global/launchd-jobs §3) is not a difference: an installed copy
# that is the repo plist plus the shim as ProgramArguments[0] and
# AssociatedBundleIdentifiers = [com.praesidium.AgentHost] matches.
#
# --check decides the same way but never copies: exit 0 when dst is absent or
# matches, 1 when it differs. `just launchd-uninstall` asks it before removing
# an installed copy, so mini's canonical wrkqd is not removed silently either.
set -euo pipefail

usage="usage: launchd-install.sh <src.plist> <dst.plist> [--force] [--check]"
force=""
check=""
positional=()
for arg in "$@"; do
  case "$arg" in
    --force) force=1 ;;
    --check) check=1 ;;
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
# What a refusal says is not happening: --check guards a removal, not a copy.
refused="NOT installing"
if [ -n "$check" ]; then refused="NOT removing"; fi

# Key-sorted, pretty JSON of a plist; non-zero when plutil cannot read it.
normalize() {
  plutil -convert json -o - "$1" 2>/dev/null | jq -S .
}

# normalize, with this host's adoption of the installed copy taken back out.
shim="${PRAESIDIUM_LAUNCH_SHIM:-$HOME/.local/bin/praesidium-launch}"
normalize_installed() {
  plutil -convert json -o - "$1" 2>/dev/null | jq -S --arg shim "$shim" '
    if .AssociatedBundleIdentifiers == ["com.praesidium.AgentHost"]
       and ((.ProgramArguments // [])[0] == $shim)
    then del(.AssociatedBundleIdentifiers) | .ProgramArguments |= .[1:]
    else . end'
}

install_it() {
  if [ -n "$check" ]; then
    echo "✓ --check: $dst would be replaced"
    return
  fi
  mkdir -p "$(dirname "$dst")"
  cp "$src" "$dst"
  echo "✓ Installed $dst"
}

src_json="$(normalize "$src")" || { echo "launchd-install: cannot read $src" >&2; exit 1; }

if [ ! -e "$dst" ]; then
  if [ -n "$check" ]; then
    echo "✓ $dst is not installed"
    exit 0
  fi
  install_it
  exit 0
fi

if ! dst_json="$(normalize_installed "$dst")"; then
  if [ -z "$force" ]; then
    {
      echo "✗ $refused: $dst exists and plutil cannot read it, so what it runs is unknown."
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
    echo "✗ $refused: $dst differs from $src."
    if [ -n "$lost" ]; then
      echo "  The installed job has, and an overwrite or removal would lose:"
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

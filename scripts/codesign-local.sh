#!/usr/bin/env bash
# codesign-local.sh <binary> <identifier>
#
# Give a launchd-run binary a stable code identity so macOS TCC grants (e.g.
# removable-volume access to /Volumes/Scratch) survive rebuilds (T-12226). An
# ad-hoc linker signature changes with every build, so TCC treats each build as
# a new app. Signing with the node's shared self-signed identity and a fixed
# identifier makes the designated requirement `identifier X and certificate
# leaf = H"…"`, which stays the same from build to build.
#
# The identity is the per-node `Praesidium Local Code Signing` cert from
# hrc-runtime T-12224 (var/agents/references/macos-code-signing.md);
# PRAESIDIUM_CODESIGN_IDENTITY overrides the name. A node without the identity
# keeps the linker's ad-hoc signature. That is a config fallback, not an error.
set -euo pipefail

binary="$1"
identifier="$2"
identity="${PRAESIDIUM_CODESIGN_IDENTITY:-Praesidium Local Code Signing}"

[[ "$(uname -s)" == "Darwin" ]] || exit 0

# No -v: a self-signed identity is not "valid" (untrusted root) but codesign
# signs with it all the same.
if ! security find-identity -p codesigning 2>/dev/null | grep -qF "\"$identity\""; then
  echo "  codesign: no \"$identity\" identity on this node; $(basename "$binary") stays ad-hoc signed"
  exit 0
fi

if ! codesign -f -s "$identity" -i "$identifier" "$binary" 2>&1; then
  echo "  ✗ codesign: signing $binary with \"$identity\" failed; TCC grants will not survive this build" >&2
  exit 1
fi
echo "✓ Signed $(basename "$binary") as $identifier ($(codesign -dr - "$binary" 2>&1 | sed -n 's/^designated => //p'))"

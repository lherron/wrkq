#!/usr/bin/env bash
# Stage the full build under the revision stamped into its companion daemon.
set -euo pipefail

revision="$(bin/wrkqd version --json | jq -er '.build_revision | select(test("^[0-9a-f]{40}$"))')"
platform="$(go env GOOS)-$(go env GOARCH)"
clients="$HOME/.local/share/wrkq/clients"
mkdir -p "$clients/$revision"
tmp="$(mktemp "$clients/$revision/.wrkq.stage.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
cp -p bin/wrkq "$tmp"
mv -f "$tmp" "$clients/$revision/wrkq-$platform"
touch "$clients/$revision"

# Limit pruning to Git revision directories, sorted by staging time.
cd "$clients"
revisions=()
for entry in *; do
  if [[ "$entry" =~ ^[0-9a-f]{40}$ && -d "$entry" && ! -L "$entry" ]]; then
    revisions+=("$entry")
  fi
done
if [ "${#revisions[@]}" -gt 5 ]; then
  while IFS= read -r old; do
    rm -rf -- "$old"
  done < <(ls -1td -- "${revisions[@]}" | tail -n +6)
fi

set shell := ["bash", "-cu"]

# Default task
default:
  @just info
  @just --list

# Project information
info:
  @echo "Current Project: wrkq"
  @echo "Description: Task management CLI and library for agent work queues"
  @echo "Stack:       Go with SQLite"
  @echo ""
  @echo "Key commands:"
  @echo "  just build     - Build wrkq, wrkf, wrkqadm, wrkqd binaries"
  @echo "  just test      - Run Go tests"
  @echo "  just lint      - Run golangci-lint"
  @echo "  just verify    - Run lint + test"

# --- Database tasks ---

# Seed development data
db-seed-dev:
  node scripts/db-seed.mjs --dir db/seeds/dev

# Seed test data (requires --allow-remote flag for safety)
db-seed-test url:
  node scripts/db-seed.mjs --dir db/seeds/test --url "{{url}}" --allow-remote

# Generate baseline schema from current database
db-baseline:
  node scripts/db-baseline.mjs --out db/baseline.sql

# Reset database (drop all tables and re-migrate)
db-reset:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "⚠️  This will DROP all tables and re-run migrations!"
  read -p "Are you sure? [y/N] " -n 1 -r
  echo
  if [[ $REPLY =~ ^[Yy]$ ]]; then
    node scripts/db-reset.mjs --dir db/migrations
  else
    echo "Cancelled."
  fi

# --- CLI tasks (Golang) ---

# Build shipped CLI binaries (wrkq, wrkf, wrkc, wrkqadm, wrkqd)
build:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "Building wrkq, wrkf, wrkc, wrkqadm, and wrkqd binaries..."
  rm -f bin/wrkq-rpccli bin/wrkq-legacy
  LDFLAGS="$(scripts/ldflags.sh)"
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkq ./cmd/wrkq
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkf ./cmd/wrkf
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkc ./cmd/wrkc
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkqadm ./cmd/wrkqadm
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkqd ./cmd/wrkqd

# Build the portable, CGO-free, REMOTE-ONLY wrkq client (T-07090).
#
# This is the second build product: it links no SQLite driver, needs no cgo
# toolchain, and cross-compiles to any Go target. It speaks rpc:// only and
# refuses a local database locator. For local-file operation use `just build`,
# which carries the wrkq_local tag.
#
#   just build-portable                  # host platform
#   just build-portable windows amd64
#   just build-portable linux arm64
build-portable goos="" goarch="":
  #!/usr/bin/env bash
  set -euo pipefail
  target_os="{{goos}}"; target_arch="{{goarch}}"
  target_os="${target_os:-$(go env GOHOSTOS)}"
  target_arch="${target_arch:-$(go env GOHOSTARCH)}"
  ext=""; [ "$target_os" = "windows" ] && ext=".exe"
  out="dist/portable/wrkq-${target_os}-${target_arch}${ext}"
  mkdir -p dist/portable
  LDFLAGS="$(scripts/ldflags.sh)"
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
    go build -ldflags "$LDFLAGS" -o "$out" ./cmd/wrkq
  # Fail loudly if the cgo/server dependency ever regrows: a portable binary
  # that silently linked SQLite would stop cross-compiling on the next target.
  if CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go list -deps ./cmd/wrkq \
     | grep -qE 'sqlite|wrkq/internal/(db|store|search|wrkqd)$'; then
    echo "✗ portable build linked durable local state; see internal/rpccli/portable_importguard_test.go" >&2
    exit 1
  fi
  echo "✓ $out ($(du -h "$out" | cut -f1 | tr -d ' '), remote-only)"

# Build the portable client for every target we ship to
build-portable-all:
  #!/usr/bin/env bash
  set -euo pipefail
  for target in "windows amd64" "linux amd64" "linux arm64" "darwin arm64"; do
    just build-portable $target
  done

# Conservative no-network check for agents/CI sandboxes
agent-check:
  scripts/agent-check.sh

# Compile packages only with vendored deps, no network, and conservative parallelism
agent-compile:
  GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOFLAGS='-mod=vendor -p=1' CGO_CFLAGS='-O0 -g0' \
    go test -tags "sqlite_fts5,wrkq_local" -run '^$' ./...

# Build all binaries with vendored deps, no network, and conservative parallelism
agent-build:
  #!/usr/bin/env bash
  set -euo pipefail
  mkdir -p bin
  LDFLAGS="$(scripts/ldflags.sh)"
  export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOFLAGS='-mod=vendor -p=1' CGO_CFLAGS='-O0 -g0'
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkq ./cmd/wrkq
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkf ./cmd/wrkf
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkc ./cmd/wrkc
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkqadm ./cmd/wrkqadm
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkqd ./cmd/wrkqd

# Run the wrkq CLI
run *args:
  go run ./cmd/wrkq {{args}}

# Find source entry points for a topic
find-entry-points topic:
  @go run ./cmd/find-entry-points --root . "{{topic}}"

# Explain source area exports and import relationships
explain-area path:
  @go run ./cmd/explain-area --root . "{{path}}"

# Run the wrkqadm CLI
wrkqadm-run *args:
  go run ./cmd/wrkqadm {{args}}

# Run CLI tests with coverage
test-coverage:
  go test -tags "sqlite_fts5,wrkq_local" -v -coverprofile=coverage.out ./...
  go tool cover -html=coverage.out -o coverage.html

# Install CLI binaries to ~/.local/bin and, on producer nodes, publish @wrkq/client.
# Pass no-sync=1 to skip syncing downstream consumer repos on a producer node.
install no-sync="": build
  #!/usr/bin/env bash
  set -euo pipefail
  echo "Installing to ~/.local/bin/..."
  mkdir -p ~/.local/bin
  # Remove old binaries first to avoid crashes when overwriting running binaries
  rm -f ~/.local/bin/wrkq ~/.local/bin/wrkf ~/.local/bin/wrkc ~/.local/bin/wrkqadm ~/.local/bin/wrkqd ~/.local/bin/wrkq-rpccli
  cp bin/wrkq ~/.local/bin/wrkq
  cp bin/wrkf ~/.local/bin/wrkf
  cp bin/wrkc ~/.local/bin/wrkc
  cp bin/wrkqadm ~/.local/bin/wrkqadm
  cp bin/wrkqd ~/.local/bin/wrkqd
  chmod +x ~/.local/bin/wrkq
  chmod +x ~/.local/bin/wrkf
  chmod +x ~/.local/bin/wrkc
  chmod +x ~/.local/bin/wrkqadm
  chmod +x ~/.local/bin/wrkqd
  echo "✓ Installed to ~/.local/bin/wrkq"
  echo "✓ Installed to ~/.local/bin/wrkf"
  echo "✓ Installed to ~/.local/bin/wrkc"
  echo "✓ Installed to ~/.local/bin/wrkqadm"
  echo "✓ Installed to ~/.local/bin/wrkqd"
  echo ""
  if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
    echo "⚠️  Add ~/.local/bin to your PATH:"
    echo "   export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo ""
  fi
  echo "✓ Run 'wrkq version', 'wrkf --help', 'wrkc info', 'wrkqadm version', and 'wrkqd --help' to verify"
  echo ""
  node_role="$(bash scripts/resolve-node-role.sh)"
  if [ "$node_role" = "producer" ]; then
    just client-publish-dev
    if [ -z "{{ no-sync }}" ]; then
      just sync-downstream
    else
      echo "[install] skipping downstream sync (no-sync=1)"
    fi
  else
    echo "[install] consumer node role; skipping client publish and downstream sync"
  fi

# Sync downstream repos that consume @wrkq/client from local Verdaccio.
sync-downstream:
  bun scripts/sync-downstream.ts

# Validate the downstream consumer inventory and fail-closed sync driver.
sync-downstream-test:
  bun test test/sync-downstream.test.ts

# Install the wrkq launchd agent plist
install-launchd:
  #!/usr/bin/env bash
  set -euo pipefail
  src="launchd/com.praesidium.wrkq-server.plist"
  dst="$HOME/Library/LaunchAgents/com.praesidium.wrkq-server.plist"
  mkdir -p "$HOME/Library/LaunchAgents"
  cp "$src" "$dst"
  echo "✓ Installed $dst"
  echo "Bootstrap with:"
  echo "  launchctl bootstrap gui/$(id -u) $dst"
  echo "  launchctl kickstart -k gui/$(id -u)/com.praesidium.wrkq-server"

# Install the llama-server launchd plist (dense embeddings for search index)
install-llama-launchd:
  #!/usr/bin/env bash
  set -euo pipefail
  src="launchd/com.praesidium.llama-server.plist"
  dst="$HOME/Library/LaunchAgents/com.praesidium.llama-server.plist"
  mkdir -p "$HOME/Library/LaunchAgents"
  mkdir -p "$HOME/praesidium/var/logs/llama-cpp"
  cp "$src" "$dst"
  echo "✓ Installed $dst"
  echo "Bootstrap with:"
  echo "  launchctl bootstrap gui/$(id -u) $dst"
  echo "  launchctl kickstart -k gui/$(id -u)/com.praesidium.llama-server"
  echo
  echo "Verify with:"
  echo "  curl http://127.0.0.1:18480/health"

# Install CLI binaries to /usr/local/bin (requires sudo - run manually)
install-system:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "Building wrkq binaries..."
  LDFLAGS="$(scripts/ldflags.sh)"
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkq ./cmd/wrkq
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkf ./cmd/wrkf
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkc ./cmd/wrkc
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkqadm ./cmd/wrkqadm
  go build -tags "sqlite_fts5,wrkq_local" -ldflags "$LDFLAGS" -o bin/wrkqd ./cmd/wrkqd
  echo "Installing to /usr/local/bin/wrkq (requires sudo)..."
  # Remove old binary first to avoid crashes when overwriting running binaries
  sudo rm -f /usr/local/bin/wrkq
  sudo rm -f /usr/local/bin/wrkf
  sudo rm -f /usr/local/bin/wrkqadm
  sudo rm -f /usr/local/bin/wrkqd
  sudo cp bin/wrkq /usr/local/bin/wrkq
  sudo cp bin/wrkf /usr/local/bin/wrkf
  sudo cp bin/wrkqadm /usr/local/bin/wrkqadm
  sudo cp bin/wrkqd /usr/local/bin/wrkqd
  sudo chmod +x /usr/local/bin/wrkq
  sudo chmod +x /usr/local/bin/wrkf
  sudo chmod +x /usr/local/bin/wrkqadm
  sudo chmod +x /usr/local/bin/wrkqd
  echo "✓ Installed to /usr/local/bin/wrkq"
  echo "✓ Installed to /usr/local/bin/wrkf"
  echo "✓ Installed to /usr/local/bin/wrkqadm"
  echo "✓ Installed to /usr/local/bin/wrkqd"
  echo "✓ Run 'wrkq --version' to verify"

# Uninstall CLI binaries from ~/.local/bin
uninstall:
  #!/usr/bin/env bash
  set -euo pipefail
  UNINSTALLED=0
  if [ -f ~/.local/bin/wrkq ]; then
    echo "Removing ~/.local/bin/wrkq..."
    rm ~/.local/bin/wrkq
    echo "✓ Uninstalled wrkq from ~/.local/bin"
    UNINSTALLED=1
  fi
  if [ -f ~/.local/bin/wrkf ]; then
    echo "Removing ~/.local/bin/wrkf..."
    rm ~/.local/bin/wrkf
    echo "✓ Uninstalled wrkf from ~/.local/bin"
    UNINSTALLED=1
  fi
  if [ -f ~/.local/bin/wrkqadm ]; then
    echo "Removing ~/.local/bin/wrkqadm..."
    rm ~/.local/bin/wrkqadm
    echo "✓ Uninstalled wrkqadm from ~/.local/bin"
    UNINSTALLED=1
  fi
  if [ -f ~/.local/bin/wrkqd ]; then
    echo "Removing ~/.local/bin/wrkqd..."
    rm ~/.local/bin/wrkqd
    echo "✓ Uninstalled wrkqd from ~/.local/bin"
    UNINSTALLED=1
  fi
  if [ -f /usr/local/bin/wrkq ]; then
    echo "Removing /usr/local/bin/wrkq (requires sudo)..."
    sudo rm /usr/local/bin/wrkq
    echo "✓ Uninstalled wrkq from /usr/local/bin"
    UNINSTALLED=1
  fi
  if [ -f /usr/local/bin/wrkf ]; then
    echo "Removing /usr/local/bin/wrkf (requires sudo)..."
    sudo rm /usr/local/bin/wrkf
    echo "✓ Uninstalled wrkf from /usr/local/bin"
    UNINSTALLED=1
  fi
  if [ -f /usr/local/bin/wrkqadm ]; then
    echo "Removing /usr/local/bin/wrkqadm (requires sudo)..."
    sudo rm /usr/local/bin/wrkqadm
    echo "✓ Uninstalled wrkqadm from /usr/local/bin"
    UNINSTALLED=1
  fi
  if [ -f /usr/local/bin/wrkqd ]; then
    echo "Removing /usr/local/bin/wrkqd (requires sudo)..."
    sudo rm /usr/local/bin/wrkqd
    echo "✓ Uninstalled wrkqd from /usr/local/bin"
    UNINSTALLED=1
  fi
  if [ $UNINSTALLED -eq 0 ]; then
    echo "wrkq, wrkf, and wrkqadm are not installed"
  fi

# Format CLI code
fmt:
  go fmt ./...

# Lint all code (Go + JS/TS when available)
lint:
  @echo "Linting Golang code..."
  golangci-lint run
  @echo "✓ Golang linting complete"

# Fail on ungoverned nolint suppressions and report governed counts by rule
suppression-lint:
  go run ./cmd/suppression-lint --root . --exclude vendor --exclude testdata

# Fail on forbidden transitive Go layer-boundary imports and report governed exceptions
layer-boundary:
  go run ./cmd/layer-boundary --root . --exclude vendor --exclude testdata

# Fail on stale TDD-phase comments in first-party test files (rot sensor)
rot-sensor:
  go run ./cmd/rot-sensor --root .

# Fail on new wrkf.* RPC surfaces registered without test/smoke evidence or a governed ARCH-EXCEPTION
surface-guard:
  go run ./cmd/surface-guard --root .

# Fail on unreachable links and repo paths in router/canonical docs
doc-links:
  go run ./cmd/doc-link-check --root .

# Fail on malformed/stale durable architecture records or out-of-date projections under architecture/
# (pass --write to regenerate the generated projections: just architecture-records --write)
architecture-records *args:
  go run ./cmd/architecture-records --root . {{args}}

# Install tools/hooks/pre-push into .git/hooks — only when ABSENT, never clobbering
hooks-install:
  #!/usr/bin/env bash
  set -euo pipefail
  # Git does not track .git/hooks, so a fresh clone has no pre-push hook and
  # fitkit-s6 fails with hook.pre-push.missing — on laptops and CI exactly as in
  # the devbox container. This bootstrap makes the gate satisfiable from a virgin
  # clone (T-06894, ruling T-06894/C-12337).
  #
  # NON-CLOBBERING ON PURPOSE. An existing hook is left untouched, so a hook that
  # someone edited to skip verify is still caught by fitkit-s6 rather than being
  # silently repaired. Bootstrapping absence is not the same as overwriting
  # divergence, and only the first is safe to automate.
  hook_dir="$(git rev-parse --git-path hooks)"
  hook="$hook_dir/pre-push"
  if [[ -e "$hook" ]]; then
    echo "hooks-install: .git/hooks/pre-push already present — left untouched"
    exit 0
  fi
  mkdir -p "$hook_dir"
  cp tools/hooks/pre-push "$hook"
  chmod +x "$hook"
  echo "hooks-install: installed .git/hooks/pre-push from tools/hooks/pre-push"

# Run local fitkit S6 guard: pre-push hook must delegate to just verify.
fitkit-s6: hooks-install
  node tools/fitkit/s6-hook-runs-verify.mjs --root .

# Emit machine-readable per-predicate verify evidence: json, ndjson, recipe, predicate_id, exit_code, diagnostic.
verify-evidence-summary format="":
  @: "json ndjson recipe predicate_id exit_code diagnostic"
  @node tools/verify-evidence-summary.mjs "{{format}}"

# Run all tests (Go + Node.js when available)
test:
  @echo "Running Golang tests..."
  go test -tags "sqlite_fts5,wrkq_local" ./...
  @echo "✓ Golang tests complete"

# Run all tests with verbose logs
test-verbose:
  go test -v -tags "sqlite_fts5,wrkq_local" ./...

# Verify code quality (suppression meta-lint + layer boundary + lint + test + rot sensor + surface guard + doc links + architecture records + downstream sync + @wrkq/client unit+integration RPC)
verify summary="": fitkit-s6 suppression-lint layer-boundary lint test rot-sensor surface-guard doc-links architecture-records sync-downstream-test verify-rpc
  @just verify-evidence-summary "{{summary}}"
  @echo "✓ All checks passed"

# Run the full slow backstop tier (verify [incl. RPC] + smoke + canonical adoption probe)
verify-full: verify smoke check-wrkf-adoption
  @echo "✓ Full verification passed"

# --- Documentation tasks ---

# Serve standalone HTML docs from docs/html
serve-docs port="8000" host="0.0.0.0":
  #!/usr/bin/env bash
  set -euo pipefail
  mkdir -p docs/html
  echo "Serving docs/html at http://{{host}}:{{port}}/"
  python3 -m http.server "{{port}}" --bind "{{host}}" --directory docs/html

# Run pre-commit checks
pre-commit: fmt lint test
  @echo "✓ Pre-commit checks passed"

# --- Clean tasks ---

# Clean Go build artifacts
clean:
  rm -rf bin/ coverage.out coverage.html

# Show project structure
tree:
  tree -I 'node_modules|dist|bin|coverage*|.git' -L 3

# Run quick smoke test (build + wrkqd + merge + wrkf smoke scripts)
smoke: build
  test/smoke-wrkqd.sh
  test/smoke-mergeadm.sh
  test/smoke-wrkf.sh
  test/smoke-wrkf-rpc.sh
  test/smoke-wrkf-wrkq-code-change.sh
  test/smoke-wrkf-adoption.sh
  test/smoke-wrkf-adoption-negative.sh
  @echo "✓ Smoke test passed"

# Probe the canonical wrkf adoption signal through the supported wrkf API.
# The durable canary selector can be overridden with WRKF_ADOPTION_TASK.
check-wrkf-adoption:
  scripts/check-wrkf-adoption.sh

# --- Environment lifecycle: env-up / env-down / e2e ---------------------------
#
# Roster convention (T-06887): env-up provisions everything the e2e suite needs
# from a fresh clone using only image substrate; env-down tears it down; e2e
# depends on env-up and runs the suite. The verbs are host-agnostic by design —
# the same three serve a laptop, CI, and the devbox container, so none of them
# may assume a host service, a shared database, or the network.
#
# WHY env-up STARTS NO DAEMON. Every test/smoke-*.sh script already owns its own
# environment: it mktemp's a private SQLite DB and, where it needs wrkqd, binds
# its own free port and reaps the process on exit. A long-lived wrkqd here would
# be provisioning something nothing consumes, and would add a shared-port flake
# to a suite that currently has none. So env-up does the two things the scripts
# genuinely cannot do for themselves — prove the substrate is present, and build
# the binaries they refuse to start without — plus a scratch DB for interactive
# poking. Projects whose suites DO need a live service should start it here, on
# a free port, recorded in the state dir. (Reference implementation for the
# roster; see T-06894.)

env_dir := justfile_directory() / "tmp/env"

# Provision the e2e environment (idempotent, self-healing, no host services)
env-up:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "==> env-up: wrkq"

  # Substrate preflight. Failing here names the one missing tool, instead of
  # letting the seventh smoke script fail eight minutes in with a bare 127.
  missing=()
  for tool in go python3 jq curl; do
    command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
  done
  if (( ${#missing[@]} > 0 )); then
    echo "env-up: missing substrate: ${missing[*]}" >&2
    echo "        These are image/host substrate, not project env — wrkq does not vendor them." >&2
    echo "        go: build+test · python3: free-port helper and JSON parsing in smoke-wrkqd/smoke-wrkf-rpc" >&2
    echo "        jq: assertions in the wrkf smoke scripts · curl: wrkqd HTTP probes" >&2
    exit 1
  fi
  echo "  ok    substrate present: go python3 jq curl"

  # Self-healing: a crashed env-down can leave a half-written state dir behind,
  # so treat whatever is here as suspect and rebuild it rather than trusting it.
  mkdir -p "{{env_dir}}"
  rm -f "{{env_dir}}/wrkq.db" "{{env_dir}}/wrkq.db-wal" "{{env_dir}}/wrkq.db-shm"

  just build >/dev/null
  echo "  ok    built bin/{wrkq,wrkf,wrkqadm,wrkqd}"

  bin/wrkqadm --db "{{env_dir}}/wrkq.db" init >/dev/null
  echo "  ok    ephemeral store initialized at tmp/env/wrkq.db (scratch; the suite uses its own)"

  echo "==> env-up: ready — run 'just e2e'"

# Tear down the e2e environment (safe to run when nothing is up)
env-down:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "==> env-down: wrkq"
  # Nothing long-lived is started by env-up, so teardown is the state dir alone.
  # Kept unconditional and non-failing: env-down must be safe to run twice, and
  # after a crash, without becoming the reason a room cannot clean up.
  if [[ -d "{{env_dir}}" ]]; then
    rm -rf "{{env_dir}}"
    echo "  ok    removed tmp/env"
  else
    echo "  ok    tmp/env already absent"
  fi
  echo "==> env-down: clean"

# Run the e2e suite (provisions its own environment first)
e2e: env-up
  #!/usr/bin/env bash
  set -euo pipefail
  echo "==> e2e: wrkq smoke suite"
  test/smoke-wrkqd.sh
  test/smoke-mergeadm.sh
  test/smoke-wrkf.sh
  test/smoke-wrkf-rpc.sh
  test/smoke-wrkf-wrkq-code-change.sh
  test/smoke-wrkf-adoption.sh
  test/smoke-wrkf-adoption-negative.sh
  echo "==> e2e: green"

# --- @wrkq/client TS package (not part of `just build`; published by `just install`) ---

# Install JS deps for the quarantined @wrkq/client package
client-install:
  cd packages/client && bun install

# Type-check + run @wrkq/client unit tests (fake transport; no binary needed)
# Depends on client-install: on a fresh clone packages/client/node_modules is
# absent, and `bun x tsc` auto-installs runtime deps only — it rewrites bun.lock
# and still leaves the @types/bun devDependency unresolved, so typecheck dies
# with TS2688 on every virgin clone (T-06894).
client-test: client-install
  cd packages/client && bun run typecheck && bun run test:unit

# Run @wrkq/client integration tests against repo-local binaries and isolated temp DBs.
# Verification must never mutate the installed CLI or publish the local client package.
client-integration: build client-install
  #!/usr/bin/env bash
  set -euo pipefail
  repo_root="$PWD"
  cd packages/client
  WRKQ_BIN="$repo_root/bin/wrkq" \
    WRKF_BIN="$repo_root/bin/wrkf" \
    WRKQADM_BIN="$repo_root/bin/wrkqadm" \
    WRKQD_BIN="$repo_root/bin/wrkqd" \
    bun run test:integration

# Full RPC verification: repo-local Go build + TS unit + isolated TS integration
verify-rpc: client-test client-integration
  @echo "✓ verify-rpc passed (@wrkq/client unit + integration green)"

# Build @wrkq/client dist (tsc → dist/{index,wrkq,wrkf,testing})
client-build: client-install
  cd packages/client && bun run build

# Publish @wrkq/client to local Verdaccio as a timestamped dev build
# (<base>-dev.YYYYMMDDHHMMSS) tagged latest — matches the agent-spaces convention.
client-publish-dev: client-build
  cd packages/client && bun scripts/publish-local-verdaccio.ts

# Validate packing of the timestamped dev build without publishing
client-publish-dev-dry-run: client-build
  cd packages/client && bun scripts/publish-local-verdaccio.ts --dry-run

# Publish @wrkq/client at its exact package.json version (e.g. 0.1.0); use for tagged releases
client-publish-source: client-build
  cd packages/client && bun scripts/publish-local-verdaccio.ts --source-versions

# Publish @wrkq/client at an explicit semver (e.g. `just client-publish-semver 0.1.1`)
client-publish-semver version tag="latest" force="": client-build
  cd packages/client && bun scripts/publish-local-verdaccio.ts --version "{{version}}" --tag "{{tag}}" {{force}}

# Validate packing of an explicit semver without publishing
client-publish-semver-dry-run version tag="latest": client-build
  cd packages/client && bun scripts/publish-local-verdaccio.ts --version "{{version}}" --tag "{{tag}}" --dry-run

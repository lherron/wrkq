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

# Build shipped CLI binaries (wrkq, wrkf, wrkqadm, wrkqd)
build:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "Building wrkq, wrkf, wrkqadm, and wrkqd binaries..."
  rm -f bin/wrkq-rpccli bin/wrkq-legacy
  LDFLAGS="$(scripts/ldflags.sh)"
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkq ./cmd/wrkq
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkf ./cmd/wrkf
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkqadm ./cmd/wrkqadm
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkqd ./cmd/wrkqd

# Build the temporary old-vs-new RPC cutover oracle binaries.
build-rpc-oracle:
  echo "Building wrkq legacy/RPC oracle binaries..."
  go build -tags sqlite_fts5 -o bin/wrkq-legacy ./cmd/wrkq-legacy
  go build -tags sqlite_fts5 -o bin/wrkq-rpccli ./cmd/wrkq-rpccli

# Conservative no-network check for agents/CI sandboxes
agent-check:
  scripts/agent-check.sh

# Compile packages only with vendored deps, no network, and conservative parallelism
agent-compile:
  GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOFLAGS='-mod=vendor -p=1' CGO_CFLAGS='-O0 -g0' \
    go test -tags sqlite_fts5 -run '^$' ./...

# Build all binaries with vendored deps, no network, and conservative parallelism
agent-build:
  #!/usr/bin/env bash
  set -euo pipefail
  mkdir -p bin
  LDFLAGS="$(scripts/ldflags.sh)"
  export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOFLAGS='-mod=vendor -p=1' CGO_CFLAGS='-O0 -g0'
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkq ./cmd/wrkq
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkf ./cmd/wrkf
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkqadm ./cmd/wrkqadm
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkqd ./cmd/wrkqd

# Run the wrkq CLI
run *args:
  go run ./cmd/wrkq {{args}}

# Find source entry points for a topic
find-entry-points topic:
  @go run ./cmd/find-entry-points --root . "{{topic}}"

# Explain source area exports and import relationships
explain-area path:
  @go run ./cmd/explain-area --root . "{{path}}"

# Scaffold a new Cobra CLI command in internal/cli
new-command name:
  scripts/new-cli-command "{{name}}"

# Run the wrkqadm CLI
wrkqadm-run *args:
  go run ./cmd/wrkqadm {{args}}

# Run CLI tests with coverage
test-coverage:
  go test -tags sqlite_fts5 -v -coverprofile=coverage.out ./...
  go tool cover -html=coverage.out -o coverage.html

# Install CLI binaries to ~/.local/bin, publish @wrkq/client, and sync downstream consumers.
# Pass no-sync=1 to skip syncing downstream consumer repos.
install no-sync="": build
  #!/usr/bin/env bash
  set -euo pipefail
  echo "Installing to ~/.local/bin/..."
  mkdir -p ~/.local/bin
  # Remove old binaries first to avoid crashes when overwriting running binaries
  rm -f ~/.local/bin/wrkq ~/.local/bin/wrkf ~/.local/bin/wrkqadm ~/.local/bin/wrkqd ~/.local/bin/wrkq-rpccli
  cp bin/wrkq ~/.local/bin/wrkq
  cp bin/wrkf ~/.local/bin/wrkf
  cp bin/wrkqadm ~/.local/bin/wrkqadm
  cp bin/wrkqd ~/.local/bin/wrkqd
  chmod +x ~/.local/bin/wrkq
  chmod +x ~/.local/bin/wrkf
  chmod +x ~/.local/bin/wrkqadm
  chmod +x ~/.local/bin/wrkqd
  echo "✓ Installed to ~/.local/bin/wrkq"
  echo "✓ Installed to ~/.local/bin/wrkf"
  echo "✓ Installed to ~/.local/bin/wrkqadm"
  echo "✓ Installed to ~/.local/bin/wrkqd"
  echo ""
  if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
    echo "⚠️  Add ~/.local/bin to your PATH:"
    echo "   export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo ""
  fi
  echo "✓ Run 'wrkq version', 'wrkf --help', 'wrkqadm version', and 'wrkqd --help' to verify"
  echo ""
  just client-publish-dev
  if [ -z "{{ no-sync }}" ]; then
    just sync-downstream
  else
    echo "[install] skipping downstream sync (no-sync=1)"
  fi

# Sync downstream repos that consume @wrkq/client from local Verdaccio.
sync-downstream:
  #!/usr/bin/env bash
  set -euo pipefail
  ( cd ../hrc-runtime && bun run sync:wrkq ) 2>&1 | sed 's/^/[hrc-sync] /'
  ( cd ../agent-control-plane && bun run sync:wrkq ) 2>&1 | sed 's/^/[acp-sync] /'
  ( cd ../taskboard && bun run sync:wrkq ) 2>&1 | sed 's/^/[taskboard-sync] /'

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
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkq ./cmd/wrkq
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkf ./cmd/wrkf
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkqadm ./cmd/wrkqadm
  go build -tags sqlite_fts5 -ldflags "$LDFLAGS" -o bin/wrkqd ./cmd/wrkqd
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

# Run all tests (Go + Node.js when available)
test:
  @echo "Running Golang tests..."
  go test -tags sqlite_fts5 ./...
  @echo "✓ Golang tests complete"

# Run all tests with verbose logs
test-verbose:
  go test -v -tags sqlite_fts5 ./...

# Verify code quality (suppression meta-lint + layer boundary + lint + test + rot sensor + surface guard + doc links + architecture records + @wrkq/client unit+integration RPC)
verify: suppression-lint layer-boundary lint test rot-sensor surface-guard doc-links architecture-records verify-rpc
  @echo "✓ All checks passed"

# Run the full slow backstop tier (verify [incl. RPC] + smoke)
verify-full: verify smoke
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
  @echo "✓ Smoke test passed"

# Probe the canonical wrkf adoption signal. This is intentionally standalone;
# it fails until the real canonical wrkq-code-change adoption run exists.
check-wrkf-adoption:
  scripts/check-wrkf-adoption.sh

# --- @wrkq/client TS package (not part of `just build`; published by `just install`) ---

# Install JS deps for the quarantined @wrkq/client package
client-install:
  cd packages/client && bun install

# Type-check + run @wrkq/client unit tests (fake transport; no binary needed)
client-test:
  cd packages/client && bun run typecheck && bun run test:unit

# Run @wrkq/client integration tests against the REAL installed `wrkq`/`wrkf rpc --stdio`
client-integration: install
  cd packages/client && bun run test:integration

# Full RPC verification: Go build/install unaffected + TS unit + TS integration
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

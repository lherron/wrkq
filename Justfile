set shell := ["bash", "-cu"]

# Default task: show common commands
default:
  @just --list

# --- Database tasks ---

# Run migrations against local database
db-migrate-local:
  node scripts/db-migrate.mjs --dir db/migrations

# Run migrations against remote database
db-migrate-remote url:
  node scripts/db-migrate.mjs --dir db/migrations --url "{{url}}"

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

# Build CLI binaries (wrkq, wrkqadm, wrkqd)
build:
  echo "Building wrkq, wrkqadm, and wrkqd binaries..."
  go build -o bin/wrkq ./cmd/wrkq
  go build -o bin/wrkqadm ./cmd/wrkqadm
  go build -o bin/wrkqd ./cmd/wrkqd

# Run the wrkq CLI
run *args:
  go run ./cmd/wrkq {{args}}

# Run the wrkqadm CLI
wrkqadm-run *args:
  go run ./cmd/wrkqadm {{args}}

# Run CLI tests with coverage
test-coverage:
  go test -v -coverprofile=coverage.out ./...
  go tool cover -html=coverage.out -o coverage.html

# Install CLI binaries to ~/.local/bin (no sudo required)
install: build
  #!/usr/bin/env bash
  set -euo pipefail
  echo "Installing to ~/.local/bin/..."
  mkdir -p ~/.local/bin
  # Remove old binaries first to avoid crashes when overwriting running binaries
  rm -f ~/.local/bin/wrkq ~/.local/bin/wrkqadm ~/.local/bin/wrkqd
  cp bin/wrkq ~/.local/bin/wrkq
  cp bin/wrkqadm ~/.local/bin/wrkqadm
  cp bin/wrkqd ~/.local/bin/wrkqd
  chmod +x ~/.local/bin/wrkq
  chmod +x ~/.local/bin/wrkqadm
  chmod +x ~/.local/bin/wrkqd
  echo "✓ Installed to ~/.local/bin/wrkq"
  echo "✓ Installed to ~/.local/bin/wrkqadm"
  echo "✓ Installed to ~/.local/bin/wrkqd"
  echo ""
  if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
    echo "⚠️  Add ~/.local/bin to your PATH:"
    echo "   export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo ""
  fi
  echo "✓ Run 'wrkq version', 'wrkqadm version', and 'wrkqd --help' to verify"

# Install CLI binaries to /usr/local/bin (requires sudo - run manually)
install-system:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "Building wrkq binaries..."
  go build -o bin/wrkq ./cmd/wrkq
  go build -o bin/wrkqadm ./cmd/wrkqadm
  go build -o bin/wrkqd ./cmd/wrkqd
  echo "Installing to /usr/local/bin/wrkq (requires sudo)..."
  # Remove old binary first to avoid crashes when overwriting running binaries
  sudo rm -f /usr/local/bin/wrkq
  sudo rm -f /usr/local/bin/wrkqadm
  sudo rm -f /usr/local/bin/wrkqd
  sudo cp bin/wrkq /usr/local/bin/wrkq
  sudo cp bin/wrkqadm /usr/local/bin/wrkqadm
  sudo cp bin/wrkqd /usr/local/bin/wrkqd
  sudo chmod +x /usr/local/bin/wrkq
  sudo chmod +x /usr/local/bin/wrkqadm
  sudo chmod +x /usr/local/bin/wrkqd
  echo "✓ Installed to /usr/local/bin/wrkq"
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
    echo "wrkq and wrkqadm are not installed"
  fi

# Format CLI code
fmt:
  go fmt ./...

# Lint all code (Go + JS/TS when available)
lint:
  @echo "Linting Golang code..."
  golangci-lint run
  @echo "✓ Golang linting complete"

# Run all tests (Go + Node.js when available)
test:
  @echo "Running Golang tests..."
  go test -v ./...
  @echo "✓ Golang tests complete"

# Verify code quality (lint + test)
verify: lint test
  @echo "✓ All checks passed"

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

# Run quick smoke test (build + wrkqd + merge smoke scripts)
smoke: build
  test/smoke-wrkqd.sh
  test/smoke-mergeadm.sh
  @echo "✓ Smoke test passed"

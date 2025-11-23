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

# Build both CLI binaries (wrkq and wrkqadm)
build:
  go build -o bin/wrkq ./cmd/wrkq
  go build -o bin/wrkqadm ./cmd/wrkqadm

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

# Install both CLI binaries to ~/.local/bin (no sudo required)
install:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "Building wrkq and wrkqadm binaries..."
  go build -o bin/wrkq ./cmd/wrkq
  go build -o bin/wrkqadm ./cmd/wrkqadm
  echo "Installing to ~/.local/bin/..."
  mkdir -p ~/.local/bin
  cp bin/wrkq ~/.local/bin/wrkq
  cp bin/wrkqadm ~/.local/bin/wrkqadm
  chmod +x ~/.local/bin/wrkq
  chmod +x ~/.local/bin/wrkqadm
  echo "✓ Installed to ~/.local/bin/wrkq"
  echo "✓ Installed to ~/.local/bin/wrkqadm"
  echo ""
  if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
    echo "⚠️  Add ~/.local/bin to your PATH:"
    echo "   export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo ""
  fi
  echo "✓ Run 'wrkq version' and 'wrkqadm version' to verify"

# Install CLI binary to /usr/local/bin (requires sudo - run manually)
install-system:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "Building wrkq binary..."
  go build -o bin/wrkq ./cmd/wrkq
  echo "Installing to /usr/local/bin/wrkq (requires sudo)..."
  sudo cp bin/wrkq /usr/local/bin/wrkq
  sudo chmod +x /usr/local/bin/wrkq
  echo "✓ Installed to /usr/local/bin/wrkq"
  echo "✓ Run 'wrkq --version' to verify"

# Uninstall both CLI binaries from ~/.local/bin
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

# Run quick smoke test (build + basic tests)
smoke: build test
  @echo "✓ Smoke test passed"

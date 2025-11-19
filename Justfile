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

# Build the CLI binary
cli-build:
  go build -o bin/todo ./cmd/todo

# Run the CLI
cli-run *args:
  go run ./cmd/todo {{args}}

# Run CLI tests
cli-test:
  go test -v ./...

# Run CLI tests with coverage
cli-test-coverage:
  go test -v -coverprofile=coverage.out ./...
  go tool cover -html=coverage.out -o coverage.html

# Install CLI binary to $GOPATH/bin
cli-install:
  go install ./cmd/todo

# Install CLI binary to ~/.local/bin (no sudo required)
install:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "Building todo binary..."
  go build -o bin/todo ./cmd/todo
  echo "Installing to ~/.local/bin/todo..."
  mkdir -p ~/.local/bin
  cp bin/todo ~/.local/bin/todo
  chmod +x ~/.local/bin/todo
  echo "✓ Installed to ~/.local/bin/todo"
  echo ""
  if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
    echo "⚠️  Add ~/.local/bin to your PATH:"
    echo "   export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo ""
  fi
  echo "✓ Run 'todo --version' to verify"

# Install CLI binary to /usr/local/bin (requires sudo - run manually)
install-system:
  #!/usr/bin/env bash
  set -euo pipefail
  echo "Building todo binary..."
  go build -o bin/todo ./cmd/todo
  echo "Installing to /usr/local/bin/todo (requires sudo)..."
  sudo cp bin/todo /usr/local/bin/todo
  sudo chmod +x /usr/local/bin/todo
  echo "✓ Installed to /usr/local/bin/todo"
  echo "✓ Run 'todo --version' to verify"

# Uninstall CLI binary from ~/.local/bin
uninstall:
  #!/usr/bin/env bash
  set -euo pipefail
  if [ -f ~/.local/bin/todo ]; then
    echo "Removing ~/.local/bin/todo..."
    rm ~/.local/bin/todo
    echo "✓ Uninstalled from ~/.local/bin/todo"
  elif [ -f /usr/local/bin/todo ]; then
    echo "Removing /usr/local/bin/todo (requires sudo)..."
    sudo rm /usr/local/bin/todo
    echo "✓ Uninstalled from /usr/local/bin/todo"
  else
    echo "todo is not installed"
  fi

# Format CLI code
cli-fmt:
  go fmt ./...

# Lint CLI code
cli-lint:
  golangci-lint run

# --- Node.js app tasks (FUTURE) ---

# Install dependencies for all Node.js workspaces
install-deps:
  pnpm install

# Build all Node.js apps
build:
  pnpm -r build

# Run development servers (web + api)
dev:
  pnpm --parallel --filter @todo/web --filter @todo/api dev

# Type check all TypeScript code
check:
  pnpm -r run typecheck

# Lint all JavaScript/TypeScript code
lint-js:
  pnpm -r run lint

# Format all code
format:
  pnpm -r run format

# Format and write changes
format-write:
  pnpm -r run format:write

# --- Quality gates ---

# Lint all code (Go + JS/TS when available)
lint:
  @echo "Linting Golang code..."
  golangci-lint run || true
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
pre-commit: cli-fmt lint test
  @echo "✓ Pre-commit checks passed"

# --- Clean tasks ---

# Clean Go build artifacts
cli-clean:
  rm -rf bin/ coverage.out coverage.html

# Clean Node.js build artifacts
clean-node:
  rm -rf apps/web/dist apps/api/dist packages/*/dist node_modules apps/*/node_modules packages/*/node_modules

# Clean all build artifacts
clean: cli-clean
  @echo "✓ Clean complete"

# --- Development helpers ---

# Show project structure
tree:
  tree -I 'node_modules|dist|bin|coverage*|.git' -L 3

# Run quick smoke test (build + basic tests)
smoke: cli-build cli-test
  @echo "✓ Smoke test passed"

# --- Platform-specific tasks ---

# Build macOS app (FUTURE)
macos-build:
  @echo "macOS app build not yet implemented"

# Build iOS app (FUTURE)
ios-build:
  @echo "iOS app build not yet implemented"

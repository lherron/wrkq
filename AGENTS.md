# Repository Guidelines


## WRKQ Usage Reference

** ALWAYS USE WRKQ TO TRACK YOUR TASK **

@WRKQ-USAGE.md

## Project Structure & Module Organization
- `cmd/wrkq` and `cmd/wrkqadm`: CLI entrypoints. Keep binaries in `bin/` (gitignored).
- `internal/*`: domain packages (actors, db, parser, render, etc.). Prefer extending existing packages before adding new ones.
- `db/migrations`, `db/seeds/*`, `schema_dump.sql`: schema changes and seed data.
- `scripts/*.mjs`: Node helpers for migrations, seeds, and resets.
- `docs/`, `WRKQ-USAGE.md`, `test/` (smoke shells), `tmp/` (scratch).

## Build, Test, and Development Commands
- Go 1.25+; fetch deps with `go mod download` if needed.
- Build binaries: `just build`; run via `./bin/wrkq ...` or `just run -- ...`.
- Quality gates: `just fmt` (gofmt), `just lint` (golangci-lint), `just test` (go test ./...).
- Coverage: `just test-coverage` → `coverage.out` + `coverage.html`.
- Install/uninstall: `just install` / `just uninstall`. DB chores: `just db-migrate-local`, `just db-seed-dev`, `just db-reset` (destructive, reads a prompt).

## Coding Style & Naming Conventions
- Trust `gofmt`; keep functions short and package-level tests nearby.
- Go naming: packages lower_snake, exported identifiers CamelCase, CLI flags/subcommands kebab-case (Cobra style).
- Wrap errors with context; avoid new globals outside `config` and `db`.
- New commands follow verb-first patterns already in `cmd/wrkq`.

## Testing Guidelines
- Co-locate table-driven unit tests with code; favor small fixtures.
- Run `go test ./...` before pushing; use `coverage.out` to watch deltas on new features.
- Smoke scripts in `test/smoke-*.sh` rely on built binaries in `bin/`; run after impactful CLI changes.
- Use `db/seeds/test` only against local/test URLs; `--allow-remote` is required for anything else.

## Commit & Pull Request Guidelines
- Conventional Commits preferred (`feat`, `fix`, `chore`, `test`, optional scopes like `fix(mcp): ...`).
- Keep commits tight; mention affected commands or migration IDs when relevant.
- Before opening a PR: run `just verify`, describe user-visible CLI changes, link issues/tasks, and include sample command output when it clarifies behavior.

## Configuration & Security Notes
- Config order: `.env.local` → `~/.config/wrkq/config.yaml` → env vars. Key vars: `WRKQ_DB_PATH`, `WRKQ_ATTACH_DIR`, `WRKQ_LOG_LEVEL`, `WRKQ_OUTPUT`, `WRKQ_PAGER`, `WRKQ_ACTOR`.
- Use env vars or `_FILE` variants for secrets; never commit SQLite files or attachment contents.
- Default attachment path `~/.local/share/wrkq/attachments` is user-specific and should stay untracked. 

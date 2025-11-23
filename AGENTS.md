# Repository Guidelines

## Project Structure & Module Organization
- Go CLI sources live in `cmd/wrkq` and `cmd/wrkqadm`; shared logic is in `internal/` (keep new packages here, not under `cmd/`).
- Database assets are under `db/` (`migrations/`, `seeds/`, `baseline.sql`); helper scripts sit in `scripts/`.
- Cross-cutting docs are in `docs/`; high-level planning docs include `M2-SUMMARY.md` and `MILESTONE-VERIFICATION.md`.
- Frontend experiments reside in `apps/web` (pnpm workspace scaffold). Place any new JS/TS packages under `apps/` or `packages/` to keep Go and Node paths clean.

## Build, Test, and Development Commands
- `just cli-build` — build both Go binaries into `bin/`.
- `just cli-run --help` — run the wrkq CLI locally; swap `cli-run` with `wrkqadm-run` for admin.
- `just test` — run Go test suite (verbose) after a quick lint banner.
- `go test ./...` — fallback when `just` is unavailable.
- `just cli-test-coverage` — generate `coverage.out` and `coverage.html`.
- `just db-migrate-local` / `db-reset` — apply or reset local SQLite migrations; use with care.

## Coding Style & Naming Conventions
- Go code: format with `go fmt ./...`; lint with `golangci-lint run`. Target Go `1.25.x`.
- Prefer small packages in `internal/`; name files after the feature (e.g., `queue.go`, `queue_test.go`).
- Public-facing commands use verb-first Cobra commands; flags are `kebab-case`. Keep functions under ~50 lines when possible.
- Tests live alongside code as `*_test.go`; table-driven tests are preferred.

## Testing Guidelines
- Primary framework: Go standard testing. Aim to cover new behaviors and edge conditions; expose panics via `t.Run` subtests.
- Coverage gate: run `just cli-test-coverage` before PRs and inspect new logic.
- For DB changes, add a migration in `db/migrations` plus seed updates in `db/seeds/dev` when needed.

## Commit & Pull Request Guidelines
- Follow Conventional Commits (`feat:`, `fix:`, `chore:`, `test:`, `docs:`). Scope in parentheses is welcome (`feat(api): ...`).
- Keep commits small and reversible; include tests and migration files with the behavior change.
- PRs should describe intent, how to test (`just` commands), and any schema changes. Link issues; add screenshots for UI changes under `apps/web`.
- Before opening, run `just pre-commit` (fmt + lint + tests) and ensure binary outputs are not committed (`bin/`, `coverage*` are ignored).

## Security & Configuration Tips
- Store secrets via environment variables; local dotenv is read with `github.com/joho/godotenv`.
- SQLite files are transient; avoid committing generated DB artifacts. If testing against a remote DB, pass `--url` to migration scripts explicitly.

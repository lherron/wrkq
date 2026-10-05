# 7. Project facts (wrkp)

wrkp posts **foreign facts**, attributed and typed, onto a project's timeline. It reads that timeline merged with
wrkq's own task and message entries. Two built-in producers feed it: the git hooks (`git.commit`, `git.push`)
and the `just` shim (`run.settled`). Fitkit consumes these facts, including `verify.upkeep` from this skill's
MAINTAIN.md. Code: `cmd/wrkp/`, `internal/rpccli/wrkp*.go` (`wrkp.go`, `wrkp_log.go`, `wrkp_git.go`,
`wrkp_just.go`), `tools/just/just` (the shim), `tools/hooks/`, and `internal/rpccli/set.go` (`--root`). Guide:
`internal/rpccli/embedded/WRKP-USAGE.md` (`wrkp info`).

## Sub-features

- **Post.** `wrkp post <project> --type a.b --key K -m SUMMARY --attr k=v ... [--task T] [--occurred-at TS]`.
  The same `--key` again returns the same uuid, so a repeated post is a single fact.
- **Read.** `wrkp log <project> [--after CURSOR] [--since] [--type a,b,x.*] [--task T] [--json|--ndjson]`. Each
  entry is `{type: "project.event"|task.*|message, principalRef, projectEvent: {type, summary, attributes}}`.
  `task.created`/`task.edited`/`task.moved` show only when `--type` names them.
- **Forward cursor.** `wrkp cursor <project>` captures a point, and `wrkp log --after C` reads oldest-first from
  it up to now.
- **Types.** `wrkp types <project>` lists every stored fact type with its count.
- **Show.** `wrkp show <uuid> --json` returns the full row, including `idempotencyKey`, `occurredAt`,
  `principalRef` and `scopeRef`.
- **Post refusals** (all `WRKQ_VALIDATION`, exit 1): a reserved first segment (`--type task.x`,
  `reserved_namespace`), a type that isn't dotted lowercase (`Foo`, `invalid_format`), and no `--attr` at all
  (`attributes are required`).
- **Git producers.** `wrkp git commit` (post-commit) and `wrkp git push` (pre-push, reads ref lines on stdin)
  resolve the checkout through the **registered project roots**. They always exit 0, and anything they can't do
  is reported as one `wrkp git: ...` line on stderr.
- **just producer.** The shim at `~/.local/bin/just` (installed by `just install`, ahead of the real `just`)
  hands every invocation to `wrkp just`. A justfile that contains the line `# wrkp: run.settled` gets one
  `run.settled` fact per top-level recipe run (`recipe`, `status`, `duration_ms`, `source: wrkp-just`, ...).
  Listings and other non-run modes go straight to the real `just`. The exit status is always just's.
- **Project roots.** `wrkq set <project> --root <dir>` stores a top-level project's checkout root (as `~/...`
  under `$HOME`). `wrkq projects --json` shows it as `root`.

## How to get to it

Run `eval "$(wv env <name>)"`. The scratch project `wv-<name>` has no root until you set one, so make a
throwaway git repo under `$WV_STATE` for the producers.

## Driving it

```bash
C=$(wrkp cursor wv-<name>)
wrkp post wv-<name> --type wv.probe --key wv-probe:1 -m "WV fact WV-P-1" --attr outcome=ok --attr n=1 --json | jq -r .uuid
wrkp post wv-<name> --type wv.probe --key wv-probe:1 -m "repeat" --attr outcome=ok --json | jq -r .uuid     # same uuid
wrkp log wv-<name> --after "$C" --ndjson | jq -c '.projectEvent | {type, summary, attributes}'
wrkp types wv-<name>                                                                          # wv.probe count 1
mkdir -p "$WV_STATE/repo" && cd "$WV_STATE/repo" && git init -q -b main \
  && printf '# wrkp: run.settled\nhello:\n\techo wv-just-ok\nfail:\n\texit 3\n' > justfile \
  && git add justfile && git -c user.name=wv -c user.email=wv@local commit -qm "wv seed"
wrkp git commit                                   # "wrkp git: ... is not a registered project root; skipping", exit 0
wrkq set wv-<name> --root "$WV_STATE/repo"
wrkp git commit && wrkp log wv-<name> --type git.commit --json | jq -c '[.[].projectEvent | {summary, attributes}]'
just hello; just fail; just --list >/dev/null
wrkp log wv-<name> --type run.settled --json | jq -c '[.[].projectEvent | {summary, recipe: .attributes.recipe, status: .attributes.status}]'
```

Live, read only: `wrkp types wrkq` and `wrkp log wrkq --type verify.upkeep --limit 1 --json`.

## Gotchas

- **Producers need a registered root.** Without `--root`, `wrkp git` and `wrkp just` resolve no project
  (source read, not driven: `wrkp git --project <slug>` skips the root lookup). They
  skip with a one-line stderr notice and exit 0, so nothing fails visibly. A linked worktree resolves to the
  registered main checkout that owns it.
- The shim posts with the caller's environment, so a `just` run inside a shell with `wv env` evaluated posts to
  the **scratch** daemon. A `just` run in the real wrkq checkout from that shell would post its `run.settled`
  to the scratch, not to canonical.
- `git.commit` facts carry `source: lefthook` even when you run `wrkp git commit` by hand.
- The log projection has no `idempotencyKey` field, so `.projectEvent.idempotencyKey` reads null even for a keyed
  post. `wrkp show <uuid>` carries it. The direct proof of idempotency is the replayed `post --json` returning
  `{"uuid": <same>, "created": false}`. A replay with a different summary or attributes still returns the
  original row unchanged, with no conflict (2026-10-05, T-10349 `07-project-facts/drive.txt`).
- Scratch facts carry your seat's canonical `scopeRef` (SKILL.md "Launch").
- `wrkp log` hides `turn.*` facts and the quiet task entries unless `--type` names them. `--timeout` and
  `--stall-after` without `--follow` are a usage error (exit 2).
- `just -n <recipe>` and `just --summary` are non-run modes, like `--list`. They post nothing.

## Proven when

The keyed post repeats to the same uuid and `types` counts it once. The forward read after the cursor returns
exactly the new fact, with its attributes and your principal. `wrkp git commit` skips before `--root` and
posts a `git.commit` (sha, branch, author, `files_changed`, plus node, insertions, deletions, parents) after it. `just hello`/`just fail` post
`run.settled` with status 0 and 3, the listing posts nothing, and just's exit code passes through (3).

Driven 2026-10-05 on wv `t-10349` (T-10349 upkeep), shim from installed 037fe66: `var/wrkq-artifacts/T-10349/07-project-facts/drive.txt`.
Live reads: `live/reads.txt` (`wrkp types wrkq`; no `verify.upkeep` before this pass).

---
name: verify-wrkq
description: Launch, check, drive and prove any wrkq feature (the store and wrkqd, tasks and containers, comments/attachments/handoffs, claims and promises, campaigns, wrkc rooms, wrkp project facts, wrkf workflows, monitor and webhooks, the RPC and @wrkq/client) against the installed build, with evidence that survives. Use when changing, operating, debugging or grading wrkq, or before claiming a wrkq change works.
---

# verify-wrkq

You prove a wrkq claim by driving the installed build and keeping the evidence. This skill gives the one
way to do that. `features/README.md` maps the ten features. Each feature file says how to reach the feature,
how to drive it, what bites, and what "proven" looks like.

Only the wrkq project composes this skill. `asp-targets.toml` in the wrkq repo merges it into clod, cody and
the foundry resident when they run in project wrkq.

## Launch

You can drive on a scratch daemon or read the live one:

- **Scratch (the default).** `wv up --name <task>` (the helper next to this file) runs `wrkqadm init` on a fresh
  store under `~/praesidium/var/state/wrkq-wv/<name>/`. It serves that store with the **installed** `wrkqd`
  (`~/.local/bin`) on a free loopback port, with a stub wrkf hook catalog (`wv_pass`, `wv_fail`), node tokens for
  nodes `wv` and `wv2`, and its own attachment dir. It also creates the scratch project `wv-<name>`. Then
  `eval "$(wv env <name>)"` points `wrkq`, `wrkc`, `wrkf`, `wrkp` and `@wrkq/client` at it as node `wv`. Use
  the scratch for every drive that writes. Name it after your task (`--name T-10298` becomes `t-10298`), so its
  ids and facts start empty.
- **What the scratch doesn't isolate** (2026-10-05, T-10349). `wv env` leaves the seat's `HRC_SESSION_REF`,
  `AGENT_SCOPE_REF` and `ASP_*` in place. Scratch writes still carry your real canonical scope as `scope_ref`
  (`wrkq whoami`, `wrkp show`), and an unattributed write resolves your agent from `ASP_AGENT_ID`, not a refusal.
  Feature 6 unsets `HRC_SESSION_REF`. A scratch task's `artifact_dir` is a host hint that points into the
  **canonical** `~/praesidium/var/wrkq-artifacts/<id>`, and `wrkq touch` creates that directory (`touch.go`;
  empty `T-00001.*` dirs from earlier scratch drives already sit in the canonical root). Never write
  evidence there. It goes under the pass task's own `artifact_dir`.
- **Live (read-only).** The canonical daemon runs on mini (`WRKQ_DB=rpc://mini` from `~/praesidium/.env.local`).
  On any node, read only: `wrkq whoami`, `wrkq server health --addr mini:7171`, an `rpc.initialize` frame through
  `wrkq rpc --stdio` (pass `protocolVersion`, feature 10, and read `.result.server.revision`; without it the frame
  is refused, though `error.data.serverRevision` still names the build), `wrkq projects`, `wrkp types <project>`,
  `wrkf workflow list --json` (`.templates[]`), `wrkq webhook list --output json` (it has no `--json`), and
  `wrkc inbox` for your own scope. Never write test data to the
  canonical store, never migrate it, never restart its job (its procedure is AGENTS.md "Justfile is the
  lifecycle", and only Mable or Lance runs it).
- Scratch tests **what is installed**. To test an uninstalled change, build it (`just build`) and run
  `WV_BIN=$PWD/bin wv up --name <task>-dev`: the scratch daemon is then `bin/wrkqd`. Run the clients from
  `bin/` too. Never run `just install` for a verification drive.

## Doctor

Start with these three checks, and run them again after anything surprising. All are read-only.

```bash
eval "$(wv env <name>)"
wrkq whoami --json                                     # db_locator must be the scratch rpc://127.0.0.1:<port>
wrkq server health --addr "127.0.0.1:${WRKQ_DB##*:}"   # {"status":"ok"}
wrkqadm --db "$WV_STATE/wrkq.db" doctor --json | jq -c '[.checks[] | select(.status!="ok")]'
```

On a scratch, doctor should report every row ok. Any non-ok row is real. For the live
daemon, `whoami` must show `rpc://<mini address>:7171` and health must be ok. Doctor's DB checks need the DB
file, which only mini has, so don't run doctor against the canonical store from another node.

## Drive

1. Find the feature in [features/README.md](features/README.md) and read its file.
2. Run Doctor.
3. Drive the feature as the file's "Driving it" section shows. Record each command with
   `wv rec <artifact_dir>/NN-<feature>/drive.txt '<command>'` as you go (see "Evidence" below).
4. Check the file's "Proven when" against what you recorded.
5. If the drive disagreed with the file, fix the file in the same change (add a Gotcha with the date and the
   evidence).

`<binary> --help` and `<binary> <verb> --help` give the live surface. `wrkq info`, `wrkc info` and `wrkp info`
serve the agent guides.

## Evidence

- **Use the installed surface, not a unit test.** A drive proves a claim when the installed binaries run
  end to end against a real `wrkqd`, and you read the result from their own outputs: `--json` reads,
  `wrkq log`, `wrkp log`, `wrkf task timeline`, `wrkc log`, the webhook sink's file, the daemon log.
- **Keep an artifact that survives.** Put it under your task's `artifact_dir`
  (`~/praesidium/var/wrkq-artifacts/<task>/`), in this layout:
  - `NN-<feature>/drive.txt` for each feature, written by `wv rec`. Each entry is a `## <UTC time>` line, then
    `$ <command>`, then its output, then `exit: <code>`. The file reruns as written after
    `eval "$(wv env <name>)"`. Commands run under `bash -o pipefail`, so a failed command can't hide behind a
    pipe into `jq`. The flip side: a stream cut short by `| head` records `exit: 141` (SIGPIPE). Bound streams
    with `--timeout` and filter with `jq` instead;
  - `evidence/<scratch name>/` for each scratch, written by `wv evidence <name> <dir>` before `wv down`. It holds
    the daemon log, health, doctor, the project tree, the hook catalog and a `.backup` copy of the store;
  - `live/` for reads of the canonical daemon.

  `wv evidence` refuses a directory inside the scratch state, because `down` removes that state.
- **Make it repeatable.** Someone else can rerun `drive.txt` on a fresh `wv up` and reach the same end state.
  Ids are deterministic on a fresh store (`T-00001`, `EN-00001`, ...) when the drives run in file order.
- **Reproduce before you fix.** For a defect, record the failing drive first. If you can't reproduce it, say so
  and show what you ran.
- **Name what you couldn't drive,** and the concrete prerequisite that stopped you. On a scratch, HRC delivery
  of `wrkc` mail (presenting, steering, waking a seat) is out of reach, because HRC's kicker tails the canonical
  ledger. Feature 6 says what the scratch can prove instead.
- **Prove an absence with a count:** for example, a `wrkp log --type X --json | jq length` of 0 after the
  action, or `wc -l` on the webhook sink's file.

## Cleanup

`wv down <name>` stops the scratch's `wrkqd` (only the pid that `up` recorded, and only while it still serves
that store) and removes its state directory. `wv down <name> --dry-run` shows the plan first. `wv list` shows
every scratch with its pid, port and liveness. Also stop anything you started next to the scratch, such as a
webhook sink (feature 9). The evidence directories stay.

## Maintain

The upkeep pass keeps this skill true. It covers index hygiene, one source read per feature, a scratch drive of
every feature, triage (doc drift, harness gap, product gap), at most one commit, and one `verify.upkeep` fact at
the end. The procedure is in [MAINTAIN.md](MAINTAIN.md).

## Helpers

The drive inputs the feature files name live outside this skill directory, in the repo at `~/praesidium/wrkq/spaces/verify-wrkq/fixtures/`
(`spaces/verify-wrkq/fixtures/`): `wv-flow.json` and `wv-flow-fail.json` (feature 8), `sink.py` (feature 9) and
`client-drive.ts` (feature 10). They are data you pass to `wrkf`, `python3` and `bun`, not helpers.

`wv` (this directory, mode 100755) is the only helper. Every verb except `env`, `run` and `rec` prints one JSON
object. A refusal prints `{error, message, next}` and exits 1.

| Verb | Invocation | Does |
| --- | --- | --- |
| `up` | `wv up [--name N]` | Inits a fresh store, starts the installed `wrkqd` on a free loopback port with node tokens (`wv`, `wv2`) and a stub hook catalog, creates project `wv-N` (N lowercased), and prints name, state_root, db, locator, port, pid, project and hook_catalog. Refuses `already_up` |
| `env` | `eval "$(wv env N)"` | Exports `WRKQ_DB`, `WRKQD_TOKEN_FILE` (node `wv`), `WRKQ_PROJECT_ROOT=wv-N`, `WRKF_HOOK_CATALOG`, `WRKQ_ATTACH_DIR` and `WV_STATE`. Process env beats the dotenv `rpc://mini` |
| `run` | `wv run N -- CMD ...` | Runs one command with N's environment |
| `rec` | `wv rec FILE 'CMD'` | Runs CMD under `bash -o pipefail` in the caller's environment, and appends `## <UTC>`, `$ CMD`, the output and `exit: <code>` to FILE (and stdout) |
| `evidence` | `wv evidence N DIR` | Copies the daemon log and hook catalog, and writes health, `wrkqadm doctor --json`, `wrkq tree /wv-N --state all` and a `.backup` of the store into DIR |
| `down` | `wv down N [--dry-run]` | Stops N's `wrkqd` and removes its state |
| `list` | `wv list` | Every scratch, with pid, port, `alive` and `started_at` |

`WV_HOME` (default `~/praesidium/var/state/wrkq-wv`) moves the scratch root. `WV_BIN` (default `~/.local/bin`)
picks the `wrkqadm`/`wrkqd`/`wrkq` binaries that `up` runs. `wv scratch up|down|list` are aliases that match
foundry's `fv`.

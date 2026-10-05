# Maintain: the upkeep pass

This is the recurring pass that keeps this skill true as wrkq changes. Once fitkit's `verify-upkeep` is stood up
for project wrkq, the wrkq foundry resident files each pass as a task on its upkeep schedule tick and dispatches
an agent to it. Anyone can also run a pass by hand the same way. The pass follows foundry's verify-foundry
MAINTAIN.md.

**Edit scope:** this skill directory, including `wv`. No product code. A product defect becomes a wrkq task.

**The last pass** is the `head` of the newest `verify.upkeep` fact
(`wrkp log wrkq --type verify.upkeep --limit 1 --json`). When there is none, it is the skill's first commit
(`git log --reverse --format=%H -- spaces/verify-wrkq | head -1`). Below, "since the last pass" always means
`<that sha>..HEAD`.

**Evidence** goes under the pass task's `artifact_dir` (`~/praesidium/var/wrkq-artifacts/<task>/`), in the layout
[SKILL.md](SKILL.md) "Evidence" describes: `index/` (step 1), `sources/NN.md` (step 2), `plan.md` (step 3), one
`NN-<feature>/drive.txt` per feature (step 4), `evidence/<scratch name>/` from each `wv evidence`, `live/` for the
canonical reads, and `ship.txt` (step 6).

**A pass never installs.** It never runs `just install`, never migrates, and never restarts `wrkqd`, ACP or
HRC. It drives what is installed. If it has to drive uninstalled code, it builds `bin/` and runs
`WV_BIN=$PWD/bin wv up`.

## 1. Index hygiene

Check `features/README.md` against `features/*.md`: every file is linked, every link resolves, and each row's
summary still names what its file covers. Then compare the binaries' verbs with the map:
`for b in wrkq wrkc wrkf wrkp wrkqadm; do $b --help; done`. Every user-facing verb should be named in some feature
file. Map any verb that isn't, in step 6, unless it is truly internal (say which in `index/unmapped.txt`). Save
the comparison in `index/`.

## 2. Source reads, one per feature

For each feature file, read the code it names, plus `git log <last pass>..HEAD -- <those paths>`, and answer two
questions. What does the code do now? Which Sub-features, Gotchas or Proven-when lines have probably drifted?
Also flag any behavior the file doesn't mention at all. Cite file:line. When the session can, fan these reads out
as read-only subagents. Otherwise read them one after another. Write one note per feature to
`<artifact_dir>/sources/NN.md`.

## 3. Reconcile

Merge the source notes into a plan that drives every feature on as few scratches as practical. One
`wv up --name <task>` usually serves all ten, run in file order. Name each scratch after the pass task, so its ids
and facts start empty. For each feature, list its drive and the suspected drift and new behavior the drive must
confirm or clear. Write the plan to `<artifact_dir>/plan.md`.

## 4. Live pass in scratch, over every feature

Drive every feature on every pass (ten today), even when no wrkq commit landed since the last pass. Drift also
comes from what wrkq runs on and with: the HRC session env, the `just` shim, the published `@wrkq/client`, and
the Go and Bun toolchains. The source reads in step 2 don't see those.

Run Doctor first, and again after any surprise. Drive each feature's "Driving it" section on the scratch, **plus
every drift or new behavior that step 2 flagged**, with `wv rec` into `NN-<feature>/drive.txt`. Do the canonical
read-only reads (SKILL.md "Launch") into `live/`. Check each result against "Proven when". Run
`wv evidence <name> <artifact_dir>/evidence/<name>` before `wv down`, so the evidence survives cleanup. Count the
features you drove to their Proven when (`driven`). Name any feature you couldn't drive, with the prerequisite
that stopped you.

## 5. Triage every failure

| Class | Meaning | Action |
| --- | --- | --- |
| Doc drift | The build is right and the file is stale | Fix the feature file (a Gotcha with the date and evidence) |
| Harness gap | `wv` or this skill can't reach or observe it | Fix `wv` or SKILL.md, and re-drive |
| Product gap | The build is wrong | File a task under `wrkq/inbox` with the failing drive. Never paper over it in the map |

## 6. Ship

Make at most one commit, holding the proven map and harness fixes, and re-drive each fix before you commit it.
Commit with explicit paths and a private index, and push to origin/main only when `origin/main..HEAD` is all
yours (shared-checkout rules). Keep the commit, push and verification output in `ship.txt`. Comment the outcome
and coverage on the pass task:

- `clean`: nothing changed;
- `changed`: the commit SHA and what moved;
- `blocked`: what stopped the pass, and the task that tracks it.

Add `driven/features`, the evidence path, any product tasks you filed, and a **source-only, undriven** list:
each finding from step 2 that went into the map without a drive behind it, with why it wasn't driven.

## 7. Post the fact

End every pass by posting `verify.upkeep`. That includes a blocked pass and a pass that stopped early. Its
attributes and their meanings are the `consumes` block of fitkit's `verify-upkeep@2` entry
(`foundry direct fitkit.catalog '{}'` from a foundry checkout). `features` is the number of `features/*.md`
files other than README.md, and `head` is `git rev-parse HEAD` after the ship. Post the fact last, after the
task comment, so its `occurred_at` is the time the pass finished:

```bash
wrkp post wrkq --type verify.upkeep --key verify-upkeep:<task> -m "verify-wrkq upkeep <task>: <outcome>" \
  --attr outcome=<clean|changed|blocked> --attr features=<n> --attr driven=<n> --attr head=<sha> --attr task=<task>
```

This posts to the **canonical** daemon, so run it in a shell where `wv env` is not evaluated. The key makes a
repeated post a single fact. Then complete the task, or leave it open only when the pass is blocked, and say
what it is blocked on.

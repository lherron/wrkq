Here’s a self‑contained spec you can drop in as `docs/PATCH-MODE.md` (or splice into `SPEC.md` as a new major section), plus an explicit `wrkqadm` CLI surface.

I’ll write it assuming it *extends* the existing SPEC and supersedes the old “bundle”‑style branch sync (18.x) when enabled.

---

```md
# wrkq Patch-First Mode (Canonical Snapshots + JSON Patch)

This document defines a **patch-first** Git/PR workflow for wrkq based on:

- Canonical JSON **state snapshots** of the DB.
- RFC 6902 **JSON Patch** change sets.
- A dedicated `wrkqadm` CLI surface for export, diff, apply, rebase, and validation.
- Deterministic auto‑renumbering of friendly IDs (e.g. `T-00050`) during patch rebase.

It builds on the domain model, ETag semantics, and CLI conventions defined in `SPEC.md` and is intended to be compatible with existing commands. When enabled, this mode replaces the “bundle‑based” Branch/PR flow (Section 18) for configuration / schema‑level sync.

---

## 1. Goals

- PRs carry **minimal, semantic diffs** instead of opaque DB dumps.
- Snapshots and patches are **deterministic**: same DB state → same `state.json` and patch bytes.
- Agents can **plan, validate, and dry‑run** changes using JSON Patch primitives.
- Main is protected by **strong invariants** (FKs, DAG, state machines, friendly ID uniqueness).
- Branch conflicts on friendly IDs (e.g. `T-00050`) are resolved deterministically via **auto‑renumber on rebase**.

---

## 2. Canonical State Snapshot

### 2.1 Location

- Repo canonical snapshot: `.wrkq/state.json`
- Each branch may have its own `.wrkq/state.json` representing desired DB state on that branch.
- Main branch is the **authoritative base**: `main:.wrkq/state.json`

Snapshots are treated as **state**, not change logs.

### 2.2 JSON Shape

Canonical snapshot is a single JSON object:

```json
{
  "meta": {
    "schema_version": 1,
    "snapshot_rev": "sha256:…",   // optional but recommended
    "generated_at": "2025-11-30T12:34:56Z",
    "machine_interface_version": 1
  },
  "actors": {
    "<actor_uuid>": { ... actor fields ... }
  },
  "containers": {
    "<container_uuid>": { ... container fields ... }
  },
  "tasks": {
    "<task_uuid>": { ... task fields ... }
  },
  "comments": {
    "<comment_uuid>": { ... comment fields ... }
  },
  "links": {
    "<link_uuid>": { ... link fields ... }
  },
  "events": {
    "<event_uuid>": { ... minimal materialized metadata ... }
  }
}
```

Rules:

- **Keys under each table** are **UUIDs** from the DB (not friendly IDs).
- Each value is a JSON object containing the same logical fields as the DB row:
  - `id` (friendly ID: `P-xxxxx`, `T-xxxxx`, etc.), `slug`, `title`, `state`, `actor_id`, timestamps, etc.
- Fields that are not relevant to patch‑level operations (e.g. internal SQLite rowids) are omitted.

### 2.3 Canonicalization Rules

Canonicalization ensures byte‑for‑byte determinism:

- Encoding:
  - UTF‑8, JSON Canonicalization Scheme (JCS) or equivalent:
    - stable key order (lexicographic),
    - no insignificant whitespace.
- Object key ordering:
  - Root keys: `meta`, `actors`, `containers`, `tasks`, `comments`, `links`, `events` (this order).
  - Within each table: UUID keys sorted lexicographically.
  - Within each entity object: properties sorted lexicographically by key.
- Arrays:
  - Arrays that semantically represent sets (e.g. `labels`, dependency lists) are sorted:
    - strings: lexicographically.
    - IDs: lexicographically by underlying UUID or friendly ID (consistent choice).
  - Arrays with meaningful order get an explicit `order` field; sorting is by that field.
- Null/empty:
  - `null` and empty collections are **omitted** entirely.
  - Absence ≡ default.
- Timestamps:
  - ISO‑8601 with `Z` suffix, e.g. `2025-11-30T12:34:56Z`.
  - Only fields that **semantically change** content are emitted; purely mechanical `updated_at` churn should be avoided where possible.
- Numeric precision:
  - Integers serialized normally.
  - Floats/decimals (if any) canonicalized to a fixed precision (e.g. 9 decimal places) or stringified.

### 2.4 Snapshot Revision

`meta.snapshot_rev` is an opaque revision for the entire snapshot, recommended as:

- `snapshot_rev = "sha256:" + hex(sha256(canonical_json_bytes))`

It can be persisted in the DB (e.g. a `meta` table) and used by `wrkqadm patch apply --if-match` to avoid blind writes.

---

## 3. RFC 6902 JSON Patch

### 3.1 Patch Location

- One **canonical patch file per branch/PR**:

  - `.wrkq/patches/<branch>.json` (e.g. `.wrkq/patches/feature-tidy-auth.json`)

The patch represents changes from a **base snapshot** (usually `main:.wrkq/state.json`) to the branch’s desired snapshot.

### 3.2 Format

- Standard RFC 6902 JSON Patch: an array of operations.

```json
[
  {
    "op": "add",
    "path": "/tasks/2fa0a6d6-3b0d-4b3b-9fdd-4bb0d5e6a7c1",
    "value": {
      "id": "T-00050",
      "slug": "ship-json-patches",
      "title": "Ship JSON patches",
      "state": "open",
      "project_id": "4e44b1fb-6a3e-4c6d-9d80-0d41e742cda9",
      "labels": ["infra"]
    }
  },
  {
    "op": "replace",
    "path": "/tasks/2fa0a6d6-3b0d-4b3b-9fdd-4bb0d5e6a7c1/state",
    "value": "completed"
  },
  {
    "op": "remove",
    "path": "/tasks/9a8b7c6d-.../archived_at"
  }
]
```

### 3.3 Path Conventions

- Top‑level path segments: `/actors`, `/containers`, `/tasks`, `/comments`, `/links`, `/events`, `/meta`.
- Second segment:
  - For entity tables: UUID (`/tasks/<uuid>`).
  - For meta: known keys (`/meta/snapshot_rev`, `/meta/version`, etc.).
- Deeper segments: field names sorted lexicographically for nested structures.

Patch operations **never** use friendly IDs (`T-00050`) or slugs in the path; those are fields in the `value` object.

### 3.4 Allowed Ops and Normalization

- `add`, `remove`, `replace` are primary.
- `move`, `copy`, `test` may be supported but are not required for PR patches.
- Normalization rules:
  - No `add` on an existing path.
  - No `remove` of non‑existent path.
  - No redundant `replace` where value is unchanged.
  - Patch is normalized to **smallest equivalent op set** under these rules.

### 3.5 Domain Invariants During Apply

When a patch is applied to a snapshot (or DB), the following must hold (enforced in `wrkqadm patch validate` / `apply`):

- DB‑level schema invariants:
  - FK constraints.
  - NOT NULL, value ranges, etc.
- Domain invariants (from `SPEC.md`):
  - Task dependency graph is acyclic.
  - No task in `completed` with open dependencies.
  - Slug uniqueness among siblings.
  - Friendly ID uniqueness per resource type (see next section).
- Snapshot invariants:
  - No orphan tasks, comments, or attachments.

If any invariant fails in `--strict` / CI mode, exit code **4** (conflict).

---

## 4. IDs, Friendly IDs, and Auto-Renumber

### 4.1 Internal vs Friendly IDs

- **Internal ID**: `uuid` (primary key in DB), used in:
  - Snapshot map keys (`/tasks/<uuid>`).
  - Cross‑refs: `project_id`, `task_id`, etc.
- **Friendly ID**:
  - `id` field, readable code: `P-00007`, `T-00123`, `C-00042`, `A-00003`.
  - Must be unique **per resource type** on main:
    - No two tasks share the same `id`.
    - No two containers share the same `id`, etc.

All machine references in patches and the DB use UUIDs; friendly IDs are metadata.

### 4.2 Friendly ID Format

Friendly IDs are:

- Prefix: a single letter for the resource type:
  - `P-` (containers/projects)
  - `T-` (tasks)
  - `C-` (comments)
  - `A-` (actors), etc.
- Hyphen separator: `-`
- Numeric suffix:
  - Zero‑padded decimal.
  - Width is stable per resource type (e.g. 5 digits):
    - `T-00001` .. `T-99999`

Pattern:

```text
^[PTCA]-[0-9]{5,}$    # "at least 5 digits" allows future extension
```

Width is inferred from existing IDs (e.g., first task ID on a new DB fixes `T` width).

### 4.3 Uniqueness Invariant

On **main**:

> For each resource type, `id` is unique among all non‑deleted/non‑archived rows.

This is enforced:

- At DB level via UNIQUE index.
- At patch apply time via validation.

On feature branches:

- The same invariant SHOULD hold within the branch DB.
- Collisions with future main are allowed temporarily; they are resolved at rebase.

### 4.4 Auto-Renumber on Rebase

When rebasing a patch from `base_snapshot` to `new_base_snapshot`, we may discover that friendly IDs introduced by the patch collide with IDs that now exist on `new_base_snapshot`.

Rather than failing, `wrkqadm patch rebase` **auto‑renumbers** colliding friendly IDs in the patch to maintain uniqueness.

#### 4.4.1 Terminology

- `S_base` – base snapshot (e.g. `main` at time of branching).
- `S_new` – new base snapshot (e.g. `main` at merge time).
- `P` – original patch from `S_base` → branch state.
- `S_branch` – snapshot after applying `P` to `S_base`.

#### 4.4.2 Algorithm (per resource type, e.g. tasks)

For each resource type:

1. **Compute branch state**:
   - `S_branch = apply(P, S_base)`

2. **Determine new entities**:
   - For tasks:
     - `new_task_ids = keys(S_branch.tasks) - keys(S_base.tasks)`
   - For each `uuid` in `new_task_ids`, record `friendly_id = S_branch.tasks[uuid].id`.

3. **Collect existing friendly IDs on new base**:
   - `existing_ids = { task.id | task ∈ S_new.tasks }`
   - Include only non‑deleted / non‑archived tasks.

4. **Resolve collisions in deterministic order**:
   - Sort `new_task_ids` lexicographically by UUID (or stable creation order if available).
   - For each `uuid` in that order:
     - Let `orig_id = S_branch.tasks[uuid].id` (e.g. `T-00050`).
     - If `orig_id` not in `existing_ids`, reserve it:
       - `existing_ids.add(orig_id)`; continue.
     - If `orig_id` **is** in `existing_ids`, compute a new friendly ID:
       1. Parse `orig_id` into `(prefix, number, width)`:
          - Example: `T-00050` → `prefix="T-"`, `number=50`, `width=5`.
       2. Find the highest existing number among IDs with the same prefix:
          - `max_n = max({ n | prefix + zero_pad(n, width) ∈ existing_ids }, default=0)`
       3. Set `new_n = max_n + 1`.
       4. Construct `new_id = prefix + zero_pad(new_n, width)`.
       5. Update `S_branch.tasks[uuid].id = new_id`.
       6. Add `new_id` to `existing_ids`.
   - Repeat for each resource type that uses friendly IDs (containers, comments, actors) as needed.

5. **Recompute rebased patch**:
   - `P_rebased = diff(S_new, S_branch)` using the canonical diff logic.

6. **Emit optionally a mapping**:
   - `wrkqadm patch rebase --json` MAY return:

     ```json
     {
       "code_rewrites": {
         "tasks": {
           "2fa0a6d6-...": { "from": "T-00050", "to": "T-00051" }
         }
       }
     }
     ```

   - For human PR summaries: _“Renumbered T‑00050 → T‑00051 on new task ‘Ship JSON patches’ due to collision with an existing task on main.”_

#### 4.4.3 Edge Cases

- **Non-standard friendly IDs**:
  - If an `id` does not match the expected pattern (e.g., `T-foo`), `wrkqadm patch rebase` MUST:
    - Either:
      - treat it as a hard error in `--strict` mode (exit 4, explain), or
      - fall back to appending a numeric suffix (e.g., `T-foo-2`) in non‑strict mode.
  - The default for CI should be **strict** for predictability.

- **Multiple colliding new tasks**:
  - Algorithm handles this via sorting and extended `existing_ids`:
    - If both new tasks wanted `T-00050`, one will keep it (if first and not yet taken on new base), the second will be bumped to `T-00051`, etc.

- **Containers / comments / actors**:
  - Same pattern applies type‑wise; all use the same `(prefix, number, width)` logic.

---

## 5. New `wrkqadm` CLI Surface (Patch-First)

This section defines new `wrkqadm` commands for managing canonical snapshots and patches.

All commands obey global config rules (DB path resolution, logging) from `SPEC.md` and share the same exit code semantics.

### 5.1 `wrkqadm state export`

Export the current DB into a canonical JSON snapshot.

**Synopsis**

```sh
wrkqadm state export [--out <file>] [--canonical] [--include-events] [--json]
```

**Behavior**

- Reads the current DB (from `WRKQ_DB_PATH`, config, or flag).
- Produces the canonical snapshot JSON as described in §2.
- If `--canonical` (default `true`), applies full canonicalization (JCS).
- Computes `snapshot_rev` and writes into `meta.snapshot_rev`.
- By default, **omits full event payloads** and only includes minimal event metadata; `--include-events` retains full event objects.

**Flags**

- `--out <file>`:
  - Output path (default `.wrkq/state.json`).
- `--canonical` / `--no-canonical`:
  - Toggle canonicalization (non‑canonical not recommended).
- `--include-events`:
  - Include full `events` table in snapshot.
- `--json`:
  - Print a summary object to stdout:
    - `{"out": ".wrkq/state.json", "snapshot_rev": "sha256:...", "actors": N, "containers": N, "tasks": N, ...}`

**Exit codes**

- `0` success.
- `1` IO/DB error.
- `2` usage error.

---

### 5.2 `wrkqadm state import`

Hydrate the DB from a canonical snapshot.

**Synopsis**

```sh
wrkqadm state import [--from <file>] [--dry-run] [--if-empty] [--force] [--json]
```

**Behavior**

- Reads snapshot from `--from` (default `.wrkq/state.json`).
- Validates shape and invariants.
- If not `--dry-run`:
  - Option A (default, safe):
    - Require the DB to be empty (no tasks/containers) unless `--force` is set.
  - Option B (force):
    - Truncate relevant tables and reinsert from snapshot.
- Updates DB‑level meta (e.g. stored `snapshot_rev`) as appropriate.

**Flags**

- `--from <file>`: snapshot file.
- `--dry-run`: validate only; do not write.
- `--if-empty`: require DB to be empty; else exit 4.
- `--force`: allow import into non‑empty DB by truncating tables.
- `--json`: output summary of what would be or was done.

**Exit codes**

- `0` success.
- `1` IO/DB error.
- `2` usage error.
- `4` conflict (DB not empty with `--if-empty`, invariant violation, etc.).

---

### 5.3 `wrkqadm state verify`

Check round‑trip determinism for a snapshot file.

**Synopsis**

```sh
wrkqadm state verify <snapshot-file> [--json]
```

**Behavior**

- Import the snapshot into an in‑memory or temporary DB.
- Re‑export with `state export --canonical`.
- Compare bytes of input vs output.
- If unequal, exit 4 and emit diagnostics (diff summary).

**Exit codes**

- `0` snapshots are byte‑identical under canonical export.
- `1` IO/DB error.
- `2` usage error.
- `4` non‑deterministic (round‑trip failed).

---

### 5.4 `wrkqadm patch create`

Compute a canonical patch between two snapshots.

**Synopsis**

```sh
wrkqadm patch create \
  --from <base-snapshot> \
  --to <target-snapshot> \
  --out <patch-file> \
  [--allow-noncanonical] \
  [--json]
```

**Behavior**

- Loads `base` and `target` snapshots (usually `main:.wrkq/state.json` and branch `.wrkq/state.json`).
- Normalizes them to canonical form (unless `--allow-noncanonical`).
- Computes RFC 6902 patch `P` such that `apply(P, base) == target`.
- Normalizes patch (no redundant ops, valid paths).
- Writes patch to `--out` (e.g. `.wrkq/patches/feature-branch.json`).

**Flags**

- `--from <file>`: base snapshot.
- `--to <file>`: target snapshot.
- `--out <file>`: patch output path.
- `--allow-noncanonical`: skip canonicalization (not recommended).
- `--json`: summary:

  ```json
  {
    "out": ".wrkq/patches/feature-foo.json",
    "ops": 12,
    "adds": 5,
    "replaces": 6,
    "removes": 1
  }
  ```

**Exit codes**

- `0` success.
- `1` IO error.
- `2` usage error.

---

### 5.5 `wrkqadm patch validate`

Validate a patch against a base snapshot and domain rules.

**Synopsis**

```sh
wrkqadm patch validate \
  --patch <patch-file> \
  --base <snapshot-file> \
  [--strict] \
  [--json]
```

**Behavior**

- Loads base snapshot and patch.
- Applies patch to base in memory.
- Validates:
  - RFC 6902 shape and path correctness.
  - Schema invariants.
  - Domain invariants (DAG acyclic, slugs unique, etc.).
  - Friendly ID uniqueness in the resulting snapshot.
- In `--strict` mode, any violation → exit 4.

**Exit codes**

- `0` patch is valid.
- `1` IO error.
- `2` usage error.
- `4` validation failure.

---

### 5.6 `wrkqadm patch apply`

Apply a patch to the **live DB**, with optional DB‑level revision guard.

**Synopsis**

```sh
wrkqadm patch apply \
  --patch <patch-file> \
  [--if-match <snapshot-rev>] \
  [--dry-run] \
  [--strict] \
  [--json]
```

**Behavior**

1. Exports current DB into `S_current` (`state export` in memory).
2. If `--if-match` is provided:
   - Fetch DB‑stored `snapshot_rev` (from meta).
   - If mismatch, exit 4 (no changes).
3. Apply patch to `S_current` → `S_new_snapshot`.
4. Validate `S_new_snapshot` as in `patch validate`.
5. If `--dry-run`:
   - Exit after validation; no writes.
6. Else:
   - Import `S_new_snapshot` into DB (transactional):
     - Compute row‑level diffs and perform minimal DB updates.
     - Update DB meta `snapshot_rev` to hash of `S_new_snapshot`.

**Exit codes**

- `0` success.
- `1` IO/DB error.
- `2` usage error.
- `4` conflict (snapshot_rev mismatch, validation failure, invariant violation).

---

### 5.7 `wrkqadm patch rebase`

Rebase a patch from an old base snapshot to a new base snapshot, including auto‑renumber.

**Synopsis**

```sh
wrkqadm patch rebase \
  --patch <patch-file> \
  --old-base <snapshot-file> \
  --new-base <snapshot-file> \
  --out <rebased-patch-file> \
  [--strict-ids] \
  [--json]
```

**Behavior**

1. Load `old_base`, `new_base`, and `patch`.
2. Compute `S_branch = apply(patch, old_base)`.
3. Run auto‑renumber algorithm (§4.4) against `new_base`:
   - For each resource type, renumber colliding `id` fields on **new entities**.
4. Compute `rebased_patch = diff(new_base, S_branch)` using canonical diff.
5. Write `rebased_patch` to `--out`.

**Flags**

- `--strict-ids`:
  - Treat malformed friendly IDs as a hard error (no auto “T-foo‑2” hacks).
- `--json`:
  - Emit summary plus optional code rewrite mapping:

    ```json
    {
      "out": ".wrkq/patches/feature-foo.rebased.json",
      "ops": 14,
      "code_rewrites": {
        "tasks": {
          "2fa0a6d6-...": { "from": "T-00050", "to": "T-00051" }
        }
      }
    }
    ```

**Exit codes**

- `0` success.
- `1` IO error.
- `2` usage error.
- `4` failure to rebase (invalid patch, malformed IDs under `--strict-ids`, etc.).

---

### 5.8 `wrkqadm patch summarize` (optional but recommended)

Generate a human/agent‑friendly summary of a patch for PR descriptions.

**Synopsis**

```sh
wrkqadm patch summarize \
  --patch <patch-file> \
  [--base <snapshot-file>] \
  [--format text|markdown|json]
```

**Behavior**

- Optionally uses the base snapshot for richer context (titles, paths).
- Groups ops by entity:
  - “3 tasks added, 1 task updated, 2 containers renamed, 4 comments added.”
- For Markdown:
  - Emits a small table:

    | Entity    | Op       | ID      | Path / Title             |
    |----------|----------|---------|--------------------------|
    | task     | add      | T-00051 | `portal/auth/login-ux`   |
    | task     | replace  | T-00012 | state: `open` → `done`   |

**Exit codes**

- `0` success.
- `1` IO error.
- `2` usage error.

---

## 6. Git / PR Workflow (Patch-First Mode)

This section is **normative** for patch‑first; hooks/CI implementations can vary as long as they maintain the invariants.

### 6.1 Files

- `main`:
  - `.wrkq/state.json` – canonical snapshot (authoritative base).
  - Optionally, historical patches under `.wrkq/patches/applied/…` for audit.
- Feature branch `feature/foo`:
  - `.wrkq/state.json` – optional snapshot (used locally and in CI).
  - `.wrkq/patches/feature-foo.json` – **required**; canonical patch from `main` to branch.

Invariant:

> `.wrkq/patches/<branch>.json` must equal `wrkqadm patch create --from main:.wrkq/state.json --to .wrkq/state.json` for that branch.

### 6.2 Local Developer Flow (Conceptual)

1. Developer / agent edits DB via `wrkq` commands.
2. Run snapshot + patch:

   ```sh
   wrkqadm state export --out .wrkq/state.json
   wrkqadm patch create \
     --from main:.wrkq/state.json \
     --to .wrkq/state.json \
     --out .wrkq/patches/$(git rev-parse --abbrev-ref HEAD).json
   ```

3. Pre‑commit hook (recommended):
   - Runs `wrkqadm state verify .wrkq/state.json`.
   - Recomputes patch in a temp file and ensures it matches `.wrkq/patches/<branch>.json`.
   - Optionally `wrkqadm patch validate --strict`.

4. Commit `.wrkq/patches/<branch>.json` (and optionally `.wrkq/state.json`).

### 6.3 CI Flow (Conceptual)

On PR for `feature/foo`:

1. Check out main and branch.
2. Confirm:

   ```sh
   wrkqadm patch create --from main:.wrkq/state.json --to feature:.wrkq/state.json > tmp.patch
   diff -q tmp.patch .wrkq/patches/feature-foo.json
   ```

3. Validate patch:

   ```sh
   wrkqadm patch validate \
     --patch .wrkq/patches/feature-foo.json \
     --base main:.wrkq/state.json \
     --strict
   ```

4. Apply to an ephemeral DB:

   ```sh
   wrkqadm patch apply \
     --patch .wrkq/patches/feature-foo.json \
     --dry-run --strict
   ```

5. If main has advanced since branch was created:
   - Rebase patch:

     ```sh
     wrkqadm patch rebase \
       --patch .wrkq/patches/feature-foo.json \
       --old-base <snapshot-at-branch-point>.json \
       --new-base main:.wrkq/state.json \
       --out tmp.rebased.patch
     ```

   - Re‑validate and apply `tmp.rebased.patch`.

6. Run domain tests / integration tests.
7. On merge, canonical main snapshot is updated and committed:

   ```sh
   wrkqadm state export --out .wrkq/state.json
   git commit -am "Update wrkq state for merged PR #123"
   ```

---

### 6.4 Pre-Commit Hook Example

The following shell script can be used as a Git pre-commit hook (`.git/hooks/pre-commit`):

```bash
#!/bin/bash
set -e

# Skip if no wrkq changes
if ! git diff --cached --name-only | grep -qE '^\.wrkq/'; then
  exit 0
fi

BRANCH=$(git rev-parse --abbrev-ref HEAD)
PATCH_FILE=".wrkq/patches/${BRANCH}.json"

echo "Verifying wrkq state..."

# 1. Verify snapshot round-trip
if [ -f .wrkq/state.json ]; then
  wrkqadm state verify .wrkq/state.json
  if [ $? -ne 0 ]; then
    echo "Error: Snapshot verification failed (exit 4)"
    exit 1
  fi
fi

# 2. Verify patch matches current diff (if on feature branch)
if [ "$BRANCH" != "main" ] && [ -f "$PATCH_FILE" ]; then
  TEMP_PATCH=$(mktemp)
  wrkqadm patch create \
    --from main:.wrkq/state.json \
    --to .wrkq/state.json \
    --out "$TEMP_PATCH" 2>/dev/null

  if ! diff -q "$TEMP_PATCH" "$PATCH_FILE" > /dev/null 2>&1; then
    echo "Error: Patch file out of sync with state"
    echo "Run: wrkqadm patch create --from main:.wrkq/state.json --to .wrkq/state.json --out $PATCH_FILE"
    rm -f "$TEMP_PATCH"
    exit 1
  fi
  rm -f "$TEMP_PATCH"

  # 3. Validate patch
  wrkqadm patch validate \
    --patch "$PATCH_FILE" \
    --base main:.wrkq/state.json \
    --strict
  if [ $? -eq 4 ]; then
    echo "Error: Patch validation failed"
    exit 1
  fi
fi

echo "wrkq checks passed"
exit 0
```

### 6.5 Exit Codes Reference

All `wrkqadm` patch commands follow these exit codes:

| Code | Meaning | Example Scenario |
|------|---------|------------------|
| 0 | Success | Operation completed successfully |
| 1 | IO/DB error | File not found, database error |
| 2 | Usage error | Missing required flags, invalid arguments |
| 4 | Conflict/Validation | ETag mismatch, `--if-match` failure, invariant violation, strict validation failure |

Use these exit codes in CI scripts for precise error handling:

```bash
wrkqadm patch validate --patch .wrkq/patches/feature.json --base main:.wrkq/state.json --strict
case $? in
  0) echo "Validation passed" ;;
  1) echo "IO error - check file paths"; exit 1 ;;
  2) echo "Usage error - check command syntax"; exit 1 ;;
  4) echo "Validation failed - fix invariants"; exit 1 ;;
esac
```

### 6.6 GitHub Actions CI Example

```yaml
name: wrkq-patch-validate
on:
  pull_request:
    paths:
      - '.wrkq/**'

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0  # Need full history for main comparison

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.22'

      - name: Install wrkqadm
        run: go install github.com/lherron/wrkq/cmd/wrkqadm@latest

      - name: Get main snapshot
        run: |
          git show origin/main:.wrkq/state.json > /tmp/main-state.json

      - name: Verify snapshot determinism
        run: wrkqadm state verify .wrkq/state.json

      - name: Validate patch
        run: |
          BRANCH="${GITHUB_HEAD_REF}"
          PATCH_FILE=".wrkq/patches/${BRANCH}.json"
          if [ -f "$PATCH_FILE" ]; then
            wrkqadm patch validate \
              --patch "$PATCH_FILE" \
              --base /tmp/main-state.json \
              --strict
          fi

      - name: Dry-run apply
        run: |
          BRANCH="${GITHUB_HEAD_REF}"
          PATCH_FILE=".wrkq/patches/${BRANCH}.json"
          if [ -f "$PATCH_FILE" ]; then
            wrkqadm patch apply \
              --patch "$PATCH_FILE" \
              --dry-run \
              --strict
          fi

      - name: Generate PR summary
        if: success()
        run: |
          BRANCH="${GITHUB_HEAD_REF}"
          PATCH_FILE=".wrkq/patches/${BRANCH}.json"
          if [ -f "$PATCH_FILE" ]; then
            echo "## wrkq Patch Summary" >> $GITHUB_STEP_SUMMARY
            echo "" >> $GITHUB_STEP_SUMMARY
            wrkqadm patch summarize \
              --patch "$PATCH_FILE" \
              --base /tmp/main-state.json \
              --format markdown >> $GITHUB_STEP_SUMMARY
          fi
```

---

## 7. Compatibility and Transition

### 7.1 Coexistence with Bundle Flow

- Patch‑first mode can be introduced alongside the existing `wrkq bundle` commands.
- For repos adopting this spec:
  - `wrkqadm state export` / `patch create/apply` are the **preferred** primitives for config/infra PRs.
  - `wrkq bundle create/apply` may remain for task‑level content workflows if desired.
- Machine interfaces (`--json`, `--porcelain`, exit codes) follow the guarantees in `SPEC.md`.

### 7.2 Adoption Steps

1. **Enable patch-first** (incremental adoption):
   ```bash
   # Export initial snapshot from main
   wrkqadm state export --out .wrkq/state.json
   git add .wrkq/state.json
   git commit -m "Add canonical wrkq state snapshot"
   ```

2. **Set up directory structure**:
   ```bash
   mkdir -p .wrkq/patches
   ```

3. **Install pre-commit hook** (optional but recommended):
   ```bash
   cp docs/hooks/pre-commit .git/hooks/
   chmod +x .git/hooks/pre-commit
   ```

4. **Add CI workflow** (see section 6.6 for GitHub Actions example)

5. **Document team workflow**:
   - Before committing wrkq changes: run `wrkqadm state export`
   - Create patch: `wrkqadm patch create --from main:.wrkq/state.json --to .wrkq/state.json --out .wrkq/patches/<branch>.json`
   - Validate before push: `wrkqadm patch validate --strict`

### 7.3 Common Commands Cheatsheet

```bash
# Export current DB state to canonical snapshot
wrkqadm state export --out .wrkq/state.json

# Verify snapshot round-trip determinism
wrkqadm state verify .wrkq/state.json

# Create patch from main to current branch
wrkqadm patch create \
  --from main:.wrkq/state.json \
  --to .wrkq/state.json \
  --out .wrkq/patches/$(git rev-parse --abbrev-ref HEAD).json

# Validate patch against base
wrkqadm patch validate \
  --patch .wrkq/patches/feature.json \
  --base main:.wrkq/state.json \
  --strict

# Dry-run apply (no DB changes)
wrkqadm patch apply --patch .wrkq/patches/feature.json --dry-run

# Apply patch to DB
wrkqadm patch apply --patch .wrkq/patches/feature.json

# Rebase patch when main has advanced
wrkqadm patch rebase \
  --patch .wrkq/patches/feature.json \
  --old-base old-main-state.json \
  --new-base main:.wrkq/state.json \
  --out .wrkq/patches/feature.json

# Generate human-readable summary for PR
wrkqadm patch summarize \
  --patch .wrkq/patches/feature.json \
  --base main:.wrkq/state.json \
  --format markdown
```

---
```


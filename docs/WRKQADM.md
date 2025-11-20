Here’s how I’d split it: **`wrkq` = collab + agent surface**, **`wrkqadm` = DB / config / infra surface**. Everything an agent should ever need stays on `wrkq`; everything you don’t want inside an agent container moves to `wrkqadm`. [oai_citation:0‡SPEC.md](sediment://file_00000000b60c722f84e74d8a724f2d67)  

---

## 1. `wrkq` – Agent + Human Collaboration Surface

Think “things that operate *within* whatever DB I’m pointed at”: projects, tasks, comments, attachments, search, history.

### Top‑level commands on `wrkq`

```text
wrkq
  ls            # list containers/tasks
  tree          # tree view
  stat          # metadata only
  ids           # canonical IDs
  resolve       # fuzzy -> IDs/paths

  cat           # task doc (md+frontmatter)
  edit          # $EDITOR + 3-way merge
  apply         # apply doc; supports --base
  set           # quick field updates

  mkdir         # create containers
  touch         # create tasks
  mv            # move/rename containers/tasks
  cp            # copy tasks/containers (optional)
  rm            # archive/purge
  restore       # un-archive

  find          # metadata search
  rg            # FUTURE: content search

  log           # history from event log
  diff          # compare tasks/docs
  watch         # stream events

  attach ls     # list attachments
  attach get    # read attachment bytes
  attach put    # add attachment
  attach rm     # remove attachment

  comment ls    # list comments
  comment add   # add comment
  comment cat   # show comment body
  comment rm    # soft/hard delete comment

  bundle create # export PR bundle from current DB

  whoami        # resolved actor + DB
  version       # machine_interface_version etc
  completion    # shell completions
```

### Rationale

- **Agents need full CRUD on tasks/containers/comments/attachments** to be useful:
  - `ls/tree/stat/ids/resolve`, `cat/apply/set`, `mkdir/touch/mv/rm/restore`, `find`, `log/diff/watch`, `comment *`, `attach *`.
- **`bundle create` stays on `wrkq`** because agents and branch workers create bundles from their ephemeral DBs.
- **`whoami` + `version` stay** so agents can self‑describe and coordinate with orchestrators.
- `completion` is “nice for humans”, harmless for agents, and belongs on the primary CLI.

---

## 2. `wrkqadm` – Admin / Infra / Non‑Agent Surface

Think “things that operate on *the lifecycle of a DB* or on global config / actors / exports”, which you’ll run from CI, bootstrap scripts, or a human shell on the canonical DB.

### Top‑level commands on `wrkqadm`

```text
wrkqadm
  init              # create/migrate DB, seed default project/actors
  db snapshot       # WAL-safe point-in-time copy for agents

  bundle apply      # apply bundle into canonical DB
  bundle replay     # (optional/future) replay events.ndjson

  actors ls         # list all actors in the DB
  actor add         # create human/agent/system actors

  attach path       # expose absolute on-disk path for an attachment

  doctor            # DB/config/attach_dir health check
  config doctor     # show effective config + sources

  version           # same or superset of `wrkq version`
  completion        # (optional) completions for wrkqadm itself
```

#### Notes / intent per command

- **`init` → `wrkqadm init`**
  - DB creation + migrations + seeding default actor/project is clearly non‑agent.
- **`db snapshot` → `wrkqadm db snapshot`**
  - Run by CI or orchestration on the canonical DB to create safe read/write copies for agents.
- **`bundle apply` / `bundle replay` → `wrkqadm bundle …`**
  - These mutate the *authoritative* DB as part of merge/apply flows; they’re exactly the sort of thing you *don’t* want a coding agent doing ad‑hoc.
- **`actors ls` / `actor add` → `wrkqadm actor …`**
  - Defining and introspecting the global actor set is admin‑y; agents only need `whoami` and an injected actor slug/ID.
- **`attach path` → `wrkqadm attach path`**
  - Exposes raw host filesystem paths for exporters; keep out of the agent surface where they should stick to `attach get/put`.
- **`doctor` / `config doctor` → `wrkqadm …`**
  - These are operational health/config introspection commands.
- **`version` / `completion`**
  - I’d support them on both binaries; it’s cheap and keeps UX consistent.

---

## 3. “What moves where” – explicit diff

Starting from the current single‑binary spec:

### Moves from `wrkq` → `wrkqadm` (non‑agent)

- `init`
- `db snapshot`
- `bundle apply`
- `bundle replay` (future)
- `actors ls`
- `actor add`
- `attach path`
- `doctor`
- `config doctor`

### Stays on `wrkq` (agent-safe surface)

- Navigation / metadata: `ls`, `tree`, `stat`, `ids`, `resolve`
- Content: `cat`, `edit`, `apply` (incl. `--base`), `set`
- Structure/lifecycle: `mkdir`, `touch`, `mv`, `cp`, `rm`, `restore`
- Search: `find`, `rg` (future)
- History/streaming: `log`, `diff`, `watch`
- Attachments: `attach ls/get/put/rm`
- Comments: `comment ls/add/cat/rm`
- Bundles: `bundle create`
- Identity/introspection: `whoami`, `version`
- Misc: `completion`

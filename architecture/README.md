# wrkq — Durable Architecture Records

`architecture/` is the **canonical repo-local surface for wrkq's durable architecture
law**. It owns durable law only: invariant/risk **records**, producer **contracts**,
**ADR** provenance, and the generated **projections** of those. It is *not* a home for
all design docs — the [product/domain spec](docs/SPEC.md), runbooks, how-tos, and
explanatory material stay under [docs/](docs) and may link *into* `architecture/` when
they describe durable law.

> Paths in this file are repo-root-relative (the doc-link checker resolves them from the
> repository root), not relative to `architecture/`.

## What is normative vs projection

| Surface | Role |
|---|---|
| [invariant records](architecture/records/invariants), [risk records](architecture/records/risks) | **normative** — active records are wrkq's durable law |
| [producer contracts](architecture/contracts) | **normative** — active producer contracts (e.g. the wrkf RPC contract) |
| [ADRs](architecture/adr) | provenance / orientation (not authority unless elevated by an active record) |
| [INVARIANTS.md](architecture/INVARIANTS.md), [RISKS.md](architecture/RISKS.md), [index.jsonl](architecture/index.jsonl) | **generated projections** — derived from the records; never authority, never hand-edited |

Only records with `status: active` are normative and projected. `proposed` records are
drafts; `superseded`/`retired`/`rejected` records are history kept for lineage.

Precedence when reading (and when reconciling a stale projection in the same change):
active records > active contracts > predicates imported by active records > ADRs
(provenance) > generated projections and `docs/` (projections) > HRC/wrkq/chat history
(provenance only). This is **reading-order + "active wins, fix the stale projection in the
same change"** — not a runtime multi-authority resolver.

## The gate

[cmd/architecture-records](cmd/architecture-records) (run as `just architecture-records`,
and inside `just verify`) validates **structure + freshness only** — it never judges taste,
never requires a record per consult, and never executes `required_tests` (no live e2e in the
gate). It checks:

- schema validity per `kind` (`invariant` | `contract` | `accepted_risk`); unknown fields fail
- unique `id`; legal `status` and supersession referential integrity
- required fields present on `active` records (per `kind`)
- `source:` paths exist; `required_tests:` Go test identifiers (matching `^Test`) exist as
  functions in the test files named in `source:` — so renaming/deleting a guarding test turns
  the gate red (records are **fail-when-stale carriers**). Command/prose `required_tests`
  entries (e.g. a `just verify exits 0` assertion) are documentation: present-only, never
  grepped, never executed.
- the projections are diff-clean against the records (generated-and-diffed)

Failures teach: each states the expected shape, what was got, the blessed FIX, the WHY, and the
lifecycle/exception channel.

## Authoring & changing a record

1. Add or edit the YAML under `architecture/records/` or `architecture/contracts/`. Seed
   **only law already backed by a live guard/evidence** — document an *enforced* predicate, not
   an aspiration.
2. Run `just architecture-records --write` to regenerate the projections.
3. Run `just architecture-records` (or `just verify`) to confirm green; commit records +
   projections together.

`last_verified` (invariants/contracts) changes **only on concrete evidence** — a real green run,
not a date bump.

## Consultation signal (anti-DORMANT)

A normative surface that agents are *instructed* to read but never actually consult is dead law.
To keep these records load-bearing: **architecture rulings that touch a recorded surface cite the
active record id** (e.g. `wrkq.wrkf-rpc.stdout-purity`), and `last_verified` advances only when
fresh evidence is produced. Without this signal these records risk wrkq's TF outcome — built,
tested, never exercised.

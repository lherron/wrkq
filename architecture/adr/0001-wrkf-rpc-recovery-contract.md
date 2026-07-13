# ADR 0001 — wrkf JSON-RPC error-recovery contract

Status: accepted (provenance for [the wrkf-rpc contract record](architecture/contracts/wrkf-rpc.yaml))
Date: 2026-06-22

> Paths in this file are repo-root-relative (resolved from the repository root by the doc-link
> checker), not relative to `architecture/adr/`.

## Context

wrkq exposes its workflow surface to TypeScript consumers (notably the
[@wrkq/client package](packages/client)) over a wrkf JSON-RPC transport (`wrkf rpc --stdio`).
Clients need to distinguish *retryable* failures (stale revision, transient) from *terminal*
ones (validation, not-found) to drive their own recovery, and that distinction must survive the
JSON-RPC boundary unchanged.

## Decision

wrkf RPC errors carry a stable `WRKF_*` code and a boolean `retryable`. The codes and their
retryability are **canonical in wrkq** ([internal/wrkfapi/errors.go](internal/wrkfapi/errors.go),
mapped onto JSON-RPC by [internal/workrpc/errors.go](internal/workrpc/errors.go)) and are
preserved across the wire to clients, which echo `data.code` + retryability
([packages/client/src/errors.ts](packages/client/src/errors.ts)).

This decision is the durable law captured by the active contract record `wrkq.contract.wrkf-rpc`
([architecture/contracts/wrkf-rpc.yaml](architecture/contracts/wrkf-rpc.yaml)); the rich
explanatory walkthrough lives in [docs/wrkf-rpc.md](docs/wrkf-rpc.md). The contract — not this
ADR — is the authority; this ADR records why the contract exists.

## Consequences

- Changing the `WRKF_*` code set, the retryability semantics, or the JSON-RPC error envelope
  reopens the contract record (its `reopen_when`) and requires re-verifying the consumer.
- The contract is kept honest by `required_tests`: the workrpc codec and error-mapping tests plus the `@wrkq/client`
  unit + integration suites run under `just verify-rpc`.

# ADR 0002: wrkq owns durable agent collaboration

- Status: Accepted
- Date: 2026-08-27
- Authority: Lance rulings and T-07612 rev 2, verified by Daedalus

## Context

HRC's durable message rows preserve individual payloads but do not provide a
work-keyed conversation object that survives provider-continuation loss. A TUI
`/quit` is an explicit continuation barrier and can leave the next runtime cold
inside the same HRC generation. Keeping rooms or obligations beside runtime
state would make HRC a second owner of work continuity.

## Decision

wrkq is the sole durable authority for agent-collaboration rooms, envelopes,
obligations, membership, attendance, and presentation receipts. Work-addressed
rooms coalesce strictly to an effective campaign when one exists. Each envelope
has one addressee; a multi-addressee send atomically creates one envelope per
addressee under a shared group id.

HRC remains the authority for sessions, generations, runtimes, runs,
presentation, delivery actuation, stop-hook enforcement, and summon/placement.
It consumes the wrkq ledger and writes HRC execution identifiers into
`presented_to` only as opaque join data. wrkq and `wrkc` do not depend on HRC and
remain usable while every HRC daemon is down.

Cross-node collaboration uses the shared canonical wrkqd ledger. The HRC
federation message path is retired at the fleet-complete cutover; federation
continues to own birth, placement, and summon authority. ACP participates as a
ledger consumer/producer, including scope-less human principals represented by
the existing `agent:<id>` grammar.

## Consequences

`wrkc` replaces the durable collaboration portions of `hrcchat` and `hrcmail`.
HRC-owned message/mail tables become read-only at cutover and are removed after
their retention window; historical rows are not migrated. Conversation history
is pulled from the room rather than injected into a runtime. Presentation is
at-least-once across crash windows, and same-UID disposition confusion remains
an explicitly accepted risk.

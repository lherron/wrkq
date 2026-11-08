# ADR 0004: Bounded commit waits preserve client-owned observation

- Status: Accepted design; implementation and product-gate evidence pending
- Date: 2026-11-04
- Authority: Lance's direction approval; Daedalus verification of T-12538
  (bounded commit-wait RPC), specification revision 2
- Record: `wrkq.wrkf-rpc.bounded-polling-streaming` (amended)
- Submission: `/Users/lherron/praesidium/var/wrkq-artifacts/T-12538/spec-rev2.md`
- Counsel provenance: EN-45804 (revision 1), EN-45815 (rejection),
  EN-45828 (revision 2 resubmission)

## Decision

Permit exactly one bounded wake-hint operation, `wrkq.commit.wait
{since?, timeoutMs?} -> {token, changed}`, with a maximum wait of 30 seconds.
The process-incarnation/commit-sequence token is ephemeral. No events, filters,
durable cursors, subscriptions, or server-owned client loops cross through this
operation. A parked wait holds no database connection or transaction, and client
disconnect cancels its request context. The server retains no per-client state
across requests.

The submitted signal covers successful in-process commits appending to
`event_log`, `project_events`, or `workflow_events`, with burst coalescing.
Callers acquire the token before reading existing bounded views. The token
detects commits crossing that read; the existing durable view cursors remain
the replay authority. Restart changes the token incarnation. External-process
writes surface at timeout; time-based materialization remains governed by
caller clocks and authoritative reads. These are explicit product boundaries.
A remote caller may treat an unchanged token as proof that wrkqd committed
nothing and park again without reading, provided it still forces an
authoritative read at a bounded interval; that interval is then its latency
bound for external-process writes (`wrkp log --follow`: 5 minutes).

The client still owns its loop, clocks, terminal record, rendering, and exit
codes. Existing bounded read-model contracts and exact schema-hash verification
remain unchanged.

## Revision 2 disposition

Revision 1 incorrectly claimed old clients would keep working after the new
method changed the complete protocol schema hash. Source inspection of
`pkg/client/transport.go:224` established that Go clients refuse a different
hash before business dispatch, irrespective of which methods they use.

Revision 2 resolves that flaw by declaring a coordinated producer/client
cutover with an announced refusal window, preparing builds before live
replacement, including the external Go consumer `hcs`, and checking client
versions afterward. This preserves `wrkq.rpc.remote-transport-locator` rather
than weakening its exact-hash contract. The expected refusal frequency is every
old Go-client initialization against the changed-schema daemon during the
cutover interval; the submitted window estimate is one to three minutes, not a
measured guarantee.

Verification was source inspection, including the revision diff, Go initialize
check, hcs dependency, TypeScript initialization path, existing commit hooks,
and HTTP dispatch. No build, test, install, or restart evidence is claimed.
The invariant's existing `last_verified` remains unchanged. The five product
gates in the submitted specification remain the requester's implementation
evidence to produce; this decision approves the design and permission only.

# wrkq feature map

There is one file per feature. Each has the same sections: Sub-features, How to get to it, Driving it, Gotchas
and Proven when. The last lines of each file name the drive that last proved it and where its evidence is.

| # | Feature | File | Drive on |
| --- | --- | --- | --- |
| 1 | Store and daemon (`wrkqadm init/migrate/doctor`, `wrkqd` serve and auth, health, locator precedence, whoami) | [01-store-daemon.md](01-store-daemon.md) | scratch; health and initialize read-only on canonical |
| 2 | Tasks and containers (create, cat, etag set, ls/tree/find/search, mv, cp, rm/restore, container archive, rmdir, rename, named subtasks, relations, apply, diff, ack, check-inbox, timeline, log) | [02-tasks-containers.md](02-tasks-containers.md) | scratch |
| 3 | Comments, attachments and handoffs (comment add/ls, attach put/ls/get, handoff create/list/acknowledge) | [03-comments-attachments-handoffs.md](03-comments-attachments-handoffs.md) | scratch |
| 4 | Claims and promises (node-authenticated claim, takeover, release; promise add/ready/renew/resolve) | [04-claims-promises.md](04-claims-promises.md) | scratch |
| 5 | Campaigns (convert, activate, close, enrollment, members, portfolio) | [05-campaigns.md](05-campaigns.md) | scratch |
| 6 | Rooms, wrkc (say and routing, obligations, reply-is-ack, inbox, defer, withdraw, log, members) | [06-rooms.md](06-rooms.md) | scratch; own inbox read-only on canonical |
| 7 | Project facts, wrkp (post and idempotency, show, log and cursor, types, git producers, the `just` shim and `run.settled`, project roots) | [07-project-facts.md](07-project-facts.md) | scratch; types read-only on canonical |
| 8 | Workflows, wrkf (templates, attach, next, evidence, blocked and checked transitions, inspect, timeline) | [08-workflows.md](08-workflows.md) | scratch; template list read-only on canonical |
| 9 | Monitor and webhooks (`monitor wait/watch`, container and global webhooks) | [09-monitor-webhooks.md](09-monitor-webhooks.md) | scratch with a local sink; global list read-only |
| 10 | RPC and `@wrkq/client` (stdio JSON-RPC, errors, idempotency, CAS, the published client) | [10-rpc-client.md](10-rpc-client.md) | scratch; initialize read-only on canonical |

## Keeping the map honest

- When you change a feature, change its file in the same commit. A file with no drive behind it is a draft, and
  should say so at its end.
- When a drive turns up something the file doesn't say, add it to Gotchas with the date and the evidence path.
  When a Gotcha stops being true, delete it, or say which commit ended it.
- Re-drive a feature after any change to its code. Put the evidence under your task's `artifact_dir` in the
  layout from [SKILL.md](../SKILL.md) (`NN-<feature>/drive.txt`, `evidence/<scratch>/`, `live/`), and update the
  file's last line.
- The contracts (`docs/wrkq-wrkf-rpc.md`, `docs/wrkf-rpc.md`, `docs/wrkc-reference.md`, `docs/SPEC.md`) say what
  was specified. These files say what the installed build does and how to watch it do it.

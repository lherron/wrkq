<task_tracking_rules>
# wrkq Quick Reference

Task management CLI for agent/human collaboration. Unix-style filesystem-flavored commands, structured output.

## Essential Commands
```bash
wrkq tree                               # Show task tree
wrkq cat T-00001                        # View task details
wrkq touch inbox/task-slug -t "Title"   # Create task
wrkq set T-00001 --state STATE          # Update state
wrkq set T-00001 --outcome -            # Curated result from stdin; blank clears
wrkq find --state open                  # Find open tasks
wrkq find --label refactor --label urgent --state all --type t
wrkq ls --sort updated_at --reverse --limit 5  # Recent tasks
wrkq search "query" --ndjson            # Full-text search with timestamps
wrkq search "query" --label refactor --state all
wrkq promise ready                       # Attention reviews due now
```

`--label VALUE` is a repeatable, exact, case-sensitive read filter with AND
semantics. Duplicate filters are idempotent. The plural task/campaign write flag
accepts comma-separated shorthand or a JSON string array. Only trimmed input
beginning `[` selects JSON; every other input, including JSON-looking scalars,
is shorthand. Shorthand trims and drops empty segments; JSON is required for
comma-bearing labels and preserves element whitespace, duplicates, case, order,
and empty strings. Empty input or `[]` clears; an omitted flag leaves labels
unchanged.

## Task Lifecycle
```bash
wrkq set T-00001 --state in_progress    # Start task
wrkq comment add T-00001 -m "message"   # Add progress
wrkq comment add T-00001 -m - <<'EOF'   # Add progress from stdin
multi-line message
EOF
wrkq set T-00001 --state completed      # Complete task
```

## Stdin Conventions
`-` reads stdin for text flags/body slots (`-d -`, `--specification -`, `-m -`, `--body-file -`).
Final comments are the worker's raw record; outcome is the curated plain-terms result. Completion never requires an outcome.
`@file` reads file content for text flags (`-m @notes.md`).
Use one stdin consumer per command. Dash-stdin on a terminal errors; pipe input or use a heredoc.
Destructive commands with piped selector input may need `--yes` because prompts read stdin.

## States
`idea` | `draft` | `open` | `in_progress` | `completed` | `blocked` | `cancelled` | `archived` | `deleted`

## Reserved Labels
`needs_smoketest` requests Smokey through webhook automation; it is not a state.

## Project Scope
```bash
wrkq projects                          # List all projects
wrkq ls --project other inbox          # Work in different project
```

## Campaign Membership
A task belongs to a campaign in one of two ways. **Resident**: it lives inside
the campaign container, so it is a member with nothing else set. **Enrolled**:
it lives somewhere else entirely — including another project — and
`wrkq set T-00001 --campaign P-00452` (or `wrkq touch <path> --campaign P-00452`
at creation) records the membership. Enrollment is how a campaign holds a slot
whose work belongs to another project: the task keeps its own path, project,
and agent home, while the campaign owns its room, portfolio rollup and close
guard. `wrkq cat` prints the effective membership as
`campaign: <id> <path> resident|enrolled`.

```bash
wrkq touch other/inbox/slot -t "Slot" --campaign P-00452  # create enrolled, in another project
wrkq set T-00001 --campaign P-00452                       # enroll an existing task
wrkq set T-00001 --campaign ""                            # unenroll
wrkq find --campaign P-00452 --state all                  # resident + enrolled members
```
A campaign in another project is addressable by its `P-` id from anywhere, or by
absolute path (`otherproject/campaign-slug`) — a path is tried under your own
project root first.

## Search Freshness: run `wrkq index update` before search when freshness matters

## Promises
```bash
wrkq promise add --subject "Revisit rollout" --in 7d
wrkq promise add --for lance --on-behalf --in 7d --subject "Review rollout"
wrkq promise add --task T-00001 --in 36h --question "What remains?"
wrkq promise list
wrkq promise list --task T-00001          # Cross-owner subject visibility
wrkq promise ready --for lance
wrkq promise ready --for lance --project wrkq --include-global
wrkq promise renew PR-00001 --in 7d --note "Still active"
wrkq promise resolve PR-00001 --note "Satisfied"
wrkq promise abandon PR-00001 --note "Superseded"
wrkq cat PR-00001 --json --one
wrkq log PR-00001 --patch
```

Promises commit a principal to revisit a subject; they are not task deadlines.
`add`/`renew` require exactly one of `--review-at` or `--in`; the API, not the
CLI, normalizes/resolves that value. Text flags use literal/`@file`/`-` stdin.
Use root `cat`/`log` for detail/history. `wrkq check` shows ready promises and
`wrkq tree --state all` includes closed attached promise leaves.

## Output: Add `--json` or `--ndjson` to most commands
</task_tracking_rules>

<task_tracking_rules>
# wrkq Quick Reference

Task management CLI for agent/human collaboration. Unix-style filesystem-flavored commands, structured output.

## Essential Commands
```bash
wrkq tree                               # Show task tree
wrkq cat T-00001                        # View task details
wrkq touch inbox/task-slug -t "Title"   # Create task
wrkq set T-00001 --state STATE          # Update state
wrkq find --state open                  # Find open tasks
```

## Task Lifecycle
```bash
wrkq set T-00001 --state in_progress    # Start task
wrkq comment add T-00001 -m "message"   # Add progress
wrkq set T-00001 --state completed      # Complete task
```

## States
`draft` | `open` | `in_progress` | `completed` | `blocked` | `cancelled`

## Project Scope
```bash
wrkq projects                          # List all projects
wrkq ls --project other inbox          # Work in different project
```

## Output: Add `--json` or `--ndjson` to most commands
</task_tracking_rules>

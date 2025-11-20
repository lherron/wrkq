# Agent Usage Guide

This guide documents recommended patterns for AI agents and automation tools working with `wrkq`. These patterns enable agents to collaborate effectively with human users while maintaining structured, machine-readable communication.

## Core Principles

1. **Structured metadata**: Use `--meta` to attach structured data to comments
2. **NDJSON polling**: Use `--ndjson` for efficient, parseable output
3. **Typed selectors**: Use `t:` and `c:` prefixes for stable resource addressing
4. **Progress tracking**: Emit progress comments with consistent metadata fields

## Comment Metadata Patterns

### Adding Progress Comments

Agents should emit progress and summary comments with structured metadata using `--meta`:

```bash
# Agent starting work
wrkq comment add T-00001 -m "Starting analysis of authentication flow..." \
  --meta '{"run_id":"abc123", "kind":"progress", "model":"claude-sonnet-4"}'

# Agent providing intermediate update
wrkq comment add T-00001 -m "Found 3 potential issues in login handler" \
  --meta '{"run_id":"abc123", "kind":"progress", "step":"analysis"}'

# Agent completing work
wrkq comment add T-00001 -m "Analysis complete. See attached report." \
  --meta '{"run_id":"abc123", "kind":"summary", "status":"success"}'
```

### Recommended Metadata Fields

**Standard fields for all agent comments:**
- `run_id`: Unique identifier for this execution/session
- `kind`: Type of comment (`progress`, `summary`, `error`, `question`)
- `model`: Model identifier (e.g., `claude-sonnet-4`, `gpt-4`)

**Optional fields:**
- `step`: Current step/phase in multi-step process
- `status`: Completion status (`success`, `failed`, `partial`)
- `confidence`: Confidence score for suggestions (0.0-1.0)
- `source`: Origin of information (e.g., `static-analysis`, `test-run`)
- `timestamp`: ISO8601 timestamp for precise timing

### Example Metadata Structures

**Progress comment:**
```json
{
  "run_id": "abc123",
  "kind": "progress",
  "model": "claude-sonnet-4",
  "step": "code-review",
  "progress": 0.6
}
```

**Summary comment:**
```json
{
  "run_id": "abc123",
  "kind": "summary",
  "model": "claude-sonnet-4",
  "status": "success",
  "stats": {
    "files_analyzed": 12,
    "issues_found": 3
  }
}
```

**Error comment:**
```json
{
  "run_id": "abc123",
  "kind": "error",
  "model": "claude-sonnet-4",
  "error_code": "PARSE_ERROR",
  "recoverable": false
}
```

## Polling for Comments

### Efficient NDJSON Polling

Instead of parsing `wrkq cat` output, agents should use `wrkq comment ls --ndjson` for efficient, machine-readable polling:

```bash
# Poll for all comments on a task
wrkq comment ls T-00001 --ndjson | jq -r 'select(.actor_role == "human")'

# Poll with cursor for new comments only
CURSOR=$(cat .last_cursor)
wrkq comment ls T-00001 --ndjson --since-cursor $CURSOR | \
  while IFS= read -r line; do
    echo "$line" | jq -r '.message'
    echo "$line" | jq -r '.cursor' > .last_cursor
  done

# Filter by metadata
wrkq comment ls T-00001 --ndjson | \
  jq -r 'select(.meta.kind == "progress") | "\(.created_at) - \(.message)"'
```

### Cursor-Based Polling Loop

For long-running agents monitoring multiple tasks:

```bash
#!/bin/bash
CURSOR_FILE=".wrkq_cursor"
CURSOR=$(cat "$CURSOR_FILE" 2>/dev/null || echo "0")

while true; do
  # Poll for new comments across all tasks
  wrkq comment ls --ndjson --since-cursor "$CURSOR" | while IFS= read -r line; do
    # Update cursor
    NEW_CURSOR=$(echo "$line" | jq -r '.cursor')
    echo "$NEW_CURSOR" > "$CURSOR_FILE"

    # Process only human comments
    ACTOR_ROLE=$(echo "$line" | jq -r '.actor_role')
    if [ "$ACTOR_ROLE" = "human" ]; then
      TASK_ID=$(echo "$line" | jq -r '.task_id')
      MESSAGE=$(echo "$line" | jq -r '.message')

      # Agent processes the comment
      process_human_comment "$TASK_ID" "$MESSAGE"
    fi
  done

  sleep 5  # Poll every 5 seconds
done
```

### Filtering Human vs Agent Comments

Distinguish between human and agent comments using `actor_role`:

```bash
# Get only human comments
wrkq comment ls T-00001 --ndjson | jq -r 'select(.actor_role == "human")'

# Get only agent comments
wrkq comment ls T-00001 --ndjson | jq -r 'select(.actor_role == "agent")'

# Get comments from specific agent
wrkq comment ls T-00001 --ndjson | \
  jq -r 'select(.meta.model == "claude-sonnet-4")'
```

## Typed Selectors

### Using `t:` and `c:` Prefixes

For stable, unambiguous resource addressing, use typed selectors:

```bash
# Task selectors - works with any task identifier
wrkq comment add t:T-00001 -m "Completed task"
wrkq comment add t:auth-bug -m "Fixed the issue"
wrkq comment add t:portal/auth/login-ux -m "Updated designs"

# Comment selectors - reference specific comments
wrkq comment cat c:C-00012 --json
wrkq comment rm c:C-00015 --yes

# Attachment selectors (future)
wrkq attach get a:A-00003 --output report.pdf
```

### Why Use Typed Selectors?

1. **Unambiguous**: Clear what resource type you're referencing
2. **Stable**: Works with friendly IDs, slugs, or paths
3. **Future-proof**: Supports additional resource types (attachments, etc.)
4. **Self-documenting**: Code clearly shows resource types

## Complete Agent Workflow Example

Here's a complete example of an agent performing code review:

```bash
#!/bin/bash

TASK_ID="$1"
RUN_ID=$(uuidgen)
MODEL="claude-sonnet-4"

# Start work
wrkq set "$TASK_ID" state=in_progress
wrkq comment add "t:$TASK_ID" -m "Starting code review..." \
  --meta "{\"run_id\":\"$RUN_ID\", \"kind\":\"progress\", \"model\":\"$MODEL\"}"

# Perform analysis
FILES=$(git diff --name-only main)
TOTAL=$(echo "$FILES" | wc -l)
CURRENT=0

for file in $FILES; do
  CURRENT=$((CURRENT + 1))
  PROGRESS=$(echo "scale=2; $CURRENT / $TOTAL" | bc)

  wrkq comment add "t:$TASK_ID" -m "Reviewing $file..." \
    --meta "{\"run_id\":\"$RUN_ID\", \"kind\":\"progress\", \"step\":\"review\", \"progress\":$PROGRESS}"

  # Analyze file...
  analyze_file "$file"
done

# Complete work
ISSUES=$(count_issues)
wrkq comment add "t:$TASK_ID" -m "Code review complete. Found $ISSUES issues." \
  --meta "{\"run_id\":\"$RUN_ID\", \"kind\":\"summary\", \"status\":\"success\", \"issues_found\":$ISSUES}"

wrkq set "$TASK_ID" state=completed
```

## Integration with CI/CD

### GitHub Actions Example

```yaml
name: AI Code Review
on: [pull_request]

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Install wrkq
        run: |
          curl -L https://github.com/example/wrkq/releases/download/v1.0.0/wrkq_linux_amd64 -o wrkq
          chmod +x wrkq
          sudo mv wrkq /usr/local/bin/

      - name: Create task for PR
        id: task
        run: |
          TASK_ID=$(wrkq touch "reviews/pr-${{ github.event.pull_request.number }}" \
            -t "Review PR #${{ github.event.pull_request.number }}" --porcelain)
          echo "task_id=$TASK_ID" >> $GITHUB_OUTPUT

      - name: Run AI review
        env:
          TASK_ID: ${{ steps.task.outputs.task_id }}
          RUN_ID: ${{ github.run_id }}
        run: |
          wrkq comment add "t:$TASK_ID" -m "Starting automated review..." \
            --meta "{\"run_id\":\"$RUN_ID\", \"kind\":\"progress\", \"model\":\"ai-reviewer\"}"

          # Run review...
          ./scripts/ai-review.sh "$TASK_ID" "$RUN_ID"
```

## Best Practices

### DO:
- Use structured metadata for all agent comments
- Poll with `--ndjson` for efficient parsing
- Use typed selectors (`t:`, `c:`) for clarity
- Include `run_id` to group related comments
- Emit progress comments for long-running operations
- Use `actor_role` to filter human vs agent comments
- Store cursors for efficient polling

### DON'T:
- Parse `wrkq cat` YAML output (use `comment ls --ndjson`)
- Hardcode UUID references (use friendly IDs or slugs)
- Emit comments without metadata (makes filtering impossible)
- Poll without cursors (inefficient, redundant processing)
- Mix different `run_id` values in same execution
- Ignore `actor_role` when filtering comments

## Future Considerations

As the comments API evolves, expect:

1. **Webhooks**: Real-time push notifications instead of polling
2. **Comment threads**: Nested replies to specific comments
3. **Reactions**: Emoji/vote-based lightweight feedback
4. **Search**: Full-text search across comment history
5. **Aggregation**: Summary views of metadata across tasks

Agent implementations should be prepared to adapt to these features while maintaining backward compatibility with the current polling-based approach.

## Additional Resources

- `docs/SPEC.md` - Complete product specification
- `docs/COMMENTS-SPEC.md` - Detailed comments system specification
- `internal/cli/comment.go` - Comment command implementation
- `examples/agent-workflow.sh` - Complete agent example scripts

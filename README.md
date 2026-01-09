# wrkq

WRKQ is task-based collaboration surface between coding agents and humans. Command structure mimics a Unix filesystem-style interface for maximum human/agent familiarity, and structured output formats and actor attribution make it native to agent workflows. Changes can be bundled as diffs and committed to git, enabling version-controlled task state that flows through your normal PR process.  The easiest way to integrate is to have your agent run `wrkq info` directly or add to an agent startup hook.

## Features

- **Agent-first** - Structured output, actor attribution, and machine-readable formats designed for AI agent workflows
- **Unix-style interface** - Familiar commands like `ls`, `cat`, `mv`, `rm`, `tree`, `touch`, `mkdir`
- **Git-native** - SQLite database hydrated from git; bundle changes as diffs for PRs
- **Pipe-friendly** - JSON, NDJSON, and porcelain output formats for scripting
- **Optimistic concurrency** - ETag-based conflict detection on writes

## Installation

### Homebrew (macOS/Linux)

```bash
brew tap lherron/wrkq
brew install wrkq
```

### From Source

```bash
# Clone and build
git clone https://github.com/lherron/wrkq.git
cd wrkq
just build

# Install to ~/.local/bin
just install
```

Then update your agent startup hook to run `wrkq info`:

```bash
echo "=== This project uses wrkq ==="
wrkq agent-info 2>/dev/null || echo "(wrkq info failed or not available, notify user)"
```

### Requirements

- Go 1.21+
- SQLite 3.x (bundled via go-sqlite3)

## Quick Start

```bash
# Initialize a new project database
wrkqadm init

# Create a container (project)
wrkq mkdir myproject

# Create a task
wrkq touch myproject/implement-feature -t "Implement new feature" -d "Description here"

# List tasks
wrkq ls myproject

# View task details
wrkq cat myproject/implement-feature

# Update task state
wrkq set T-00001 --state in_progress

# Add a comment
wrkq comment add T-00001 -m "Started implementation"

# Mark complete
wrkq set T-00001 --state completed
```

## Architecture

The system ships two binaries:

- **`wrkq`** - Day-to-day task management, made for agents
- **`wrkqadm`** - Administrative operations (database init, migrations, actor management)

## Core Concepts

| Concept | Description |
|---------|-------------|
| **Actor** | Human or agent performing actions (attribution, not authentication) |
| **Container** | Project or subproject (hierarchical); changesets bundle container state for git |
| **Task** | Actionable item with state, priority, labels |
| **Comment** | Append-only notes on tasks |
| **Attachment** | File references stored alongside tasks |

### Task States

`draft` → `open` → `in_progress` → `completed` | `blocked` | `cancelled`

### Addressing

Resources can be referenced by:
- **Path**: `myproject/subproject/task-slug`
- **Friendly ID**: `T-00123`, `P-00007`
- **UUID**: Full database UUID

## Output Formats

```bash
wrkq ls myproject --json      # Pretty JSON
wrkq ls myproject --ndjson    # Newline-delimited JSON
wrkq ls myproject --porcelain # Stable machine-readable
```

## Configuration

Configuration is loaded from (in precedence order):
1. CLI flags
2. Environment variables (`WRKQ_DB_PATH`, `WRKQ_ACTOR`)
3. `.env.local` in current directory
4. `~/.config/wrkq/config.yaml`

## License

MIT License - see [LICENSE](LICENSE) for details.

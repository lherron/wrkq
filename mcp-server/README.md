# wrkq MCP Server

MCP (Model Context Protocol) server for wrkq task management. Exposes wrkq CLI functionality as tools that can be used by AI assistants like Claude.

## Features

### Tools

- **`wrkq_task_write`**: Update a task's body content
  - Inputs:
    - `taskId` (string): Task identifier - friendly ID (T-00001), UUID, or path (project/task-slug)
    - `taskBody` (string): New body content for the task (markdown supported)
  - Uses wrkq's `apply` command under the hood
  - Automatically handles temp file creation and cleanup

## Installation

### Prerequisites

- Node.js 20+
- wrkq CLI installed and in PATH
- Properly configured wrkq environment (see main project README)

### Setup

1. Install dependencies:
```bash
cd mcp-server
npm install
```

2. Build the server:
```bash
npm run build
```

3. (Optional) Install globally:
```bash
npm link
```

## Usage

### With Claude Code

Add to your Claude Code MCP settings (`.claude/mcp.json` or global config):

```json
{
  "mcpServers": {
    "wrkq": {
      "command": "node",
      "args": ["/absolute/path/to/wrkq/mcp-server/dist/index.js"],
      "env": {
        "WRKQ_DB_PATH": "/path/to/your/wrkq.db",
        "WRKQ_ACTOR": "claude-assistant"
      }
    }
  }
}
```

If you installed globally via `npm link`:

```json
{
  "mcpServers": {
    "wrkq": {
      "command": "wrkq-mcp-server",
      "env": {
        "WRKQ_DB_PATH": "/path/to/your/wrkq.db",
        "WRKQ_ACTOR": "claude-assistant"
      }
    }
  }
}
```

### Manual Testing

You can test the MCP server manually using stdio:

```bash
# Start the server
node dist/index.js

# Send JSON-RPC requests via stdin (example)
{"jsonrpc":"2.0","id":1,"method":"tools/list"}
```

## Environment Variables

The MCP server inherits environment variables from its parent process. You can configure wrkq behavior via:

- `WRKQ_DB_PATH`: Path to wrkq database
- `WRKQ_ACTOR`: Actor slug for mutations
- `WRKQ_ACTOR_ID`: Actor UUID for mutations

See main project documentation for full configuration options.

## Development

### Watch mode

```bash
npm run watch
```

### Project Structure

```
mcp-server/
├── src/
│   └── index.ts         # Main MCP server implementation
├── dist/                # Compiled JavaScript (generated)
├── package.json
├── tsconfig.json
└── README.md
```

## How It Works

1. The MCP server exposes wrkq functionality as JSON-RPC tools
2. When `wrkq_task_write` is called:
   - Task body is written to a temp file in `/tmp/claude/`
   - `wrkq apply <taskId> --file <tempfile>` is executed
   - Temp file is cleaned up
   - Result is returned to the caller

3. The `apply` command uses wrkq's 3-way merge logic to safely update tasks

## Future Tools

Potential additions for M2/M3:

- `wrkq_task_create`: Create new tasks
- `wrkq_task_list`: List/find tasks
- `wrkq_task_read`: Read task details
- `wrkq_task_set`: Set task metadata (state, priority, etc.)
- `wrkq_tree`: Get project tree structure
- `wrkq_log`: Read event log

## Troubleshooting

### "wrkq command not found"

Ensure `wrkq` is installed and in PATH:

```bash
which wrkq
# Should output: /path/to/wrkq

# If not found, install it
cd ..
just install
```

### "Failed to open database"

Set `WRKQ_DB_PATH` environment variable when configuring the MCP server, or ensure wrkq has a default database configured.

### Permission errors with temp files

The server uses `/tmp/claude/` for temp files (per project guidelines). Ensure this directory exists and is writable:

```bash
mkdir -p /tmp/claude
chmod 755 /tmp/claude
```

## License

Same as main wrkq project.

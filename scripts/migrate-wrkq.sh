#!/bin/bash
# Migration script to convert TODO.md tracking into wrkq CLI tasks
# This creates a parallel task structure in the wrkq CLI

set -e

WRKQ_BIN="./bin/wrkq"

echo "🚀 Migrating TODO.md to wrkq CLI..."
echo ""

# Create base structure
echo "Creating project structure..."
$WRKQ_BIN mkdir wrkq/meta -p
$WRKQ_BIN mkdir wrkq/m0 -p
$WRKQ_BIN mkdir wrkq/m1 -p
$WRKQ_BIN mkdir wrkq/m2 -p
$WRKQ_BIN mkdir wrkq/m3 -p

echo "✅ Project structure created"
echo ""

# Helper to create and configure a task
create_task() {
    local path=$1
    local title=$2
    local state=$3
    local priority=$4

    $WRKQ_BIN touch "$path" -t "$title" 2>/dev/null || true
    $WRKQ_BIN set "$path" state="$state" priority="$priority" 2>/dev/null || true
}

echo "Migrating M0 tasks (completed)..."
create_task "wrkq/m0/core-cli" "Core CLI commands (mkdir, touch, ls, stat)" "completed" 1
create_task "wrkq/m0/database" "SQLite database with migrations" "completed" 1
create_task "wrkq/m0/friendly-ids" "Friendly ID generation (T-, P-, A-)" "completed" 1
create_task "wrkq/m0/etag-concurrency" "ETag-based optimistic locking" "completed" 1
create_task "wrkq/m0/event-log" "Append-only event log" "completed" 1
create_task "wrkq/m0/actors" "Multi-actor support" "completed" 1
create_task "wrkq/m0/pathspecs" "Path resolution and globs" "completed" 1
create_task "wrkq/m0/mv-command" "Move/rename command" "completed" 1
create_task "wrkq/m0/rm-restore" "Archive and restore commands" "completed" 1
create_task "wrkq/m0/cat-set" "Content viewing and mutation" "completed" 1

echo "✅ M0 tasks migrated"
echo ""

echo "Migrating M1 tasks..."
create_task "wrkq/m1/find-command" "Implement find command with filters" "completed" 1
create_task "wrkq/m1/tree-command" "Implement tree command" "completed" 1
create_task "wrkq/m1/log-command" "Implement log command for history" "completed" 1
create_task "wrkq/m1/watch-command" "Implement watch for event streaming" "completed" 1
create_task "wrkq/m1/diff-command" "Implement diff for task comparison" "completed" 1
create_task "wrkq/m1/apply-command" "Implement apply for file/stdin input" "completed" 1
create_task "wrkq/m1/edit-command" "Implement edit with $EDITOR" "completed" 1
create_task "wrkq/m1/3way-merge" "Implement 3-way merge logic" "completed" 1
create_task "wrkq/m1/smoke-tests" "Write smoke tests for M1" "completed" 2
create_task "wrkq/m1/documentation" "Write M1 documentation" "completed" 2

# M1 optional features (deferred)
create_task "wrkq/m1/fields-flag" "Add --fields flag for field selection" "open" 3
create_task "wrkq/m1/sort-flag" "Add --sort flag for custom sorting" "open" 3
create_task "wrkq/m1/golden-tests" "Write golden tests for outputs" "open" 2
create_task "wrkq/m1/unit-tests" "Write unit tests for merge logic" "open" 2
create_task "wrkq/m1/perf-benchmarks" "Performance benchmarks (5k tasks)" "open" 3

echo "✅ M1 tasks migrated"
echo ""

echo "Migrating M2 tasks (upcoming)..."
create_task "wrkq/m2/attachments" "Implement attachment management (put/get/ls/rm)" "open" 1
create_task "wrkq/m2/pagination" "Add pagination cursors for large result sets" "open" 2
create_task "wrkq/m2/bulk-ops" "Add bulk operation support (--jobs, --continue-on-error)" "open" 2
create_task "wrkq/m2/copy-command" "Implement cp command for copying tasks" "open" 2
create_task "wrkq/m2/purge-delete" "Implement hard delete with --purge" "open" 3
create_task "wrkq/m2/doctor-tools" "Database health check and repair tools" "open" 3
create_task "wrkq/m2/completions" "Enhanced shell completions (bash/zsh/fish)" "open" 3
create_task "wrkq/m2/goreleaser" "GoReleaser config for packaging" "open" 2
create_task "wrkq/m2/install-scripts" "Install scripts for easy deployment" "open" 3
create_task "wrkq/m2/sbom" "Software Bill of Materials generation" "open" 4

echo "✅ M2 tasks migrated"
echo ""

echo "Migrating M3 tasks (deferred per spec)..."
create_task "wrkq/m3/comments-schema" "Add comments table schema" "blocked" 2
create_task "wrkq/m3/comment-commands" "Implement comment add/ls/rm commands" "blocked" 2
create_task "wrkq/m3/machine-interface" "Freeze machine interface v1 contracts" "blocked" 1
create_task "wrkq/m3/http-spec" "HTTP/JSON façade specification" "blocked" 2
create_task "wrkq/m3/api-docs" "Browser UI enablers documentation" "blocked" 3

echo "✅ M3 tasks migrated (marked as blocked per CLI-MVP.md)"
echo ""

echo "Creating meta tasks..."
create_task "wrkq/meta/dogfood-migration" "Migrate from TODO.md to wrkq CLI" "completed" 1
create_task "wrkq/meta/workflow-setup" "Set up daily wrkq workflow and aliases" "open" 2
create_task "wrkq/meta/ci-integration" "Integrate wrkq with CI/CD pipeline" "open" 3

echo "✅ Meta tasks created"
echo ""

echo "📊 Migration Summary:"
echo ""
$WRKQ_BIN tree wrkq
echo ""

echo "🎉 Migration complete!"
echo ""
echo "Next steps:"
echo "  1. Review tasks: wrkq tree wrkq"
echo "  2. See what's next: wrkq find --state open"
echo "  3. Start working: wrkq set <task-id> state=in_progress"
echo "  4. Track progress: wrkq watch --follow"
echo ""
echo "Suggested aliases (add to ~/.bashrc or ~/.zshrc):"
echo "  alias tl='wrkq tree wrkq'           # List all"
echo "  alias tn='wrkq find --state open'   # What's next"
echo "  alias td='wrkq find --state completed' # What's done"
echo "  alias te='EDITOR=vim wrkq edit'     # Quick edit"
echo ""

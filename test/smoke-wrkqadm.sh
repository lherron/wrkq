#!/usr/bin/env bash
set -euo pipefail

# Smoke test for wrkqadm commands
# Tests all administrative commands in the wrkqadm binary

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

echo "=== wrkqadm Smoke Test ==="
echo

# Build both binaries
echo "Building binaries..."
cd "$PROJECT_ROOT"
just cli-build
just wrkqadm-build
echo "✓ Binaries built"
echo

# Setup test database
TEST_DB="/tmp/claude/test-wrkqadm-smoke.db"
TEST_ATTACH_DIR="/tmp/claude/test-wrkqadm-attach"
rm -rf "$TEST_DB" "$TEST_ATTACH_DIR"
mkdir -p "$(dirname "$TEST_DB")" "$TEST_ATTACH_DIR"

export WRKQ_DB_PATH="$TEST_DB"
export WRKQ_ATTACH_DIR="$TEST_ATTACH_DIR"
export WRKQ_ACTOR="local-human"

WRKQ="$PROJECT_ROOT/bin/wrkq"
WRKQADM="$PROJECT_ROOT/bin/wrkqadm"

echo "Test database: $TEST_DB"
echo "Attach dir: $TEST_ATTACH_DIR"
echo

# Test 1: wrkqadm version
echo "Test 1: wrkqadm version"
$WRKQADM version
$WRKQADM version --json | grep -q '"binary": "wrkqadm"'
echo "✓ Version command works"
echo

# Test 2: wrkqadm init
echo "Test 2: wrkqadm init"
$WRKQADM init
if [ ! -f "$TEST_DB" ]; then
    echo "✗ Database not created"
    exit 1
fi
echo "✓ Database initialized"
echo

# Test 3: wrkqadm actors ls
echo "Test 3: wrkqadm actors ls"
$WRKQADM actors ls
$WRKQADM actors ls --json | grep -q '"slug": "local-human"'
echo "✓ Actors list works"
echo

# Test 4: wrkqadm actors add
echo "Test 4: wrkqadm actors add"
$WRKQADM actors add test-agent --name "Test Agent"
$WRKQADM actors ls | grep -q "test-agent"
echo "✓ Actor creation works"
echo

# Test 5: wrkqadm doctor
echo "Test 5: wrkqadm doctor"
$WRKQADM doctor
echo "✓ Doctor command works"
echo

# Test 6: wrkqadm config doctor
echo "Test 6: wrkqadm config doctor"
$WRKQADM config doctor
echo "✓ Config doctor works"
echo

# Test 7: wrkqadm db snapshot
echo "Test 7: wrkqadm db snapshot"
TEST_SNAPSHOT="/tmp/claude/test-wrkqadm-snapshot.db"
rm -f "$TEST_SNAPSHOT"

# Create some data first
$WRKQ mkdir portal
$WRKQ touch portal/task-1 -t "Test Task 1"

$WRKQADM db snapshot --out "$TEST_SNAPSHOT"
if [ ! -f "$TEST_SNAPSHOT" ]; then
    echo "✗ Snapshot not created"
    exit 1
fi

# Verify snapshot is usable
WRKQ_DB_PATH="$TEST_SNAPSHOT" $WRKQ ls portal | grep -q "task-1"
echo "✓ Snapshot creation and verification works"
rm -f "$TEST_SNAPSHOT"
echo

# Test 8: Create test bundle for bundle apply
echo "Test 8: Preparing bundle apply test"
BUNDLE_DIR="/tmp/claude/test-bundle"
rm -rf "$BUNDLE_DIR"
mkdir -p "$BUNDLE_DIR/tasks/portal"
mkdir -p "$BUNDLE_DIR/attachments"

# Create manifest.json
cat > "$BUNDLE_DIR/manifest.json" <<EOF
{
  "machine_interface_version": 1,
  "timestamp": "2025-11-20T12:00:00Z",
  "with_attachments": false,
  "with_events": false
}
EOF

# Create containers.txt
cat > "$BUNDLE_DIR/containers.txt" <<EOF
# Test containers
portal
portal/api
backend
EOF

# Get UUID of existing task for testing
TASK_UUID=$($WRKQ cat portal/task-1 | grep "^uuid:" | awk '{print $2}')

# Create a task document for bundle (simulating modification)
cat > "$BUNDLE_DIR/tasks/portal/task-1.md" <<EOF
---
uuid: $TASK_UUID
path: portal/task-1
base_etag: 1
---

# Updated Test Task 1

This is an updated version of the task from the bundle.

## Implementation Notes

- Testing bundle apply
- Verifying etag handling
EOF

# Create a new task in bundle
cat > "$BUNDLE_DIR/tasks/portal/new-task.md" <<EOF
---
path: portal/new-task
---

# New Task from Bundle

This task is created via bundle apply.
EOF

echo "✓ Test bundle prepared"
echo

# Test 9: wrkqadm bundle apply --dry-run
echo "Test 9: wrkqadm bundle apply --dry-run"
$WRKQADM bundle apply --from "$BUNDLE_DIR" --dry-run
echo "✓ Dry-run works"
echo

# Test 10: wrkqadm bundle apply (actual apply)
echo "Test 10: wrkqadm bundle apply"
$WRKQADM bundle apply --from "$BUNDLE_DIR"

# Verify containers were created
$WRKQ ls | grep -q "portal"
$WRKQ ls | grep -q "backend"
$WRKQ ls portal | grep -q "api"

echo "✓ Bundle applied successfully"
echo

# Test 11: wrkqadm attach path (if task has attachments)
echo "Test 11: wrkqadm attach path"
# Create a test file and attach it
echo "test content" > /tmp/claude/test-file.txt
ATTACH_OUTPUT=$($WRKQ attach put portal/task-1 /tmp/claude/test-file.txt)

# Extract attachment ID from output (format: "Attached: ATT-00001 (filename, size)")
ATTACH_ID=$(echo "$ATTACH_OUTPUT" | grep -o 'ATT-[0-9]*')

# Now test wrkqadm attach path
ATTACH_PATH=$($WRKQADM attach path "$ATTACH_ID")
if [ ! -f "$ATTACH_PATH" ]; then
    echo "✗ Attachment path not found: $ATTACH_PATH"
    exit 1
fi
echo "✓ Attach path works"
rm -f /tmp/claude/test-file.txt
echo

# Test 12: Binary separation verification
echo "Test 12: Verifying binary separation"

# Commands that should be in wrkq
$WRKQ whoami > /dev/null || { echo "✗ whoami should be in wrkq"; exit 1; }
$WRKQ ls > /dev/null || { echo "✗ ls should be in wrkq"; exit 1; }

# Commands that should be in wrkqadm
$WRKQADM init --help > /dev/null || { echo "✗ init should be in wrkqadm"; exit 1; }
$WRKQADM actors --help > /dev/null || { echo "✗ actors should be in wrkqadm"; exit 1; }
$WRKQADM doctor --help > /dev/null || { echo "✗ doctor should be in wrkqadm"; exit 1; }

# Commands that should NOT be in wrong binary
if $WRKQ init --help 2>&1 | grep -q "unknown command"; then
    echo "✓ init correctly not in wrkq"
else
    echo "✗ init should not be in wrkq"
    exit 1
fi

if $WRKQADM whoami 2>&1 | grep -q "unknown command"; then
    echo "✓ whoami correctly not in wrkqadm"
else
    echo "✗ whoami should not be in wrkqadm"
    exit 1
fi

echo "✓ Binary separation verified"
echo

# Cleanup
rm -rf "$TEST_DB" "$TEST_ATTACH_DIR" "$BUNDLE_DIR"

echo "=== All wrkqadm smoke tests passed! ==="

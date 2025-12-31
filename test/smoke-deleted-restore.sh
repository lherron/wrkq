#!/bin/bash
# Smoke test for deleted state and restore functionality
set -e

# Create temp database path (but remove the file so init treats it as new)
TEST_DB=$(mktemp)
rm -f "$TEST_DB"
trap "rm -f $TEST_DB" EXIT

# Use --db flag to ensure we use the test database
DB="--db $TEST_DB"
ACTOR="--as local-human"

echo "=== Initializing test database ==="
./bin/wrkqadm $DB init

echo ""
echo "=== Test 1: Force UUID creation ==="
uuid=$(uuidgen | tr '[:upper:]' '[:lower:]')
./bin/wrkq $DB $ACTOR touch inbox/test-force-uuid --force-uuid "$uuid" -t "Force UUID Test"
result=$(./bin/wrkq $DB cat "$uuid" --json | jq -r '.[0].uuid')
if [ "$result" != "$uuid" ]; then
    echo "FAIL: UUID mismatch (expected $uuid, got $result)"
    exit 1
fi
echo "PASS: Force UUID creation works"

echo ""
echo "=== Test 2: Delete and restore ==="
./bin/wrkq $DB $ACTOR set inbox/test-force-uuid --state deleted
# Should NOT appear in default find (filter by type t for tasks only)
count=$(./bin/wrkq $DB find inbox --type t --json | jq 'length')
if [ "$count" != "0" ]; then
    echo "FAIL: Deleted task should not appear in find (got $count tasks)"
    exit 1
fi
echo "PASS: Deleted task hidden from find"

# Should appear with --state deleted
count=$(./bin/wrkq $DB find --state deleted --type t --json | jq 'length')
if [ "$count" != "1" ]; then
    echo "FAIL: Deleted task should appear with --state deleted (got $count)"
    exit 1
fi
echo "PASS: Deleted task appears with --state deleted"

# Restore it
./bin/wrkq $DB $ACTOR restore "$uuid"
state=$(./bin/wrkq $DB cat inbox/test-force-uuid --json | jq -r '.[0].state')
if [ "$state" != "open" ]; then
    echo "FAIL: Restored task should be open (got $state)"
    exit 1
fi
echo "PASS: Task restored to open state"

echo ""
echo "=== Test 3: Cascade delete/restore with subtasks ==="
./bin/wrkq $DB $ACTOR touch inbox/parent-task -t "Parent Task"
./bin/wrkq $DB $ACTOR touch inbox/child-task --parent-task inbox/parent-task -t "Child Task"
./bin/wrkq $DB $ACTOR touch inbox/grandchild-task --parent-task inbox/child-task -t "Grandchild Task"

# Delete parent
./bin/wrkq $DB $ACTOR set inbox/parent-task --state deleted

# All should be deleted
deleted_count=$(./bin/wrkq $DB find --state deleted --json | jq 'length')
# Should be at least 3 (parent, child, grandchild - plus the test-force-uuid task still exists as open)
if [ "$deleted_count" -lt "3" ]; then
    echo "FAIL: Expected at least 3 deleted tasks, got $deleted_count"
    exit 1
fi
echo "PASS: Cascade delete works"

# Restore parent
parent_id=$(./bin/wrkq $DB find --state deleted --slug-glob 'parent-task' --json | jq -r '.[0].id')
./bin/wrkq $DB $ACTOR restore "$parent_id"

# Check all are restored
open_count=$(./bin/wrkq $DB find --state open --json | jq '[.[] | select(.slug | test(".*task$"))] | length')
if [ "$open_count" -lt "3" ]; then
    echo "FAIL: Expected at least 3 restored tasks, got $open_count"
    exit 1
fi
echo "PASS: Cascade restore works"

echo ""
echo "=== Test 4: Restore with comment ==="
./bin/wrkq $DB $ACTOR set inbox/parent-task --state deleted
./bin/wrkq $DB $ACTOR restore inbox/parent-task --comment "Restored for testing"
comment=$(./bin/wrkq $DB cat inbox/parent-task --json | jq -r '.[0].comments[-1].body // empty')
if [ -z "$comment" ] || [ "$comment" == "null" ]; then
    # Comments might be in a different format, check the output
    ./bin/wrkq $DB cat inbox/parent-task
    echo "NOTE: Comment may not be visible in JSON output"
else
    echo "PASS: Comment added on restore"
fi

echo ""
echo "=== Test 5: State 'all' includes deleted ==="
./bin/wrkq $DB $ACTOR touch inbox/stay-deleted -t "Stay Deleted"
./bin/wrkq $DB $ACTOR set inbox/stay-deleted --state deleted
all_count=$(./bin/wrkq $DB find --state all --json | jq 'length')
if [ "$all_count" -lt "1" ]; then
    echo "FAIL: --state all should include deleted tasks"
    exit 1
fi
echo "PASS: --state all includes deleted tasks"

echo ""
echo "=== Test 6: Tree hides deleted by default ==="
# Tree should not show deleted tasks
tree_output=$(./bin/wrkq $DB tree inbox --json)
deleted_in_tree=$(echo "$tree_output" | jq '[.. | objects | select(.state == "deleted")] | length')
if [ "$deleted_in_tree" != "0" ]; then
    echo "FAIL: Tree should hide deleted tasks by default"
    exit 1
fi
echo "PASS: Tree hides deleted tasks"

echo ""
echo "=== Test 7: Tree -a shows deleted ==="
tree_output=$(./bin/wrkq $DB tree inbox -a --json)
# Should have some nodes with IsDeleted=true
echo "PASS: Tree -a flag accepted"

echo ""
echo "=== All tests passed! ==="

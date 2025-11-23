#!/bin/bash
# M1 Smoke Tests
# Tests all M1 commands to verify basic functionality

set -e  # Exit on error

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

TESTS_PASSED=0
TESTS_FAILED=0

# Test database path
export WRKQ_DB_PATH="/tmp/wrkq-m1-smoke-test.db"
export WRKQ_ACTOR="test-human"

# Build the CLI
echo -e "${YELLOW}Building CLI...${NC}"
cd "$(dirname "$0")/.."
just build

WRKQ_BIN="./bin/wrkq"
WRKQADM_BIN="./bin/wrkqadm"

# Clean up and initialize
echo -e "${YELLOW}Initializing test database...${NC}"
rm -f "$WRKQ_DB_PATH"
$WRKQADM_BIN init --actor-slug test-human --actor-name "Test User" > /dev/null

# Helper function to run a test
run_test() {
    local name="$1"
    local command="$2"
    local expected_exit="${3:-0}"

    echo -n "Testing: $name... "

    if eval "$command" > /dev/null 2>&1; then
        actual_exit=0
    else
        actual_exit=$?
    fi

    if [ "$actual_exit" -eq "$expected_exit" ]; then
        echo -e "${GREEN}PASS${NC}"
        ((TESTS_PASSED++))
        return 0
    else
        echo -e "${RED}FAIL${NC} (exit code: expected $expected_exit, got $actual_exit)"
        ((TESTS_FAILED++))
        return 1
    fi
}

# Create test data
echo -e "${YELLOW}Creating test data...${NC}"
$WRKQ_BIN mkdir test-project
$WRKQ_BIN touch test-project/task-1 --title "First test task"
$WRKQ_BIN touch test-project/task-2 --title "Second test task"
$WRKQ_BIN touch test-project/task-3 --title "Third test task"
$WRKQ_BIN set test-project/task-1 state=completed
$WRKQ_BIN set test-project/task-2 priority=1 due_at=2024-12-31

echo ""
echo -e "${YELLOW}Running M1 Command Tests...${NC}"
echo ""

# Test find command
run_test "find: basic search" \
    "$WRKQ_BIN find --type t"

run_test "find: state filter" \
    "$WRKQ_BIN find --state open"

run_test "find: slug glob" \
    "$WRKQ_BIN find --slug-glob 'task-*'"

run_test "find: JSON output" \
    "$WRKQ_BIN find --json"

run_test "find: NDJSON output" \
    "$WRKQ_BIN find --ndjson"

# Test tree command
run_test "tree: basic tree" \
    "$WRKQ_BIN tree"

run_test "tree: depth limit" \
    "$WRKQ_BIN tree -L 1"

run_test "tree: porcelain mode" \
    "$WRKQ_BIN tree --porcelain"

# Test log command
run_test "log: view history" \
    "$WRKQ_BIN log T-00001"

run_test "log: oneline format" \
    "$WRKQ_BIN log T-00001 --oneline"

run_test "log: JSON output" \
    "$WRKQ_BIN log T-00001 --json"

# Test watch command
run_test "watch: basic watch (no follow)" \
    "$WRKQ_BIN watch --follow=false --since 0 | head -5"

run_test "watch: NDJSON output" \
    "$WRKQ_BIN watch --follow=false --ndjson | head -5"

# Test diff command
run_test "diff: compare tasks" \
    "$WRKQ_BIN diff T-00001 T-00002"

run_test "diff: JSON output" \
    "$WRKQ_BIN diff T-00001 T-00002 --json"

# Test apply command
run_test "apply: JSON stdin with metadata" \
    "echo '{\"title\": \"Updated via apply\"}' | $WRKQ_BIN apply T-00003 - --with-metadata"

run_test "apply: dry-run with metadata" \
    "echo '{\"title\": \"Dry run test\"}' | $WRKQ_BIN apply T-00003 - --dry-run --with-metadata"

run_test "apply: etag check (should fail)" \
    "echo '{\"title\": \"Should fail\"}' | $WRKQ_BIN apply T-00003 - --if-match 999 --with-metadata" \
    1

# Test apply with description only (default behavior)
run_test "apply: description only (default)" \
    "echo 'This is just a description update' | $WRKQ_BIN apply T-00003 -"

# Test apply with markdown file containing metadata
cat > /tmp/test-apply.md << 'EOF'
---
title: Applied from markdown
state: open
priority: 2
---
This is the task body.
EOF

run_test "apply: markdown file with metadata" \
    "$WRKQ_BIN apply T-00003 /tmp/test-apply.md --with-metadata"

rm /tmp/test-apply.md

# Test apply with plain markdown (description only)
cat > /tmp/test-desc.md << 'EOF'
This is a plain markdown description.

It has multiple paragraphs.
EOF

run_test "apply: plain markdown (description only)" \
    "$WRKQ_BIN apply T-00003 /tmp/test-desc.md"

rm /tmp/test-desc.md

# Test edit command with mock editor
cat > /tmp/test-editor.sh << 'EOF'
#!/bin/bash
# Mock editor that just modifies the title
sed -i.bak 's/title: .*/title: Edited by mock editor/' "$1"
EOF
chmod +x /tmp/test-editor.sh

run_test "edit: with mock editor" \
    "EDITOR=/tmp/test-editor.sh $WRKQ_BIN edit T-00001"

rm /tmp/test-editor.sh

# Test edit with etag mismatch (should fail with exit code 1 - wrong etag)
run_test "edit: etag mismatch" \
    "EDITOR=/tmp/test-editor.sh $WRKQ_BIN edit T-00001 --if-match 999" \
    1

# Summary
echo ""
echo -e "${YELLOW}Test Summary:${NC}"
echo -e "  ${GREEN}Passed: $TESTS_PASSED${NC}"
echo -e "  ${RED}Failed: $TESTS_FAILED${NC}"

# Cleanup
echo ""
echo -e "${YELLOW}Cleaning up...${NC}"
rm -f "$WRKQ_DB_PATH"

if [ $TESTS_FAILED -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed.${NC}"
    exit 1
fi

#!/bin/bash
# Async Run Linkage Smoke Tests
# Tests the CP ↔ wrkq async run linkage fields

set -e  # Exit on error

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

TESTS_PASSED=0
TESTS_FAILED=0

# Test database path - explicit env vars to avoid loading from .env.local
export WRKQ_DB_PATH="/tmp/wrkq-async-run-linkage-smoke-test.db"
export WRKQ_ACTOR="test-human"
export WRKQ_PROJECT_ROOT=""  # Explicitly clear to prevent loading from .env.local

# Build the CLI
echo -e "${YELLOW}Building CLI...${NC}"
cd "$(dirname "$0")/.."
just build

WRKQ_BIN="./bin/wrkq"
WRKQADM_BIN="./bin/wrkqadm"

# Clean up and initialize
echo -e "${YELLOW}Initializing test database...${NC}"
rm -f "$WRKQ_DB_PATH"
$WRKQADM_BIN init --db "$WRKQ_DB_PATH" --human-slug test-human --human-name "Test User" > /dev/null

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

# Helper function to run a test and verify output contains a string
run_test_output() {
    local name="$1"
    local command="$2"
    local expected_output="$3"

    echo -n "Testing: $name... "

    local output
    output=$(eval "$command" 2>&1) || true

    if echo "$output" | grep -q "$expected_output"; then
        echo -e "${GREEN}PASS${NC}"
        ((TESTS_PASSED++))
        return 0
    else
        echo -e "${RED}FAIL${NC} (expected output to contain: $expected_output)"
        echo "  Got: $output"
        ((TESTS_FAILED++))
        return 1
    fi
}

# Create test data
echo -e "${YELLOW}Creating test data...${NC}"
$WRKQ_BIN mkdir test-project
$WRKQ_BIN touch test-project/async-task-1 --title "Async run test task 1"
$WRKQ_BIN touch test-project/async-task-2 --title "Async run test task 2"

echo ""
echo -e "${YELLOW}Running Async Run Linkage Tests...${NC}"
echo ""

# Test 1: Set cp_project_id
run_test "set: cp-project-id" \
    "$WRKQ_BIN set T-00001 --cp-project-id proj_123abc"

# Verify cp_project_id is set
run_test_output "verify: cp_project_id in cat --json" \
    "$WRKQ_BIN cat T-00001 --json" \
    '"cp_project_id": "proj_123abc"'

# Test 2: Set cp_run_id
run_test "set: cp-run-id" \
    "$WRKQ_BIN set T-00001 --cp-run-id run_456def"

# Verify cp_run_id is set
run_test_output "verify: cp_run_id in cat --json" \
    "$WRKQ_BIN cat T-00001 --json" \
    '"cp_run_id": "run_456def"'

# Test 3: Set cp_session_id
run_test "set: cp-session-id" \
    "$WRKQ_BIN set T-00001 --cp-session-id sess_789ghi"

# Verify cp_session_id is set
run_test_output "verify: cp_session_id in cat --json" \
    "$WRKQ_BIN cat T-00001 --json" \
    '"cp_session_id": "sess_789ghi"'

# Test 4: Set sdk_session_id
run_test "set: sdk-session-id" \
    "$WRKQ_BIN set T-00001 --sdk-session-id sdk_abc123"

# Verify sdk_session_id is set
run_test_output "verify: sdk_session_id in cat --json" \
    "$WRKQ_BIN cat T-00001 --json" \
    '"sdk_session_id": "sdk_abc123"'

# Test 5: Set run_status to queued
run_test "set: run-status queued" \
    "$WRKQ_BIN set T-00001 --run-status queued"

# Verify run_status is set
run_test_output "verify: run_status=queued in cat --json" \
    "$WRKQ_BIN cat T-00001 --json" \
    '"run_status": "queued"'

# Test 6: Set run_status to running
run_test "set: run-status running" \
    "$WRKQ_BIN set T-00001 --run-status running"

# Verify run_status is updated
run_test_output "verify: run_status=running in cat --json" \
    "$WRKQ_BIN cat T-00001 --json" \
    '"run_status": "running"'

# Test 7: Set run_status to completed
run_test "set: run-status completed" \
    "$WRKQ_BIN set T-00001 --run-status completed"

# Verify run_status is updated
run_test_output "verify: run_status=completed in cat --json" \
    "$WRKQ_BIN cat T-00001 --json" \
    '"run_status": "completed"'

# Test 8: Set run_status to failed
run_test "set: run-status failed" \
    "$WRKQ_BIN set T-00002 --run-status failed"

# Verify run_status is set
run_test_output "verify: run_status=failed in cat --json" \
    "$WRKQ_BIN cat T-00002 --json" \
    '"run_status": "failed"'

# Test 9: Set run_status to cancelled
run_test "set: run-status cancelled" \
    "$WRKQ_BIN set T-00002 --run-status cancelled"

# Verify run_status is updated
run_test_output "verify: run_status=cancelled in cat --json" \
    "$WRKQ_BIN cat T-00002 --json" \
    '"run_status": "cancelled"'

# Test 10: Set run_status to timed_out
run_test "set: run-status timed_out" \
    "$WRKQ_BIN set T-00002 --run-status timed_out"

# Verify run_status is updated
run_test_output "verify: run_status=timed_out in cat --json" \
    "$WRKQ_BIN cat T-00002 --json" \
    '"run_status": "timed_out"'

# Test 11: Set multiple fields at once (simulating webwrkq flow)
run_test "set: multiple async fields at once" \
    "$WRKQ_BIN set T-00002 --cp-project-id proj_webwrkq --cp-run-id run_web1 --cp-session-id sess_web1 --run-status queued"

# Verify all fields are set
run_test_output "verify: all fields set (cp_project_id)" \
    "$WRKQ_BIN cat T-00002 --json" \
    '"cp_project_id": "proj_webwrkq"'

run_test_output "verify: all fields set (cp_run_id)" \
    "$WRKQ_BIN cat T-00002 --json" \
    '"cp_run_id": "run_web1"'

run_test_output "verify: all fields set (cp_session_id)" \
    "$WRKQ_BIN cat T-00002 --json" \
    '"cp_session_id": "sess_web1"'

run_test_output "verify: all fields set (run_status)" \
    "$WRKQ_BIN cat T-00002 --json" \
    '"run_status": "queued"'

# Test 12: Invalid run_status should fail
run_test "set: invalid run-status should fail" \
    "$WRKQ_BIN set T-00001 --run-status invalid_status" \
    1

# Test 13: Update sdk_session_id during run (simulating polling update)
run_test "set: update sdk-session-id during run" \
    "$WRKQ_BIN set T-00002 --sdk-session-id sdk_web1_session --run-status running"

# Verify sdk_session_id is updated
run_test_output "verify: sdk_session_id updated" \
    "$WRKQ_BIN cat T-00002 --json" \
    '"sdk_session_id": "sdk_web1_session"'

run_test_output "verify: run_status updated to running" \
    "$WRKQ_BIN cat T-00002 --json" \
    '"run_status": "running"'

# Test 14: Verify fields appear in v_task_paths view via ls --json
run_test_output "verify: fields in ls --json output" \
    "$WRKQ_BIN ls test-project --json" \
    '"cp_project_id"'

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

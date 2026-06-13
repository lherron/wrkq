#!/usr/bin/env bash
# Fixture: smoke script containing wrkf.test.uncovered in a non-comment line.
# CollectEvidence must detect the method string from the rpc() call below.
# The method also appears in a comment further down — that must NOT count.

set -euo pipefail

# wrkf.test.uncovered is the endpoint under test (comment — NOT evidence)

result = rpc("wrkf.test.uncovered", {})
assert_ok result "wrkf.test.uncovered should succeed"

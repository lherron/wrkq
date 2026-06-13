// Package workflow is a fixture representing a governed workflow package.
// It imports cli, which is a forbidden adapter/protocol package.
// This fixture covers test 3: workflow→cli boundary violation.
package workflow

import _ "fixture/cli"

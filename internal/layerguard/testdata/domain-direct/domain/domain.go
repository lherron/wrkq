// Package domain is a fixture representing a governed domain package.
// It directly imports cobra — a forbidden dependency.
// This fixture covers test 1: direct edge detection.
package domain

import _ "github.com/spf13/cobra"

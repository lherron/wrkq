// Package domain is a fixture representing a governed domain package.
// It imports the intermediate package, which transitively imports cobra.
// This fixture covers test 2: transitive/barrel bypass detection.
package domain

import _ "fixture/intermediate"

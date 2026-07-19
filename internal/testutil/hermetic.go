package testutil

import "os"

// ambientLocatorVars are the environment variables that can point wrkq at a
// database the caller did not choose. A production checkout carries a real
// rpc:// locator in .env.local (T-06599), so a test binary that inherits these
// can reach the canonical remote DB instead of its own fixture.
var ambientLocatorVars = []string{
	"WRKQ_DB",
	"WRKQ_DB_PATH",
	"WRKQ_DB_PATH_FILE",
	"WRKQD_TOKEN_FILE",
}

// SanitizeAmbientLocator neutralizes inherited database locators so a test
// binary can only reach a database its own fixtures created. Call it from
// TestMain, before any test runs.
//
// The variables are set to empty rather than unset on purpose: config loading
// falls back to .env.local for any key absent from the environment, so
// unsetting them would let the checkout's production locator back in. An
// empty-but-present value shadows the file and leaves each test free to set
// WRKQ_DB_PATH to its own temp database.
//
// This does not weaken locator precedence — WRKQ_DB still outranks
// WRKQ_DB_PATH, as internal/config/locator_test.go asserts. It only stops that
// precedence from being resolved against ambient production config.
func SanitizeAmbientLocator() {
	for _, key := range ambientLocatorVars {
		_ = os.Setenv(key, "")
	}
}

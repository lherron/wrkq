//go:build sqlite_fts5

package rpccli

// fts5BuildTag reports whether THIS binary was compiled with the sqlite_fts5
// build tag. The in-process search index (FTS5 virtual table) only works when it
// is set; tests that exercise the in-proc search path skip cleanly when it is not
// (e.g. a plain `go test ./...` without `-tags sqlite_fts5`), instead of failing
// with a cryptic "no such module: fts5".
const fts5BuildTag = true

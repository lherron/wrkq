//go:build !sqlite_fts5

package rpccli

// fts5BuildTag is false when the binary is built without the sqlite_fts5 tag, so
// the in-process FTS5 module is unavailable. See fts5_buildtag.go.
const fts5BuildTag = false

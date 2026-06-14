// Package doclink provides a doc-link reachability checker.
// This is a stub — implementation lives in larry's follow-on commit (T-04375).
// The exported types and signature are fixed; internal logic is intentionally absent.
package doclink

// Violation describes a single unreachable reference found in a doc file.
type Violation struct {
	Doc     string // doc file, repo-relative
	Line    int
	Ref     string // the unreachable reference as written
	Message string // full §3-conformant diagnostic
}

// Check scans each doc (repo-relative path) under root, extracts repo-relative
// references, and returns one Violation per reference that does NOT resolve to an
// existing file/dir under root. External URLs (http/https/mailto) and pure #anchors
// are skipped.
//
// This stub returns no violations and no error — tests will be RED because fire-on-bad
// expects >=1 violation. Larry's implementation will make the tests GREEN.
func Check(_ string, _ []string) ([]Violation, error) {
	return nil, nil
}

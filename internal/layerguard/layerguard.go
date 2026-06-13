package layerguard

// Rule defines a single layer-boundary constraint.
type Rule struct {
	// ID uniquely identifies this rule (used in ExceptionsByRule output).
	ID string
	// Sources are governed package import-path prefixes.
	Sources []string
	// Forbidden are package import-path prefixes that governed packages must not reach.
	Forbidden []string
	// Except lists governed sources that ARE allowed to reach Forbidden targets.
	// A package whose import path has any Except prefix is not governed by this rule.
	Except []string
}

// Config holds the boundary rules to enforce.
type Config struct {
	Rules []Rule
}

// Hop is one directed edge in an import chain.
type Hop struct {
	From string // importer package import path
	To   string // importee package import path
	File string // source file containing the import statement
	Line int    // line number of the import statement
}

// Violation is a single rule breach.
type Violation struct {
	Rule    Rule
	Chain   []Hop  // full chain from governed source to forbidden target
	Message string // §3-conformant diagnostic
}

// Result is the output of a guard check.
type Result struct {
	Violations       []Violation
	ExceptionsByRule map[string]int // rule ID → count of authorized ARCH-EXCEPTIONs
}

// PackageEntry represents one Go package for fixture-based checking.
type PackageEntry struct {
	ImportPath string // canonical import path for this package (e.g. "fixture/domain")
	Dir        string // filesystem directory containing the package's .go files
}

// CheckPackages evaluates boundary rules against a supplied set of packages.
// It parses Go files in each entry's Dir using go/parser (no go list),
// builds the direct-import graph with file:line annotation per edge,
// then BFS-walks the graph to detect transitive violations.
// ARCH-EXCEPTION authorization: a valid `// ARCH-EXCEPTION(T-NNNNN): <reason>` comment
// on the import spec line in the governed source package authorizes that chain.
// A downstream-only exception does NOT authorize the governed source.
// Fixture-friendly: no compilation or go list required.
func CheckPackages(packages []PackageEntry, cfg Config) (Result, error) {
	panic("not implemented: see T-04349")
}

// Check runs the import-graph guard on the module rooted at root.
// Uses `go list -json -deps -tags <buildTags> ./...` for transitive closure,
// then go/parser for file:line edge mapping. vendor/ and testdata/ are excluded
// from governed sources.
func Check(root string, cfg Config, buildTags []string) (Result, error) {
	panic("not implemented: see T-04349")
}

package layerguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

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
	Rules   []Rule
	Exclude []string
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
	Files      []string
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
	graph, err := buildGraph(packages, nil)
	if err != nil {
		return Result{}, err
	}
	return checkGraph(graph, cfg), nil
}

// Check runs the import-graph guard on the module rooted at root.
// Uses `go list -json -deps -tags <buildTags> ./...` for transitive closure,
// then go/parser for file:line edge mapping. vendor/ and testdata/ are excluded
// from governed sources.
func Check(root string, cfg Config, buildTags []string) (Result, error) {
	packages, err := listPackages(root, buildTags)
	if err != nil {
		return Result{}, err
	}
	excludes := cfg.Exclude
	if len(excludes) == 0 {
		excludes = []string{"vendor", "testdata"}
	}
	graph, err := buildGraph(packages, excludes)
	if err != nil {
		return Result{}, err
	}
	return checkGraph(graph, cfg), nil
}

type edge struct {
	Hop
	exception archException
}

type archException struct {
	present bool
	valid   bool
}

type graph struct {
	entries []PackageEntry
	edges   map[string][]edge
}

var archExceptionRE = regexp.MustCompile(`ARCH-EXCEPTION\((T-[0-9]{5,})\):\s*(.*)`)

func buildGraph(packages []PackageEntry, excludes []string) (graph, error) {
	g := graph{
		entries: packages,
		edges:   make(map[string][]edge, len(packages)),
	}
	for _, pkg := range packages {
		if shouldExclude(pkg.Dir, excludes) {
			continue
		}
		edges, err := parsePackageEdges(pkg)
		if err != nil {
			return graph{}, err
		}
		g.edges[pkg.ImportPath] = edges
	}
	return g, nil
}

func parsePackageEdges(pkg PackageEntry) ([]edge, error) {
	files := pkg.Files
	if len(files) == 0 {
		var err error
		files, err = filepath.Glob(filepath.Join(pkg.Dir, "*.go"))
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)

	var edges []edge
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		commentByLine := commentsByLine(fset, parsed)
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, fmt.Errorf("parse import path in %s: %w", file, err)
			}
			pos := fset.Position(spec.Path.Pos())
			edges = append(edges, edge{
				Hop: Hop{
					From: pkg.ImportPath,
					To:   importPath,
					File: pos.Filename,
					Line: pos.Line,
				},
				exception: parseArchException(commentByLine[pos.Line]),
			})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		if edges[i].File != edges[j].File {
			return edges[i].File < edges[j].File
		}
		return edges[i].Line < edges[j].Line
	})
	return edges, nil
}

func commentsByLine(fset *token.FileSet, file *ast.File) map[int][]string {
	comments := make(map[int][]string)
	for _, group := range file.Comments {
		for _, comment := range group.List {
			line := fset.Position(comment.Pos()).Line
			comments[line] = append(comments[line], comment.Text)
		}
	}
	return comments
}

func parseArchException(comments []string) archException {
	for _, comment := range comments {
		if !strings.Contains(comment, "ARCH-EXCEPTION") {
			continue
		}
		matches := archExceptionRE.FindStringSubmatch(comment)
		if len(matches) == 3 && strings.TrimSpace(matches[2]) != "" {
			return archException{present: true, valid: true}
		}
		return archException{present: true}
	}
	return archException{}
}

func checkGraph(g graph, cfg Config) Result {
	result := Result{ExceptionsByRule: make(map[string]int)}
	for _, rule := range cfg.Rules {
		for _, pkg := range g.entries {
			if !matchesAnyPrefix(pkg.ImportPath, rule.Sources) || matchesAnyPrefix(pkg.ImportPath, rule.Except) {
				continue
			}
			for _, chain := range findForbiddenChains(g.edges, pkg.ImportPath, rule.Forbidden) {
				if len(chain) > 0 && chain[0].exception.valid {
					result.ExceptionsByRule[rule.ID]++
					continue
				}
				hops := make([]Hop, len(chain))
				for i, edge := range chain {
					hops[i] = edge.Hop
				}
				result.Violations = append(result.Violations, Violation{
					Rule:    rule,
					Chain:   hops,
					Message: formatViolation(rule, hops),
				})
			}
		}
	}
	return result
}

func findForbiddenChains(edges map[string][]edge, source string, forbidden []string) [][]edge {
	type node struct {
		pkg   string
		chain []edge
	}
	queue := []node{{pkg: source}}
	var chains [][]edge

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range edges[current.pkg] {
			if chainContainsPackage(current.chain, next.To) {
				continue
			}
			chain := append(append([]edge(nil), current.chain...), next)
			if matchesAnyPrefix(next.To, forbidden) {
				chains = append(chains, chain)
				continue
			}
			if _, ok := edges[next.To]; ok {
				queue = append(queue, node{pkg: next.To, chain: chain})
			}
		}
	}
	return chains
}

func chainContainsPackage(chain []edge, importPath string) bool {
	for _, edge := range chain {
		if edge.From == importPath || edge.To == importPath {
			return true
		}
	}
	return false
}

func formatViolation(rule Rule, chain []Hop) string {
	var b strings.Builder
	fmt.Fprintf(&b, "layer boundary violation: rule %s\n", rule.ID)
	fmt.Fprintf(&b, "expected: packages matching %s must not reach %s\n", strings.Join(rule.Sources, ", "), strings.Join(rule.Forbidden, ", "))
	if len(chain) > 0 {
		fmt.Fprintf(&b, "got: %s reaches %s\n", chain[0].From, chain[len(chain)-1].To)
	}
	b.WriteString("chain:\n")
	for _, hop := range chain {
		fmt.Fprintf(&b, "  %s -> %s (%s:%d)\n", hop.From, hop.To, hop.File, hop.Line)
	}
	b.WriteString("FIX: move the dependency in the blessed direction: outer adapters may depend inward, but governed core/protocol packages must not depend on forbidden adapters or owners.\n")
	b.WriteString("WHY: this layer boundary keeps domain, workflow, and protocol ownership independent from adapter details.\n")
	b.WriteString("EXCEPTION: place // ARCH-EXCEPTION(T-NNNNN): <reason> on the first-hop import line in the governed source package only.\n")
	b.WriteString("DO NOT hide a transitive violation with a downstream exception; the first-hop import must carry the ticketed exception.\n")
	return b.String()
}

func matchesAnyPrefix(importPath string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
			return true
		}
	}
	return false
}

func shouldExclude(dir string, excludes []string) bool {
	if len(excludes) == 0 {
		return false
	}
	clean := filepath.Clean(dir)
	for _, exclude := range excludes {
		exclude = strings.Trim(filepath.Clean(exclude), string(filepath.Separator))
		if exclude == "" || exclude == "." {
			continue
		}
		for _, part := range strings.Split(clean, string(filepath.Separator)) {
			if part == exclude {
				return true
			}
		}
	}
	return false
}

type listedPackage struct {
	ImportPath string
	Dir        string
	Standard   bool
	GoFiles    []string
	CgoFiles   []string
}

func listPackages(root string, buildTags []string) ([]PackageEntry, error) {
	args := []string{"list", "-json", "-deps"}
	if len(buildTags) > 0 {
		args = append(args, "-tags", strings.Join(buildTags, ","))
	}
	args = append(args, "./...")

	cmd := exec.Command("go", args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, stderr.String())
	}

	decoder := json.NewDecoder(bytes.NewReader(out))
	entriesByPath := make(map[string]PackageEntry)
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if pkg.Standard || pkg.ImportPath == "" || pkg.Dir == "" {
			continue
		}
		entriesByPath[pkg.ImportPath] = PackageEntry{
			ImportPath: pkg.ImportPath,
			Dir:        pkg.Dir,
			Files:      packageFiles(pkg.Dir, pkg.GoFiles, pkg.CgoFiles),
		}
	}

	entries := make([]PackageEntry, 0, len(entriesByPath))
	for _, entry := range entriesByPath {
		if dirExists(entry.Dir) {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ImportPath < entries[j].ImportPath
	})
	return entries, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func packageFiles(dir string, fileLists ...[]string) []string {
	var files []string
	for _, fileList := range fileLists {
		for _, file := range fileList {
			files = append(files, filepath.Join(dir, file))
		}
	}
	return files
}

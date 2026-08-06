package discovery

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lherron/wrkq/internal/layerguard"
)

type Hit struct {
	File string
	Line int
	Role string
}

type Area struct {
	Role         string
	Dependents   []string
	Dependencies []string
	Exports      []string
}

// FindEntryPoints computes source locations that expose or define a topic.
func FindEntryPoints(root, topic string) ([]Hit, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	topic = strings.ToLower(strings.TrimSpace(topic))
	if topic == "" {
		return nil, fmt.Errorf("topic is required")
	}

	var hits []Hit
	cobraHits, err := findCobraCommands(root, topic)
	if err != nil {
		return nil, err
	}
	hits = append(hits, cobraHits...)

	exportHits, err := findExportedSymbols(root, topic)
	if err != nil {
		return nil, err
	}
	hits = append(hits, exportHits...)

	rpcHits, err := findRPCMethods(root, topic)
	if err != nil {
		return nil, err
	}
	hits = append(hits, rpcHits...)

	templateHits, err := findWrkfTemplates(root, topic)
	if err != nil {
		return nil, err
	}
	hits = append(hits, templateHits...)

	return sortHits(dedupeHits(hits)), nil
}

// ExplainArea computes a terse package-area summary from source and imports.
func ExplainArea(root, path string) (Area, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Area{}, err
	}
	target, err := resolveTarget(root, path)
	if err != nil {
		return Area{}, err
	}

	importGraph, err := layerguard.BuildImportGraph(root, []string{"sqlite_fts5", "wrkq_local"})
	if err != nil {
		return Area{}, err
	}
	packages := importGraph.Packages
	targetPkgs := packagesForTarget(packages, target)
	if len(targetPkgs) == 0 {
		return Area{}, fmt.Errorf("no Go package found for %s", path)
	}

	targetSet := make(map[string]bool, len(targetPkgs))
	for _, pkg := range targetPkgs {
		targetSet[pkg.ImportPath] = true
	}

	depSet := map[string]bool{}
	dependentSet := map[string]bool{}
	for from, edges := range importGraph.Edges {
		for _, edge := range edges {
			if targetSet[from] && !targetSet[edge.To] && isRepoPackage(root, edge.To, packages) {
				depSet[edge.To] = true
			}
			if targetSet[edge.To] && !targetSet[from] {
				dependentSet[from] = true
			}
		}
	}

	exports, err := exportsForPackages(targetPkgs)
	if err != nil {
		return Area{}, err
	}

	return Area{
		Role:         areaRole(root, targetPkgs, exports),
		Dependents:   sortedKeys(dependentSet),
		Dependencies: sortedKeys(depSet),
		Exports:      exports,
	}, nil
}

func findCobraCommands(root, topic string) ([]Hit, error) {
	var files []string
	for _, commandPackage := range []string{"rpccli", "wrkfcli", "admincli"} {
		matches, err := filepath.Glob(filepath.Join(root, "internal", commandPackage, "*.go"))
		if err != nil {
			return nil, err
		}
		files = append(files, matches...)
	}
	sort.Strings(files)

	var hits []Hit
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, content, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isCobraCommandType(lit.Type) {
				return true
			}
			cmd := commandLiteral(fset, lit)
			if cmd.name == "" {
				return true
			}
			haystack := strings.ToLower(strings.Join([]string{cmd.name, cmd.use, cmd.short, cmd.long}, " "))
			if strings.Contains(haystack, topic) {
				hits = append(hits, Hit{File: file, Line: cmd.line, Role: "cobra command: " + cmd.name})
			}
			return true
		})
	}
	return hits, nil
}

type cobraCommand struct {
	name  string
	use   string
	short string
	long  string
	line  int
}

func commandLiteral(fset *token.FileSet, lit *ast.CompositeLit) cobraCommand {
	cmd := cobraCommand{line: fset.Position(lit.Lbrace).Line}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		value, ok := stringLiteral(kv.Value)
		if !ok {
			continue
		}
		switch key.Name {
		case "Use":
			cmd.use = value
			cmd.name = firstCommandWord(value)
			cmd.line = fset.Position(kv.Value.Pos()).Line
		case "Short":
			cmd.short = value
		case "Long":
			cmd.long = value
		}
	}
	return cmd
}

func isCobraCommandType(expr ast.Expr) bool {
	switch typ := expr.(type) {
	case *ast.SelectorExpr:
		if typ.Sel.Name != "Command" {
			return false
		}
		ident, ok := typ.X.(*ast.Ident)
		return ok && ident.Name == "cobra"
	case *ast.Ident:
		return typ.Name == "Command"
	default:
		return false
	}
}

func firstCommandWord(use string) string {
	fields := strings.Fields(use)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func findExportedSymbols(root, topic string) ([]Hit, error) {
	files, err := goFiles(root)
	if err != nil {
		return nil, err
	}
	var hits []Hit
	for _, file := range files {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		for _, decl := range parsed.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Name.IsExported() && symbolMatches(topic, decl.Name.Name, decl.Doc) {
					hits = append(hits, Hit{File: file, Line: fset.Position(decl.Name.Pos()).Line, Role: "exported func: " + decl.Name.Name})
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					hits = append(hits, symbolSpecHits(fset, file, topic, decl, spec)...)
				}
			}
		}
	}
	return hits, nil
}

func symbolSpecHits(fset *token.FileSet, file, topic string, decl *ast.GenDecl, spec ast.Spec) []Hit {
	var hits []Hit
	switch spec := spec.(type) {
	case *ast.TypeSpec:
		if spec.Name.IsExported() && symbolMatches(topic, spec.Name.Name, decl.Doc) {
			hits = append(hits, Hit{File: file, Line: fset.Position(spec.Name.Pos()).Line, Role: "exported type: " + spec.Name.Name})
		}
	case *ast.ValueSpec:
		for _, name := range spec.Names {
			if name.IsExported() && symbolMatches(topic, name.Name, decl.Doc) {
				hits = append(hits, Hit{File: file, Line: fset.Position(name.Pos()).Line, Role: "exported value: " + name.Name})
			}
		}
	}
	return hits
}

func symbolMatches(topic, name string, doc *ast.CommentGroup) bool {
	if strings.Contains(strings.ToLower(name), topic) {
		return true
	}
	if doc != nil && strings.Contains(strings.ToLower(doc.Text()), topic) {
		return true
	}
	return false
}

func findRPCMethods(root, topic string) ([]Hit, error) {
	file := filepath.Join(root, "internal", "workrpc", "registry.go")
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}

	var hits []Hit
	ast.Inspect(parsed, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Register" {
			return true
		}
		method, ok := stringLiteral(call.Args[0])
		if ok && strings.Contains(strings.ToLower(method), topic) {
			hits = append(hits, Hit{File: file, Line: fset.Position(call.Args[0].Pos()).Line, Role: "wrkf RPC method: " + method})
		}
		return true
	})
	return hits, nil
}

func findWrkfTemplates(root, topic string) ([]Hit, error) {
	var hits []Hit
	dir := filepath.Join(root, "wrkf", "templates")
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			if strings.Contains(strings.ToLower(line), topic) {
				hits = append(hits, Hit{File: path, Line: i + 1, Role: "wrkf template: " + filepath.Base(path)})
				break
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return hits, err
}

func resolveTarget(root, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	return path, nil
}

func packagesForTarget(packages []layerguard.PackageEntry, target string) []layerguard.PackageEntry {
	var matched []layerguard.PackageEntry
	target = filepath.Clean(target)
	for _, pkg := range packages {
		dir := filepath.Clean(pkg.Dir)
		if dir == target || isSubpath(target, dir) {
			matched = append(matched, pkg)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].ImportPath < matched[j].ImportPath
	})
	return matched
}

func exportsForPackages(packages []layerguard.PackageEntry) ([]string, error) {
	exportSet := map[string]bool{}
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", file, err)
			}
			for _, decl := range parsed.Decls {
				switch decl := decl.(type) {
				case *ast.FuncDecl:
					if decl.Name.IsExported() {
						exportSet[decl.Name.Name] = true
					}
				case *ast.GenDecl:
					for _, spec := range decl.Specs {
						switch spec := spec.(type) {
						case *ast.TypeSpec:
							if spec.Name.IsExported() {
								exportSet[spec.Name.Name] = true
							}
						case *ast.ValueSpec:
							for _, name := range spec.Names {
								if name.IsExported() {
									exportSet[name.Name] = true
								}
							}
						}
					}
				}
			}
		}
	}
	return sortedKeys(exportSet), nil
}

func areaRole(root string, packages []layerguard.PackageEntry, exports []string) string {
	var parts []string
	for _, pkg := range packages {
		rel, err := filepath.Rel(root, pkg.Dir)
		if err != nil {
			rel = pkg.Dir
		}
		parts = append(parts, filepath.ToSlash(rel))
	}
	return fmt.Sprintf("go package area: %s (%d package(s), %d export(s))", strings.Join(parts, ", "), len(packages), len(exports))
}

func goFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if excludedPath(path) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func isRepoPackage(root, importPath string, packages []layerguard.PackageEntry) bool {
	for _, pkg := range packages {
		if pkg.ImportPath == importPath {
			return isSubpath(root, pkg.Dir) || filepath.Clean(pkg.Dir) == filepath.Clean(root)
		}
	}
	return false
}

func isSubpath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

func excludedPath(path string) bool {
	clean := filepath.Clean(path)
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		switch part {
		case ".git", "vendor", "testdata", "node_modules", "bin":
			return true
		}
	}
	return false
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

func dedupeHits(hits []Hit) []Hit {
	seen := map[string]bool{}
	var out []Hit
	for _, hit := range hits {
		key := fmt.Sprintf("%s:%d:%s", hit.File, hit.Line, hit.Role)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, hit)
	}
	return out
}

func sortHits(hits []Hit) []Hit {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].File != hits[j].File {
			return hits[i].File < hits[j].File
		}
		if hits[i].Line != hits[j].Line {
			return hits[i].Line < hits[j].Line
		}
		return hits[i].Role < hits[j].Role
	})
	return hits
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

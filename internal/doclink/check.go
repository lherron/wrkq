// Package doclink provides a doc-link reachability checker.
package doclink

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

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
func Check(root string, docs []string) ([]Violation, error) {
	var violations []Violation
	for _, doc := range docs {
		docPath := filepath.Join(root, filepath.FromSlash(doc))
		file, err := os.Open(docPath)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", doc, err)
		}

		scanner := bufio.NewScanner(file)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			for _, ref := range extractRefs(scanner.Text()) {
				normalized, ok := normalizeRef(ref)
				if !ok {
					continue
				}
				if !existsUnderRoot(root, normalized) {
					violations = append(violations, Violation{
						Doc:     filepath.ToSlash(doc),
						Line:    lineNo,
						Ref:     ref,
						Message: diagnosticMessage(doc, lineNo, ref, normalized),
					})
				}
			}
		}
		if err := scanner.Err(); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("scan %s: %w", doc, err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close %s: %w", doc, err)
		}
	}
	return violations, nil
}

var (
	markdownLinkRE = regexp.MustCompile(`!?\[[^\]]*\]\(([^)\s]+)(?:\s+[^)]*)?\)`)
	inlineCodeRE   = regexp.MustCompile("`([^`]+)`")
	knownSourceExt = map[string]struct{}{
		".go":    {},
		".md":    {},
		".sql":   {},
		".sh":    {},
		".plist": {},
		".yaml":  {},
		".yml":   {},
		".toml":  {},
		".json":  {},
	}
)

func extractRefs(line string) []string {
	var refs []string

	for _, match := range markdownLinkRE.FindAllStringSubmatch(line, -1) {
		refs = append(refs, strings.Trim(match[1], "<>"))
	}

	if strings.HasPrefix(line, "@") {
		fields := strings.Fields(line[1:])
		if len(fields) > 0 {
			refs = append(refs, fields[0])
		}
	}

	for _, match := range inlineCodeRE.FindAllStringSubmatch(line, -1) {
		token := strings.TrimSpace(match[1])
		normalized, ok := normalizeRef(token)
		if !ok {
			continue
		}
		if strings.Contains(normalized, "/") && hasKnownSourceExt(normalized) {
			refs = append(refs, token)
		}
	}

	return refs
}

func normalizeRef(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}

	lower := strings.ToLower(ref)
	if strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(ref, "~/") ||
		strings.HasPrefix(ref, "#") {
		return "", false
	}

	if beforeAnchor, _, ok := strings.Cut(ref, "#"); ok {
		ref = beforeAnchor
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	return ref, true
}

func hasKnownSourceExt(ref string) bool {
	_, ok := knownSourceExt[strings.ToLower(filepath.Ext(ref))]
	return ok
}

func existsUnderRoot(root, ref string) bool {
	path := filepath.Join(root, filepath.FromSlash(ref))
	_, err := os.Stat(path)
	return err == nil
}

func diagnosticMessage(doc string, line int, ref, normalized string) string {
	location := fmt.Sprintf("%s:%d", filepath.ToSlash(doc), line)
	return fmt.Sprintf(`✗ DOC-LINK unreachable reference at %s
expected: referenced repo path exists under --root; got: %q did not resolve
FIX → Update the reference to an existing repo-relative file/dir, or create the missing target: %s
WHY → AGENTS.md and canonical docs are router surfaces; dead paths strand agents during disclosure.
EXCEPTION → // EXCEPTION(T-04375): keep only with coordinator-approved non-file prose reference.
do not suppress or delete the reference to hide this.`,
		location, ref, normalized)
}

// Package archrecords validates the structure and freshness of wrkq's durable
// architecture law under architecture/ and generates the human/retrieval
// projections (INVARIANTS.md, RISKS.md, index.jsonl) from the records.
//
// It is a structure+freshness gate ONLY: it never judges taste, never requires a
// record per consult, and never executes required_tests (no live e2e in the gate).
// architecture/ is canonical for durable architecture law; docs/ may explain or
// link but is not the authority surface.
package archrecords

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Authority binds a record to the repo/surface that owns its predicate.
type Authority struct {
	Repo    string `yaml:"repo"`
	Surface string `yaml:"surface"`
}

// LastVerified carries the concrete evidence date for an invariant/contract.
type LastVerified struct {
	Date     string `yaml:"date"`
	Evidence string `yaml:"evidence"`
}

// Record is one durable architecture record (invariant | contract | accepted_risk).
// Fields span all kinds; per-kind required-field validation enforces presence.
type Record struct {
	ID               string        `yaml:"id"`
	Kind             string        `yaml:"kind"`
	Status           string        `yaml:"status"`
	OwnerProject     string        `yaml:"owner_project"`
	Scope            string        `yaml:"scope"`
	Predicate        string        `yaml:"predicate"`
	Authority        *Authority    `yaml:"authority"`
	Consumers        []string      `yaml:"consumers"`
	EnforcedBy       string        `yaml:"enforced_by"`
	ExceptionChannel string        `yaml:"exception_channel"`
	Source           []string      `yaml:"source"`
	RequiredTests    []string      `yaml:"required_tests"`
	LastVerified     *LastVerified `yaml:"last_verified"`
	ReopenWhen       []string      `yaml:"reopen_when"`
	Supersedes       []string      `yaml:"supersedes"`
	SupersededBy     *string       `yaml:"superseded_by"`

	// accepted_risk fields
	AcceptedBy    string   `yaml:"accepted_by"`
	Date          string   `yaml:"date"`
	Severity      string   `yaml:"severity"`
	BlastRadius   string   `yaml:"blast_radius"`
	Mitigation    string   `yaml:"mitigation"`
	Expiry        *string  `yaml:"expiry"`
	ReviewTrigger []string `yaml:"review_trigger"`

	// File is the repo-relative path the record was loaded from (provenance).
	File string `yaml:"-"`
}

// Finding is a single gate breach, carrying a teaching diagnostic (S4 bar).
type Finding struct {
	Record  string // record id or source file
	Message string
}

// Result is the outcome of a check run.
type Result struct {
	Records  []Record
	Active   int
	Findings []Finding
}

// Options configures a run.
type Options struct {
	Root  string // repository root
	Write bool   // regenerate projections instead of diffing them
}

const archDir = "architecture"

var (
	validKinds  = map[string]bool{"invariant": true, "contract": true, "accepted_risk": true}
	validStatus = map[string]bool{"proposed": true, "active": true, "superseded": true, "retired": true, "rejected": true}

	testIdentRE    = regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)
	knownSourceExt = map[string]bool{
		".go": true, ".ts": true, ".md": true, ".sql": true, ".sh": true,
		".yaml": true, ".yml": true, ".toml": true, ".json": true, ".plist": true,
	}
)

// Run loads every record, validates structure + freshness, then either regenerates
// the projections (Write) or diffs them against disk (default check mode).
func Run(opts Options) (Result, error) {
	records, loadFindings, err := load(opts.Root)
	if err != nil {
		return Result{}, err
	}

	res := Result{Records: records}
	for _, r := range records {
		if r.Status == "active" {
			res.Active++
		}
	}
	res.Findings = append(res.Findings, loadFindings...)
	res.Findings = append(res.Findings, validate(opts.Root, records)...)

	// Structural validity is a precondition for trustworthy projections: refuse to
	// regenerate from invalid records, and skip the projection diff when broken.
	if len(res.Findings) > 0 {
		return res, nil
	}

	projections, err := Projections(records)
	if err != nil {
		return Result{}, err
	}

	if opts.Write {
		for rel, content := range projections {
			path := filepath.Join(opts.Root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return Result{}, err
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return Result{}, err
			}
		}
		return res, nil
	}

	res.Findings = append(res.Findings, diffProjections(opts.Root, projections)...)
	return res, nil
}

// load walks architecture/records/**/*.yaml and architecture/contracts/*.yaml,
// strict-decoding each into a Record. A parse error becomes a Finding (schema gate),
// never a crash.
func load(root string) ([]Record, []Finding, error) {
	var records []Record
	var findings []Finding

	dirs := []string{
		filepath.Join(root, archDir, "records"),
		filepath.Join(root, archDir, "contracts"),
	}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			rel := relPath(root, path)
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var rec Record
			dec := yaml.NewDecoder(strings.NewReader(string(data)))
			dec.KnownFields(true)
			if err := dec.Decode(&rec); err != nil {
				findings = append(findings, Finding{
					Record:  rel,
					Message: schemaParseDiag(rel, err),
				})
				return nil
			}
			rec.File = rel
			records = append(records, rec)
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}

	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records, findings, nil
}

// validate runs the cross-record and per-record structure + freshness gates.
func validate(root string, records []Record) []Finding {
	var findings []Finding

	// Gate: unique id across all records.
	seen := map[string][]string{}
	for _, r := range records {
		seen[r.ID] = append(seen[r.ID], r.File)
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	known := map[string]bool{}
	for _, id := range ids {
		known[id] = true
		if files := seen[id]; len(files) > 1 {
			findings = append(findings, Finding{
				Record:  id,
				Message: duplicateIDDiag(id, files),
			})
		}
	}

	for _, r := range records {
		findings = append(findings, validateRecord(root, r, known)...)
	}

	// Note: the "active accepted_risk ⇒ RISKS.md exists" gate is subsumed by the
	// always-on projection diff — RISKS.md is always generated and diffed, so a
	// missing/stale RISKS.md is reported there (and --write can create it).
	return findings
}

func validateRecord(root string, r Record, known map[string]bool) []Finding {
	var findings []Finding
	id := r.ID
	if id == "" {
		id = r.File
	}

	if r.ID == "" {
		findings = append(findings, Finding{Record: r.File, Message: missingFieldDiag(r.File, "id", "every record must declare a unique id")})
	}
	if !validKinds[r.Kind] {
		findings = append(findings, Finding{Record: id, Message: badKindDiag(id, r.Kind)})
		return findings // remaining per-kind checks are meaningless without a legal kind
	}
	if !validStatus[r.Status] {
		findings = append(findings, Finding{Record: id, Message: badStatusDiag(id, r.Status)})
	}

	// Gate: status transition / supersession referential integrity.
	findings = append(findings, validateSupersession(r, id, known)...)

	// Required fields only enforced on active law (proposed records may be incomplete).
	if r.Status == "active" {
		findings = append(findings, validateRequiredFields(r, id)...)
	}

	// Gate: source path existence (path-like entries only).
	for _, src := range r.Source {
		if strings.TrimSpace(src) == "" {
			findings = append(findings, Finding{Record: id, Message: emptyEntryDiag(id, "source")})
			continue
		}
		if isPathLike(root, src) && !existsUnderRoot(root, src) {
			findings = append(findings, Finding{Record: id, Message: missingPathDiag(id, "source", src)})
		}
	}

	// Gate: required_tests — Go test identifiers cross-checked, paths existence-checked,
	// command/prose entries asserted present only (never grepped, never executed).
	findings = append(findings, validateRequiredTests(root, r, id)...)

	return findings
}

func validateSupersession(r Record, id string, known map[string]bool) []Finding {
	var findings []Finding
	for _, sup := range r.Supersedes {
		if !known[sup] {
			findings = append(findings, Finding{Record: id, Message: danglingSupersedeDiag(id, "supersedes", sup)})
		}
	}
	if r.Status == "superseded" {
		if r.SupersededBy == nil || strings.TrimSpace(*r.SupersededBy) == "" {
			findings = append(findings, Finding{Record: id, Message: supersededMissingByDiag(id)})
		} else if !known[*r.SupersededBy] {
			findings = append(findings, Finding{Record: id, Message: danglingSupersedeDiag(id, "superseded_by", *r.SupersededBy)})
		}
	} else if r.SupersededBy != nil && strings.TrimSpace(*r.SupersededBy) != "" {
		findings = append(findings, Finding{Record: id, Message: nonSupersededHasByDiag(id, r.Status, *r.SupersededBy)})
	}
	return findings
}

func validateRequiredFields(r Record, id string) []Finding {
	var findings []Finding
	req := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			findings = append(findings, Finding{Record: id, Message: missingFieldDiag(id, field, requiredFieldReason(r.Kind, field))})
		}
	}
	reqList := func(field string, value []string) {
		if len(value) == 0 {
			findings = append(findings, Finding{Record: id, Message: missingFieldDiag(id, field, requiredFieldReason(r.Kind, field))})
		}
	}

	// Common to all active records.
	req("owner_project", r.OwnerProject)
	reqList("source", r.Source)

	switch r.Kind {
	case "invariant":
		req("scope", r.Scope)
		req("predicate", r.Predicate)
		if r.Authority == nil || strings.TrimSpace(r.Authority.Repo) == "" {
			findings = append(findings, Finding{Record: id, Message: missingFieldDiag(id, "authority.repo", requiredFieldReason(r.Kind, "authority"))})
		}
		reqList("required_tests", r.RequiredTests)
		if r.LastVerified == nil || strings.TrimSpace(r.LastVerified.Date) == "" {
			findings = append(findings, Finding{Record: id, Message: missingFieldDiag(id, "last_verified.date", requiredFieldReason(r.Kind, "last_verified"))})
		}
	case "contract":
		req("scope", r.Scope)
		req("predicate", r.Predicate)
		if r.Authority == nil || strings.TrimSpace(r.Authority.Repo) == "" {
			findings = append(findings, Finding{Record: id, Message: missingFieldDiag(id, "authority.repo", requiredFieldReason(r.Kind, "authority"))})
		}
		reqList("consumers", r.Consumers)
		reqList("required_tests", r.RequiredTests)
		if r.LastVerified == nil || strings.TrimSpace(r.LastVerified.Date) == "" {
			findings = append(findings, Finding{Record: id, Message: missingFieldDiag(id, "last_verified.date", requiredFieldReason(r.Kind, "last_verified"))})
		}
	case "accepted_risk":
		req("accepted_by", r.AcceptedBy)
		req("date", r.Date)
		req("severity", r.Severity)
		req("blast_radius", r.BlastRadius)
		req("mitigation", r.Mitigation)
		reqList("review_trigger", r.ReviewTrigger)
	}
	return findings
}

func validateRequiredTests(root string, r Record, id string) []Finding {
	var findings []Finding

	// _test.go files declared in source: are the search corpus for the func cross-check.
	var testFiles []string
	for _, src := range r.Source {
		if strings.HasSuffix(src, "_test.go") && isPathLike(root, src) {
			testFiles = append(testFiles, src)
		}
	}

	for _, entry := range r.RequiredTests {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			findings = append(findings, Finding{Record: id, Message: emptyEntryDiag(id, "required_tests")})
			continue
		}
		switch {
		case testIdentRE.MatchString(entry):
			// Go test identifier → must exist as a func in a source _test.go file.
			if !testFuncFound(root, testFiles, entry) {
				findings = append(findings, Finding{Record: id, Message: missingTestFuncDiag(id, entry, testFiles)})
			}
		case isPathLike(root, entry):
			if !existsUnderRoot(root, entry) {
				findings = append(findings, Finding{Record: id, Message: missingPathDiag(id, "required_tests", entry)})
			}
		default:
			// Command/prose entry (e.g. "just verify exits 0"): documentation only.
			// Structurally present — never grepped, never executed (no live e2e in the gate).
		}
	}
	return findings
}

func testFuncFound(root string, testFiles []string, name string) bool {
	re := regexp.MustCompile(`func\s+` + regexp.QuoteMeta(name) + `\s*\(`)
	for _, rel := range testFiles {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		if re.Match(data) {
			return true
		}
	}
	return false
}

// isPathLike reports whether tok should be treated as a repo path (and thus
// existence-checked) rather than a provenance/prose token (e.g. "hrcchat#9647",
// "just verify exits 0"). A token with a known source extension under a directory,
// or one that already resolves on disk (catches bare "justfile"), is path-like.
func isPathLike(root, tok string) bool {
	tok = strings.TrimSpace(tok)
	if tok == "" || strings.ContainsAny(tok, " \t(") {
		return false
	}
	if strings.Contains(tok, "/") && knownSourceExt[strings.ToLower(filepath.Ext(tok))] {
		return true
	}
	return existsUnderRoot(root, tok)
}

func existsUnderRoot(root, ref string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(ref)))
	return err == nil
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func recordDate(r Record) string {
	if r.LastVerified != nil && strings.TrimSpace(r.LastVerified.Date) != "" {
		return r.LastVerified.Date
	}
	return r.Date
}

func diffProjections(root string, projections map[string]string) []Finding {
	var findings []Finding
	rels := make([]string, 0, len(projections))
	for rel := range projections {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		want := projections[rel]
		path := filepath.Join(root, filepath.FromSlash(rel))
		got, err := os.ReadFile(path)
		if err != nil {
			findings = append(findings, Finding{Record: rel, Message: staleProjectionDiag(rel, "missing")})
			continue
		}
		if string(got) != want {
			findings = append(findings, Finding{Record: rel, Message: staleProjectionDiag(rel, "out of date")})
		}
	}
	return findings
}

func requiredFieldReason(kind, field string) string {
	switch field {
	case "authority":
		return "active law must name the repo/surface that owns its predicate"
	case "required_tests":
		return "active law must name the test(s)/evidence that keep it honest (fail-when-stale carrier)"
	case "last_verified":
		return "active law must record the concrete date its evidence was last confirmed"
	case "source":
		return "active law must point at the live source/guard it is derived from"
	case "review_trigger":
		return "an accepted risk must name the conditions that force a re-review"
	default:
		return fmt.Sprintf("active %s records require %s", kind, field)
	}
}

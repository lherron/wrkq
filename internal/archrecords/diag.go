package archrecords

import (
	"fmt"
	"sort"
	"strings"
)

// Teaching diagnostics (S4 bar): each states the expected shape, what was got, the
// blessed FIX, the WHY, and (where relevant) the lifecycle/exception channel —
// matching cmd/layer-boundary's expected/WHY/DO-NOT house style.

func schemaParseDiag(file string, err error) string {
	return fmt.Sprintf(`✗ ARCH-RECORD schema invalid: %s
expected: a record parseable into the %s schema (kind ∈ {invariant, contract, accepted_risk}, only known fields)
got: %v
FIX → Correct the YAML to the schema for its kind; remove unknown/misspelled fields.
WHY → architecture/ is canonical durable law; an unparseable record cannot gate anything and silently rots.
DO NOT add fields the checker does not model to smuggle in unvalidated semantics.`, file, archDir, err)
}

func duplicateIDDiag(id string, files []string) string {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	return fmt.Sprintf(`✗ ARCH-RECORD duplicate id: %q
expected: every record id is unique across architecture/
got: id %q declared in %s
FIX → Rename one record id, or merge the duplicates into a single record.
WHY → ids are the retrieval/consultation handle (rulings cite the id); a collision makes the cited law ambiguous.`, id, id, strings.Join(sorted, ", "))
}

func badKindDiag(id, kind string) string {
	return fmt.Sprintf(`✗ ARCH-RECORD illegal kind: %s
expected: kind ∈ {invariant, contract, accepted_risk}
got: kind=%q
FIX → Set kind to one of the three legal values; file invariants under records/invariants, risks under records/risks, contracts under contracts/.
WHY → per-kind required-field validation cannot run without a legal kind.`, id, kind)
}

func badStatusDiag(id, status string) string {
	return fmt.Sprintf(`✗ ARCH-RECORD illegal status: %s
expected: status ∈ {proposed, active, superseded, retired, rejected}
got: status=%q
FIX → Use a legal status; the lifecycle is proposed → active → superseded/retired/rejected.
WHY → only active records are normative law and projected; an illegal status leaves authority undefined.`, id, status)
}

func supersededMissingByDiag(id string) string {
	return fmt.Sprintf(`✗ ARCH-RECORD superseded without successor: %s
expected: a superseded record names its replacement in superseded_by
got: status=superseded but superseded_by is empty/null
FIX → Set superseded_by to the id of the record that replaces this one.
WHY → a dangling supersession strands readers on dead law with no forward pointer.`, id)
}

func nonSupersededHasByDiag(id, status, by string) string {
	return fmt.Sprintf(`✗ ARCH-RECORD spurious superseded_by: %s
expected: superseded_by is null unless status=superseded
got: status=%q with superseded_by=%q
FIX → Clear superseded_by (set null), or set status to superseded if it really was replaced.
WHY → superseded_by on live law falsely signals the record is dead.`, id, status, by)
}

func danglingSupersedeDiag(id, field, target string) string {
	return fmt.Sprintf(`✗ ARCH-RECORD dangling %s reference: %s
expected: %s entries reference an existing record id
got: %q does not match any record id
FIX → Point %s at a real record id, or remove the stale entry.
WHY → supersession links are the provenance chain; a dangling link breaks lineage tracking.`, field, id, field, target, field)
}

func missingFieldDiag(id, field, reason string) string {
	return fmt.Sprintf(`✗ ARCH-RECORD missing required field: %s
expected: active records carry %s
got: %s is empty/absent
FIX → Add %s to the record.
WHY → %s.`, id, field, field, field, reason)
}

func missingPathDiag(id, field, path string) string {
	return fmt.Sprintf(`✗ ARCH-RECORD unreachable %s path: %s
expected: %s paths resolve to an existing file/dir under --root
got: %q does not exist
FIX → Update the path to the live source, or remove it if the law no longer derives from it.
WHY → records must point at live source; a dead path is the lie-when-stale failure DAP exists to kill.
DO NOT delete the reference to silence this — fix the path or reopen the record.`, field, id, field, path)
}

func missingTestFuncDiag(id, name string, testFiles []string) string {
	corpus := "the _test.go file(s) listed in source:"
	if len(testFiles) > 0 {
		corpus = strings.Join(testFiles, ", ")
	}
	return fmt.Sprintf(`✗ ARCH-RECORD required test not found: %s
expected: required_tests entry %q exists as func %s(... in %s
got: no matching test function found
FIX → Restore/rename the test, or update required_tests + source to the test that now guards this law.
WHY → required_tests is a fail-when-stale carrier: renaming/deleting a guarding test must turn this gate red, not pass silently.
NOTE → command/prose entries (e.g. "just verify exits 0") are documentation and are NOT cross-checked here; only Go test identifiers (^Test…) are.`, id, name, name, corpus)
}

func emptyEntryDiag(id, field string) string {
	return fmt.Sprintf(`✗ ARCH-RECORD empty %s entry: %s
expected: every %s list entry is a non-empty path, test identifier, or evidence string
got: an empty entry
FIX → Remove the empty list item or fill it in.
WHY → empty evidence entries are silent gaps in the law's provenance.`, field, id, field)
}

func staleProjectionDiag(rel, state string) string {
	return fmt.Sprintf(`✗ ARCH-RECORD projection %s: %s
expected: %s is a clean generated projection of the active records
got: it is %s relative to the records
FIX → Run 'just architecture-records --write' to regenerate it, then commit the result.
WHY → projections are generated-and-diffed (§10-D): a hand-edited or stale projection is exactly the lie-when-stale surface DAP kills.
DO NOT hand-edit the projection — edit the YAML records and regenerate.`, state, rel, rel, state)
}

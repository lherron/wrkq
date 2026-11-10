package scope

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

// The shared scope grammar fixture (agent-spaces contracts/agent-scope/
// conformance/scope-cases.json), copied here by `just sync-scope-fixture`.
const (
	fixtureCopyPath      = "testdata/scope-cases.json"
	fixtureCanonicalPath = "../../../agent-spaces/contracts/agent-scope/conformance/scope-cases.json"
)

// declaredOps is wrkq's declared operation list under the scope and env
// contract: every grammar operation this package has code for. The package doc
// states the same list.
var declaredOps = map[string]bool{
	"scopeRef.parse":      true,
	"scopeRef.format":     true,
	"scopeHandle.parse":   true,
	"scopeHandle.format":  true,
	"laneRef.normalize":   true,
	"sessionHandle.parse": true,
	"sessionRef.parse":    true,
	"input.resolve":       true,
}

// undeclaredOps are fixture operations wrkq has no code for: it formats no
// session ref, and it has no lenient identity projection. An operation the
// fixture names that is in neither list fails the test.
var undeclaredOps = map[string]bool{
	"sessionRef.format": true,
	"identity.project":  true,
}

type scopeCase struct {
	ID     string          `json:"id"`
	Op     string          `json:"op"`
	In     json.RawMessage `json:"in"`
	Opts   json.RawMessage `json:"opts"`
	Ok     json.RawMessage `json:"ok"`
	Refuse string          `json:"refuse"`
}

type caseOpts struct {
	ProjectID       *string `json:"projectId"`
	TaskID          *string `json:"taskId"`
	DefaultTaskID   *string `json:"defaultTaskId"`
	DefaultRoleName *string `json:"defaultRoleName"`
	EnvDefaultTask  *string `json:"envDefaultTask"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// runCase evaluates one case and returns its result in the fixture's JSON shape.
func runCase(c scopeCase) (any, error) {
	var in string
	switch c.Op {
	case "scopeRef.format", "laneRef.normalize":
	default:
		if err := json.Unmarshal(c.In, &in); err != nil {
			return nil, fmt.Errorf("decode in: %w", err)
		}
	}
	switch c.Op {
	case "scopeRef.parse":
		p, err := ParseScopeRef(in)
		if err != nil {
			return nil, err
		}
		return partsOf(p), nil
	case "scopeRef.format":
		var parts struct{ AgentID, ProjectID, TaskID, RoleName string }
		if err := json.Unmarshal(c.In, &parts); err != nil {
			return nil, fmt.Errorf("decode in: %w", err)
		}
		return FormatScopeRef(ParsedScopeRef{AgentID: parts.AgentID, ProjectID: parts.ProjectID, TaskID: parts.TaskID, RoleName: parts.RoleName}), nil
	case "scopeHandle.parse":
		p, err := ParseScopeHandle(in)
		if err != nil {
			return nil, err
		}
		return p.ScopeRef, nil
	case "scopeHandle.format":
		p, err := ParseScopeRef(in)
		if err != nil {
			return nil, err
		}
		return FormatScopeHandle(p), nil
	case "laneRef.normalize":
		var lane *string
		if err := json.Unmarshal(c.In, &lane); err != nil {
			return nil, fmt.Errorf("decode in: %w", err)
		}
		return NormalizeLaneRef(lane)
	case "sessionHandle.parse":
		s, err := ParseSessionHandle(in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"scopeRef": s.ScopeRef, "laneRef": s.LaneRef}, nil
	case "sessionRef.parse":
		s, err := ParseSessionRef(in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"scopeRef": s.ScopeRef, "laneRef": s.LaneRef}, nil
	case "input.resolve":
		var o caseOpts
		if len(c.Opts) > 0 {
			if err := json.Unmarshal(c.Opts, &o); err != nil {
				return nil, fmt.Errorf("decode opts: %w", err)
			}
		}
		r, err := ResolveQualifiedInput(in, InputOptions{
			ProjectID:       deref(o.ProjectID),
			TaskID:          deref(o.TaskID),
			DefaultTaskID:   deref(o.DefaultTaskID),
			DefaultRoleName: deref(o.DefaultRoleName),
			EnvDefaultTask:  deref(o.EnvDefaultTask),
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"scopeRef": r.ScopeRef, "laneRef": r.LaneRef}, nil
	}
	return nil, fmt.Errorf("no runner for op %q", c.Op)
}

func partsOf(p ParsedScopeRef) map[string]any {
	out := map[string]any{"kind": string(p.Kind), "agentId": p.AgentID}
	for k, v := range map[string]string{"projectId": p.ProjectID, "taskId": p.TaskID, "roleName": p.RoleName} {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// sameJSON compares a Go result with the fixture's expected JSON.
func sameJSON(got any, want json.RawMessage) (bool, string) {
	raw, err := json.Marshal(got)
	if err != nil {
		return false, err.Error()
	}
	var g, w any
	_ = json.Unmarshal(raw, &g)
	if err := json.Unmarshal(want, &w); err != nil {
		return false, err.Error()
	}
	return reflect.DeepEqual(g, w), string(raw)
}

func loadFixture(t *testing.T) []scopeCase {
	t.Helper()
	data, err := os.ReadFile(fixtureCopyPath)
	if err != nil {
		t.Fatalf("read fixture copy: %v (run `just sync-scope-fixture`)", err)
	}
	var doc struct {
		Version int         `json:"version"`
		Cases   []scopeCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode fixture copy: %v", err)
	}
	if doc.Version != 1 {
		t.Fatalf("fixture version %d, this runner reads version 1", doc.Version)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("fixture copy has no cases")
	}
	return doc.Cases
}

// TestScopeGrammarConformance runs every fixture case for wrkq's declared
// operations and compares the exact result or refusal code.
func TestScopeGrammarConformance(t *testing.T) {
	for _, c := range loadFixture(t) {
		c := c
		if !declaredOps[c.Op] {
			if !undeclaredOps[c.Op] {
				t.Errorf("%s: fixture operation %q is neither declared nor listed as undeclared", c.ID, c.Op)
			}
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			got, err := runCase(c)
			if c.Refuse != "" {
				if err == nil {
					t.Fatalf("%s %s %s: accepted as %v, want refusal %s", c.ID, c.Op, c.In, got, c.Refuse)
				}
				if code := CodeOf(err); string(code) != c.Refuse {
					t.Fatalf("%s %s %s: refused with code %q (%v), want %s", c.ID, c.Op, c.In, code, err, c.Refuse)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s %s %s: refused (%v), want %s", c.ID, c.Op, c.In, err, c.Ok)
			}
			if ok, gotJSON := sameJSON(got, c.Ok); !ok {
				t.Fatalf("%s %s %s: got %s, want %s", c.ID, c.Op, c.In, gotJSON, c.Ok)
			}
		})
	}
}

// TestScopeFixtureCopyIsCurrent fails when the copy differs from the canonical
// fixture in the sibling agent-spaces checkout. Without that checkout it skips.
func TestScopeFixtureCopyIsCurrent(t *testing.T) {
	canonical, err := os.ReadFile(fixtureCanonicalPath)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("no sibling agent-spaces checkout at %s", fixtureCanonicalPath)
	}
	if err != nil {
		t.Fatalf("read canonical fixture: %v", err)
	}
	copied, err := os.ReadFile(fixtureCopyPath)
	if err != nil {
		t.Fatalf("read fixture copy: %v", err)
	}
	if !bytes.Equal(copied, canonical) {
		t.Fatalf("%s differs from %s; run `just sync-scope-fixture`", fixtureCopyPath, fixtureCanonicalPath)
	}
}

package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseAndValidateEvidenceFacts(t *testing.T) {
	spec := &KindSpec{Facts: &FactsContract{
		Required: []string{"verdict", "count"},
		Properties: map[string]FactProperty{
			"verdict": {Type: "string", Enum: []json.RawMessage{raw(`"ready"`), raw(`"needs_patch"`)}},
			"count":   {Type: "integer"},
			"tags":    {Type: "array", ItemsType: "string", MaxItems: 2},
		},
	}}

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "valid", input: `{"verdict":"ready","count":1,"tags":["a","b"]}`},
		{name: "missing required", input: `{"verdict":"ready"}`, want: "missing required fact count"},
		{name: "not object", input: `[]`, want: "facts must be a JSON object"},
		{name: "nested object", input: `{"verdict":"ready","count":1,"extra":{"x":1}}`, want: "nested objects are not supported"},
		{name: "array object", input: `{"verdict":"ready","count":1,"tags":[{"x":1}]}`, want: "arrays may contain scalar values only"},
		{name: "invalid enum", input: `{"verdict":"bad","count":1}`, want: "must be one of"},
		{name: "invalid integer", input: `{"verdict":"ready","count":1.2}`, want: "must be integer"},
		{name: "too many items", input: `{"verdict":"ready","count":1,"tags":["a","b","c"]}`, want: "at most 2 items"},
		{name: "unknown flat accepted", input: `{"verdict":"ready","count":1,"extra":true}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseAndValidateEvidenceFacts("implementation", tc.input, spec)
			if tc.want == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.want != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tc.want)
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
				}
			}
		})
	}
}

func TestValidateFactsContractsRejectsBadSelectors(t *testing.T) {
	tpl := &Template{
		EvidenceKinds: map[string]KindSpec{
			"pressure_pass": {Facts: &FactsContract{
				Required:   []string{"verdict"},
				Properties: map[string]FactProperty{"verdict": {Type: "string"}},
			}},
		},
		Transitions: []TransitionSpec{{
			ID: "finalize",
			Requires: []RequirementSpec{{
				Evidence: &EvidenceRequirementSpec{Kind: "pressure_pass", Facts: map[string]json.RawMessage{"unknown": raw(`true`)}},
			}},
		}},
	}
	errs := validateFactsContracts(tpl)
	if len(errs) == 0 {
		t.Fatalf("expected selector validation error")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "does not declare that fact") {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestLatestEvidenceMatcher(t *testing.T) {
	ev := []Evidence{
		{ID: "ev_000001", Kind: "pressure_pass", Facts: raw(`{"verdict":"ready"}`)},
		{ID: "ev_000002", Kind: "pressure_pass", Facts: raw(`{"verdict":"needs_patch"}`)},
	}
	req := EvidenceRequirementSpec{Kind: "pressure_pass", Facts: map[string]json.RawMessage{"verdict": raw(`"ready"`)}}
	match := matchEvidenceRequirement(ev, req)
	if match.OK {
		t.Fatalf("older ready evidence must not satisfy newer needs_patch decision")
	}
	if !strings.Contains(match.Detail, "needs_patch") || !strings.Contains(match.Detail, "ev_000002") {
		t.Fatalf("unexpected mismatch detail: %s", match.Detail)
	}

	ev = append(ev, Evidence{ID: "ev_000003", Kind: "pressure_pass", Facts: raw(`{"verdict":"ready"}`)})
	match = matchEvidenceRequirement(ev, req)
	if !match.OK {
		t.Fatalf("newest ready evidence should satisfy requirement: %s", match.Detail)
	}
}

func raw(s string) json.RawMessage {
	return json.RawMessage(s)
}

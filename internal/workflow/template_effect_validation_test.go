//go:build wrkq_local

package workflow

import (
	"strings"
	"testing"
)

func TestTemplateValidationRejectsInvalidEffects(t *testing.T) {
	for _, suspension := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			effect EffectSpec
			want   string
		}{
			{"unknown placeholder", EffectSpec{Kind: "notify", Data: map[string]interface{}{"nested": []interface{}{map[string]interface{}{"message": "{unknownToken}"}}}}, "{unknownToken}"},
			{"invalid task state", EffectSpec{Kind: "set_task_state", Data: map[string]interface{}{"state": "bogus"}}, "bogus"},
		} {
			t.Run(tc.name+map[bool]string{true: " suspension", false: " transition"}[suspension], func(t *testing.T) {
				data, err := builtinTemplateData(BuiltinSimpleTaskV5TemplateRef)
				if err != nil {
					t.Fatal(err)
				}
				tpl, canonical, err := ParseTemplate(data)
				if err != nil {
					t.Fatal(err)
				}
				if suspension {
					tpl.Suspension.Effects["close"] = []EffectSpec{tc.effect}
				} else {
					tpl.Transitions[0].Outcomes[0].Effects = []EffectSpec{tc.effect}
				}
				errs := ValidateTemplate(tpl, canonical, nil)
				if !strings.Contains(strings.Join(errs, "\n"), tc.want) {
					t.Fatalf("validation errors = %v; want rejection naming %s", errs, tc.want)
				}
			})
		}
	}
}

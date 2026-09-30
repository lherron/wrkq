//go:build wrkq_local

package workflow

import (
	"reflect"
	"testing"
)

func TestObligationTaskMentionsUseSharedGrammar(t *testing.T) {
	for _, tt := range []struct {
		text string
		want []string
	}{
		{"see T-12345.render-preview.", []string{"T-12345.render-preview"}},
		{"see T-12345.Next", []string{"T-12345"}},
		{"see T-12345.", []string{"T-12345"}},
		{"T-not-a-task", []string{}},
	} {
		if got := extractTaskIDsFromText(tt.text); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("mentions = %v, want %v", got, tt.want)
		}
	}
}

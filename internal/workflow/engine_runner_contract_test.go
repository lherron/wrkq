package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestEngineRunnerContractDirectLandOperatorRequired(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file")
	}
	fixturePath := filepath.Join(filepath.Dir(testFile), "..", "..", "architecture", "contracts", "wrkf-engine-runner-contract.v1.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read canonical engine-runner contract: %v", err)
	}

	var contract struct {
		Fixtures []struct {
			ID     string         `json:"id"`
			Engine map[string]any `json:"engine"`
			Runner map[string]any `json:"runner"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("decode canonical engine-runner contract: %v", err)
	}

	for _, fixture := range contract.Fixtures {
		if fixture.ID != "simple-task-v4.direct-land-complete-outcomes" {
			continue
		}
		wantOutcomes := []any{
			map[string]any{
				"id": "landed",
				"evidence": map[string]any{
					"kind":  "landing_result",
					"facts": map[string]any{"result": "landed", "workflow.lane": "leaf"},
				},
				"to":        map[string]any{"status": "closed", "phase": "done"},
				"taskState": "completed",
			},
			map[string]any{
				"id": "push_rejected",
				"evidence": map[string]any{
					"kind":  "landing_result",
					"facts": map[string]any{"result": "push_rejected", "workflow.lane": "leaf"},
				},
				"to": map[string]any{"status": "active", "phase": "gate"},
			},
			map[string]any{
				"id": "operator_required",
				"evidence": map[string]any{
					"kind":  "landing_result",
					"facts": map[string]any{"result": "operator_required", "workflow.lane": "leaf"},
				},
				"to":        map[string]any{"status": "waiting", "phase": "operator_required"},
				"taskState": "blocked",
			},
		}
		if got := fixture.Engine["outcomes"]; !reflect.DeepEqual(got, wantOutcomes) {
			t.Errorf("direct_land_complete outcomes = %#v, want %#v", got, wantOutcomes)
		}
		wantOperatorRequired := map[string]any{
			"workflowState":        map[string]any{"status": "waiting", "phase": "operator_required"},
			"taskState":            "blocked",
			"retriesAutomatically": false,
		}
		if got := fixture.Runner["operatorRequired"]; !reflect.DeepEqual(got, wantOperatorRequired) {
			t.Errorf("operator_required runner projection = %#v, want %#v", got, wantOperatorRequired)
		}
		return
	}
	t.Fatal("canonical engine-runner contract is missing direct_land_complete outcomes")
}

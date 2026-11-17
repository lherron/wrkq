package rpccli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvelopeFailureDetailSurvivesWireAndHumanLabel(t *testing.T) {
	var envelope envelopeWire
	if err := json.Unmarshal([]byte(`{"state":"failed","failureReason":"unconfirmed","failureDetail":"its runtime ended first","dischargedBy":"EN-00009"}`), &envelope); err != nil {
		t.Fatal(err)
	}
	if got := envelopeStateLabel(envelope); !strings.Contains(got, "its runtime ended first") {
		t.Fatalf("failure line = %q", got)
	}
	b, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["failureDetail"] != "its runtime ended first" || fields["dischargedBy"] != "EN-00009" {
		t.Fatalf("wire lost terminal fields: %s", b)
	}
}

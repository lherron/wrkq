package wrkfcli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	workrpcclient "github.com/lherron/wrkq/internal/workrpc/client"
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

type transitionCall struct {
	method string
	params json.RawMessage
}

type transitionRecordingTransport struct {
	calls     []transitionCall
	responses map[string]json.RawMessage
	errors    map[string]error
}

var _ workrpcclient.Transport = (*transitionRecordingTransport)(nil)

func (t *transitionRecordingTransport) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	t.calls = append(t.calls, transitionCall{method: method, params: raw})
	if callErr := t.errors[method]; callErr != nil {
		return nil, callErr
	}
	return t.responses[method], nil
}

func (*transitionRecordingTransport) Close() error { return nil }

func TestApplyTransitionRunChecksPersistsThenAppliesIDs(t *testing.T) {
	transport := &transitionRecordingTransport{responses: map[string]json.RawMessage{
		"wrkf.check.run":        json.RawMessage(`{"runs":[{"id":"chk_1"},{"id":"chk_2"}]}`),
		"wrkf.transition.apply": json.RawMessage(`{"task":"T-00001","instanceId":"wfi_1","state":{"status":"closed"},"revision":1,"eventId":"wfe_1"}`),
	}}
	cmd := &cobra.Command{}
	a := &app{transport: transport}
	out, err := applyTransition(cmd, a, wrkfapi.TransitionApplyParams{
		TaskSelector: "T-00001", Transition: "complete", PrincipalRef: "agent:cody", Role: "implementer", CheckIDs: []string{"chk_explicit"},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.EventID != "wfe_1" {
		t.Fatalf("result = %#v", out)
	}
	if len(transport.calls) != 2 || transport.calls[0].method != "wrkf.check.run" || transport.calls[1].method != "wrkf.transition.apply" {
		t.Fatalf("call order = %#v", transport.calls)
	}
	var checkParams wrkfapi.CheckRunParams
	if err := json.Unmarshal(transport.calls[0].params, &checkParams); err != nil {
		t.Fatal(err)
	}
	if checkParams.TaskSelector != "T-00001" || checkParams.Transition != "complete" || checkParams.PrincipalRef != "agent:cody" || checkParams.Role != "implementer" {
		t.Fatalf("check params = %#v", checkParams)
	}
	var applyParams wrkfapi.TransitionApplyParams
	if err := json.Unmarshal(transport.calls[1].params, &applyParams); err != nil {
		t.Fatal(err)
	}
	if applyParams.RunChecks {
		t.Fatal("transition.apply retained runChecks=true")
	}
	want := []string{"chk_explicit", "chk_1", "chk_2"}
	if len(applyParams.CheckIDs) != len(want) {
		t.Fatalf("check ids = %#v", applyParams.CheckIDs)
	}
	for i := range want {
		if applyParams.CheckIDs[i] != want[i] {
			t.Fatalf("check ids = %#v, want %#v", applyParams.CheckIDs, want)
		}
	}
}

func TestApplyTransitionStopsWhenCheckRunFails(t *testing.T) {
	wantErr := errors.New("hook refused")
	transport := &transitionRecordingTransport{
		responses: map[string]json.RawMessage{},
		errors:    map[string]error{"wrkf.check.run": wantErr},
	}
	_, err := applyTransition(&cobra.Command{}, &app{transport: transport}, wrkfapi.TransitionApplyParams{
		TaskSelector: "T-00001", Transition: "complete",
	}, true)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v", err)
	}
	if len(transport.calls) != 1 || transport.calls[0].method != "wrkf.check.run" {
		t.Fatalf("calls = %#v", transport.calls)
	}
}

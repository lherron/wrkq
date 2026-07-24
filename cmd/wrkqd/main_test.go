package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/wrkqd"
)

func TestPrintVersionJSONReportsBuildAndProtocolRevision(t *testing.T) {
	oldVersion, oldCommit, oldDate := wrkqd.Version, wrkqd.GitCommit, wrkqd.BuildDate
	t.Cleanup(func() {
		wrkqd.Version, wrkqd.GitCommit, wrkqd.BuildDate = oldVersion, oldCommit, oldDate
	})
	wrkqd.Version = "v1.2.3-dirty"
	wrkqd.GitCommit = "0123456789abcdef"
	wrkqd.BuildDate = "2026-07-23T12:00:00Z"

	var out, stderr bytes.Buffer
	if err := printVersion([]string{"--json"}, &out, &stderr); err != nil {
		t.Fatalf("printVersion: %v; stderr=%s", err, stderr.String())
	}
	var payload struct {
		Version            string `json:"version"`
		BuildRevision      string `json:"build_revision"`
		BuildDate          string `json:"build_date"`
		ProtocolVersion    string `json:"protocol_version"`
		ProtocolSchemaHash string `json:"protocol_schema_hash"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if payload.Version != "v1.2.3-dirty" || payload.BuildRevision != "0123456789abcdef" {
		t.Fatalf("build metadata=%+v", payload)
	}
	if payload.BuildDate != wrkqd.BuildDate ||
		payload.ProtocolVersion != workrpc.ProtocolVersion ||
		payload.ProtocolSchemaHash != workrpc.ProtocolSchemaHash() {
		t.Fatalf("version payload=%+v", payload)
	}
}

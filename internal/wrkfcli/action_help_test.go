package wrkfcli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

func TestActionResponseShapesAreDocumentedInHelp(t *testing.T) {
	tests := []struct {
		name string
		cmd  *cobra.Command
		want []string
	}{
		{
			name: "claim",
			cmd:  actionClaimCmd(),
			want: []string{
				"binding.run.id",
				"binding.authority.ownerToken",
				"binding.authority.ownerGeneration",
				"error.message",
				"error.fix",
				"error.predecessor.runId",
			},
		},
		{
			name: "settle",
			cmd:  actionSettleCmd(),
			want: []string{
				"run.id",
				"evidence (optional)",
				"transition (optional)",
				"effects (optional)",
				"obligations (optional)",
			},
		},
		{
			name: "heartbeat",
			cmd:  actionHeartbeatCmd(),
			want: []string{
				"actionRunId",
				"runId",
				"leaseToken",
				"leaseExpiresAt",
				"heartbeatAt",
				"binding.authority.ownerToken",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			tt.cmd.SetOut(&output)
			tt.cmd.SetErr(&output)
			if err := tt.cmd.Help(); err != nil {
				t.Fatalf("render help: %v", err)
			}
			help := output.String()
			for _, want := range tt.want {
				if !strings.Contains(help, want) {
					t.Errorf("help missing %q:\n%s", want, help)
				}
			}
		})
	}
}

func TestActionStartHelpUsesProducerOwnedDefault(t *testing.T) {
	var output bytes.Buffer
	cmd := actionStartCmd()
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.Help(); err != nil {
		t.Fatalf("render help: %v", err)
	}
	help := output.String()
	if want := "defaults to built-in " + wrkfapi.DefaultActionWorkflowRef(); !strings.Contains(help, want) {
		t.Fatalf("help missing producer-owned default %q:\n%s", want, help)
	}
	if strings.Contains(help, "defaults to built-in wrkq-simple-task@1") {
		t.Fatalf("help still advertises discontinued default:\n%s", help)
	}
}

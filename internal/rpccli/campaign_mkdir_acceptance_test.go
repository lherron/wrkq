//go:build wrkq_local

package rpccli

// campaign_mkdir_acceptance_test.go — `wrkq mkdir -p` ensures a path exists,
// `wrkq mkdir --campaign` creates a campaign in one write, and enrollment into
// a container that cannot take members says which container and what to do.

import (
	"strings"
	"testing"
)

func TestMkdirParentsEnsuresPath(t *testing.T) {
	t.Setenv("WRKQ_PROJECT_ROOT", "")
	t.Setenv("ASP_PROJECT", "")
	f := newCampaignCLIFixture(t)
	countPath := func(slug string) int64 {
		return f.count(t, "SELECT COUNT(*) FROM containers WHERE slug = ?", slug)
	}

	f.mustRun(t, "mkdir", "-p", "campaign-cli-a/deep/er/est")
	for _, slug := range []string{"deep", "er", "est"} {
		if n := countPath(slug); n != 1 {
			t.Fatalf("container %s count = %d, want 1", slug, n)
		}
	}

	out := f.mustRun(t, "mkdir", "-p", "campaign-cli-a/deep/er/est")
	if !strings.Contains(out, `"created": false`) {
		t.Fatalf("rerun output = %q, want created false", out)
	}
	if n := countPath("est"); n != 1 {
		t.Fatalf("rerun created a duplicate: count = %d", n)
	}

	if out, err := runCampaignCLI(t, f.dbPath, "mkdir", "campaign-cli-a/deep"); err == nil {
		t.Fatalf("mkdir without -p on an existing container succeeded: %s", out)
	}

	// A one-character slug is refused as invalid input, not as a retryable
	// "already exists" conflict.
	out, err := runCampaignCLI(t, f.dbPath, "mkdir", "campaign-cli-a/x")
	if err == nil || !strings.Contains(err.Error(), "at least 2 characters") || strings.Contains(err.Error()+out, "already exists") {
		t.Fatalf("one-character slug: err=%v out=%q", err, out)
	}

	if out, err := runCampaignCLI(t, f.dbPath, "mkdir", "-p", "no-such-project/child"); err == nil {
		t.Fatalf("mkdir -p invented a top-level project: %s", out)
	}
	if n := countPath("no-such-project"); n != 0 {
		t.Fatalf("mkdir -p created a project: count = %d", n)
	}
}

func TestMkdirCampaignCreatesInOneWrite(t *testing.T) {
	t.Setenv("WRKQ_PROJECT_ROOT", "")
	t.Setenv("ASP_PROJECT", "")
	f := newCampaignCLIFixture(t)
	stateOf := func(slug string) string {
		database := f.openDB(t)
		defer func() { _ = database.Close() }()
		var state *string
		if err := database.QueryRow("SELECT campaign_state FROM containers WHERE slug = ?", slug).Scan(&state); err != nil {
			t.Fatalf("read %s state: %v", slug, err)
		}
		if state == nil {
			return "plain"
		}
		return *state
	}

	f.mustRun(t, "mkdir", "--campaign", "campaign-cli-a/launch")
	if got := stateOf("launch"); got != "active" {
		t.Fatalf("bare --campaign state = %s, want active", got)
	}
	f.mustRun(t, "mkdir", "--campaign=draft", "campaign-cli-a/plan")
	if got := stateOf("plan"); got != "draft" {
		t.Fatalf("--campaign=draft state = %s, want draft", got)
	}

	// The create and the conversion are one write: a refused conversion leaves
	// no plain container behind.
	if out, err := runCampaignCLI(t, f.dbPath, "mkdir", "--campaign", "campaign-cli-a/wave-a/nested"); err == nil {
		t.Fatalf("nested campaign create succeeded: %s", out)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM containers WHERE slug = 'nested'"); n != 0 {
		t.Fatalf("refused campaign create left %d container(s)", n)
	}

	// -p --campaign makes an existing plain container a campaign and leaves an
	// existing campaign alone, so a retry is always safe.
	f.mustRun(t, "mkdir", "campaign-cli-a/later")
	out := f.mustRun(t, "mkdir", "-p", "--campaign", "campaign-cli-a/later")
	if !strings.Contains(out, `"converted": true`) || stateOf("later") != "active" {
		t.Fatalf("-p --campaign on a plain container: %q state %s", out, stateOf("later"))
	}
	out = f.mustRun(t, "mkdir", "-p", "--campaign", "campaign-cli-a/later")
	if strings.Contains(out, `"converted"`) || !strings.Contains(out, `"created": false`) {
		t.Fatalf("-p --campaign on a campaign: %q", out)
	}

	if out, err := runCampaignCLI(t, f.dbPath, "mkdir", "--campaign=done", "campaign-cli-a/bad"); err == nil {
		t.Fatalf("--campaign=done accepted: %s", out)
	}
}

func TestEnrollmentRefusalNamesTargetAndRemedy(t *testing.T) {
	f := newCampaignCLIFixture(t)
	f.resetToPlainContainer(t)
	plainID := f.friendlyID(t, f.campaignAUUID)

	out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", plainID)
	if err == nil {
		t.Fatalf("enrollment into a plain container succeeded: %s", out)
	}
	for _, want := range []string{
		"campaign enrollment target must be a draft or active campaign",
		plainID + " is a plain container",
		"wrkq campaign convert " + plainID,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("plain refusal %q missing %q", out, want)
		}
	}

	f.exec(t, "UPDATE containers SET campaign_state = 'completed' WHERE uuid = ?", f.campaignAUUID)
	out, err = runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", plainID)
	if err == nil {
		t.Fatalf("enrollment into a completed campaign succeeded: %s", out)
	}
	if !strings.Contains(out, plainID+" is completed") {
		t.Fatalf("terminal refusal %q does not name the state", out)
	}
}

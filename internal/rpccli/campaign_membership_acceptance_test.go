//go:build wrkq_local

package rpccli

// campaign_membership_acceptance_test.go — how a task joins or leaves a
// campaign from the CLI: `set --campaign` enrollment and its exclusivity rule,
// move edges, the uniform campaign selector grammar (scoped first, absolute
// fallback), `touch --campaign` admission at create, and cat's membership line.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCampaignRPCEnrollmentAndMoveMatrix(t *testing.T) {
	t.Run("cross project enroll and unenroll", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		f.mustRun(t, "set", f.enrolledID, "--campaign", f.campaignAUUID)
		if got := f.campaignOf(t, f.enrolledUUID); !got.Valid || got.String != f.campaignAUUID {
			t.Fatalf("enrollment = %v, want %s", got, f.campaignAUUID)
		}
		f.mustRun(t, "set", f.enrolledID, "--campaign", "")
		if got := f.campaignOf(t, f.enrolledUUID); got.Valid {
			t.Fatalf("unenrollment retained campaign %s", got.String)
		}
	})

	t.Run("resident exclusivity rejection", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		out, err := runCampaignCLI(t, f.dbPath, "set", f.residentID, "--campaign", f.campaignBUUID)
		if err == nil || strings.Contains(strings.ToLower(err.Error()), "unknown flag") {
			t.Fatalf("foreign enrollment error = %v output=%q; want effective-membership rejection", err, out)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "campaign") {
			t.Fatalf("foreign enrollment error = %q; want campaign context", err)
		}
		if got := f.campaignOf(t, f.residentUUID); got.Valid {
			t.Fatalf("rejected foreign enrollment persisted %s", got.String)
		}
	})

	t.Run("different and same campaign move edges", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		f.enroll(t, f.campaignAUUID, f.enrolledUUID)

		out, err := runCampaignCLI(t, f.dbPath, "mv", f.enrolledID, f.campaignBUUID)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unenroll") {
			t.Fatalf("different-campaign move error = %v output=%q; want unenroll rejection", err, out)
		}
		f.mustRun(t, "mv", f.enrolledID, f.campaignAUUID)
		if got := f.campaignOf(t, f.enrolledUUID); got.Valid {
			t.Fatalf("same-campaign move retained redundant enrollment %s", got.String)
		}
	})
}

// TestCampaignSelectorGrammarIsUniform pins the finding from T-06823: a BARE
// campaign slug resolved for `campaign convert|close` (which scope the argument
// to the project root) but not for `set --campaign`, which shipped the raw token
// to the server and failed with "campaign not found". One selector grammar,
// every command.
func TestCampaignSelectorGrammarIsUniform(t *testing.T) {
	f := newCampaignCLIFixture(t)
	// The bare slug first, then the qualified forms, which must keep resolving
	// identically.
	for i, selector := range []string{"wave-a", "campaign-cli-a/wave-a", f.campaignAUUID} {
		if i > 0 {
			f.mustRun(t, "set", f.enrolledID, "--campaign", "")
		}
		f.mustRun(t, "--project", "campaign-cli-a", "set", f.enrolledID, "--campaign", selector)
		if got := f.campaignOf(t, f.enrolledUUID); !got.Valid || got.String != f.campaignAUUID {
			t.Fatalf("--campaign %q enrollment = %v, want %s", selector, got, f.campaignAUUID)
		}
	}
}

// useForeignRootB makes campaign-cli-b the caller's project root.
func useForeignRootB(t *testing.T) {
	t.Helper()
	t.Setenv("ASP_PROJECT", "")
	t.Setenv("WRKQ_PROJECT_ROOT", "campaign-cli-b")
}

// TestCampaignSelectorAbsoluteFallback pins the scoped-first / absolute-fallback
// container-selector rule (wrkq.project-root.caller-semantics, T-07701). From a
// FOREIGN project root a campaign was previously reachable only by its P- id:
// every path form was prefixed with the caller's root and missed.
func TestCampaignSelectorAbsoluteFallback(t *testing.T) {
	t.Run("absolute path resolves from a foreign root", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		useForeignRootB(t)
		f.mustRun(t, "set", f.enrolledID, "--campaign", "campaign-cli-a/wave-a")
		if got := f.campaignOf(t, f.enrolledUUID); !got.Valid || got.String != f.campaignAUUID {
			t.Fatalf("enrollment = %v, want %s", got, f.campaignAUUID)
		}
	})

	for _, tc := range []struct{ name, selector, wantScoped string }{
		{"scoped first: a missing path errors with the SCOPED path", "campaign-cli-b/nope", "campaign-cli-b/nope"},
		{"a bare slug is never re-pointed at another project", "wave-a", "campaign-cli-b/wave-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCampaignCLIFixture(t)
			useForeignRootB(t)
			_, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", tc.selector)
			if err == nil {
				t.Fatalf("--campaign %q unexpectedly resolved", tc.selector)
			}
			if !strings.Contains(err.Error(), tc.wantScoped) {
				t.Fatalf("error = %q; want it to name the scoped path %s", err, tc.wantScoped)
			}
			if got := f.campaignOf(t, f.enrolledUUID); got.Valid {
				t.Fatalf("--campaign %q enrolled the task in %s", tc.selector, got.String)
			}
		})
	}
}

// TestTouchCampaignEnrollsAtCreate pins `wrkq touch --campaign`: a cross-project
// slot is ONE command, and create is a full campaign admission path (T-07701).
func TestTouchCampaignEnrollsAtCreate(t *testing.T) {
	t.Run("enrolls a task resident in another project", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		useForeignRootB(t)
		out := f.mustRun(t, "touch", "slot", "--title", "Slot", "--campaign", "campaign-cli-a/wave-a", "--json")
		var created []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(out), &created); err != nil || len(created) != 1 {
			t.Fatalf("decode touch output %q: %v", out, err)
		}
		if created[0].Path != "campaign-cli-b/slot" {
			t.Fatalf("created path = %q; the task must keep its OWN project", created[0].Path)
		}
		if got := f.count(t, "SELECT COUNT(*) FROM tasks WHERE id = ? AND campaign_uuid = ?", created[0].ID, f.campaignAUUID); got != 1 {
			t.Fatalf("created task %s is not enrolled in %s", created[0].ID, f.campaignAUUID)
		}
	})

	t.Run("a terminal campaign rejects the create outright", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		useForeignRootB(t)
		f.mustRun(t, "campaign", "close", f.campaignAUUID, "--state", "cancelled")
		out, err := runCampaignCLI(t, f.dbPath, "touch", "late-slot", "--title", "Late", "--campaign", f.campaignAUUID, "--json")
		if err == nil {
			t.Fatalf("create into a terminal campaign succeeded: %s", out)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "draft or active") {
			t.Fatalf("error = %q; want the shared admission rejection", err)
		}
		if count := f.count(t, "SELECT COUNT(*) FROM tasks WHERE slug = 'late-slot'"); count != 0 {
			t.Fatalf("rejected create left %d task(s) behind; the insert must roll back", count)
		}
	})
}

// TestCatShowsEffectiveCampaignMembership pins the cat/show projection that made
// enrolment visible at all (T-07701): before it, a task could be enrolled and no
// reader of the task could tell.
func TestCatShowsEffectiveCampaignMembership(t *testing.T) {
	f := newCampaignCLIFixture(t)
	f.enroll(t, f.campaignAUUID, f.enrolledUUID)

	for _, tc := range []struct {
		name, taskID, wantPath, wantMembership string
	}{
		{"resident member", f.residentID, "campaign-cli-a/wave-a", "resident"},
		{"enrolled cross-project member", f.enrolledID, "campaign-cli-a/wave-a", "enrolled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := f.mustRun(t, "cat", tc.taskID, "--output", "raw")
			want := "campaign: " + f.friendlyID(t, f.campaignAUUID) + " " + tc.wantPath + " " + tc.wantMembership
			if !strings.Contains(out, want) {
				t.Fatalf("cat output missing %q:\n%s", want, out)
			}
		})
	}

	t.Run("a task in no campaign prints no campaign line", func(t *testing.T) {
		f2 := newCampaignCLIFixture(t)
		if out := f2.mustRun(t, "cat", f2.enrolledID, "--output", "raw"); strings.Contains(out, "campaign:") {
			t.Fatalf("non-member cat output carries a campaign line:\n%s", out)
		}
	})
}

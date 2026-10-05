//go:build wrkq_local

package rpccli

// campaign_tree_acceptance_test.go — how campaign membership reads: find
// --campaign marks resident vs enrolled; machine find/tree/ls stay
// residency-only unless --campaign-members opts in; the human tree overlays
// enrolled members at every campaign node, keeps an all-enrolled campaign
// through pruning, prunes one with no admitted member, and counts enrolled
// members in its "(All done)" rollup.

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
)

// assertContainsAll fails for each want missing from out.
func assertContainsAll(t *testing.T, out, label string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("%s missing %q: %q", label, want, out)
		}
	}
}

func TestCampaignFindAndTreeReadSemantics(t *testing.T) {
	f := newCampaignCLIFixture(t)
	f.enroll(t, f.campaignAUUID, f.enrolledUUID)

	unionOut := f.mustRun(t, "find", "--campaign", f.campaignAUUID, "--type", "t", "--ndjson")
	membership := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(unionOut))
	for scanner.Scan() {
		var row struct {
			Slug       string `json:"slug"`
			Membership string `json:"membership"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("decode find campaign row %q: %v", scanner.Text(), err)
		}
		membership[row.Slug] = row.Membership
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan campaign union: %v", err)
	}
	if membership["resident-member"] != "resident" || membership["enrolled-member"] != "enrolled" {
		t.Fatalf("campaign union membership = %#v; want resident and enrolled markings", membership)
	}

	for _, args := range [][]string{
		{"--project", "campaign-cli-a", "find", "wave-a", "--type", "t", "--ndjson"},
		{"--project", "campaign-cli-a", "tree", "wave-a", "--ndjson"},
	} {
		out := f.mustRun(t, args...)
		if !strings.Contains(out, "resident-member") || strings.Contains(out, "enrolled-member") {
			t.Fatalf("machine %s must stay residency-only, got %q", args[2], out)
		}
	}

	humanOut := f.mustRun(t, "--project", "campaign-cli-a", "tree", "wave-a", "--pretty")
	assertContainsAll(t, humanOut, "interactive campaign tree", "resident-member", "enrolled-member", "↗ campaign-cli-b")
}

// TestCampaignMembersFlagDefaultsAndRecursion pins the --campaign-members
// contract. The overlay attaches at EVERY campaign node in the walked tree, not
// just the requested root, and whether it runs at all is the render mode's
// default unless the flag overrides it.
func TestCampaignMembersFlagDefaultsAndRecursion(t *testing.T) {
	f := newCampaignCLIFixture(t)
	f.enroll(t, f.campaignAUUID, f.enrolledUUID)

	// A campaign with NO resident member: every task belongs to another project
	// and is only enrolled. Empty-container pruning used to drop it before the
	// overlay could run, so it could never show its own membership from above.
	database := f.openDB(t)
	hollow := createActiveCampaign(t, database, "wave-hollow", f.projectAUUID)
	foreign := createCampaignFixtureTask(t, database, "hollow-member", "Hollow member", f.campaignBUUID, "open")
	_ = database.Close()
	f.enroll(t, hollow, foreign.UUID)

	t.Run("recurses below the requested root", func(t *testing.T) {
		// Rooted at the PROJECT, one level above the campaign. Before the
		// overlay recursed, this showed resident-member only.
		out := f.mustRun(t, "--project", "campaign-cli-a", "tree", "--pretty")
		assertContainsAll(t, out, "project-rooted tree", "resident-member", "enrolled-member", "↗ campaign-cli-b")
	})

	t.Run("keeps an all-enrolled campaign through pruning", func(t *testing.T) {
		out := f.mustRun(t, "--project", "campaign-cli-a", "tree", "--pretty")
		assertContainsAll(t, out, "all-enrolled campaign", "wave-hollow", "hollow-member")
	})

	t.Run("machine modes stay residency-only by default", func(t *testing.T) {
		if out := f.mustRun(t, "--project", "campaign-cli-a", "tree", "wave-a", "--ndjson"); strings.Contains(out, "enrolled-member") {
			t.Errorf("machine tree leaked an enrolled member without the flag: %q", out)
		}
	})

	t.Run("machine modes opt in with the flag", func(t *testing.T) {
		if out := f.mustRun(t, "--project", "campaign-cli-a", "tree", "wave-a", "--ndjson", "--campaign-members"); !strings.Contains(out, "enrolled-member") {
			t.Errorf("--campaign-members did not reach ndjson: %q", out)
		}
	})

	t.Run("human view opts out with the flag", func(t *testing.T) {
		out := f.mustRun(t, "--project", "campaign-cli-a", "tree", "wave-a", "--pretty", "--campaign-members=false")
		if strings.Contains(out, "enrolled-member") {
			t.Errorf("--campaign-members=false still showed an enrolled member: %q", out)
		}
		// The resident member is still listed, by ID and by slug. Slug rendering
		// is no longer tied to the campaign overlay — every task row prints one —
		// so turning the overlay off removes enrolled MEMBERS and nothing else.
		if !strings.Contains(out, f.residentID) || !strings.Contains(out, "resident-member") {
			t.Errorf("--campaign-members=false dropped a RESIDENT member: %q", out)
		}
	})

	t.Run("ls honours the same flag", func(t *testing.T) {
		if out := f.mustRun(t, "--project", "campaign-cli-a", "ls", "wave-a", "--type", "t", "--ndjson"); strings.Contains(out, "enrolled-member") {
			t.Errorf("machine ls leaked an enrolled member without the flag: %q", out)
		}
		if out := f.mustRun(t, "--project", "campaign-cli-a", "ls", "wave-a", "--type", "t", "--ndjson", "--campaign-members"); !strings.Contains(out, "enrolled-member") {
			t.Errorf("--campaign-members did not reach ls ndjson: %q", out)
		}
	})
}

func TestCampaignTreePrunesCampaignWithNoAdmittedMember(t *testing.T) {
	f := newCampaignCLIFixture(t)
	// The campaign is kept through pruning so the overlay can attach its
	// enrolled members — but only members the state selector admits count.
	// One whose every member is completed is as empty as any other container.
	database := f.openDB(t)
	done := createActiveCampaign(t, database, "wave-done", f.projectAUUID)
	finished := createCampaignFixtureTask(t, database, "finished-member", "Finished member", f.campaignBUUID, "completed")
	_ = database.Close()
	f.enroll(t, done, finished.UUID)

	out := f.mustRun(t, "--project", "campaign-cli-a", "tree", "--pretty")
	if strings.Contains(out, "wave-done") {
		t.Errorf("campaign with no admitted member still displayed: %q", out)
	}
	if !strings.Contains(out, "empty containers not displayed") {
		t.Errorf("pruned campaign not counted as hidden: %q", out)
	}
	all := f.mustRun(t, "--project", "campaign-cli-a", "tree", "--pretty", "--all")
	assertContainsAll(t, all, "--all tree (must still show the campaign and its member)", "wave-done", "finished-member")
}

func TestCampaignTreeRollupCountsEnrolledMembers(t *testing.T) {
	// "(All done)" is a claim about everything the campaign holds. An open
	// member enrolled from another project is shown right under the campaign,
	// so a rollup that counts residents only contradicts its own children —
	// and the claim must not leak upward into the container above it either.
	f := newCampaignCLIFixture(t)
	database := f.openDB(t)
	group := createDirectory(t, database, "group", f.projectAUUID)
	campaign := createActiveCampaign(t, database, "wave-open", group)
	createCampaignFixtureTask(t, database, "resident-done", "Resident done", campaign, "completed")
	_ = database.Close()
	f.enroll(t, campaign, f.enrolledUUID)

	out := f.mustRun(t, "--project", "campaign-cli-a", "tree", "--pretty")
	if !strings.Contains(out, "enrolled-member") {
		t.Fatalf("open enrolled member missing from tree: %q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if (strings.Contains(line, "wave-open/") || strings.Contains(line, "group/")) && strings.Contains(line, "(All done)") {
			t.Errorf("container holding an open enrolled member rendered as done: %q\nfull tree: %q", line, out)
		}
	}
}

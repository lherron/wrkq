//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/render"
)

func TestPromiseCLICommandsAndRootSelectors(t *testing.T) {
	f := newCampaignCLIFixture(t)
	createdOut, err := runCampaignCLI(t, f.dbPath,
		"promise", "add", "--task", f.residentID,
		"--review-at", "2026-08-24T00:30:00+01:00",
		"--question", "What remains?", "--json")
	if err != nil {
		t.Fatalf("promise add: %v\n%s", err, createdOut)
	}
	var created promiseWire
	if err := json.Unmarshal([]byte(createdOut), &created); err != nil {
		t.Fatalf("decode add: %v\n%s", err, createdOut)
	}
	if created.ID == "" || created.ReviewAt != "2026-08-23T23:30:00Z" || created.SubjectRef == nil || created.SubjectRef.ID != f.residentID {
		t.Fatalf("created promise = %#v", created)
	}

	catOut, err := runCampaignCLI(t, f.dbPath, "cat", created.ID, "--json")
	if err != nil {
		t.Fatalf("cat promise: %v\n%s", err, catOut)
	}
	var catItems []promiseWire
	if err := json.Unmarshal([]byte(catOut), &catItems); err != nil || len(catItems) != 1 || catItems[0].ID != created.ID {
		t.Fatalf("cat array = %#v, err=%v", catItems, err)
	}
	oneOut, err := runCampaignCLI(t, f.dbPath, "cat", created.ID, "--json", "--one")
	if err != nil {
		t.Fatalf("cat --one: %v\n%s", err, oneOut)
	}
	var one promiseWire
	if err := json.Unmarshal([]byte(oneOut), &one); err != nil || one.ID != created.ID {
		t.Fatalf("cat --one = %#v, err=%v", one, err)
	}

	editOut, err := runCampaignCLIInput(t, f.dbPath, "Updated question from stdin\n",
		"promise", "edit", created.ID, "--question", "-", "--etag", "1", "--json")
	if err != nil {
		t.Fatalf("promise edit: %v\n%s", err, editOut)
	}
	var edited promiseWire
	if err := json.Unmarshal([]byte(editOut), &edited); err != nil || edited.ETag != 2 || edited.ReviewQuestion == nil || !strings.Contains(*edited.ReviewQuestion, "stdin") {
		t.Fatalf("edited = %#v, err=%v", edited, err)
	}

	renewOut, err := runCampaignCLI(t, f.dbPath, "promise", "renew", created.ID, "--in", "7d", "--note", "Reviewed", "--if-match", "2", "--json")
	if err != nil {
		t.Fatalf("promise renew: %v\n%s", err, renewOut)
	}
	var renewed promiseWire
	if err := json.Unmarshal([]byte(renewOut), &renewed); err != nil || renewed.ETag != 3 || renewed.LastReviewNote == nil || *renewed.LastReviewNote != "Reviewed" {
		t.Fatalf("renewed = %#v, err=%v", renewed, err)
	}

	logOut, err := runCampaignCLI(t, f.dbPath, "log", created.ID, "--json")
	if err != nil || !strings.Contains(logOut, "promise.renewed") {
		t.Fatalf("promise log: %v\n%s", err, logOut)
	}

	resolvedOut, err := runCampaignCLI(t, f.dbPath, "promise", "resolve", created.ID, "--note", "Satisfied", "--etag", "3", "--json")
	if err != nil {
		t.Fatalf("promise resolve: %v\n%s", err, resolvedOut)
	}
	var resolved promiseWire
	if err := json.Unmarshal([]byte(resolvedOut), &resolved); err != nil || resolved.State != "resolved" || resolved.ETag != 4 {
		t.Fatalf("resolved = %#v, err=%v", resolved, err)
	}

	standaloneOut, err := runCampaignCLIInput(t, f.dbPath, "Standalone subject from stdin\n",
		"promise", "add", "--subject", "-", "--in", "7d", "--json")
	if err != nil {
		t.Fatalf("standalone add: %v\n%s", err, standaloneOut)
	}
	var standalone promiseWire
	if err := json.Unmarshal([]byte(standaloneOut), &standalone); err != nil {
		t.Fatal(err)
	}
	attachOut, err := runCampaignCLI(t, f.dbPath, "promise", "attach", standalone.ID, "--campaign", f.campaignAUUID, "--etag", "1", "--json")
	if err != nil {
		t.Fatalf("promise attach: %v\n%s", err, attachOut)
	}
	var attached promiseWire
	if err := json.Unmarshal([]byte(attachOut), &attached); err != nil || attached.SubjectRef == nil || attached.SubjectRef.Type != "container" {
		t.Fatalf("attached = %#v, err=%v", attached, err)
	}
	containerCat, err := runCampaignCLI(t, f.dbPath, "container", "cat", f.campaignAUUID, "--json")
	if err != nil || !strings.Contains(containerCat, standalone.ID) || !strings.Contains(containerCat, `"promises"`) {
		t.Fatalf("container subject surface: %v\n%s", err, containerCat)
	}
	detachOut, err := runCampaignCLI(t, f.dbPath, "promise", "detach", standalone.ID, "--etag", "2", "--json")
	if err != nil {
		t.Fatalf("promise detach: %v\n%s", err, detachOut)
	}
	var detached promiseWire
	if err := json.Unmarshal([]byte(detachOut), &detached); err != nil || detached.SubjectRef != nil {
		t.Fatalf("detached = %#v, err=%v", detached, err)
	}

	rmOut, err := runCampaignCLI(t, f.dbPath, "rm", standalone.ID, "--json")
	if err != nil || !strings.Contains(rmOut, `"type": "promise"`) {
		t.Fatalf("rm promise: %v\n%s", err, rmOut)
	}
	purgeOut, err := runCampaignCLI(t, f.dbPath, "rm", standalone.ID, "--purge", "--yes", "--json")
	if err != nil || !strings.Contains(purgeOut, `"purged": true`) {
		t.Fatalf("rm promise --purge: %v\n%s", err, purgeOut)
	}
}

func TestPromiseAttentionSurfacesAndTreeRenderers(t *testing.T) {
	f := newCampaignCLIFixture(t)
	create := func(args ...string) promiseWire {
		t.Helper()
		out, err := runCampaignCLI(t, f.dbPath, append([]string{"promise", "add"}, args...)...)
		if err != nil {
			t.Fatalf("create promise: %v\n%s", err, out)
		}
		var promise promiseWire
		if err := json.Unmarshal([]byte(out), &promise); err != nil {
			t.Fatalf("decode promise: %v\n%s", err, out)
		}
		return promise
	}
	ready := create("--task", f.residentID, "--review-at", "2000-01-01T00:00:00Z", "--subject", "Ready cross-owner leaf", "--for", "mable", "--on-behalf", "--json")
	standaloneReady := create("--review-at", "2000-01-02T00:00:00Z", "--subject", "Ready owner-global promise", "--for", "mable", "--on-behalf", "--json")
	otherProjectReady := create("--task", f.enrolledID, "--review-at", "2000-01-03T00:00:00Z", "--subject", "Ready other-project promise", "--for", "mable", "--on-behalf", "--json")
	closed := create("--task", f.residentID, "--in", "7d", "--subject", "Closed leaf", "--json")
	containerPromise := create("--container", f.campaignBUUID, "--in", "7d", "--subject", "Keeps empty container visible", "--json")
	if out, err := runCampaignCLI(t, f.dbPath, "promise", "abandon", closed.ID, "--etag", "1", "--json"); err != nil {
		t.Fatalf("abandon closed fixture: %v\n%s", err, out)
	}

	readyOut, err := runCampaignCLI(t, f.dbPath, "promise", "ready", "--for", "mable", "--ndjson")
	if err != nil || !strings.Contains(readyOut, ready.ID) || !strings.Contains(readyOut, standaloneReady.ID) || !strings.Contains(readyOut, otherProjectReady.ID) || !strings.Contains(readyOut, `"readyFor"`) {
		t.Fatalf("ready queue: %v\n%s", err, readyOut)
	}
	scopedOut, err := runCampaignCLI(t, f.dbPath, "promise", "ready", "--for", "mable", "--project", "campaign-cli-a", "--include-global", "--porcelain")
	if err != nil || !strings.Contains(scopedOut, ready.ID) || !strings.Contains(scopedOut, standaloneReady.ID) || strings.Contains(scopedOut, otherProjectReady.ID) {
		t.Fatalf("project-scoped ready queue with globals: %v\n%s", err, scopedOut)
	}
	projectOnlyOut, err := runCampaignCLI(t, f.dbPath, "promise", "ready", "--for", "mable", "--project", "campaign-cli-a", "--porcelain")
	if err != nil || !strings.Contains(projectOnlyOut, ready.ID) || strings.Contains(projectOnlyOut, standaloneReady.ID) || strings.Contains(projectOnlyOut, otherProjectReady.ID) {
		t.Fatalf("project-only ready queue: %v\n%s", err, projectOnlyOut)
	}
	checkOut, err := runCampaignCLI(t, f.dbPath, "check", "--for", "mable", "--json")
	if err != nil || !strings.Contains(checkOut, ready.ID) {
		t.Fatalf("check ready section: %v\n%s", err, checkOut)
	}

	taskCat, err := runCampaignCLI(t, f.dbPath, "cat", f.residentID, "--json", "--one")
	if err != nil || !strings.Contains(taskCat, ready.ID) || !strings.Contains(taskCat, closed.ID) || !strings.Contains(taskCat, `"promises"`) {
		t.Fatalf("task subject surface: %v\n%s", err, taskCat)
	}

	jsonOut, err := runCampaignCLI(t, f.dbPath, "--project", "campaign-cli-a", "tree", "wave-a", "--json")
	if err != nil || !strings.Contains(jsonOut, ready.ID) || strings.Contains(jsonOut, closed.ID) || !strings.Contains(jsonOut, `"promises"`) {
		t.Fatalf("tree json open promises: %v\n%s", err, jsonOut)
	}
	allOut, err := runCampaignCLI(t, f.dbPath, "--project", "campaign-cli-a", "tree", "wave-a", "--json", "--state", "all")
	if err != nil || !strings.Contains(allOut, ready.ID) || !strings.Contains(allOut, closed.ID) {
		t.Fatalf("tree json all promises: %v\n%s", err, allOut)
	}
	ndjsonOut, err := runCampaignCLI(t, f.dbPath, "--project", "campaign-cli-a", "tree", "wave-a", "--ndjson")
	if err != nil || !strings.Contains(ndjsonOut, `"type":"promise"`) || !strings.Contains(ndjsonOut, `"owner_principal_ref":"agent:mable"`) {
		t.Fatalf("tree ndjson: %v\n%s", err, ndjsonOut)
	}
	porcelainOut, err := runCampaignCLI(t, f.dbPath, "--project", "campaign-cli-a", "tree", "wave-a", "--porcelain")
	if err != nil || !strings.Contains(porcelainOut, "promise\t"+ready.ID) {
		t.Fatalf("tree porcelain: %v\n%s", err, porcelainOut)
	}
	humanOut, err := runCampaignCLI(t, f.dbPath, "--project", "campaign-cli-a", "tree", "wave-a", "--pretty")
	if err != nil || !strings.Contains(humanOut, ready.ID) || !strings.Contains(humanOut, "ready") {
		t.Fatalf("tree human: %v\n%s", err, humanOut)
	}
	containerTree, err := runCampaignCLI(t, f.dbPath, "--project", "campaign-cli-b", "tree", "--json")
	if err != nil || !strings.Contains(containerTree, "wave-b") || !strings.Contains(containerTree, containerPromise.ID) {
		t.Fatalf("promise-only container tree visibility: %v\n%s", err, containerTree)
	}
}

func TestPromiseOutputFingerprint(t *testing.T) {
	question, readyFor := "What next?", "2 days"
	promise := promiseWire{
		UUID: "uuid", ID: "PR-00001", OwnerPrincipalRef: "agent:cody", Subject: "Subject",
		ReviewQuestion: &question, SubjectRef: nil, ReviewAt: "2026-08-21T00:00:00Z",
		Ready: true, ReadyFor: &readyFor, State: "open", Meta: map[string]any{}, ETag: 1,
		CreatedAt: "2026-08-20T00:00:00Z", UpdatedAt: "2026-08-20T00:00:00Z",
		CreatedByPrincipalRef: "agent:cody", UpdatedByPrincipalRef: "agent:cody",
	}
	gotType := reflect.TypeOf(promise)
	var tags []string
	for i := 0; i < gotType.NumField(); i++ {
		tags = append(tags, gotType.Field(i).Tag.Get("json"))
	}
	const wantTags = "uuid,id,ownerPrincipalRef,subject,reviewQuestion,omitempty,subjectRef,reviewAt,ready,readyFor,omitempty,state,closedAt,omitempty,lastReviewedAt,omitempty,lastReviewNote,omitempty,meta,etag,createdAt,updatedAt,createdByPrincipalRef,updatedByPrincipalRef"
	if strings.Join(tags, ",") != wantTags {
		t.Fatalf("promise output tags = %s, want %s", strings.Join(tags, ","), wantTags)
	}
	var compact bytes.Buffer
	if err := render.NewRenderer(&compact, render.Options{Porcelain: true}).RenderJSON(promise); err != nil {
		t.Fatal(err)
	}
	const wantPrefix = `{"uuid":"uuid","id":"PR-00001","ownerPrincipalRef":"agent:cody","subject":"Subject"`
	if !strings.HasPrefix(compact.String(), wantPrefix) || !strings.Contains(compact.String(), `"readyFor":"2 days"`) {
		t.Fatalf("compact promise fingerprint = %s", compact.String())
	}
}

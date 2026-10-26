//go:build wrkq_local

package wrkqapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

var updateReadModelWireGolden = flag.Bool("update-read-model-wire-golden", false,
	"rewrite testdata/read_model_wire_keys.golden from the current marshaled read models")

const readModelWireGoldenPath = "testdata/read_model_wire_keys.golden"

// TestReadModelResponsesMarshalTheGoldenKeyOrderAndTypes is the wire-shape
// golden for every read model a client re-renders (cat, ls, find, tree, log,
// watch/monitor, projects, comments, attachments incl. byte transfer, copy,
// handoffs, promises, search/index, check blocked/inbox, webhooks). Each row is
// a REAL API response over a seeded store with the optional fields populated,
// run through encoding/json exactly as the RPC codec sends it; the golden
// records every key path in marshaled order with its JSON type. A changed key
// order (the mirror's byte-identical legacy output), a dropped omitempty, a
// renamed tag or a null where a client expects [] all fail here. Any drift is a
// PROTOCOL CONTRACT change: regenerate deliberately with
//
//	go test -tags sqlite_fts5,wrkq_local ./internal/wrkqapi -run '^TestReadModelResponsesMarshalTheGoldenKeyOrderAndTypes$' -update-read-model-wire-golden
//
// then update docs/wrkq-wrkf-rpc.md + dtoCatalog and re-verify CLI parity.
func TestReadModelResponsesMarshalTheGoldenKeyOrderAndTypes(t *testing.T) {
	ctx := context.Background()
	api := newReadModelGoldenAPI(t)
	f := seedReadModelGolden(t, api)

	var out bytes.Buffer
	row := func(name string, v any, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		paths, err := wireKeyPaths(raw)
		if err != nil {
			t.Fatalf("%s: walk %s: %v", name, raw, err)
		}
		fmt.Fprintf(&out, "## %s\n", name)
		for _, p := range paths {
			out.WriteString(p)
			out.WriteByte('\n')
		}
		out.WriteByte('\n')
	}

	v1, err := api.TaskCatView(ctx, TaskCatViewParams{Task: f.ownerID})
	row("wrkq.task.catView (open owner: claimed, campaign, labels, comments, relations, blocker, subtask, promise)", v1, err)
	v2, err := api.TaskCatView(ctx, TaskCatViewParams{Task: f.subtaskID})
	row("wrkq.task.catView (named subtask)", v2, err)
	v3, err := api.TaskCatView(ctx, TaskCatViewParams{Task: f.doneID})
	row("wrkq.task.catView (completed, acknowledged, outcome, resolution, parent)", v3, err)
	v3a, err := api.TaskCatView(ctx, TaskCatViewParams{Task: f.archivedID})
	row("wrkq.task.catView (archived)", v3a, err)
	v4, err := api.ContainerCatView(ctx, ContainerCatViewParams{Path: "proj/wave"})
	row("wrkq.container.catView", v4, err)
	v5, err := api.ContainerTaskCounts(ctx, ContainerTaskCountsParams{IncludeArchived: true})
	row("wrkq.container.taskCounts (includeArchived)", v5, err)
	v6, err := api.CommentCatView(ctx, CommentCatViewParams{Comment: f.commentID})
	row("wrkq.comment.catView", v6, err)
	v6a, err := api.CommentCatView(ctx, CommentCatViewParams{Comment: f.deletedCommentID})
	row("wrkq.comment.catView (session-attributed, deleted)", v6a, err)
	v7, err := api.CommentListView(ctx, CommentListViewParams{Task: f.ownerID, Limit: 1})
	row("wrkq.comment.listView (paged)", v7, err)
	v8, err := api.AttachmentListView(ctx, AttachmentListViewParams{Task: f.ownerID})
	row("wrkq.attachment.listView", v8, err)
	row("wrkq.attachment.addBytes (final chunk)", f.addBytes, nil)
	v9, err := api.AttachmentGetBytes(ctx, AttachmentGetBytesParams{ID: f.attachmentID})
	row("wrkq.attachment.getBytes", v9, err)
	row("wrkq.task.copy", f.copied, nil)
	row("wrkq.handoff.create", f.handoff, nil)
	v10, err := api.HandoffListView(ctx, HandoffListViewParams{ScopeRef: f.handoffScope, Status: "all", Limit: 2})
	row("wrkq.handoff.listView (paged)", v10, err)
	v11, err := api.HandoffSearchView(ctx, HandoffSearchViewParams{Query: "golden", ScopeRef: f.handoffScope, Status: "all"})
	row("wrkq.handoff.searchView", v11, err)
	v12, err := api.FindListView(ctx, FindListViewParams{Paths: []string{"proj"}, Limit: 1})
	row("wrkq.find.listView (paged)", v12, err)
	v13, err := api.HistoryListView(ctx, HistoryListViewParams{Target: f.ownerID, Limit: 1})
	row("wrkq.history.listView (paged)", v13, err)
	v14, err := api.HistoryTailView(ctx, HistoryTailViewParams{Tasks: []string{f.ownerID}})
	row("wrkq.history.tailView", v14, err)
	v15, err := api.MonitorEventsView(ctx, MonitorEventsViewParams{Tasks: []string{f.ownerID}})
	row("wrkq.monitor.eventsView", v15, err)
	v16, err := api.MonitorStateView(ctx, MonitorStateViewParams{Tasks: []string{f.ownerID, f.doneID}, Condition: "state=completed"})
	row("wrkq.monitor.stateView", v16, err)
	v17, err := api.ProjectsListView(ctx, ProjectsListViewParams{Limit: 1})
	row("wrkq.projects.listView (paged)", v17, err)
	v18, err := api.TreeView(ctx, TreeViewParams{Path: "proj", States: flexString{"any"}})
	row("wrkq.tree.view", v18, err)
	v19, err := api.PromiseList(ctx, PromiseListParams{Task: f.ownerID, State: "all"})
	row("wrkq.promise.list", v19, err)
	v20, err := api.SearchListView(ctx, SearchListViewParams{Query: "golden", Explain: true})
	row("wrkq.search.listView", v20, err)
	v21, err := api.IndexStatus(ctx, IndexStatusParams{})
	row("wrkq.index.status", v21, err)
	v22, err := api.LsListView(ctx, LsListViewParams{Path: "proj", Limit: 1, States: flexString{"any"}})
	row("wrkq.ls.listView (paged)", v22, err)
	v22a, err := api.LsListView(ctx, LsListViewParams{States: flexString{"any"}})
	row("wrkq.ls.listView (root containers)", v22a, err)
	v23, err := api.TaskBlockedView(ctx, TaskBlockedViewParams{Task: f.ownerID})
	row("wrkq.task.blockedView", v23, err)
	v24, err := api.InboxView(ctx, InboxViewParams{InboxPath: "proj"})
	row("wrkq.inbox.view", v24, err)
	row("wrkq.webhook.add (changed)", f.webhookAdded, nil)
	row("wrkq.webhook.add (no change)", f.webhookNoChange, nil)
	v25, err := api.WebhookListView(ctx, WebhookListViewParams{})
	row("wrkq.webhook.listView", v25, err)

	if *updateReadModelWireGolden {
		if err := os.MkdirAll(filepath.Dir(readModelWireGoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(readModelWireGoldenPath, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(readModelWireGoldenPath)
	if err != nil {
		t.Fatalf("read golden (regenerate with -update-read-model-wire-golden): %v", err)
	}
	if got := out.String(); got != string(want) {
		t.Errorf("read-model wire shape drifted (PROTOCOL CONTRACT change).\n%s", lineDiff(string(want), got))
	}
}

type readModelGoldenFixture struct {
	ownerID, subtaskID, doneID, archivedID    string
	commentID, deletedCommentID, attachmentID string
	handoffScope                              string
	addBytes                                  *WrkqAttachmentAddBytesResult
	copied                                    *WrkqTaskCopyResult
	handoff                                   *WrkqHandoffCreateResult
	webhookAdded                              json.RawMessage
	webhookNoChange                           json.RawMessage
}

func newReadModelGoldenAPI(t *testing.T) *API {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wrkq.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database, nil, "agent:cody", filepath.Join(dir, "attachments"), 10, WithSearch(SearchConfig{
		Enabled:         true,
		CanonicalDBPath: dbPath,
		DBPath:          filepath.Join(dir, "search.sqlite"),
		DenseProvider:   "hash",
		DenseDimension:  16,
	}))
}

// seedReadModelGolden drives the real API mutation paths so every optional
// field a read model can carry is populated somewhere in the golden.
func seedReadModelGolden(t *testing.T, api *API) readModelGoldenFixture {
	t.Helper()
	ctx := context.Background()
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	var f readModelGoldenFixture

	_, err := api.ContainerCreate(ctx, ContainerCreateParams{Path: "proj", Kind: "project", Title: "Golden project"})
	must("create project", err)
	_, err = api.ContainerCreate(ctx, ContainerCreateParams{Path: "other", Kind: "project", Title: "Other project"})
	must("create other project", err)
	_, err = api.ContainerCreate(ctx, ContainerCreateParams{Path: "proj/wave", Kind: "feature"})
	must("create campaign container", err)
	labels := []string{"wire"}
	_, err = api.ContainerCampaignConvert(ctx, ContainerCampaignConvertParams{Container: "proj/wave", State: "active", Labels: &labels})
	must("convert campaign", err)

	cause, err := api.TaskCreate(ctx, TaskCreateParams{Path: "proj/cause", Title: "Cause"})
	must("create cause", err)
	blocker, err := api.TaskCreate(ctx, TaskCreateParams{Path: "proj/blocker", Title: "Blocker"})
	must("create blocker", err)

	owner, err := api.TaskCreate(ctx, TaskCreateParams{
		Path:                  "proj/owner",
		Title:                 "Golden owner",
		Description:           "golden description",
		Specification:         "golden specification",
		Kind:                  "task",
		Priority:              1,
		State:                 "open",
		AssigneePrincipalRef:  "agent:cody",
		RequesterPrincipalRef: "agent:mable",
		RequesterScopeRef:     "agent:mable:project:proj",
		RequestedByProjectID:  "other",
		AssignedProjectID:     "proj",
		Labels:                []string{"wire", "golden"},
		Meta:                  map[string]any{"k": "v"},
		DueAt:                 "2030-01-02T00:00:00Z",
		StartAt:               "2030-01-01T00:00:00Z",
		CausedBy:              []string{cause.ID},
		Campaign:              "proj/wave",
	})
	must("create owner", err)
	f.ownerID = owner.ID

	sub, err := api.TaskCreate(ctx, TaskCreateParams{SubtaskOwner: owner.ID, Slug: "diagram", Title: "Diagram"})
	must("create subtask", err)
	f.subtaskID = sub.ID

	_, err = api.RelationAdd(ctx, RelationAddParams{FromTask: blocker.ID, Kind: "blocks", ToTask: owner.ID})
	must("relation blocks", err)
	_, err = api.RelationAdd(ctx, RelationAddParams{FromTask: owner.ID, Kind: "relates_to", ToTask: cause.ID})
	must("relation relates_to", err)

	for _, body := range []string{"golden comment one", "golden comment two"} {
		c, err := api.CommentAdd(ctx, CommentAddParams{
			Task: owner.ID, Body: body, Meta: map[string]any{"k": "v"},
			ScopeRef: "agent:cody:project:proj:task:" + owner.ID,
		})
		must("comment", err)
		if f.commentID == "" {
			f.commentID = c.ID
		}
	}

	deleted, err := api.CommentAdd(ctx, CommentAddParams{
		Task: owner.ID, Body: "golden session comment",
		ScopeRef: "agent:cody:project:proj:task:" + owner.ID,
		Session:  &SessionRef{HostSessionID: "hsid-00000000-0000-4000-8000-000000000001", Generation: 3},
	})
	must("session comment", err)
	f.deletedCommentID = deleted.ID
	_, err = api.CommentDelete(ctx, CommentDeleteParams{ID: deleted.ID})
	must("delete comment", err)

	_, err = api.PromiseAdd(ctx, PromiseAddParams{Task: owner.ID, ReviewIn: "1h", ReviewQuestion: strp("still golden?")})
	must("promise", err)
	resolved, err := api.PromiseAdd(ctx, PromiseAddParams{Task: owner.ID, ReviewIn: "2h", Subject: "golden resolved promise"})
	must("resolved promise", err)
	_, err = api.PromiseResolve(ctx, PromiseReviewParams{Promise: resolved.ID, Note: strp("kept")})
	must("resolve promise", err)
	_, err = api.PromiseAdd(ctx, PromiseAddParams{Container: "proj", ReviewIn: "1h", Subject: "golden container promise"})
	must("container promise", err)

	f.addBytes, err = api.AttachmentAddBytes(ctx, AttachmentAddBytesParams{
		Task: owner.ID, Filename: "note.txt", MimeType: "text/plain", Seq: 0,
		Content: base64.StdEncoding.EncodeToString([]byte("golden bytes")), Final: true,
	})
	must("attachment add bytes", err)
	if f.addBytes.Attachment == nil {
		t.Fatalf("final addBytes chunk committed no attachment: %+v", f.addBytes)
	}
	f.attachmentID = f.addBytes.Attachment.ID

	_, err = api.TaskClaim(nodeContext("lab"), TaskClaimParams{
		Task: owner.ID, PrincipalRef: "agent:cody", Scope: "agent:cody:project:proj:task:" + owner.ID,
	})
	must("claim", err)

	done, err := api.TaskCreate(ctx, TaskCreateParams{
		Path: "proj/done", Title: "Done", RequestedByProjectID: "other", AssignedProjectID: "proj",
		ParentTask: cause.ID, Resolution: "done",
	})
	must("create done", err)
	f.doneID = done.ID
	_, err = api.TaskUpdate(ctx, TaskUpdateParams{Task: done.ID, Patch: TaskPatch{Outcome: strp("shipped"), State: strp("completed")}})
	must("complete done", err)
	_, err = api.TaskAcknowledge(ctx, TaskAcknowledgeParams{Task: done.ID})
	must("ack done", err)

	archived, err := api.TaskCreate(ctx, TaskCreateParams{Path: "proj/archived", Title: "Archived"})
	must("create archived", err)
	f.archivedID = archived.ID
	_, err = api.TaskDelete(ctx, TaskDeleteParams{Task: archived.ID, Mode: "archive"})
	must("archive task", err)
	_, err = api.ContainerCreate(ctx, ContainerCreateParams{Path: "proj/old", Kind: "directory"})
	must("create old container", err)
	_, err = api.ContainerArchive(ctx, ContainerArchiveParams{Path: "proj/old"})
	must("archive container", err)

	f.copied, err = api.TaskCopy(ctx, TaskCopyParams{Source: owner.ID, Destination: "other", WithAttachments: true})
	must("copy", err)

	f.handoffScope = "agent:cody:project:proj"
	var middle string
	for i, title := range []string{"golden handoff one", "golden handoff two", "golden handoff three"} {
		f.handoff, err = api.HandoffCreate(ctx, HandoffCreateParams{
			ScopeRef: f.handoffScope, AgentID: "cody", ProjectID: "proj", Title: title, Body: "golden body",
		})
		must("handoff", err)
		if i == 1 {
			middle = f.handoff.Handoff.ID
		}
	}
	// The middle one is acknowledged so a two-item page holds it in either sort order.
	_, err = api.HandoffAcknowledge(ctx, HandoffAcknowledgeParams{
		Handoff: middle, ActorAgentID: "cody", Note: strp("ack"),
	})
	must("handoff ack", err)

	f.webhookAdded, err = api.WebhookAdd(ctx, WebhookMutateParams{URL: "https://golden.test/hook"})
	must("webhook add", err)
	f.webhookNoChange, err = api.WebhookAdd(ctx, WebhookMutateParams{URL: "https://golden.test/hook"})
	must("webhook add again", err)

	_, err = api.IndexRebuild(ctx, IndexLifecycleParams{})
	must("index rebuild", err)
	return f
}

// wireKeyPaths walks marshaled JSON in wire order and returns one line per
// distinct key path ("a.b[].c: string"). Array elements share one "[]" path; a
// key first seen in a later element is placed right after the key that
// preceded it in that element, so the listing keeps the marshaled field order
// even when earlier elements omitted the key. A path that took several JSON
// types across elements lists them all ("string|null").
func wireKeyPaths(raw []byte) ([]string, error) {
	type node struct {
		kinds    []string
		children []string
		child    map[string]*node
	}
	newNode := func() *node { return &node{child: map[string]*node{}} }
	addKind := func(n *node, kind string) {
		for _, k := range n.kinds {
			if k == kind {
				return
			}
		}
		n.kinds = append(n.kinds, kind)
	}
	childOf := func(n *node, key, prev string) *node {
		if c, ok := n.child[key]; ok {
			return c
		}
		c := newNode()
		n.child[key] = c
		at := 0
		for i, k := range n.children {
			if k == prev {
				at = i + 1
				break
			}
		}
		n.children = append(n.children[:at], append([]string{key}, n.children[at:]...)...)
		return c
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var walk func(n *node) error
	walk = func(n *node) error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				addKind(n, "object")
				prev := ""
				for dec.More() {
					keyTok, err := dec.Token()
					if err != nil {
						return err
					}
					key := keyTok.(string)
					if err := walk(childOf(n, key, prev)); err != nil {
						return err
					}
					prev = key
				}
			case '[':
				addKind(n, "array")
				for dec.More() {
					if err := walk(childOf(n, "[]", "")); err != nil {
						return err
					}
				}
			}
			_, err = dec.Token() // closing delimiter
			return err
		case string:
			addKind(n, "string")
		case json.Number:
			addKind(n, "number")
		case bool:
			addKind(n, "bool")
		case nil:
			addKind(n, "null")
		}
		return nil
	}
	root := newNode()
	if err := walk(root); err != nil {
		return nil, err
	}

	var paths []string
	var emit func(path string, n *node)
	emit = func(path string, n *node) {
		paths = append(paths, path+": "+strings.Join(n.kinds, "|"))
		for _, key := range n.children {
			child := path + "." + key
			if key == "[]" {
				child = path + "[]"
			}
			if path == "$" {
				child = key
				if key == "[]" {
					child = "$[]"
				}
			}
			emit(child, n.child[key])
		}
	}
	emit("$", root)
	return paths, nil
}

func lineDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	n := len(wl)
	if len(gl) > n {
		n = len(gl)
	}
	shown := 0
	for i := 0; i < n && shown < 40; i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			fmt.Fprintf(&b, "line %d:\n  want: %s\n   got: %s\n", i+1, w, g)
			shown++
		}
	}
	return b.String()
}

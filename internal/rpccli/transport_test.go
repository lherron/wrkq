package rpccli

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// TestInProcessTransport_Lifecycle covers daedalus's required lifecycle test:
// a raw server rejects business calls before initialize; the constructed
// transport initializes successfully; wrkq.task.show succeeds; Close shuts down
// cleanly without leaking the server goroutine.
func TestInProcessTransport_Lifecycle(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)

	// 1. A raw server (no rpc.initialize) rejects business calls. Drive one frame
	// through the real Serve loop over pipes, mirroring server.go's initialize gate.
	assertRejectsBeforeInitialize(t, dbPath)

	before := runtime.NumGoroutine()

	// 2 + 3. Construct transport (performs initialize) and call a business method.
	tr := inProcessTransport(t, dbPath)
	raw, err := tr.Call(context.Background(), "wrkq.task.show", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("wrkq.task.show: %v", err)
	}
	var dto struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("decode dto: %v", err)
	}
	if dto.ID != taskID {
		t.Fatalf("dto id = %q, want %q", dto.ID, taskID)
	}

	// 4. Close shuts down without leaking the server goroutine.
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := tr.Close(); err != nil { // idempotent
		t.Fatalf("second Close: %v", err)
	}
	assertNoGoroutineLeak(t, before)
}

// TestInProcessTransport_ErrorMapping covers the error-mapping requirement: an
// unknown task surfaces a typed *Error exposing WRKQ_NOT_FOUND and retryable=false
// so the CLI exit-code mapper has what it needs.
func TestInProcessTransport_ErrorMapping(t *testing.T) {
	dbPath, _ := migratedDBWithTask(t)
	tr := inProcessTransport(t, dbPath)
	defer func() { _ = tr.Close() }()

	_, err := tr.Call(context.Background(), "wrkq.task.show", map[string]string{"task": "T-99999999"})
	if err == nil {
		t.Fatal("expected error for unknown task, got nil")
	}
	rpcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *rpccli.Error", err)
	}
	if rpcErr.DomainID != workrpc.CodeWRKQNotFound {
		t.Errorf("DomainID = %q, want %q", rpcErr.DomainID, workrpc.CodeWRKQNotFound)
	}
	if rpcErr.Retryable {
		t.Errorf("Retryable = true, want false for WRKQ_NOT_FOUND")
	}
}

// TestTransportEquivalence_InProcVsSubprocess covers daedalus's transport
// equivalence requirement: the same wrkq.task.show call returns byte-identical
// results through the in-process pipe transport and a real `wrkq rpc --stdio`
// subprocess. (The wrkq.task.show DTO carries no lifecycle-only fields, so no
// normalization is needed here; the initialize handshake — which does carry pid
// and db path — is validated separately inside each transport constructor.)
func TestTransportEquivalence_InProcVsSubprocess(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a wrkq binary; skipped under -short")
	}
	dbPath, taskID := migratedDBWithTask(t)
	binPath := buildWrkq(t)

	inproc := inProcessTransport(t, dbPath)
	defer func() { _ = inproc.Close() }()
	inResult, err := inproc.Call(context.Background(), "wrkq.task.show", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("in-process show: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sub, err := NewSubprocess(ctx, binPath, dbPath, nil)
	if err != nil {
		t.Fatalf("NewSubprocess: %v", err)
	}
	defer func() { _ = sub.Close() }()
	subResult, err := sub.Call(ctx, "wrkq.task.show", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("subprocess show: %v", err)
	}

	if !jsonEqual(t, inResult, subResult) {
		t.Errorf("transport results differ:\n in-process: %s\n subprocess: %s", inResult, subResult)
	}

	// catView (the cat compatibility projection) must also agree across transports.
	inCat, err := inproc.Call(context.Background(), "wrkq.task.catView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("in-process catView: %v", err)
	}
	subCat, err := sub.Call(ctx, "wrkq.task.catView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("subprocess catView: %v", err)
	}
	if !jsonEqual(t, inCat, subCat) {
		t.Errorf("catView transport results differ:\n in-process: %s\n subprocess: %s", inCat, subCat)
	}

	// container.catView (the container compat projection) must also agree.
	inCC, err := inproc.Call(context.Background(), "wrkq.container.catView", map[string]string{"container": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("in-process container.catView: %v", err)
	}
	subCC, err := sub.Call(ctx, "wrkq.container.catView", map[string]string{"container": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("subprocess container.catView: %v", err)
	}
	if !jsonEqual(t, inCC, subCC) {
		t.Errorf("container.catView transport results differ:\n in-process: %s\n subprocess: %s", inCC, subCC)
	}

	// comment.catView must also agree across transports. Seed a comment first via
	// the in-process transport so a known comment id exists.
	if _, err := inproc.Call(context.Background(), "wrkq.comment.add", map[string]any{"task": taskID, "body": "xport ✓"}); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	inCm, err := inproc.Call(context.Background(), "wrkq.comment.catView", map[string]string{"comment": "C-00001"})
	if err != nil {
		t.Fatalf("in-process comment.catView: %v", err)
	}
	subCm, err := sub.Call(ctx, "wrkq.comment.catView", map[string]string{"comment": "C-00001"})
	if err != nil {
		t.Fatalf("subprocess comment.catView: %v", err)
	}
	if !jsonEqual(t, inCm, subCm) {
		t.Errorf("comment.catView transport results differ:\n in-process: %s\n subprocess: %s", inCm, subCm)
	}

	// relation.listView (Group B compat list projection) must agree across
	// transports. Seed a second task + a relation via the in-process transport.
	t2, err := inproc.Call(context.Background(), "wrkq.task.create",
		map[string]any{"path": "rpccli-test-proj/rel-target", "title": "T2"})
	if err != nil {
		t.Fatalf("seed second task: %v", err)
	}
	var t2dto struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(t2, &t2dto)
	if _, err := inproc.Call(context.Background(), "wrkq.relation.add",
		map[string]any{"fromTask": taskID, "kind": "blocks", "toTask": t2dto.ID}); err != nil {
		t.Fatalf("seed relation: %v", err)
	}
	inRel, err := inproc.Call(context.Background(), "wrkq.relation.listView", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("in-process relation.listView: %v", err)
	}
	subRel, err := sub.Call(ctx, "wrkq.relation.listView", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("subprocess relation.listView: %v", err)
	}
	if !jsonEqual(t, inRel, subRel) {
		t.Errorf("relation.listView transport results differ:\n in-process: %s\n subprocess: %s", inRel, subRel)
	}

	// comment.listView (first paginated list view) must agree across transports.
	inCl, err := inproc.Call(context.Background(), "wrkq.comment.listView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("in-process comment.listView: %v", err)
	}
	subCl, err := sub.Call(ctx, "wrkq.comment.listView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("subprocess comment.listView: %v", err)
	}
	if !jsonEqual(t, inCl, subCl) {
		t.Errorf("comment.listView transport results differ:\n in-process: %s\n subprocess: %s", inCl, subCl)
	}

	// comment.listView multi-task form (the `tasks` array param) must also agree
	// across transports — the accumulate-per-task paging shape is server-owned.
	inClM, err := inproc.Call(context.Background(), "wrkq.comment.listView", map[string]any{"tasks": []string{taskID}})
	if err != nil {
		t.Fatalf("in-process comment.listView (multi-task): %v", err)
	}
	subClM, err := sub.Call(ctx, "wrkq.comment.listView", map[string]any{"tasks": []string{taskID}})
	if err != nil {
		t.Fatalf("subprocess comment.listView (multi-task): %v", err)
	}
	if !jsonEqual(t, inClM, subClM) {
		t.Errorf("comment.listView (multi-task) transport results differ:\n in-process: %s\n subprocess: %s", inClM, subClM)
	}

	// attachment.listView must agree across transports (empty set is sufficient
	// for transport equivalence; populated requires attachment storage setup).
	inAl, err := inproc.Call(context.Background(), "wrkq.attachment.listView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("in-process attachment.listView: %v", err)
	}
	subAl, err := sub.Call(ctx, "wrkq.attachment.listView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("subprocess attachment.listView: %v", err)
	}
	if !jsonEqual(t, inAl, subAl) {
		t.Errorf("attachment.listView transport results differ:\n in-process: %s\n subprocess: %s", inAl, subAl)
	}

	// task.lsView (the ls mixed task/container compat list projection) must agree
	// across transports. List the seeded project's children.
	inLs, err := inproc.Call(context.Background(), "wrkq.task.lsView", map[string]any{"path": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("in-process task.lsView: %v", err)
	}
	subLs, err := sub.Call(ctx, "wrkq.task.lsView", map[string]any{"path": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("subprocess task.lsView: %v", err)
	}
	if !jsonEqual(t, inLs, subLs) {
		t.Errorf("task.lsView transport results differ:\n in-process: %s\n subprocess: %s", inLs, subLs)
	}

	// task.findListView (the find recursive/filtered compat list projection) must
	// agree across transports. Search the seeded project's tasks recursively.
	inFind, err := inproc.Call(context.Background(), "wrkq.task.findListView", map[string]any{"paths": []string{"rpccli-test-proj"}, "type": "t"})
	if err != nil {
		t.Fatalf("in-process task.findListView: %v", err)
	}
	subFind, err := sub.Call(ctx, "wrkq.task.findListView", map[string]any{"paths": []string{"rpccli-test-proj"}, "type": "t"})
	if err != nil {
		t.Fatalf("subprocess task.findListView: %v", err)
	}
	if !jsonEqual(t, inFind, subFind) {
		t.Errorf("task.findListView transport results differ:\n in-process: %s\n subprocess: %s", inFind, subFind)
	}

	// task.treeView (the tree recursive compat projection) must agree across
	// transports. Walk the seeded project's hierarchy.
	inTree, err := inproc.Call(context.Background(), "wrkq.task.treeView", map[string]any{"path": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("in-process task.treeView: %v", err)
	}
	subTree, err := sub.Call(ctx, "wrkq.task.treeView", map[string]any{"path": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("subprocess task.treeView: %v", err)
	}
	if !jsonEqual(t, inTree, subTree) {
		t.Errorf("task.treeView transport results differ:\n in-process: %s\n subprocess: %s", inTree, subTree)
	}

	// task.blockedView (the `check blocked` compat projection) must agree across
	// transports. The seed task has no blockers (empty blockers slice).
	inBlk, err := inproc.Call(context.Background(), "wrkq.task.blockedView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("in-process task.blockedView: %v", err)
	}
	subBlk, err := sub.Call(ctx, "wrkq.task.blockedView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("subprocess task.blockedView: %v", err)
	}
	if !jsonEqual(t, inBlk, subBlk) {
		t.Errorf("task.blockedView transport results differ:\n in-process: %s\n subprocess: %s", inBlk, subBlk)
	}

	// task.inboxView (the `check-inbox` compat list projection) must agree across
	// transports. No inbox container is seeded, so the open-inbox set is empty.
	inInbox, err := inproc.Call(context.Background(), "wrkq.task.inboxView", map[string]any{"inboxPath": "inbox"})
	if err != nil {
		t.Fatalf("in-process task.inboxView: %v", err)
	}
	subInbox, err := sub.Call(ctx, "wrkq.task.inboxView", map[string]any{"inboxPath": "inbox"})
	if err != nil {
		t.Fatalf("subprocess task.inboxView: %v", err)
	}
	if !jsonEqual(t, inInbox, subInbox) {
		t.Errorf("task.inboxView transport results differ:\n in-process: %s\n subprocess: %s", inInbox, subInbox)
	}

	// history.listView (the `log` compat history read model over event_log) must
	// agree across transports. The seed task has at least its create event, so the
	// history is non-empty; target it by friendly ID with the server default limit.
	inHist, err := inproc.Call(context.Background(), "wrkq.history.listView", map[string]any{"target": taskID})
	if err != nil {
		t.Fatalf("in-process history.listView: %v", err)
	}
	subHist, err := sub.Call(ctx, "wrkq.history.listView", map[string]any{"target": taskID})
	if err != nil {
		t.Fatalf("subprocess history.listView: %v", err)
	}
	if !jsonEqual(t, inHist, subHist) {
		t.Errorf("history.listView transport results differ:\n in-process: %s\n subprocess: %s", inHist, subHist)
	}

	// history.tailView (the watch / monitor-raw bounded ASCENDING raw tail over
	// event_log) must agree across transports. From cursor 0 it returns the seeded
	// task's create event(s) plus the high-water cursor.
	inTail, err := inproc.Call(context.Background(), "wrkq.history.tailView", map[string]any{"cursor": 0})
	if err != nil {
		t.Fatalf("in-process history.tailView: %v", err)
	}
	subTail, err := sub.Call(ctx, "wrkq.history.tailView", map[string]any{"cursor": 0})
	if err != nil {
		t.Fatalf("subprocess history.tailView: %v", err)
	}
	if !jsonEqual(t, inTail, subTail) {
		t.Errorf("history.tailView transport results differ:\n in-process: %s\n subprocess: %s", inTail, subTail)
	}

	// monitor.eventsView (the bounded ASCENDING filtered monitor event page) must
	// agree across transports. Filter to the seeded task so the comment→task
	// backfill + isEventIncluded path is exercised identically on both transports.
	inEv, err := inproc.Call(context.Background(), "wrkq.monitor.eventsView", map[string]any{"tasks": []string{taskID}, "cursor": 0})
	if err != nil {
		t.Fatalf("in-process monitor.eventsView: %v", err)
	}
	subEv, err := sub.Call(ctx, "wrkq.monitor.eventsView", map[string]any{"tasks": []string{taskID}, "cursor": 0})
	if err != nil {
		t.Fatalf("subprocess monitor.eventsView: %v", err)
	}
	if !jsonEqual(t, inEv, subEv) {
		t.Errorf("monitor.eventsView transport results differ:\n in-process: %s\n subprocess: %s", inEv, subEv)
	}

	// monitor.stateView (the single authoritative --until condition snapshot) must
	// agree across transports. Evaluate state=open against the seeded task.
	inSt, err := inproc.Call(context.Background(), "wrkq.monitor.stateView", map[string]any{"tasks": []string{taskID}, "condition": "state=open"})
	if err != nil {
		t.Fatalf("in-process monitor.stateView: %v", err)
	}
	subSt, err := sub.Call(ctx, "wrkq.monitor.stateView", map[string]any{"tasks": []string{taskID}, "condition": "state=open"})
	if err != nil {
		t.Fatalf("subprocess monitor.stateView: %v", err)
	}
	if !jsonEqual(t, inSt, subSt) {
		t.Errorf("monitor.stateView transport results differ:\n in-process: %s\n subprocess: %s", inSt, subSt)
	}

	// task.lsView multi-path form ("paths") — the server owns the per-path query
	// plus the combined merge-sort — must also agree across transports.
	inLsM, err := inproc.Call(context.Background(), "wrkq.task.lsView", map[string]any{"paths": []string{"rpccli-test-proj"}})
	if err != nil {
		t.Fatalf("in-process task.lsView (paths): %v", err)
	}
	subLsM, err := sub.Call(ctx, "wrkq.task.lsView", map[string]any{"paths": []string{"rpccli-test-proj"}})
	if err != nil {
		t.Fatalf("subprocess task.lsView (paths): %v", err)
	}
	if !jsonEqual(t, inLsM, subLsM) {
		t.Errorf("task.lsView (paths) transport results differ:\n in-process: %s\n subprocess: %s", inLsM, subLsM)
	}

	// wrkq.container.update (T-05112): the new slug/title patch mutation must agree
	// across transports. Both transports share one DB, so seed a SEPARATE container
	// per side, rename each identically, and compare the results with the
	// per-container identity fields (uuid/id/path) normalized away — the renamed
	// slug/title/kind/etag must match.
	// Both transports share one DB and both seeds are top-level projects (unique
	// slug-in-parent), so each side renames to a DISTINCT slug; the per-side
	// identity + slug fields are normalized away and the remaining shape (title,
	// kind, etag) must match.
	normContainer := func(raw json.RawMessage) map[string]any {
		var m map[string]any
		if uerr := json.Unmarshal(raw, &m); uerr != nil {
			t.Fatalf("normContainer unmarshal: %v", uerr)
		}
		delete(m, "uuid")
		delete(m, "id")
		delete(m, "slug")
		delete(m, "path")
		delete(m, "createdAt")
		delete(m, "updatedAt")
		return m
	}
	if _, err := inproc.Call(context.Background(), "wrkq.container.create",
		map[string]any{"path": "xport-rename-a", "kind": "project"}); err != nil {
		t.Fatalf("in-process seed container: %v", err)
	}
	if _, err := sub.Call(ctx, "wrkq.container.create",
		map[string]any{"path": "xport-rename-b", "kind": "project"}); err != nil {
		t.Fatalf("subprocess seed container: %v", err)
	}
	inUpd, err := inproc.Call(context.Background(), "wrkq.container.update",
		map[string]any{"container": "xport-rename-a", "patch": map[string]any{"slug": "xport-renamed-a", "title": "Renamed"}})
	if err != nil {
		t.Fatalf("in-process container.update: %v", err)
	}
	subUpd, err := sub.Call(ctx, "wrkq.container.update",
		map[string]any{"container": "xport-rename-b", "patch": map[string]any{"slug": "xport-renamed-b", "title": "Renamed"}})
	if err != nil {
		t.Fatalf("subprocess container.update: %v", err)
	}
	if !reflect.DeepEqual(normContainer(inUpd), normContainer(subUpd)) {
		t.Errorf("container.update transport results differ:\n in-process: %s\n subprocess: %s", inUpd, subUpd)
	}

	// wrkq.webhook.* (the DEDICATED global-webhook family, T-05119) must agree
	// across transports. Both transports share one DB and the webhook URLs live on
	// the SINGLETON ROOT container, so add ONE URL in-process (the subprocess add
	// would then idempotently no-change), then read it back via listView through
	// BOTH transports and assert byte-identical rows. This proves the server-owned
	// root resolution + webhook_urls read agree regardless of transport.
	if _, err := inproc.Call(context.Background(), "wrkq.webhook.add",
		map[string]any{"url": "https://xport-hook.test/wrkq"}); err != nil {
		t.Fatalf("in-process webhook.add: %v", err)
	}
	inHooks, err := inproc.Call(context.Background(), "wrkq.webhook.listView", map[string]any{})
	if err != nil {
		t.Fatalf("in-process webhook.listView: %v", err)
	}
	subHooks, err := sub.Call(ctx, "wrkq.webhook.listView", map[string]any{})
	if err != nil {
		t.Fatalf("subprocess webhook.listView: %v", err)
	}
	if !jsonEqual(t, inHooks, subHooks) {
		t.Errorf("webhook.listView transport results differ:\n in-process: %s\n subprocess: %s", inHooks, subHooks)
	}

	// task.copy (the server-owned deep copy, T-05111) must agree across transports.
	// The source task already lives in rpccli-test-proj, so both calls use overwrite
	// to target the SAME destination row → byte-identical result DTO (same dest
	// uuid/id), proving the create-or-overwrite envelope is server-owned.
	copyParams := map[string]any{"source": taskID, "destination": "rpccli-test-proj", "overwrite": true}
	inCopy, err := inproc.Call(context.Background(), "wrkq.task.copy", copyParams)
	if err != nil {
		t.Fatalf("in-process task.copy: %v", err)
	}
	subCopy, err := sub.Call(ctx, "wrkq.task.copy", copyParams)
	if err != nil {
		t.Fatalf("subprocess task.copy: %v", err)
	}
	if !jsonEqual(t, inCopy, subCopy) {
		t.Errorf("task.copy transport results differ:\n in-process: %s\n subprocess: %s", inCopy, subCopy)
	}

	// wrkq.bundle.exportView (T-05118): the LOGICAL bundle snapshot must agree across
	// transports. The seeded task lives in rpccli-test-proj; scope the export to that
	// path-prefix. The manifest carries a wall-clock `timestamp` (time.Now per call),
	// so normalize it away before comparing — everything else (manifest fields, task
	// docs, containers, events ordering) must be byte-identical across transports.
	bundleParams := map[string]any{"pathPrefixes": []string{"rpccli-test-proj"}, "withEvents": true, "includeRefs": true}
	inBundle, err := inproc.Call(context.Background(), "wrkq.bundle.exportView", bundleParams)
	if err != nil {
		t.Fatalf("in-process bundle.exportView: %v", err)
	}
	subBundle, err := sub.Call(ctx, "wrkq.bundle.exportView", bundleParams)
	if err != nil {
		t.Fatalf("subprocess bundle.exportView: %v", err)
	}
	if !jsonEqual(t, normalizeBundleManifestTS(t, inBundle), normalizeBundleManifestTS(t, subBundle)) {
		t.Errorf("bundle.exportView transport results differ:\n in-process: %s\n subprocess: %s", inBundle, subBundle)
	}

	// Handoff family (T-05117). Seed one pending handoff via the in-process
	// transport with an EXPLICIT caller-resolved scope (the server never reads
	// env), then read it back through BOTH transports — get + listView — and assert
	// byte-identical results. This proves the handoff create/get/listView envelope
	// is transport-agnostic and that the create wrote a durable, addressable row.
	hoParams := map[string]any{
		"scopeRef":  "agent:cody:project:wrkq",
		"agentId":   "cody",
		"projectId": "wrkq",
		"title":     "xport handoff",
		"body":      "carry-over notes",
	}
	hoRaw, err := inproc.Call(context.Background(), "wrkq.handoff.create", hoParams)
	if err != nil {
		t.Fatalf("in-process handoff.create: %v", err)
	}
	var hoRes struct {
		Handoff struct {
			ID string `json:"id"`
		} `json:"handoff"`
	}
	if uerr := json.Unmarshal(hoRaw, &hoRes); uerr != nil || hoRes.Handoff.ID == "" {
		t.Fatalf("handoff.create result decode: %v (%s)", uerr, hoRaw)
	}
	inGet, err := inproc.Call(context.Background(), "wrkq.handoff.get", map[string]string{"handoff": hoRes.Handoff.ID})
	if err != nil {
		t.Fatalf("in-process handoff.get: %v", err)
	}
	subGet, err := sub.Call(ctx, "wrkq.handoff.get", map[string]string{"handoff": hoRes.Handoff.ID})
	if err != nil {
		t.Fatalf("subprocess handoff.get: %v", err)
	}
	if !jsonEqual(t, inGet, subGet) {
		t.Errorf("handoff.get transport results differ:\n in-process: %s\n subprocess: %s", inGet, subGet)
	}
	listParams := map[string]any{"scopeRef": "agent:cody:project:wrkq", "status": "pending", "limit": 50}
	inList, err := inproc.Call(context.Background(), "wrkq.handoff.listView", listParams)
	if err != nil {
		t.Fatalf("in-process handoff.listView: %v", err)
	}
	subList, err := sub.Call(ctx, "wrkq.handoff.listView", listParams)
	if err != nil {
		t.Fatalf("subprocess handoff.listView: %v", err)
	}
	if !jsonEqual(t, inList, subList) {
		t.Errorf("handoff.listView transport results differ:\n in-process: %s\n subprocess: %s", inList, subList)
	}
}

func TestTransportEquivalence_LegacyVsMirrorRPCStdio(t *testing.T) {
	if testing.Short() {
		t.Skip("builds wrkq + wrkq-rpccli binaries; skipped under -short")
	}
	dbPath, taskID := migratedDBWithTask(t)
	bins := buildParityBinaries(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	legacy, err := NewSubprocess(ctx, bins.wrkq, dbPath, nil)
	if err != nil {
		t.Fatalf("legacy NewSubprocess: %v", err)
	}
	defer func() { _ = legacy.Close() }()
	mirror, err := NewSubprocess(ctx, bins.mirror, dbPath, nil)
	if err != nil {
		t.Fatalf("mirror NewSubprocess: %v", err)
	}
	defer func() { _ = mirror.Close() }()

	legacyResult, err := legacy.Call(ctx, "wrkq.task.show", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("legacy task.show: %v", err)
	}
	mirrorResult, err := mirror.Call(ctx, "wrkq.task.show", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("mirror task.show: %v", err)
	}
	if !jsonEqual(t, legacyResult, mirrorResult) {
		t.Errorf("rpc --stdio task.show results differ:\n legacy: %s\n mirror: %s", legacyResult, mirrorResult)
	}
}

// normalizeBundleManifestTS blanks the manifest.timestamp (time.Now per call) in a
// bundle.exportView result so two independent snapshots are otherwise byte-comparable.
func normalizeBundleManifestTS(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("normalizeBundleManifestTS unmarshal: %v", err)
	}
	if man, ok := m["manifest"].(map[string]any); ok {
		man["timestamp"] = "<TS>"
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("normalizeBundleManifestTS marshal: %v", err)
	}
	return out
}

// TestTransportEquivalence_AttachmentBytes covers the byte-transfer methods
// (wrkq.attachment.getBytes / addBytes, T-05103) across both transports: an
// attachment is uploaded in-process via chunked addBytes, then read back via
// getBytes through BOTH the in-process pipe and a real `wrkq rpc --stdio`
// subprocess, asserting byte-identical results. WRKQ_ATTACH_DIR is set so both
// the in-proc bootstrap (AttachDir env precedence) and the subprocess config.Load
// resolve the SAME explicit attach dir.
func TestTransportEquivalence_AttachmentBytes(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a wrkq binary; skipped under -short")
	}
	dbPath, taskID := migratedDBWithTask(t)
	binPath := buildWrkq(t)

	attachDir := t.TempDir()
	t.Setenv("WRKQ_ATTACH_DIR", attachDir)

	inproc := inProcessTransport(t, dbPath)
	defer func() { _ = inproc.Close() }()

	// Upload bytes via the chunked addBytes path (single chunk, final=true).
	content := []byte("xport-bytes ✓\x00\x01\nline2")
	addRaw, err := inproc.Call(context.Background(), "wrkq.attachment.addBytes", map[string]any{
		"task":          taskID,
		"filename":      "xport.bin",
		"seq":           0,
		"contentBase64": base64.StdEncoding.EncodeToString(content),
		"final":         true,
	})
	if err != nil {
		t.Fatalf("addBytes: %v", err)
	}
	var addRes struct {
		Committed  bool `json:"committed"`
		Attachment struct {
			ID string `json:"id"`
		} `json:"attachment"`
	}
	if uerr := json.Unmarshal(addRaw, &addRes); uerr != nil {
		t.Fatalf("decode addBytes: %v", uerr)
	}
	if !addRes.Committed || addRes.Attachment.ID == "" {
		t.Fatalf("addBytes did not commit: %s", addRaw)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sub, err := NewSubprocess(ctx, binPath, dbPath, []string{"WRKQ_ATTACH_DIR=" + attachDir})
	if err != nil {
		t.Fatalf("NewSubprocess: %v", err)
	}
	defer func() { _ = sub.Close() }()

	getParams := map[string]any{"id": addRes.Attachment.ID, "offset": 0}
	inGet, err := inproc.Call(context.Background(), "wrkq.attachment.getBytes", getParams)
	if err != nil {
		t.Fatalf("in-process getBytes: %v", err)
	}
	subGet, err := sub.Call(ctx, "wrkq.attachment.getBytes", getParams)
	if err != nil {
		t.Fatalf("subprocess getBytes: %v", err)
	}
	if !jsonEqual(t, inGet, subGet) {
		t.Errorf("getBytes transport results differ:\n in-process: %s\n subprocess: %s", inGet, subGet)
	}

	// Sanity: the decoded content round-trips byte-for-byte through the read DTO.
	var chunk struct {
		Content string `json:"contentBase64"`
		EOF     bool   `json:"eof"`
	}
	if uerr := json.Unmarshal(inGet, &chunk); uerr != nil {
		t.Fatalf("decode getBytes: %v", uerr)
	}
	decoded, derr := base64.StdEncoding.DecodeString(chunk.Content)
	if derr != nil {
		t.Fatalf("decode chunk: %v", derr)
	}
	if string(decoded) != string(content) || !chunk.EOF {
		t.Errorf("round-trip mismatch: got %q eof=%v want %q eof=true", decoded, chunk.EOF, content)
	}
}

// TestStdoutPurity_AttachmentGetBytes is the byte-transfer stdout-purity guard
// (T-05103): a binary attachment fetched via wrkq.attachment.getBytes must NOT
// leak raw bytes onto the SERVER's stdout — every server stdout line is a
// well-formed JSON-RPC frame. The decoded raw bytes are emitted only by the
// wrkq-rpccli CLI after frame decode (proven by the attach-get parity cases).
func TestStdoutPurity_AttachmentGetBytes(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	attachDir := t.TempDir()
	t.Setenv("WRKQ_ATTACH_DIR", attachDir)

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	clientR, clientW := io.Pipe()
	serverR, serverW := io.Pipe()
	srv := workrpc.NewServer(serverW)
	workrpc.RegisterAPI(srv, api, opts)
	go func() {
		_ = srv.Serve(context.Background(), clientR)
		_ = serverW.Close()
		_ = database.Close()
	}()
	defer func() { _ = clientW.Close() }()

	// Binary payload with NUL + control bytes — exactly what would corrupt stdio if
	// it leaked unencoded.
	content := []byte("PNG\x89\x00\x01\x02\nbinary\x7f")

	// io.Pipe is UNBUFFERED, so each request must be written and its response read
	// before the next request — otherwise the server blocks writing a response while
	// the test blocks writing the next request (deadlock). Drive one frame at a time
	// and assert each server stdout line is pure JSON with no raw NUL.
	br := bufio.NewReader(serverR)
	call := func(id int, method string, params any) {
		pb, _ := json.Marshal(params)
		req, _ := json.Marshal(workrpc.Request{
			JSONRPC: "2.0", ID: json.RawMessage(itoa(id)),
			Method: method, Params: pb,
		})
		if _, werr := clientW.Write(append(req, '\n')); werr != nil {
			t.Fatalf("write frame %d: %v", id, werr)
		}
		line, rerr := br.ReadBytes('\n')
		if rerr != nil && len(line) == 0 {
			t.Fatalf("read response %d: %v", id, rerr)
		}
		trimmed := line
		if n := len(trimmed); n > 0 && trimmed[n-1] == '\n' {
			trimmed = trimmed[:n-1]
		}
		if !json.Valid(trimmed) {
			t.Fatalf("server stdout for %s is not valid JSON (raw bytes leaked): %q", method, trimmed)
		}
		for _, b := range trimmed {
			if b == 0x00 {
				t.Fatalf("server stdout for %s contains a raw NUL byte (stdio corruption): %q", method, trimmed)
			}
		}
	}

	call(1, "rpc.initialize", map[string]any{"protocolVersion": workrpc.ProtocolVersion})
	call(2, "wrkq.attachment.addBytes", map[string]any{
		"task": taskID, "filename": "pure.bin", "seq": 0,
		"contentBase64": base64.StdEncoding.EncodeToString(content), "final": true,
	})
	call(3, "wrkq.attachment.getBytes", map[string]any{"id": "ATT-00001", "offset": 0})
}

// itoa is a tiny int→ascii for building request ids without strconv churn.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// assertRejectsBeforeInitialize drives a single wrkq.task.show request through a
// real server's Serve loop without first calling rpc.initialize, and asserts the
// server returns an error rather than executing the business call.
func assertRejectsBeforeInitialize(t *testing.T, dbPath string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	clientR, clientW := io.Pipe()
	serverR, serverW := io.Pipe()
	srv := workrpc.NewServer(serverW)
	workrpc.RegisterAPI(srv, api, opts)
	go func() {
		_ = srv.Serve(context.Background(), clientR)
		_ = serverW.Close()
		_ = database.Close()
	}()
	defer func() { _ = clientW.Close() }()

	req, _ := json.Marshal(workrpc.Request{
		JSONRPC: "2.0", ID: json.RawMessage("1"),
		Method: "wrkq.task.show", Params: json.RawMessage(`{"task":"T-00001"}`),
	})
	if _, err := clientW.Write(append(req, '\n')); err != nil {
		t.Fatalf("write pre-init frame: %v", err)
	}
	line, err := bufio.NewReader(serverR).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read pre-init response: %v", err)
	}
	var resp workrpc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode pre-init response: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected error for business call before initialize, got result: %s", resp.Result)
	}
}

func buildWrkq(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "wrkq")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/wrkq")
	cmd.Dir = repoRootFromTest(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/wrkq: %v\n%s", err, b)
	}
	return out
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..")) // internal/rpccli -> repo root
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal b: %v", err)
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(ab) == string(bb)
}

func assertNoGoroutineLeak(t *testing.T, before int) {
	t.Helper()
	// Allow the server goroutine a moment to unwind after Close.
	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

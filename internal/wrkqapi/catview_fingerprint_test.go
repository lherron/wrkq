package wrkqapi

import (
	"reflect"
	"strings"
	"testing"
)

// TestWebhookMutationDTOFingerprint pins the wrkq.webhook.add/remove MUTATION
// RESULT key order + the listView row shape (T-05119 daedalus #10211). The
// mutation result deliberately OVERRIDES the struct-field-order convention and
// preserves the legacy MAP-ALPHABETICAL key order: a changed result is
// {changed,count,target,webhook_urls}; a no-change result is {changed,webhook_urls}.
// It is built from a map (encoding/json sorts map keys), NOT a struct, so this
// test reproduces the server's exact marshaling rather than reflecting a type.
// Any drift here is a PROTOCOL CONTRACT change (bump this pin, update docs +
// dtoCatalog, re-verify webhook parity deliberately).
func TestWebhookMutationDTOFingerprint(t *testing.T) {
	changed, _ := marshalAlpha(map[string]any{
		"changed":      true,
		"target":       "https://x.test",
		"webhook_urls": []string{"https://x.test"},
		"count":        1,
	})
	noChange, _ := marshalAlpha(map[string]any{
		"changed":      false,
		"webhook_urls": []string{},
	})
	const wantChanged = `{"changed":true,"count":1,"target":"https://x.test","webhook_urls":["https://x.test"]}`
	const wantNoChange = `{"changed":false,"webhook_urls":[]}`
	if string(changed) != wantChanged {
		t.Errorf("webhook changed mutation key order drifted (protocol contract change):\n got: %s\nwant: %s", changed, wantChanged)
	}
	if string(noChange) != wantNoChange {
		t.Errorf("webhook no-change mutation key order drifted (protocol contract change):\n got: %s\nwant: %s", noChange, wantNoChange)
	}

	row := dtoFingerprint(reflect.TypeOf(WebhookRow{}))
	const wantRow = "WebhookRow{url}"
	if row != wantRow {
		t.Errorf("WebhookRow DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", row, wantRow)
	}
}

// TestCatViewDTOFingerprint is the local golden guard for the catView contract
// (daedalus T-05090 follow-up). ProtocolSchemaHash() now includes reflected DTO
// shape, while this test keeps a human-readable pinned fingerprint for deliberate
// review when WrkqTaskCatView or its nested structs drift.
func TestCatViewDTOFingerprint(t *testing.T) {
	got := strings.Join([]string{
		dtoFingerprint(reflect.TypeOf(WrkqTaskCatView{})),
		dtoFingerprint(reflect.TypeOf(CatViewComment{})),
		dtoFingerprint(reflect.TypeOf(CatViewRelation{})),
		dtoFingerprint(reflect.TypeOf(CatViewBlocker{})),
	}, "\n")

	const want = "WrkqTaskCatView{id,uuid,path,artifact_dir,project_id,project_uuid,requested_by_project_id,omitempty,assigned_project_id,omitempty,slug,title,state,priority,kind,parent_task_id,omitempty,parent_task_uuid,omitempty,assignee,omitempty,assignee_uuid,omitempty,assignee_principal_ref,omitempty,start_at,omitempty,due_at,omitempty,labels,omitempty,meta,description,specification,acknowledged_at,omitempty,resolution,omitempty,etag,created_at,updated_at,completed_at,omitempty,archived_at,omitempty,created_by,created_by_principal_ref,omitempty,created_by_actor,omitempty,created_by_scope_ref,omitempty,updated_by,updated_by_principal_ref,omitempty,caused_by,blocked_by,omitempty,comments,omitempty,relations,omitempty}\n" +
		"CatViewComment{id,created_at,body,principal_ref,omitempty,actor_slug,omitempty,actor_role,omitempty}\n" +
		"CatViewRelation{direction,kind,task_id,task_uuid,task_slug,task_title,created_at,created_by_id}\n" +
		"CatViewBlocker{id,state}"

	if got != want {
		t.Errorf("WrkqTaskCatView DTO shape drifted — this is a PROTOCOL CONTRACT change.\n got:\n%s\nwant:\n%s\n"+
			"Update this fingerprint, docs/wrkq-wrkf-rpc.md, dtoCatalog, and re-verify cat parity deliberately.", got, want)
	}
}

func dtoFingerprint(t reflect.Type) string {
	parts := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		tag := f.Tag.Get("json")
		parts = append(parts, strings.ReplaceAll(tag, ",", ","))
	}
	return t.Name() + "{" + strings.Join(parts, ",") + "}"
}

// TestContainerCatViewDTOFingerprint guards the container compat projection's
// field/tag shape (same rationale as TestCatViewDTOFingerprint).
func TestContainerCatViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqContainerCatView{}))
	const want = "WrkqContainerCatView{id,uuid,slug,title,description,kind,parent_id,omitempty,parent_uuid,omitempty,parent_path,omitempty,path,webhook_urls,omitempty,sort_index,etag,created_at,updated_at,archived_at,omitempty,created_by,updated_by}"
	if got != want {
		t.Errorf("WrkqContainerCatView DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", got, want)
	}
}

// TestCommentCatViewDTOFingerprint guards the comment compat projection shape.
func TestCommentCatViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqCommentCatView{}))
	const want = "WrkqCommentCatView{actor_role,omitempty,actor_slug,omitempty,actor_uuid,omitempty,body,created_at,created_by_principal_ref,omitempty,created_by_scope_ref,omitempty,deleted_at,omitempty,deleted_by_actor_uuid,omitempty,deleted_by_principal_ref,omitempty,deleted_by_scope_ref,omitempty,etag,id,meta,omitempty,task_id,task_uuid,updated_at,omitempty,uuid}"
	if got != want {
		t.Errorf("WrkqCommentCatView DTO shape drifted:\n got: %s\nwant: %s", got, want)
	}
}

// TestCommentListViewDTOFingerprint guards the comment-ls envelope shape.
func TestCommentListViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqCommentListView{}))
	const want = "WrkqCommentListView{items,next_cursor,omitempty}"
	if got != want {
		t.Errorf("WrkqCommentListView DTO shape drifted:\n got: %s\nwant: %s", got, want)
	}
}

// TestAttachmentListViewDTOFingerprint guards the attach-ls projection shapes.
func TestAttachmentListViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqAttachmentListView{})) + "\n" + dtoFingerprint(reflect.TypeOf(WrkqAttachmentListRow{}))
	const want = "WrkqAttachmentListView{items,next_cursor,omitempty}\nWrkqAttachmentListRow{checksum,omitempty,created_at,created_by_principal_ref,omitempty,filename,id,mime_type,omitempty,relative_path,size_bytes,uuid}"
	if got != want {
		t.Errorf("attachment list view DTO shape drifted:\n got: %s\nwant: %s", got, want)
	}
}

// TestAttachmentBytesDTOFingerprint guards the byte-transfer DTO shapes (T-05103,
// daedalus OPTION 1). These carry attachment CONTENT across the RPC boundary as
// explicit protocol data (base64 + size + checksum); any field/tag drift is a
// PROTOCOL CONTRACT change and must be made deliberately (bump fingerprint, update
// docs/dtoCatalog, re-verify get/put-byte parity).
func TestAttachmentBytesDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqAttachmentBytes{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqAttachmentAddBytesResult{}))
	const want = "WrkqAttachmentBytes{uuid,id,taskUuid,filename,mimeType,omitempty,sizeBytes,checksum,omitempty,offset,contentBase64,eof}\n" +
		"WrkqAttachmentAddBytesResult{uploadId,seq,bytesReceived,committed,attachment,omitempty}"
	if got != want {
		t.Errorf("attachment byte-transfer DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", got, want)
	}
}

// TestTaskCopyDTOFingerprint guards the wrkq.task.copy request + result DTO shapes
// (T-05111, daedalus hrcchat#10196). TaskCopyParams crosses the RPC boundary as the
// server-owned copy envelope in the daedalus-approved SEMANTIC field order; the
// result keeps the LEGACY copyResult snake_case output keys so the mirror renders
// byte-identical output. Any field/tag drift is a PROTOCOL CONTRACT change.
func TestTaskCopyDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(TaskCopyParams{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqTaskCopyResult{}))
	const want = "TaskCopyParams{source,destination,overwrite,omitempty,withAttachments,omitempty,shallow,omitempty,expectEtag,omitempty,actor,omitempty,idempotencyKey,omitempty}\n" +
		"WrkqTaskCopyResult{source_id,source_uuid,dest_id,dest_uuid,dest_path,attachments_copied,omitempty,with_files,omitempty}"
	if got != want {
		t.Errorf("wrkq.task.copy DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", got, want)
	}
}

// TestHandoffDTOFingerprint guards the handoff resource DTO + envelope shapes
// (T-05117, daedalus hrcchat#10211). WrkqHandoff reproduces the LEGACY handoffJSON
// field ORDER + tags EXACTLY (internal/cli/handoff_create.go:30-56) so the mirror
// re-marshals it into byte-identical `wrkq handoff` output. Any field/tag drift is
// a PROTOCOL CONTRACT change: bump this fingerprint, update docs/dtoCatalog, and
// re-verify handoff parity deliberately.
func TestHandoffDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqHandoff{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqHandoffCreateResult{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqHandoffListResult{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqHandoffSearchResult{}))
	const want = "WrkqHandoff{uuid,id,scope_ref,scope_kind,agent_id,project_id,agent_actor_uuid,agent_principal_ref,omitempty,project_container_uuid,created_by_agent_id,created_by_actor_uuid,created_by_principal_ref,omitempty,title,body,status,idempotency_key,acknowledged_at,acknowledged_by_agent_id,acknowledged_by_actor_uuid,acknowledged_by_principal_ref,omitempty,acknowledgement_note,meta,etag,created_at,updated_at}\n" +
		"WrkqHandoffCreateResult{handoff,idempotentReplay}\n" +
		"WrkqHandoffListResult{items,nextCursor,omitempty}\n" +
		"WrkqHandoffSearchResult{handoffs,next_cursor,truncated,stale,omitempty,stale_event_count,omitempty,index_warning,omitempty}"
	if got != want {
		t.Errorf("handoff DTO shape drifted (protocol contract change):\n got:\n%s\nwant:\n%s\n"+
			"Update this fingerprint, docs/wrkq-wrkf-rpc.md, dtoCatalog, and re-verify handoff parity deliberately.", got, want)
	}
}

// TestFindListViewDTOFingerprint guards the find projection shapes.
func TestFindListViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqFindListView{})) + "\n" + dtoFingerprint(reflect.TypeOf(WrkqFindEntry{}))
	const want = "WrkqFindListView{items,next_cursor,omitempty}\nWrkqFindEntry{type,uuid,id,slug,title,path,specification,omitempty,state,omitempty,priority,omitempty,kind,omitempty,assignee,omitempty,assignee_principal_ref,omitempty,parent_task_id,omitempty,requested_by_project_id,omitempty,assigned_project_id,omitempty,acknowledged_at,omitempty,resolution,omitempty,due_at,omitempty,caused_by,omitempty,created_at,updated_at,etag}"
	if got != want {
		t.Errorf("find list view DTO shape drifted:\n got: %s\nwant: %s", got, want)
	}
}

// TestHistoryListViewDTOFingerprint guards the log compat history read-model shapes
// (same rationale as the cat/ls/find fingerprints). WrkqLogEvent is the legacy
// logEvent row shape in LEGACY STRUCT ORDER (NOT alphabetical) — payload stays a
// string, pointer/omitempty match legacy.
func TestHistoryListViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqHistoryListView{})) + "\n" + dtoFingerprint(reflect.TypeOf(WrkqLogEvent{}))
	const want = "WrkqHistoryListView{items,next_cursor,omitempty}\n" +
		"WrkqLogEvent{id,timestamp,actor_uuid,omitempty,actor_slug,omitempty,actor_id,omitempty,principal_ref,omitempty,scope_ref,omitempty,resource_type,resource_uuid,event_type,etag,omitempty,payload,omitempty}"
	if got != want {
		t.Errorf("history list view DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", got, want)
	}
}

// TestHistoryTailViewDTOFingerprint guards the watch/monitor-raw bounded-tail read
// model shapes (T-05116). WrkqWatchEvent is the legacy watchEvent row shape in
// LEGACY STRUCT ORDER (NOT alphabetical) — it INCLUDES resource_id, uses a STRING
// timestamp, and resource_uuid is a nullable pointer (omitempty), all DISTINCT from
// WrkqLogEvent. Any field/tag drift is a PROTOCOL CONTRACT change.
func TestHistoryTailViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqHistoryTailView{})) + "\n" + dtoFingerprint(reflect.TypeOf(WrkqWatchEvent{}))
	const want = "WrkqHistoryTailView{items,high_water}\n" +
		"WrkqWatchEvent{id,timestamp,actor_uuid,omitempty,actor_slug,omitempty,actor_id,omitempty,principal_ref,omitempty,scope_ref,omitempty,resource_type,resource_uuid,omitempty,resource_id,omitempty,event_type,etag,omitempty,payload,omitempty}"
	if got != want {
		t.Errorf("history tail view DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", got, want)
	}
}

// TestMonitorViewDTOFingerprint guards the monitor bounded-polling read-model shapes
// (T-05115). WrkqMonitorEvent is the legacy monitorEventLine data row MINUS the
// client-owned `type` discriminator, in legacy struct order. Any field/tag drift is
// a PROTOCOL CONTRACT change.
func TestMonitorViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqMonitorEventsView{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqMonitorEvent{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqMonitorStateView{}))
	const want = "WrkqMonitorEventsView{items,high_water}\n" +
		"WrkqMonitorEvent{id,timestamp,resource_type,resource_uuid,omitempty,resource_id,omitempty,event_type,payload,omitempty}\n" +
		"WrkqMonitorStateView{met,unmet}"
	if got != want {
		t.Errorf("monitor view DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", got, want)
	}
}

// TestProjectsListViewDTOFingerprint guards the projects compatibility projection.
func TestProjectsListViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqProjectsListView{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqProjectEntry{}))
	const want = "WrkqProjectsListView{items,next_cursor,omitempty}\n" +
		"WrkqProjectEntry{type,id,slug,title,omitempty,path}"
	if got != want {
		t.Errorf("projects list view DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", got, want)
	}
}

// TestTreeViewDTOFingerprint guards the tree projection shapes (same rationale as
// the cat/ls fingerprints). The wire_* carriers are part of the contract: the
// mirror depends on them to reconstruct legacy's NDJSON stream + JSON `path`.
func TestTreeViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqTreeView{})) + "\n" + dtoFingerprint(reflect.TypeOf(WrkqTreeNode{}))
	const want = "WrkqTreeView{path,project_id,omitempty,children,hidden_containers_not_displayed,wire_raw_path,omitempty}\n" +
		"WrkqTreeNode{type,id,slug,title,state,omitempty,uuid,requested_by_project_id,omitempty,assigned_project_id,omitempty,acknowledged_at,omitempty,resolution,omitempty,is_archived,is_deleted,all_tasks_completed,omitempty,children,omitempty,external_children,omitempty,external_backlink,omitempty,external_project_id,omitempty,external_path,omitempty,wire_created_at,omitempty,wire_parent_task_uuid,omitempty}"
	if got != want {
		t.Errorf("tree view DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", got, want)
	}
}

// TestSearchIndexViewDTOFingerprint guards the search + index family DTO shapes
// (T-05114). WrkqSearchListView reproduces the legacy search.Response struct order,
// WrkqSearchResult the legacy search.Result struct order, and WrkqIndexStatus the
// legacy indexdb.Status struct order — NOT alphabetical (these are legacy
// struct-marshal shapes). Any field/tag drift is a PROTOCOL CONTRACT change and
// must be made deliberately (bump fingerprint, update docs/dtoCatalog, re-verify
// search/index parity).
func TestSearchIndexViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqSearchListView{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqSearchResult{})) + "\n" +
		dtoFingerprint(reflect.TypeOf(WrkqIndexStatus{}))
	const want = "WrkqSearchListView{query,stale,status,results,total_matches,offset}\n" +
		"WrkqSearchResult{resource_type,resource_id,resource_uuid,task_id,omitempty,task_uuid,omitempty,comment_id,omitempty,scope_ref,omitempty,status,omitempty,path,title,state,omitempty,kind,omitempty,snippet,score,created_at,updated_at,stale,explain,omitempty}\n" +
		"WrkqIndexStatus{path,enabled,status,last_indexed_event_id,canonical_max_event_id,stale_event_count,dense_model_id,omitempty,dense_dimension,omitempty,dense_vector_count,omitempty,last_error,omitempty,searchable_chunk_count}"
	if got != want {
		t.Errorf("search/index DTO shape drifted (protocol contract change):\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestLsListViewDTOFingerprint guards the ls projection shapes.
func TestLsListViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqLsListView{})) + "\n" + dtoFingerprint(reflect.TypeOf(WrkqLsEntry{}))
	const want = "WrkqLsListView{items,next_cursor,omitempty}\nWrkqLsEntry{type,id,slug,title,omitempty,path,created_at,updated_at,state,omitempty,kind,omitempty,task_count,omitempty,active_task_count,omitempty,requested_by_project_id,omitempty,assigned_project_id,omitempty,acknowledged_at,omitempty,resolution,omitempty}"
	if got != want {
		t.Errorf("ls list view DTO shape drifted:\n got: %s\nwant: %s", got, want)
	}
}

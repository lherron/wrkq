package wrkqapi

import (
	"reflect"
	"strings"
	"testing"
)

// TestCatViewDTOFingerprint is the strong-path guard for the catView contract
// (daedalus T-05090 follow-up): ProtocolSchemaHash() hashes DTO *names*, not
// field shapes, so a field/tag change to the compatibility projection would NOT
// perturb the hash on its own. This reflection fingerprint fails on any exported
// field or json-tag drift in WrkqTaskCatView (and its nested structs), forcing a
// DELIBERATE contract update (bump the fingerprint, update docs/dtoCatalog, and
// decide whether the protocol hash input needs to change).
func TestCatViewDTOFingerprint(t *testing.T) {
	got := strings.Join([]string{
		dtoFingerprint(reflect.TypeOf(WrkqTaskCatView{})),
		dtoFingerprint(reflect.TypeOf(CatViewComment{})),
		dtoFingerprint(reflect.TypeOf(CatViewRelation{})),
		dtoFingerprint(reflect.TypeOf(CatViewBlocker{})),
	}, "\n")

	const want = "WrkqTaskCatView{id,uuid,path,artifact_dir,project_id,project_uuid,requested_by_project_id,omitempty,assigned_project_id,omitempty,slug,title,state,priority,kind,parent_task_id,omitempty,parent_task_uuid,omitempty,assignee,omitempty,assignee_uuid,omitempty,assignee_principal_ref,omitempty,start_at,omitempty,due_at,omitempty,labels,omitempty,meta,description,specification,acknowledged_at,omitempty,resolution,omitempty,cp_project_id,omitempty,cp_work_item_id,omitempty,cp_run_id,omitempty,session_id,omitempty,run_status,omitempty,etag,created_at,updated_at,completed_at,omitempty,archived_at,omitempty,created_by,created_by_principal_ref,omitempty,created_by_actor,omitempty,created_by_scope_ref,omitempty,updated_by,updated_by_principal_ref,omitempty,blocked_by,omitempty,comments,omitempty,relations,omitempty}\n" +
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

// TestFindListViewDTOFingerprint guards the find projection shapes.
func TestFindListViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqFindListView{})) + "\n" + dtoFingerprint(reflect.TypeOf(WrkqFindEntry{}))
	const want = "WrkqFindListView{items,next_cursor,omitempty}\nWrkqFindEntry{type,uuid,id,slug,title,path,specification,omitempty,state,omitempty,priority,omitempty,kind,omitempty,assignee,omitempty,assignee_principal_ref,omitempty,parent_task_id,omitempty,requested_by_project_id,omitempty,assigned_project_id,omitempty,acknowledged_at,omitempty,resolution,omitempty,due_at,omitempty,created_at,updated_at,etag}"
	if got != want {
		t.Errorf("find list view DTO shape drifted:\n got: %s\nwant: %s", got, want)
	}
}

// TestTreeViewDTOFingerprint guards the tree projection shapes (same rationale as
// the cat/ls fingerprints). The wire_* carriers are part of the contract: the
// mirror depends on them to reconstruct legacy's NDJSON stream + JSON `path`.
func TestTreeViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqTreeView{})) + "\n" + dtoFingerprint(reflect.TypeOf(WrkqTreeNode{}))
	const want = "WrkqTreeView{path,project_id,omitempty,children,hidden_containers_not_displayed,wire_raw_path,omitempty}\n" +
		"WrkqTreeNode{type,id,slug,title,state,omitempty,uuid,requested_by_project_id,omitempty,assigned_project_id,omitempty,acknowledged_at,omitempty,resolution,omitempty,is_archived,is_deleted,all_tasks_completed,omitempty,children,omitempty,wire_created_at,omitempty,wire_parent_task_uuid,omitempty}"
	if got != want {
		t.Errorf("tree view DTO shape drifted (protocol contract change):\n got: %s\nwant: %s", got, want)
	}
}

// TestLsListViewDTOFingerprint guards the ls projection shapes.
func TestLsListViewDTOFingerprint(t *testing.T) {
	got := dtoFingerprint(reflect.TypeOf(WrkqLsListView{})) + "\n" + dtoFingerprint(reflect.TypeOf(WrkqLsEntry{}))
	const want = "WrkqLsListView{items,next_cursor,omitempty}\nWrkqLsEntry{type,id,slug,title,omitempty,path,created_at,updated_at,state,omitempty,kind,omitempty,task_count,omitempty,active_task_count,omitempty,requested_by_project_id,omitempty,assigned_project_id,omitempty,acknowledged_at,omitempty,resolution,omitempty,cp_project_id,omitempty,cp_work_item_id,omitempty,cp_run_id,omitempty,session_id,omitempty,run_status,omitempty}"
	if got != want {
		t.Errorf("ls list view DTO shape drifted:\n got: %s\nwant: %s", got, want)
	}
}

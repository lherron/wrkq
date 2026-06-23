package wrkqapi

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/workflow"
)

// ─── DTOs (camelCase JSON; no DB column names leak) ──────────────────────────

// WrkqTask is the stable task resource DTO (docs/wrkq-wrkf-rpc.md §6.2).
type WrkqTask struct {
	UUID                  string         `json:"uuid"`
	ID                    string         `json:"id"`
	Slug                  string         `json:"slug"`
	Title                 string         `json:"title"`
	ProjectUUID           string         `json:"projectUuid"`
	Path                  string         `json:"path"`
	State                 string         `json:"state"`
	Priority              int            `json:"priority"`
	Kind                  string         `json:"kind"`
	RiskClass             string         `json:"riskClass,omitempty"`
	Description           string         `json:"description"`
	Specification         string         `json:"specification"`
	HasDescription        bool           `json:"hasDescription"`
	HasSpecification      bool           `json:"hasSpecification"`
	Labels                []string       `json:"labels"`
	Meta                  map[string]any `json:"meta"`
	ETag                  int64          `json:"etag"`
	StartAt               string         `json:"startAt,omitempty"`
	DueAt                 string         `json:"dueAt,omitempty"`
	CreatedAt             string         `json:"createdAt"`
	UpdatedAt             string         `json:"updatedAt"`
	CompletedAt           string         `json:"completedAt,omitempty"`
	ArchivedAt            string         `json:"archivedAt,omitempty"`
	DeletedAt             string         `json:"deletedAt,omitempty"`
	AcknowledgedAt        string         `json:"acknowledgedAt,omitempty"`
	AssigneePrincipalRef  string         `json:"assigneePrincipalRef,omitempty"`
	CreatedByPrincipalRef string         `json:"createdByPrincipalRef,omitempty"`
	UpdatedByPrincipalRef string         `json:"updatedByPrincipalRef,omitempty"`

	// createdAtRaw / updatedAtRaw hold the un-normalized timestamps for cursor
	// anchoring; they are unexported and never serialized.
	createdAtRaw string
	updatedAtRaw string
}

// WrkqTaskListResult is the paginated task list envelope.
type WrkqTaskListResult struct {
	Items      []WrkqTask `json:"items"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

// WrkqComment is the stable comment resource DTO (§6.2).
type WrkqComment struct {
	UUID                  string         `json:"uuid"`
	ID                    string         `json:"id"`
	Task                  string         `json:"task"`
	Body                  string         `json:"body"`
	Meta                  map[string]any `json:"meta"`
	ETag                  int64          `json:"etag"`
	CreatedAt             string         `json:"createdAt"`
	UpdatedAt             string         `json:"updatedAt,omitempty"`
	DeletedAt             string         `json:"deletedAt,omitempty"`
	CreatedByPrincipalRef string         `json:"createdByPrincipalRef,omitempty"`

	// createdAtRaw holds the un-normalized created_at for cursor anchoring; it is
	// unexported and never serialized.
	createdAtRaw string
}

// WrkqCommentListResult is the paginated comment list envelope.
type WrkqCommentListResult struct {
	Items      []WrkqComment `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// WrkqWorkflowAttachResult is returned by wrkq.workflow.attach (§6.2).
// task is the full WrkqTask DTO; instance is the WrkfInstance; attached is true
// for a freshly attached instance and false on an idempotency replay.
type WrkqWorkflowAttachResult struct {
	Task     *WrkqTask          `json:"task"`
	Instance *workflow.Instance `json:"instance"`
	Attached bool               `json:"attached"`
}

// WrkqWorkflowInspectResult wraps the current workflow instance for a task.
type WrkqWorkflowInspectResult struct {
	Instance *workflow.Instance `json:"instance"`
}

// WrkqWorkflowTimelineResult wraps the workflow event timeline for a task.
type WrkqWorkflowTimelineResult struct {
	Events []workflow.Event `json:"events"`
}

// ─── Params ──────────────────────────────────────────────────────────────────

// TaskCreateParams mirrors WrkqTaskCreateParams (§6.2).
type TaskCreateParams struct {
	Path                 string         `json:"path,omitempty"`
	Project              string         `json:"project,omitempty"`
	Title                string         `json:"title"`
	Description          string         `json:"description,omitempty"`
	Specification        string         `json:"specification,omitempty"`
	Kind                 string         `json:"kind,omitempty"`
	Priority             int            `json:"priority,omitempty"`
	State                string         `json:"state,omitempty"`
	RiskClass            string         `json:"riskClass,omitempty"`
	ParentTask           string         `json:"parentTask,omitempty"`
	AssigneePrincipalRef string         `json:"assigneePrincipalRef,omitempty"`
	Labels               []string       `json:"labels,omitempty"`
	Meta                 map[string]any `json:"meta,omitempty"`
	Actor                string         `json:"actor,omitempty"`
	IdempotencyKey       string         `json:"idempotencyKey,omitempty"`
}

// TaskShowParams mirrors WrkqTaskShowParams.
type TaskShowParams struct {
	Task string `json:"task"`
}

// TaskListParams mirrors WrkqTaskListParams. state/kind accept a string or an
// array of strings.
type TaskListParams struct {
	Path           string     `json:"path,omitempty"`
	State          flexString `json:"state,omitempty"`
	Kind           flexString `json:"kind,omitempty"`
	Assignee       string     `json:"assignee,omitempty"`
	Labels         []string   `json:"labels,omitempty"`
	IncludeDeleted bool       `json:"includeDeleted,omitempty"`
	Limit          int        `json:"limit,omitempty"`
	Cursor         string     `json:"cursor,omitempty"`

	// Sort selects the ordering field. Whitelist: created_at (default),
	// updated_at, priority, id, path. Non-whitelisted values are rejected with
	// WRKQ_VALIDATION (mirrors the CLI find sort whitelist plus priority).
	Sort string `json:"sort,omitempty"`
	// Direction is "asc" (default) or "desc". An empty value preserves the
	// default ordering; any other non-empty value is rejected.
	Direction string `json:"direction,omitempty"`
	// Recursive, when true, includes tasks in containers nested under Path
	// (the whole subtree). Default false keeps the direct-container-only filter.
	Recursive bool `json:"recursive,omitempty"`
	// Summary, when true, omits the heavy text bodies from returned list items.
	// Their presence is still reported via hasDescription / hasSpecification.
	Summary bool `json:"summary,omitempty"`
}

// TaskUpdateParams mirrors WrkqTaskUpdateParams. ExpectEtag is a pointer so a
// supplied value of 0 is distinguishable from "not supplied".
type TaskUpdateParams struct {
	Task           string    `json:"task"`
	Patch          TaskPatch `json:"patch"`
	ExpectEtag     *int64    `json:"expectEtag,omitempty"`
	Actor          string    `json:"actor,omitempty"`
	IdempotencyKey string    `json:"idempotencyKey,omitempty"`
}

// TaskMoveParams mirrors WrkqTaskMoveParams.
type TaskMoveParams struct {
	Task       string `json:"task"`
	TargetPath string `json:"targetPath"`
	ExpectEtag *int64 `json:"expectEtag,omitempty"`
	Actor      string `json:"actor,omitempty"`
}

// TaskPatch is the mutable subset of a task.
type TaskPatch struct {
	Title                *string         `json:"title,omitempty"`
	Description          *string         `json:"description,omitempty"`
	Specification        *string         `json:"specification,omitempty"`
	State                *string         `json:"state,omitempty"`
	Priority             *int            `json:"priority,omitempty"`
	Kind                 *string         `json:"kind,omitempty"`
	RiskClass            *string         `json:"riskClass,omitempty"`
	Labels               *[]string       `json:"labels,omitempty"`
	Meta                 *map[string]any `json:"meta,omitempty"`
	AssigneePrincipalRef *string         `json:"assigneePrincipalRef,omitempty"`
	DueAt                *string         `json:"dueAt,omitempty"`
	StartAt              *string         `json:"startAt,omitempty"`
}

func (p *TaskPatch) UnmarshalJSON(b []byte) error {
	type taskPatch TaskPatch
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	allowed := map[string]bool{
		"title": true, "description": true, "specification": true, "state": true,
		"priority": true, "kind": true, "riskClass": true, "labels": true, "meta": true,
		"assigneePrincipalRef": true, "dueAt": true, "startAt": true,
	}
	for key := range raw {
		if !allowed[key] {
			return fmt.Errorf("unsupported task patch field %q", key)
		}
	}
	var out taskPatch
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*p = TaskPatch(out)
	return nil
}

// CommentAddParams mirrors WrkqCommentAddParams.
type CommentAddParams struct {
	Task           string         `json:"task"`
	Body           string         `json:"body"`
	Meta           map[string]any `json:"meta,omitempty"`
	Actor          string         `json:"actor,omitempty"`
	IdempotencyKey string         `json:"idempotencyKey,omitempty"`
}

// CommentListParams mirrors WrkqCommentListParams.
type CommentListParams struct {
	Task           string `json:"task"`
	IncludeDeleted bool   `json:"includeDeleted,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
}

// WorkflowAttachParams mirrors WrkqWorkflowAttachParams.
type WorkflowAttachParams struct {
	Task           string `json:"task"`
	Workflow       string `json:"workflow"`
	Actor          string `json:"actor,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// WorkflowTaskParams is the task-scoped selector for inspect/timeline.
type WorkflowTaskParams struct {
	Task string `json:"task"`
}

// ─── Task lifecycle params (acknowledge / delete / restore) ──────────────────

// TaskAcknowledgeParams mirrors WrkqTaskAcknowledgeParams. force allows ack on
// non-terminal tasks (mirrors `wrkq ack --force`).
type TaskAcknowledgeParams struct {
	Task  string `json:"task"`
	Force bool   `json:"force,omitempty"`
	Actor string `json:"actor,omitempty"`
}

// TaskDeleteParams mirrors WrkqTaskDeleteParams. Reversible delete (state =
// deleted); restore is the inverse.
type TaskDeleteParams struct {
	Task  string `json:"task"`
	Actor string `json:"actor,omitempty"`
}

// TaskRestoreParams mirrors WrkqTaskRestoreParams. state is the target state
// (default open); archived/deleted targets are rejected.
type TaskRestoreParams struct {
	Task  string `json:"task"`
	State string `json:"state,omitempty"`
	Actor string `json:"actor,omitempty"`
}

// ─── Comment show / delete params ────────────────────────────────────────────

// CommentShowParams mirrors WrkqCommentShowParams.
type CommentShowParams struct {
	ID string `json:"id"`
}

// CommentDeleteParams mirrors WrkqCommentDeleteParams.
type CommentDeleteParams struct {
	ID    string `json:"id"`
	Actor string `json:"actor,omitempty"`
}

// ─── Attachments ─────────────────────────────────────────────────────────────

// WrkqAttachment is the stable attachment resource DTO. The DB/CLI field name is
// `checksum` (not sha256).
type WrkqAttachment struct {
	UUID                  string `json:"uuid"`
	ID                    string `json:"id"`
	TaskUUID              string `json:"taskUuid"`
	Filename              string `json:"filename"`
	RelativePath          string `json:"relativePath,omitempty"`
	MimeType              string `json:"mimeType,omitempty"`
	SizeBytes             int64  `json:"sizeBytes"`
	Checksum              string `json:"checksum,omitempty"`
	CreatedAt             string `json:"createdAt"`
	CreatedByPrincipalRef string `json:"createdByPrincipalRef,omitempty"`

	// createdAtRaw holds the un-normalized created_at for cursor anchoring; it is
	// unexported and never serialized.
	createdAtRaw string
}

// WrkqAttachmentListResult is the attachment list envelope.
type WrkqAttachmentListResult struct {
	Items      []WrkqAttachment `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

// AttachmentAddParams mirrors WrkqAttachmentAddParams.
type AttachmentAddParams struct {
	Task           string `json:"task"`
	Path           string `json:"path"`
	Filename       string `json:"filename,omitempty"`
	MimeType       string `json:"mimeType,omitempty"`
	Actor          string `json:"actor,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// AttachmentListParams mirrors WrkqAttachmentListParams.
type AttachmentListParams struct {
	Task   string `json:"task"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// AttachmentShowParams mirrors WrkqAttachmentShowParams.
type AttachmentShowParams struct {
	ID string `json:"id"`
}

// AttachmentRemoveParams mirrors WrkqAttachmentRemoveParams.
type AttachmentRemoveParams struct {
	ID    string `json:"id"`
	Actor string `json:"actor,omitempty"`
}

// ─── attachment byte transfer (T-05103, daedalus OPTION 1) ───────────────────
//
// Attachment CONTENT crosses the RPC boundary as explicit protocol data (base64
// + declared size + checksum), never as a server-local host path. Transfers are
// CHUNKED because the 8 MiB frame cap is smaller than the default 50 MB attach
// limit (a single base64 frame for a max-size attachment would exceed the frame
// cap). See docs/wrkq-wrkf-rpc.md "attachment byte transfer".

// AttachmentByteChunkBytes is the server's preferred raw-bytes-per-chunk on read.
// 1 MiB raw → ~1.34 MiB base64, comfortably under the 8 MiB frame cap with JSON
// envelope overhead.
const AttachmentByteChunkBytes = 1 << 20

// AttachmentGetBytesParams requests a slice of an attachment's stored bytes.
// offset/limit are byte offsets into the decoded content; the server clamps limit
// to AttachmentByteChunkBytes. A caller streams by looping until eof is true.
type AttachmentGetBytesParams struct {
	ID     string `json:"id"`
	Offset int64  `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// WrkqAttachmentBytes is a single read chunk plus the whole-file metadata needed
// for client-side integrity verification. content is base64 of the [offset,
// offset+len(decoded)) slice; sizeBytes/checksum describe the FULL file.
type WrkqAttachmentBytes struct {
	UUID      string `json:"uuid"`
	ID        string `json:"id"`
	TaskUUID  string `json:"taskUuid"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mimeType,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	Checksum  string `json:"checksum,omitempty"`
	Offset    int64  `json:"offset"`
	Content   string `json:"contentBase64"`
	EOF       bool   `json:"eof"`
}

// AttachmentAddBytesParams uploads one chunk of a stdin/byte attachment. The first
// call omits uploadId (and supplies task/filename/mimeType/actor); the server
// returns an uploadId to echo on subsequent chunks. seq is the 0-based monotonic
// chunk index. final marks the last chunk and triggers finalize (size validation,
// atomic rename into the attach dir, metadata + event). content is base64.
type AttachmentAddBytesParams struct {
	Task           string `json:"task,omitempty"`
	Filename       string `json:"filename,omitempty"`
	MimeType       string `json:"mimeType,omitempty"`
	Actor          string `json:"actor,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	UploadID       string `json:"uploadId,omitempty"`
	Seq            int    `json:"seq"`
	Content        string `json:"contentBase64,omitempty"`
	Final          bool   `json:"final,omitempty"`
}

// WrkqAttachmentAddBytesResult is the per-chunk upload ack. While the upload is
// in progress (not yet final) only uploadId/seq/bytesReceived/committed=false are
// set; the finalizing chunk additionally carries the committed WrkqAttachment.
type WrkqAttachmentAddBytesResult struct {
	UploadID      string          `json:"uploadId"`
	Seq           int             `json:"seq"`
	BytesReceived int64           `json:"bytesReceived"`
	Committed     bool            `json:"committed"`
	Attachment    *WrkqAttachment `json:"attachment,omitempty"`
}

// ─── Relations ───────────────────────────────────────────────────────────────

// WrkqRelation is the stable relation resource DTO. Relations are identified by
// the composite key (fromTask, kind, toTask); there is no synthetic id.
type WrkqRelation struct {
	FromTask  string `json:"fromTask"`
	ToTask    string `json:"toTask"`
	Kind      string `json:"kind"`
	Direction string `json:"direction,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// WrkqRelationListResult is the relation list envelope.
type WrkqRelationListResult struct {
	Items []WrkqRelation `json:"items"`
}

// RelationAddParams mirrors WrkqRelationAddParams.
type RelationAddParams struct {
	FromTask       string `json:"fromTask"`
	Kind           string `json:"kind"`
	ToTask         string `json:"toTask"`
	Actor          string `json:"actor,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// RelationListParams mirrors WrkqRelationListParams.
type RelationListParams struct {
	Task string `json:"task"`
}

// RelationRemoveParams mirrors WrkqRelationRemoveParams. Composite identity; no
// synthetic id.
type RelationRemoveParams struct {
	FromTask string `json:"fromTask"`
	Kind     string `json:"kind"`
	ToTask   string `json:"toTask"`
	Actor    string `json:"actor,omitempty"`
}

// ─── Containers ──────────────────────────────────────────────────────────────

// WrkqContainer is the stable container resource DTO (camelCase; no DB column
// names leak). path is computed.
type WrkqContainer struct {
	UUID       string `json:"uuid"`
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	ParentUUID string `json:"parentUuid,omitempty"`
	Path       string `json:"path"`
	ETag       int64  `json:"etag"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	ArchivedAt string `json:"archivedAt,omitempty"`

	// createdAtRaw holds the un-normalized created_at for cursor anchoring; it is
	// unexported and never serialized.
	createdAtRaw string
}

// WrkqContainerListResult is the container list envelope.
type WrkqContainerListResult struct {
	Items      []WrkqContainer `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// WrkqLegacyActor is the legacy actor display/admin-cache DTO (T-04850, gap2).
// Actor rows are a legacy display/admin cache: they do NOT issue, validate, or
// authorize principals, so the DTO deliberately has NO principalRef field.
type WrkqLegacyActor struct {
	UUID        string         `json:"uuid"`
	ID          string         `json:"id"`
	Slug        string         `json:"slug"`
	DisplayName string         `json:"displayName,omitempty"`
	Role        string         `json:"role"`
	Meta        map[string]any `json:"meta,omitempty"`
	CreatedAt   string         `json:"createdAt"`
	UpdatedAt   string         `json:"updatedAt"`
}

// WrkqLegacyActorListResult is the legacy actor list envelope.
type WrkqLegacyActorListResult struct {
	Items []WrkqLegacyActor `json:"items"`
}

// ActorListParams is the (empty) parameter set for wrkq.admin.legacyActor.list.
type ActorListParams struct{}

// ActorCreateParams mirrors wrkq.admin.legacyActor.create params. Meta is a raw
// message so a non-object value can be rejected with WRKQ_VALIDATION instead of
// being silently coerced.
type ActorCreateParams struct {
	Slug           string          `json:"slug"`
	DisplayName    *string         `json:"displayName,omitempty"`
	Role           string          `json:"role,omitempty"`
	Meta           json.RawMessage `json:"meta,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
}

// ActorUpdateParams mirrors wrkq.admin.legacyActor.update params. Patch is a raw
// message so unknown / immutable fields can be rejected and explicit nulls
// (clear) can be distinguished from omitted fields.
type ActorUpdateParams struct {
	Actor           string          `json:"actor"`
	Patch           json.RawMessage `json:"patch"`
	ExpectUpdatedAt string          `json:"expectUpdatedAt,omitempty"`
	IdempotencyKey  string          `json:"idempotencyKey,omitempty"`
}

// ContainerCreateParams mirrors WrkqContainerCreateParams.
type ContainerCreateParams struct {
	Path       string `json:"path,omitempty"`
	Project    string `json:"project,omitempty"`
	ParentPath string `json:"parentPath,omitempty"`
	Slug       string `json:"slug,omitempty"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Actor      string `json:"actor,omitempty"`
}

// ContainerShowParams mirrors WrkqContainerShowParams.
type ContainerShowParams struct {
	Path    string `json:"path,omitempty"`
	Project string `json:"project,omitempty"`
}

// ContainerListParams mirrors WrkqContainerListParams.
type ContainerListParams struct {
	Project         string `json:"project,omitempty"`
	IncludeArchived bool   `json:"includeArchived,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Cursor          string `json:"cursor,omitempty"`
}

// ContainerDeleteParams mirrors WrkqContainerDeleteParams.
type ContainerDeleteParams struct {
	Container  string `json:"container,omitempty"`
	Path       string `json:"path,omitempty"`
	Project    string `json:"project,omitempty"`
	ExpectETag int64  `json:"expectEtag,omitempty"`
	Actor      string `json:"actor,omitempty"`
}

// WrkqContainerDeleteResult is returned by empty-only hard delete.
type WrkqContainerDeleteResult struct {
	Deleted bool `json:"deleted"`
}

// ContainerDeleteRecursiveExpected carries the destructive impact CAS.
type ContainerDeleteRecursiveExpected struct {
	Containers  int64 `json:"containers"`
	Tasks       int64 `json:"tasks"`
	Attachments int64 `json:"attachments"`
	Bytes       int64 `json:"bytes"`
}

// ContainerDeleteRecursiveParams mirrors WrkqContainerDeleteRecursiveParams.
type ContainerDeleteRecursiveParams struct {
	Container  string                            `json:"container,omitempty"`
	Path       string                            `json:"path,omitempty"`
	Project    string                            `json:"project,omitempty"`
	DryRun     bool                              `json:"dryRun,omitempty"`
	ExpectETag int64                             `json:"expectEtag,omitempty"`
	Expected   *ContainerDeleteRecursiveExpected `json:"expected,omitempty"`
	Actor      string                            `json:"actor,omitempty"`
}

// WrkqContainerDeleteRecursiveResult is used for both dry-run and commit.
type WrkqContainerDeleteRecursiveResult struct {
	Container          *WrkqContainer `json:"container,omitempty"`
	Containers         *int64         `json:"containers,omitempty"`
	Tasks              *int64         `json:"tasks,omitempty"`
	Attachments        *int64         `json:"attachments,omitempty"`
	Bytes              *int64         `json:"bytes,omitempty"`
	Deleted            *bool          `json:"deleted,omitempty"`
	ContainersDeleted  *int64         `json:"containersDeleted,omitempty"`
	TasksDeleted       *int64         `json:"tasksDeleted,omitempty"`
	AttachmentsDeleted *int64         `json:"attachmentsDeleted,omitempty"`
	BytesFreed         *int64         `json:"bytesFreed,omitempty"`
	FileCleanupErrors  []string       `json:"fileCleanupErrors,omitempty"`
}

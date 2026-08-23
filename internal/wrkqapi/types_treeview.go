package wrkqapi

// TreeViewParams mirrors the legacy `wrkq tree [PATH]` traversal surface for one
// root path. Project-root scoping is the CALLER's responsibility: Path is already
// scoped before it reaches this method.
type TreeViewParams struct {
	Path                   string `json:"path,omitempty"`
	MaxDepth               int    `json:"maxDepth,omitempty"`
	IncludeArchived        bool   `json:"includeArchived,omitempty"`
	OpenOnly               bool   `json:"openOnly,omitempty"`
	IncludeCampaignMembers bool   `json:"includeCampaignMembers,omitempty"`
	PromiseState           string `json:"promiseState,omitempty"`
}

// WrkqTreeNode is the server-owned COMPATIBILITY projection of one tree node. Its
// exported-field/json-tag shape EXACTLY reproduces the legacy `treeNode` so the
// mirror CLI can render byte-identical `tree --json` output by marshaling these
// straight through. The two wire-only fields the legacy treeNode hides from JSON
// (`created_at` via json:"-", and the unexported parentTaskUUID) are carried here
// under distinct wire tags (wire_created_at / wire_parent_task_uuid) so the CLI
// can reconstruct the NDJSON stream + nesting without a second RPC; the CLI strips
// them before rendering the JSON mode. Not a canonical resource.
type WrkqTreeNode struct {
	Type                 string          `json:"type"`
	ID                   string          `json:"id"`
	Slug                 string          `json:"slug"`
	Title                string          `json:"title"`
	State                string          `json:"state,omitempty"`
	UUID                 string          `json:"uuid"`
	RequestedByProjectID *string         `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string         `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string         `json:"acknowledged_at,omitempty"`
	Resolution           *string         `json:"resolution,omitempty"`
	IsArchived           bool            `json:"is_archived"`
	IsDeleted            bool            `json:"is_deleted"`
	AllTasksCompleted    bool            `json:"all_tasks_completed,omitempty"`
	Promises             []WrkqPromise   `json:"promises"`
	Children             []*WrkqTreeNode `json:"children,omitempty"`
	ExternalChildren     []*WrkqTreeNode `json:"external_children,omitempty"`
	ExternalBacklink     bool            `json:"external_backlink,omitempty"`
	ExternalProjectID    string          `json:"external_project_id,omitempty"`
	ExternalPath         string          `json:"external_path,omitempty"`

	// Wire-only carriers (legacy hides these from `tree --json`). The CLI uses them
	// to build NDJSON stream entries + subtask nesting, then drops them for JSON.
	WireCreatedAt      string `json:"wire_created_at,omitempty"`
	WireParentTaskUUID string `json:"wire_parent_task_uuid,omitempty"`

	// hasVisibleContent / hiddenContainerCount are server-internal traversal state,
	// not part of the wire contract.
	hasVisibleTasks      bool
	hasVisibleContent    bool
	hiddenContainerCount int
}

// WrkqTreeView is the server-owned COMPATIBILITY tree projection for `wrkq tree`.
// It owns the full recursive traversal: container pruning, "all done" rollups,
// subtask nesting, and hidden-container counting. The CLI owns ONLY byte
// rendering (pretty/porcelain/json/ndjson). Not a canonical resource.
type WrkqTreeView struct {
	Path                         string          `json:"path"`
	ProjectID                    string          `json:"project_id,omitempty"`
	Children                     []*WrkqTreeNode `json:"children"`
	Promises                     []WrkqPromise   `json:"promises"`
	HiddenContainersNotDisplayed int             `json:"hidden_containers_not_displayed"`

	// WireRawPath carries the UN-normalized request path (empty for the root view)
	// so the CLI can reproduce legacy's NDJSON stream, which joins paths from the
	// RAW rootPath while the JSON `path` field shows the normalized "." display
	// form. Not part of the JSON `tree` projection (the CLI strips it).
	WireRawPath string `json:"wire_raw_path,omitempty"`
}

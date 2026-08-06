package wrkqapi

// ContainerTaskCountsParams selects the container rows returned by
// wrkq.container.taskCounts. Counts always cover the complete descendant
// subtree, including archived descendant containers. IncludeArchived controls
// only whether an archived container receives its own result row.
type ContainerTaskCountsParams struct {
	IncludeArchived bool `json:"includeArchived,omitempty"`
}

// WrkqContainerTaskCount is one producer-owned subtree rollup. Container UUID,
// friendly ID, and path are all included so consumers can join by durable
// identity or their existing path-keyed tree model. Project identity is empty
// only for a non-project container that has no project ancestor.
type WrkqContainerTaskCount struct {
	UUID            string  `json:"uuid"`
	ID              string  `json:"id"`
	Path            string  `json:"path"`
	Kind            string  `json:"kind"`
	ProjectUUID     string  `json:"projectUuid,omitempty"`
	ProjectID       string  `json:"projectId,omitempty"`
	ProjectSlug     string  `json:"projectSlug,omitempty"`
	ArchivedAt      *string `json:"archivedAt,omitempty"`
	TotalTaskCount  int     `json:"totalTaskCount"`
	ActiveTaskCount int     `json:"activeTaskCount"`
}

// WrkqContainerTaskCounts is a complete, unpaginated aggregate snapshot.
type WrkqContainerTaskCounts struct {
	Items []WrkqContainerTaskCount `json:"items"`
}

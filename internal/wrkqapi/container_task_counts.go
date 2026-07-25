package wrkqapi

import (
	"context"
	"database/sql"
)

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

// ContainerTaskCounts returns every selected non-root container's subtree task
// counts in one SQL statement under one read transaction. The recursive closure
// follows container residency (tasks.project_uuid), not parent_task_uuid or
// campaign enrollment. This is the same ancestry rule as wrkq.task.lsView.
//
// Total excludes deleted tasks but includes all other states, including archived
// and terminal tasks. Active is idea/draft/open/in_progress/blocked and also
// requires archived_at/deleted_at to be clear.
func (a *API) ContainerTaskCounts(
	ctx context.Context,
	p ContainerTaskCountsParams,
) (*WrkqContainerTaskCounts, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tx, err := a.db.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	archivedFilter := ""
	if !p.IncludeArchived {
		archivedFilter = " AND c.archived_at IS NULL"
	}
	rows, err := tx.QueryContext(ctx, `
		WITH RECURSIVE
		descendants(ancestor_uuid, descendant_uuid) AS (
			SELECT uuid, uuid
			  FROM containers
			UNION ALL
			SELECT d.ancestor_uuid, child.uuid
			  FROM descendants d
			  JOIN containers child ON child.parent_uuid = d.descendant_uuid
		),
		container_projects(container_uuid, ancestor_uuid, project_uuid) AS (
			SELECT uuid, uuid, CASE WHEN kind = 'project' THEN uuid END
			  FROM containers
			UNION ALL
			SELECT cp.container_uuid, parent.uuid,
			       CASE WHEN parent.kind = 'project' THEN parent.uuid END
			  FROM container_projects cp
			  JOIN containers current ON current.uuid = cp.ancestor_uuid
			  JOIN containers parent ON parent.uuid = current.parent_uuid
			 WHERE cp.project_uuid IS NULL
		),
		project_for AS (
			SELECT container_uuid, MAX(project_uuid) AS project_uuid
			  FROM container_projects
			 GROUP BY container_uuid
		)
		SELECT c.uuid, c.id, COALESCE(paths.path, c.slug), c.kind,
		       project.uuid, project.id, project.slug, c.archived_at,
		       COUNT(CASE
		               WHEN t.state != 'deleted' AND t.deleted_at IS NULL THEN 1
		             END) AS total_task_count,
		       COUNT(CASE
		               WHEN t.state IN ('idea','draft','open','in_progress','blocked')
		                AND t.archived_at IS NULL
		                AND t.deleted_at IS NULL
		               THEN 1
		             END) AS active_task_count
		  FROM containers c
		  LEFT JOIN v_container_paths paths ON paths.uuid = c.uuid
		  LEFT JOIN descendants d ON d.ancestor_uuid = c.uuid
		  LEFT JOIN tasks t ON t.project_uuid = d.descendant_uuid
		  LEFT JOIN project_for pf ON pf.container_uuid = c.uuid
		  LEFT JOIN containers project ON project.uuid = pf.project_uuid
		 WHERE c.kind != 'root'`+archivedFilter+`
		 GROUP BY c.uuid, c.id, paths.path, c.slug, c.kind,
		          project.uuid, project.id, project.slug, c.archived_at
		 ORDER BY COALESCE(paths.path, c.slug) ASC, c.uuid ASC
	`)
	if err != nil {
		return nil, NewInternalError(err)
	}

	items := []WrkqContainerTaskCount{}
	for rows.Next() {
		var item WrkqContainerTaskCount
		var projectUUID, projectID, projectSlug, archivedAt sql.NullString
		if err := rows.Scan(
			&item.UUID,
			&item.ID,
			&item.Path,
			&item.Kind,
			&projectUUID,
			&projectID,
			&projectSlug,
			&archivedAt,
			&item.TotalTaskCount,
			&item.ActiveTaskCount,
		); err != nil {
			_ = rows.Close()
			return nil, NewInternalError(err)
		}
		item.ProjectUUID = projectUUID.String
		item.ProjectID = projectID.String
		item.ProjectSlug = projectSlug.String
		if archivedAt.Valid {
			item.ArchivedAt = &archivedAt.String
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, NewInternalError(err)
	}
	if err := rows.Close(); err != nil {
		return nil, NewInternalError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, NewInternalError(err)
	}
	return &WrkqContainerTaskCounts{Items: items}, nil
}

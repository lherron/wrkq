package wrkqapi

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/store"
)

type campaignAggregate struct {
	TotalMembers        int
	StateCounts         map[string]int
	ResidentCount       int
	EnrolledCount       int
	InProgressCount     int
	MissingOutcomeCount int
	Footprint           []WrkqCampaignFootprint
	LastActivityAt      string
}

// ContainerCampaignPortfolio returns the complete selected campaign aggregate
// under one SQLite read transaction. Campaign volume is deliberately small; the
// contract has no cursor or materialized projection.
func (a *API) ContainerCampaignPortfolio(
	ctx context.Context,
	p ContainerCampaignPortfolioParams,
) (*WrkqCampaignPortfolio, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	states, err := normalizeCampaignPortfolioStates(p.States)
	if err != nil {
		return nil, err
	}

	tx, err := a.db.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	placeholders := make([]string, len(states))
	args := make([]any, 0, len(states))
	for index, state := range states {
		placeholders[index] = "?"
		args = append(args, state)
	}
	where := "c.campaign_state IN (" + strings.Join(placeholders, ",") + ")"
	if !p.IncludeArchived {
		where += " AND c.archived_at IS NULL"
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT c.uuid, c.id, c.slug, c.title, c.description, c.specification,
		       c.labels, c.campaign_state, c.kind, c.parent_uuid, c.etag,
		       c.created_at, c.updated_at, c.archived_at, COALESCE(v.path, c.slug)
		  FROM containers c
		  LEFT JOIN v_container_paths v ON v.uuid = c.uuid
		 WHERE `+where+`
		 ORDER BY c.created_at DESC, c.uuid ASC
	`, args...)
	if err != nil {
		return nil, NewInternalError(err)
	}

	containers := []WrkqContainer{}
	for rows.Next() {
		container, scanErr := scanContainerRow(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, NewInternalError(scanErr)
		}
		containers = append(containers, *container)
	}
	if err := rows.Close(); err != nil {
		return nil, NewInternalError(err)
	}

	items := make([]WrkqCampaignPortfolioRow, 0, len(containers))
	for _, container := range containers {
		aggregate, loadErr := loadCampaignAggregateTx(ctx, tx, container.UUID, container.UpdatedAt)
		if loadErr != nil {
			return nil, loadErr
		}
		items = append(items, WrkqCampaignPortfolioRow{
			Container:           container,
			TotalMembers:        aggregate.TotalMembers,
			StateCounts:         aggregate.StateCounts,
			ResidentCount:       aggregate.ResidentCount,
			EnrolledCount:       aggregate.EnrolledCount,
			InProgressCount:     aggregate.InProgressCount,
			MissingOutcomeCount: aggregate.MissingOutcomeCount,
			Footprint:           aggregate.Footprint,
			LastActivityAt:      aggregate.LastActivityAt,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, NewInternalError(err)
	}
	return &WrkqCampaignPortfolio{Items: items}, nil
}

func normalizeCampaignPortfolioStates(input []string) ([]string, error) {
	if len(input) == 0 {
		return []string{store.CampaignStateDraft, store.CampaignStateActive}, nil
	}
	allowed := map[string]bool{
		store.CampaignStateDraft: true, store.CampaignStateActive: true,
		store.CampaignStateCompleted: true, store.CampaignStateCancelled: true,
	}
	seen := map[string]bool{}
	states := make([]string, 0, len(input))
	for _, raw := range input {
		state := strings.TrimSpace(raw)
		if !allowed[state] {
			return nil, NewValidationError(
				"campaign state must be draft, active, completed, or cancelled",
				map[string]any{"field": "states", "state": raw},
			)
		}
		if !seen[state] {
			seen[state] = true
			states = append(states, state)
		}
	}
	return states, nil
}

func loadCampaignAggregateTx(
	ctx context.Context,
	tx *sql.Tx,
	campaignUUID, campaignUpdatedAt string,
) (campaignAggregate, error) {
	rows, err := tx.QueryContext(ctx, `
		WITH RECURSIVE
		member_tasks AS (
			SELECT t.uuid, t.state, t.outcome, t.updated_at, t.project_uuid,
			       CASE WHEN t.project_uuid = ? THEN 'resident' ELSE 'enrolled' END AS membership
			  FROM tasks t
			 WHERE t.project_uuid = ? OR t.campaign_uuid = ?
		),
		ancestors(task_uuid, uuid, id, slug, title, kind, parent_uuid) AS (
			SELECT m.uuid, c.uuid, c.id, c.slug, c.title, c.kind, c.parent_uuid
			  FROM member_tasks m
			  JOIN containers c ON c.uuid = m.project_uuid
			UNION ALL
			SELECT a.task_uuid, p.uuid, p.id, p.slug, p.title, p.kind, p.parent_uuid
			  FROM ancestors a
			  JOIN containers p ON p.uuid = a.parent_uuid
		),
		projects AS (
			SELECT task_uuid, uuid, id, slug, title
			  FROM ancestors
			 WHERE kind = 'project'
		)
		SELECT m.state, m.outcome, m.updated_at, m.membership,
		       p.uuid, p.id, p.slug, p.title
		  FROM member_tasks m
		  JOIN projects p ON p.task_uuid = m.uuid
		 ORDER BY m.uuid
	`, campaignUUID, campaignUUID, campaignUUID)
	if err != nil {
		return campaignAggregate{}, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()

	aggregate := campaignAggregate{
		StateCounts:    map[string]int{},
		LastActivityAt: campaignUpdatedAt,
	}
	footprintCounts := map[string]*WrkqCampaignFootprint{}
	for rows.Next() {
		var state, updatedAt, membership string
		var outcome sql.NullString
		var project WrkqCampaignProject
		if err := rows.Scan(
			&state, &outcome, &updatedAt, &membership,
			&project.UUID, &project.ID, &project.Slug, &project.Title,
		); err != nil {
			return campaignAggregate{}, NewInternalError(err)
		}
		aggregate.TotalMembers++
		aggregate.StateCounts[state]++
		if membership == "resident" {
			aggregate.ResidentCount++
		} else {
			aggregate.EnrolledCount++
		}
		if state == "in_progress" {
			aggregate.InProgressCount++
		}
		if state == "completed" && (!outcome.Valid || strings.TrimSpace(outcome.String) == "") {
			aggregate.MissingOutcomeCount++
		}
		aggregate.LastActivityAt = maxTimestamp(aggregate.LastActivityAt, toRFC3339(updatedAt))
		entry := footprintCounts[project.UUID]
		if entry == nil {
			entry = &WrkqCampaignFootprint{Project: project}
			footprintCounts[project.UUID] = entry
		}
		entry.MemberCount++
	}
	if err := rows.Err(); err != nil {
		return campaignAggregate{}, NewInternalError(err)
	}
	aggregate.Footprint = sortedCampaignFootprint(footprintCounts)
	return aggregate, nil
}

func sortedCampaignFootprint(entries map[string]*WrkqCampaignFootprint) []WrkqCampaignFootprint {
	out := make([]WrkqCampaignFootprint, 0, len(entries))
	for _, entry := range entries {
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MemberCount != out[j].MemberCount {
			return out[i].MemberCount > out[j].MemberCount
		}
		if out[i].Project.Slug != out[j].Project.Slug {
			return out[i].Project.Slug < out[j].Project.Slug
		}
		return out[i].Project.UUID < out[j].Project.UUID
	})
	return out
}

func maxTimestamp(values ...string) string {
	var max string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value > max {
			max = value
		}
	}
	return max
}

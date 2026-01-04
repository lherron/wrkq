package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

var checkInboxCmd = &cobra.Command{
	Use:   "check-inbox",
	Short: "List open tasks in the inbox",
	Long: `List all tasks in the inbox container that are in the open state, plus
tasks requested by the current project that are awaiting acknowledgment.

This is a convenience command equivalent to:
  wrkq find inbox/** --type t --state open
  wrkq find --requested-by <project> --ack-pending

If WRKQ_PROJECT_ROOT is set, the inbox path is scoped to that project.

Examples:
  wrkq check-inbox              # List open inbox tasks
  wrkq check-inbox --json       # Output as JSON
  wrkq check-inbox --ndjson     # Output as newline-delimited JSON
`,
	RunE: appctx.WithApp(appctx.DefaultOptions(), runCheckInbox),
}

var (
	checkInboxJSON      bool
	checkInboxNDJSON    bool
	checkInboxPorcelain bool
)

func init() {
	rootCmd.AddCommand(checkInboxCmd)

	checkInboxCmd.Flags().BoolVar(&checkInboxJSON, "json", false, "Output as JSON")
	checkInboxCmd.Flags().BoolVar(&checkInboxNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	checkInboxCmd.Flags().BoolVar(&checkInboxPorcelain, "porcelain", false, "Stable machine-readable output")
}

type inboxTask struct {
	Type                 string  `json:"type"`
	UUID                 string  `json:"uuid"`
	ID                   string  `json:"id"`
	Slug                 string  `json:"slug"`
	Title                string  `json:"title"`
	Path                 string  `json:"path"`
	State                *string `json:"state,omitempty"`
	Priority             *int    `json:"priority,omitempty"`
	Kind                 *string `json:"kind,omitempty"`
	DueAt                *string `json:"due_at,omitempty"`
	RequestedByProjectID *string `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string `json:"acknowledged_at,omitempty"`
	Resolution           *string `json:"resolution,omitempty"`
	ETag                 int64   `json:"etag"`
}

func runCheckInbox(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	inboxPath := applyProjectRootToPath(app.Config, "inbox", false)
	pathLike := inboxPath + "/%"

	// Query for open tasks in the inbox container
	query := `
		SELECT t.uuid, t.id, t.slug, t.title, t.state, t.priority, t.kind, t.due_at, t.etag,
		       t.requested_by_project_id, t.assigned_project_id, t.acknowledged_at, t.resolution,
		       cp.path || '/' || t.slug AS path
		FROM tasks t
		JOIN v_container_paths cp ON cp.uuid = t.project_uuid
		WHERE t.state = 'open'
		  AND (cp.path = ? OR cp.path LIKE ?)
		ORDER BY t.priority ASC, t.created_at ASC
	`

	rows, err := database.Query(query, inboxPath, pathLike)
	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	results := []inboxTask{}
	for rows.Next() {
		var r inboxTask
		var state, kind, dueAt sql.NullString
		var requestedBy, assignedProject, acknowledgedAt, resolution sql.NullString
		var priority sql.NullInt64

		err := rows.Scan(&r.UUID, &r.ID, &r.Slug, &r.Title, &state, &priority, &kind, &dueAt, &r.ETag,
			&requestedBy, &assignedProject, &acknowledgedAt, &resolution, &r.Path)
		if err != nil {
			return fmt.Errorf("scan failed: %w", err)
		}

		r.Type = "task"
		if state.Valid {
			r.State = &state.String
		}
		if priority.Valid {
			p := int(priority.Int64)
			r.Priority = &p
		}
		if kind.Valid {
			r.Kind = &kind.String
		}
		if dueAt.Valid {
			r.DueAt = &dueAt.String
		}
		if requestedBy.Valid {
			r.RequestedByProjectID = &requestedBy.String
		}
		if assignedProject.Valid {
			r.AssignedProjectID = &assignedProject.String
		}
		if acknowledgedAt.Valid {
			r.AcknowledgedAt = &acknowledgedAt.String
		}
		if resolution.Valid {
			r.Resolution = &resolution.String
		}

		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	projectID := normalizeProjectRoot(app.Config)
	if projectID != "" {
		ackQuery := `
			SELECT t.uuid, t.id, t.slug, t.title, t.state, t.priority, t.kind, t.due_at, t.etag,
			       t.requested_by_project_id, t.assigned_project_id, t.acknowledged_at, t.resolution,
			       cp.path || '/' || t.slug AS path
			FROM tasks t
			JOIN v_container_paths cp ON cp.uuid = t.project_uuid
			WHERE t.requested_by_project_id = ?
			  AND t.state IN ('completed', 'cancelled')
			  AND t.acknowledged_at IS NULL
			ORDER BY t.updated_at DESC
		`

		ackRows, err := database.Query(ackQuery, projectID)
		if err != nil {
			return fmt.Errorf("ack query failed: %w", err)
		}
		defer ackRows.Close()

		for ackRows.Next() {
			var r inboxTask
			var state, kind, dueAt sql.NullString
			var requestedBy, assignedProject, acknowledgedAt, resolution sql.NullString
			var priority sql.NullInt64

			err := ackRows.Scan(&r.UUID, &r.ID, &r.Slug, &r.Title, &state, &priority, &kind, &dueAt, &r.ETag,
				&requestedBy, &assignedProject, &acknowledgedAt, &resolution, &r.Path)
			if err != nil {
				return fmt.Errorf("ack scan failed: %w", err)
			}

			r.Type = "task"
			if state.Valid {
				r.State = &state.String
			}
			if priority.Valid {
				p := int(priority.Int64)
				r.Priority = &p
			}
			if kind.Valid {
				r.Kind = &kind.String
			}
			if dueAt.Valid {
				r.DueAt = &dueAt.String
			}
			if requestedBy.Valid {
				r.RequestedByProjectID = &requestedBy.String
			}
			if assignedProject.Valid {
				r.AssignedProjectID = &assignedProject.String
			}
			if acknowledgedAt.Valid {
				r.AcknowledgedAt = &acknowledgedAt.String
			}
			if resolution.Valid {
				r.Resolution = &resolution.String
			}

			results = append(results, r)
		}

		if err := ackRows.Err(); err != nil {
			return err
		}
	}

	// Render output
	if checkInboxJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(results)
	}
	if checkInboxNDJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		for _, entry := range results {
			if err := encoder.Encode(entry); err != nil {
				return err
			}
		}
		return nil
	}

	// Table output
	headers := []string{"ID", "Slug", "Title", "State", "Priority", "Kind"}
	var rowsData [][]string
	for _, entry := range results {
		priority := ""
		if entry.Priority != nil {
			priority = strconv.Itoa(*entry.Priority)
		}
		kind := ""
		if entry.Kind != nil {
			kind = *entry.Kind
		}
		state := ""
		if entry.State != nil {
			state = *entry.State
		}
		rowsData = append(rowsData, []string{
			entry.ID,
			entry.Slug,
			entry.Title,
			state,
			priority,
			kind,
		})
	}

	r := render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Format:    render.FormatTable,
		Porcelain: checkInboxPorcelain,
	})

	return r.RenderTable(headers, rowsData)
}

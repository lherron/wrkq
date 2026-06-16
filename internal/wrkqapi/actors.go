package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/paths"
)

const (
	nsActorCreate = "wrkq.admin.legacyActor.create"
	nsActorUpdate = "wrkq.admin.legacyActor.update"
)

// validActorRoles is the closed set of roles accepted by the legacy actor cache.
var validActorRoles = map[string]bool{"human": true, "agent": true, "system": true}

// ActorList returns all legacy actor rows as WrkqLegacyActor DTOs.
//
// Actors are a legacy display/admin cache: this method never issues or validates
// principals and is registered only under the explicit wrkq.admin.legacyActor.*
// namespace.
func (a *API) ActorList(ctx context.Context, _ ActorListParams) (*WrkqLegacyActorListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolver := actors.NewResolver(a.db.DB)
	list, err := resolver.List()
	if err != nil {
		return nil, NewInternalError(err)
	}
	items := make([]WrkqLegacyActor, 0, len(list))
	for _, actor := range list {
		items = append(items, legacyActorDTO(actor))
	}
	return &WrkqLegacyActorListResult{Items: items}, nil
}

// ActorCreate persists a new legacy actor row and logs an actor.created event.
func (a *API) ActorCreate(ctx context.Context, p ActorCreateParams) (*WrkqLegacyActor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	slug := strings.TrimSpace(p.Slug)
	if slug == "" {
		return nil, NewValidationError("slug is required", map[string]any{"field": "slug"})
	}
	if err := paths.ValidateSlug(slug); err != nil {
		return nil, NewValidationError(err.Error(), map[string]any{"field": "slug"})
	}

	role := strings.TrimSpace(p.Role)
	if role == "" {
		role = "agent"
	}
	if !validActorRoles[role] {
		return nil, NewValidationError("role must be one of human, agent, system", map[string]any{"field": "role"})
	}

	metaStr, err := metaJSONFromRaw(p.Meta)
	if err != nil {
		return nil, err
	}

	var requestHash string
	if p.IdempotencyKey != "" {
		hp := p
		hp.IdempotencyKey = ""
		requestHash = canonicalRequestHash(hp)
		if raw, ok, rerr := a.idempotentReplay(nsActorCreate, p.IdempotencyKey, requestHash); rerr != nil {
			return nil, rerr
		} else if ok {
			var dto WrkqLegacyActor
			if uerr := json.Unmarshal(raw, &dto); uerr != nil {
				return nil, NewInternalError(uerr)
			}
			return &dto, nil
		}
	}

	var displayName any
	if p.DisplayName != nil {
		displayName = *p.DisplayName
	}
	var metaBind any
	if metaStr != nil {
		metaBind = *metaStr
	}

	attr := a.attributionFor("")

	tx, err := a.db.Begin()
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	res, ierr := tx.Exec(
		`INSERT INTO actors (id, slug, display_name, role, meta) VALUES ('', ?, ?, ?, ?)`,
		slug, displayName, role, metaBind,
	)
	if ierr != nil {
		if isUniqueConstraint(ierr) {
			return nil, NewConflictError("actor slug already exists", map[string]any{"slug": slug})
		}
		return nil, NewInternalError(ierr)
	}
	rowID, ierr := res.LastInsertId()
	if ierr != nil {
		return nil, NewInternalError(ierr)
	}
	var newUUID string
	if serr := tx.QueryRow("SELECT uuid FROM actors WHERE rowid = ?", rowID).Scan(&newUUID); serr != nil {
		return nil, NewInternalError(serr)
	}

	payload := `{"slug":` + jsonString(slug) + `,"role":` + jsonString(role) + `}`
	if eerr := events.NewWriter(a.db.DB).LogEvent(tx, &domain.Event{
		ActorUUID:    attr.LegacyActorUUID,
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "actor",
		ResourceUUID: &newUUID,
		EventType:    "actor.created",
		Payload:      &payload,
	}); eerr != nil {
		return nil, NewInternalError(eerr)
	}

	if cerr := tx.Commit(); cerr != nil {
		return nil, NewInternalError(cerr)
	}

	dto, err := a.loadLegacyActor(newUUID)
	if err != nil {
		return nil, err
	}
	if p.IdempotencyKey != "" {
		if serr := a.idempotentStore(nsActorCreate, p.IdempotencyKey, requestHash, dto); serr != nil {
			return nil, serr
		}
	}
	return dto, nil
}

// ActorUpdate patches a legacy actor row and logs an actor.updated event.
//
// slug/id/uuid/createdAt are immutable; unknown patch fields and immutable-field
// patches return WRKQ_VALIDATION. A stale expectUpdatedAt returns WRKQ_CONFLICT.
// patch:{meta:null} clears meta.
func (a *API) ActorUpdate(ctx context.Context, p ActorUpdateParams) (*WrkqLegacyActor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	selector := strings.TrimSpace(p.Actor)
	if selector == "" {
		return nil, NewValidationError("actor is required", map[string]any{"field": "actor"})
	}

	// Parse and validate the patch before resolving so malformed patches fail
	// fast with WRKQ_VALIDATION.
	patch, err := parseActorPatch(p.Patch)
	if err != nil {
		return nil, err
	}

	resolver := actors.NewResolver(a.db.DB)
	actorUUID, rerr := resolver.Resolve(selector)
	if rerr != nil {
		return nil, NewNotFoundError(selector, "actor")
	}

	// Resolve() returns a UUID-shaped selector unchecked, so verify existence
	// and read the current updated_at for the CAS comparison.
	var currentUpdatedAt string
	if serr := a.db.QueryRow("SELECT updated_at FROM actors WHERE uuid = ?", actorUUID).Scan(&currentUpdatedAt); serr != nil {
		if serr == sql.ErrNoRows {
			return nil, NewNotFoundError(selector, "actor")
		}
		return nil, NewInternalError(serr)
	}

	if expect := strings.TrimSpace(p.ExpectUpdatedAt); expect != "" {
		if toRFC3339(expect) != toRFC3339(currentUpdatedAt) {
			return nil, NewConflictError("actor updatedAt precondition failed", map[string]any{
				"currentUpdatedAt": toRFC3339(currentUpdatedAt),
			})
		}
	}

	var requestHash string
	if p.IdempotencyKey != "" {
		hp := p
		hp.IdempotencyKey = ""
		requestHash = canonicalRequestHash(hp)
		if raw, ok, replayErr := a.idempotentReplay(nsActorUpdate, p.IdempotencyKey, requestHash); replayErr != nil {
			return nil, replayErr
		} else if ok {
			var dto WrkqLegacyActor
			if uerr := json.Unmarshal(raw, &dto); uerr != nil {
				return nil, NewInternalError(uerr)
			}
			return &dto, nil
		}
	}

	attr := a.attributionFor("")

	tx, err := a.db.Begin()
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	if len(patch.setClauses) > 0 {
		query := "UPDATE actors SET " + strings.Join(patch.setClauses, ", ") + " WHERE uuid = ?"
		args := append(append([]any{}, patch.args...), actorUUID)
		if _, uerr := tx.Exec(query, args...); uerr != nil {
			return nil, NewInternalError(uerr)
		}
	}

	payload := `{"actor":` + jsonString(actorUUID) + `}`
	if eerr := events.NewWriter(a.db.DB).LogEvent(tx, &domain.Event{
		ActorUUID:    attr.LegacyActorUUID,
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "actor",
		ResourceUUID: &actorUUID,
		EventType:    "actor.updated",
		Payload:      &payload,
	}); eerr != nil {
		return nil, NewInternalError(eerr)
	}

	if cerr := tx.Commit(); cerr != nil {
		return nil, NewInternalError(cerr)
	}

	dto, err := a.loadLegacyActor(actorUUID)
	if err != nil {
		return nil, err
	}
	if p.IdempotencyKey != "" {
		if serr := a.idempotentStore(nsActorUpdate, p.IdempotencyKey, requestHash, dto); serr != nil {
			return nil, serr
		}
	}
	return dto, nil
}

// actorPatch carries the validated SET clauses + bind args for an actor update.
type actorPatch struct {
	setClauses []string
	args       []any
}

// parseActorPatch validates the raw patch object. Only displayName, role, and
// meta are mutable; any other key (including the immutable slug/id/uuid/createdAt
// and unknown fields) returns WRKQ_VALIDATION.
func parseActorPatch(raw json.RawMessage) (*actorPatch, error) {
	out := &actorPatch{}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, NewValidationError("patch is required", map[string]any{"field": "patch"})
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, NewValidationError("patch must be an object", map[string]any{"field": "patch"})
	}

	for key, value := range fields {
		switch key {
		case "displayName":
			if isJSONNull(value) {
				out.setClauses = append(out.setClauses, "display_name = NULL")
				continue
			}
			var s string
			if err := json.Unmarshal(value, &s); err != nil {
				return nil, NewValidationError("displayName must be a string or null", map[string]any{"field": "displayName"})
			}
			out.setClauses = append(out.setClauses, "display_name = ?")
			out.args = append(out.args, s)
		case "role":
			var r string
			if err := json.Unmarshal(value, &r); err != nil {
				return nil, NewValidationError("role must be a string", map[string]any{"field": "role"})
			}
			if !validActorRoles[r] {
				return nil, NewValidationError("role must be one of human, agent, system", map[string]any{"field": "role"})
			}
			out.setClauses = append(out.setClauses, "role = ?")
			out.args = append(out.args, r)
		case "meta":
			if isJSONNull(value) {
				out.setClauses = append(out.setClauses, "meta = NULL")
				continue
			}
			metaStr, err := metaJSONFromRaw(value)
			if err != nil {
				return nil, err
			}
			out.setClauses = append(out.setClauses, "meta = ?")
			out.args = append(out.args, *metaStr)
		default:
			return nil, NewValidationError("unknown or immutable patch field: "+key, map[string]any{"field": key})
		}
	}
	return out, nil
}

// metaJSONFromRaw validates that a raw meta value is a JSON object and returns
// its canonical string form. Absent (nil/empty/"null") meta returns (nil, nil).
// Any non-object value returns WRKQ_VALIDATION.
func metaJSONFromRaw(raw json.RawMessage) (*string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, NewValidationError("meta must be a JSON object", map[string]any{"field": "meta"})
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, NewInternalError(err)
	}
	s := string(b)
	return &s, nil
}

// loadLegacyActor reads an actor by UUID into a WrkqLegacyActor DTO.
func (a *API) loadLegacyActor(actorUUID string) (*WrkqLegacyActor, error) {
	resolver := actors.NewResolver(a.db.DB)
	actor, err := resolver.GetByUUID(actorUUID)
	if err != nil {
		return nil, NewNotFoundError(actorUUID, "actor")
	}
	dto := legacyActorDTO(actor)
	return &dto, nil
}

// legacyActorDTO maps a domain.Actor onto the camelCase WrkqLegacyActor DTO.
func legacyActorDTO(actor *domain.Actor) WrkqLegacyActor {
	dto := WrkqLegacyActor{
		UUID:      actor.UUID,
		ID:        actor.ID,
		Slug:      actor.Slug,
		Role:      actor.Role,
		CreatedAt: actor.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: actor.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if actor.DisplayName != nil {
		dto.DisplayName = *actor.DisplayName
	}
	if actor.Meta != nil && strings.TrimSpace(*actor.Meta) != "" {
		dto.Meta = parseMeta(*actor.Meta)
	}
	return dto
}

// isJSONNull reports whether a raw JSON value is the literal null.
func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// isUniqueConstraint reports whether err is a SQLite UNIQUE constraint failure.
func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint")
}

// jsonString marshals s to a JSON string literal (with surrounding quotes) for
// inline payload construction.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

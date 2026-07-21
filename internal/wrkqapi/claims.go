package wrkqapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/nodeauth"
	"github.com/lherron/wrkq/internal/scope"
)

type taskClaimRow struct {
	taskID         string
	state          string
	claimedBy      sql.NullString
	claimedScope   sql.NullString
	claimedNode    sql.NullString
	claimedAt      sql.NullString
	generation     int64
	claimTokenHash sql.NullString
	etag           int64
}

func (a *API) TaskClaim(ctx context.Context, p TaskClaimParams) (*WrkqTaskClaim, error) {
	nodeID, ok := nodeauth.FromContext(ctx)
	if !ok {
		return nil, NewNodeIdentityError()
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}
	attr, parsedScope, err := a.claimAttribution(p.PrincipalRef, p.Scope, "scope")
	if err != nil {
		return nil, err
	}

	token, tokenHash, err := mintClaimToken()
	if err != nil {
		return nil, NewInternalError(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := a.db.BeginTx()
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	row, err := loadTaskClaimRow(tx, uuid)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Task, "task")
		}
		return nil, NewInternalError(err)
	}
	if parsedScope.TaskID != row.taskID {
		return nil, NewValidationError("claim scope must name the claimed task", map[string]any{
			"field": "scope", "scopeTask": parsedScope.TaskID, "task": row.taskID,
		})
	}
	if !isClaimableState(row.state) {
		return nil, NewWrongStateError(map[string]any{"task": row.taskID, "state": row.state})
	}
	if row.claimedBy.Valid && !p.TakeOver {
		return nil, NewAlreadyClaimedError(claimHolderData(row))
	}

	newGeneration := row.generation + 1
	result, err := tx.Exec(`
		UPDATE tasks
		SET state = 'in_progress', claimed_by_principal_ref = ?, claimed_scope_ref = ?,
		    claimed_node = ?, claimed_at = ?, claim_generation = ?, claim_token_hash = ?,
		    updated_by_principal_ref = ?, updated_by_scope_ref = ?
		WHERE uuid = ? AND claim_generation = ?
	`, attr.PrincipalRef, attr.ScopeRef, nodeID, now, newGeneration, tokenHash,
		attr.PrincipalRef, attr.ScopeRef, uuid, row.generation)
	if err != nil {
		return nil, NewInternalError(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return nil, NewInternalError(err)
		}
		return nil, NewConflictError("claim generation changed during acquisition", map[string]any{"task": row.taskID})
	}
	var etag int64
	if err := tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", uuid).Scan(&etag); err != nil {
		return nil, NewInternalError(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"action": "claimed", "claimed_by": attr.PrincipalRef, "claimed_scope": attr.ScopeRef,
		"claimed_node": nodeID, "claim_generation": newGeneration, "take_over": p.TakeOver,
	})
	payloadText := string(payload)
	if _, err := events.NewWriter(a.db.DB).LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef, ScopeRef: attr.ScopeRef, ResourceType: "task",
		ResourceUUID: &uuid, EventType: "task.claimed", ETag: &etag, Payload: &payloadText,
	}); err != nil {
		return nil, NewInternalError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, NewInternalError(err)
	}
	return &WrkqTaskClaim{
		Task: row.taskID, ClaimedBy: attr.PrincipalRef, ClaimedScope: attr.ScopeRef,
		ClaimedNode: nodeID, ClaimedAt: now, ClaimGeneration: newGeneration, ClaimToken: token,
	}, nil
}

func (a *API) TaskClaimValidate(ctx context.Context, p TaskClaimValidateParams) (*WrkqTaskClaim, error) {
	nodeID, ok := nodeauth.FromContext(ctx)
	if !ok {
		return nil, NewNodeIdentityError()
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}
	attr, parsedScope, err := a.claimAttribution(p.PrincipalRef, p.Scope, "scope")
	if err != nil {
		return nil, err
	}
	row, err := loadTaskClaimRow(a.db.DB, uuid)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Task, "task")
		}
		return nil, NewInternalError(err)
	}
	if parsedScope.TaskID != row.taskID || !claimMatches(row, attr.PrincipalRef, attr.ScopeRef, nodeID, p.ClaimGeneration, p.ClaimToken) {
		return nil, NewClaimSupersededError(claimHolderData(row))
	}
	return claimProjection(row), nil
}

func (a *API) TaskRelease(ctx context.Context, p TaskReleaseParams) (*WrkqTaskClaim, error) {
	nodeID, ok := nodeauth.FromContext(ctx)
	if !ok {
		return nil, NewNodeIdentityError()
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.PrincipalRef) == "" {
		return nil, NewValidationError("principalRef is required", map[string]any{"field": "principalRef"})
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	claimScope := strings.TrimSpace(p.Scope)
	if !p.Force {
		claimAttr, _, err := a.claimAttribution(p.PrincipalRef, p.Scope, "scope")
		if err != nil {
			return nil, err
		}
		claimScope = claimAttr.ScopeRef
	}
	tx, err := a.db.BeginTx()
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	row, err := loadTaskClaimRow(tx, uuid)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Task, "task")
		}
		return nil, NewInternalError(err)
	}
	if !row.claimedBy.Valid {
		return nil, NewValidationError("task is not claimed", map[string]any{"task": row.taskID})
	}
	if !p.Force && !claimMatches(row, attr.PrincipalRef, claimScope, nodeID, p.ClaimGeneration, p.ClaimToken) {
		return nil, NewClaimSupersededError(claimHolderData(row))
	}
	prior := claimProjection(row)
	if _, err := tx.Exec(`
		UPDATE tasks
		SET claimed_by_principal_ref = NULL, claimed_scope_ref = NULL, claimed_node = NULL,
		    claimed_at = NULL, claim_token_hash = NULL,
		    updated_by_principal_ref = ?, updated_by_scope_ref = ?
		WHERE uuid = ? AND claim_generation = ?
	`, attr.PrincipalRef, nullIfBlank(claimScope), uuid, row.generation); err != nil {
		return nil, NewInternalError(err)
	}
	var etag int64
	if err := tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", uuid).Scan(&etag); err != nil {
		return nil, NewInternalError(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"action": "released", "claim_generation": row.generation, "force": p.Force,
		"prior_holder": row.claimedBy.String, "prior_node": row.claimedNode.String,
	})
	payloadText := string(payload)
	if _, err := events.NewWriter(a.db.DB).LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef, ScopeRef: claimScope, ResourceType: "task",
		ResourceUUID: &uuid, EventType: "task.claim_released", ETag: &etag, Payload: &payloadText,
	}); err != nil {
		return nil, NewInternalError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, NewInternalError(err)
	}
	return prior, nil
}

func (a *API) claimAttribution(principalRef, scopeRef, field string) (attribution.Attribution, scope.ParsedScopeRef, error) {
	if strings.TrimSpace(principalRef) == "" {
		return attribution.Attribution{}, scope.ParsedScopeRef{}, NewValidationError("principalRef is required", map[string]any{"field": "principalRef"})
	}
	attr, err := a.attributionFor(principalRef)
	if err != nil {
		return attribution.Attribution{}, scope.ParsedScopeRef{}, err
	}
	parsed, err := scope.ParseScopeRef(strings.TrimSpace(scopeRef))
	if err != nil || parsed.TaskID == "" {
		return attribution.Attribution{}, scope.ParsedScopeRef{}, NewValidationError("a task-scoped sessionRef is required", map[string]any{
			"field": field, "expected": "agent:<id>:project:<project>:task:<task>",
		})
	}
	if "agent:"+parsed.AgentID != attr.PrincipalRef {
		return attribution.Attribution{}, scope.ParsedScopeRef{}, NewValidationError("claim principal must match claim scope agent", map[string]any{
			"field": field, "principalRef": attr.PrincipalRef, "scope": scopeRef,
		})
	}
	attr.ScopeRef = parsed.ScopeRef
	return attr, parsed, nil
}

type claimRowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func loadTaskClaimRow(q claimRowQuerier, uuid string) (taskClaimRow, error) {
	var row taskClaimRow
	err := q.QueryRow(`
		SELECT id, state, claimed_by_principal_ref, claimed_scope_ref, claimed_node,
		       claimed_at, claim_generation, claim_token_hash, etag
		FROM tasks WHERE uuid = ?
	`, uuid).Scan(&row.taskID, &row.state, &row.claimedBy, &row.claimedScope, &row.claimedNode,
		&row.claimedAt, &row.generation, &row.claimTokenHash, &row.etag)
	return row, err
}

func isClaimableState(state string) bool {
	return state == "open" || state == "in_progress" || state == "blocked"
}

func mintClaimToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, hashClaimToken(token), nil
}

func hashClaimToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func claimMatches(row taskClaimRow, principalRef, scopeRef, nodeID string, generation int64, token string) bool {
	if !row.claimedBy.Valid || !row.claimedScope.Valid || !row.claimedNode.Valid || !row.claimTokenHash.Valid {
		return false
	}
	if row.claimedBy.String != principalRef || row.claimedScope.String != scopeRef || row.claimedNode.String != nodeID || row.generation != generation {
		return false
	}
	got := []byte(hashClaimToken(token))
	want := []byte(row.claimTokenHash.String)
	return len(got) == len(want) && subtle.ConstantTimeCompare(got, want) == 1
}

func claimHolderData(row taskClaimRow) map[string]any {
	data := map[string]any{"task": row.taskID, "claimGeneration": row.generation}
	if row.claimedBy.Valid {
		data["claimedBy"] = row.claimedBy.String
		data["claimedScope"] = row.claimedScope.String
		data["claimedNode"] = row.claimedNode.String
		data["claimedAt"] = row.claimedAt.String
	}
	return data
}

func claimProjection(row taskClaimRow) *WrkqTaskClaim {
	return &WrkqTaskClaim{
		Task: row.taskID, ClaimedBy: row.claimedBy.String, ClaimedScope: row.claimedScope.String,
		ClaimedNode: row.claimedNode.String, ClaimedAt: row.claimedAt.String, ClaimGeneration: row.generation,
	}
}

func nullIfBlank(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

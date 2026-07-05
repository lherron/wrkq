package workflow

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type WorkspaceLease struct {
	CanonicalRoot   string `json:"canonicalRoot"`
	LeaseOwner      string `json:"leaseOwner,omitempty"`
	LeaseToken      string `json:"leaseToken,omitempty"`
	OwnerGeneration int64  `json:"ownerGeneration"`
	LeaseExpiresAt  string `json:"leaseExpiresAt,omitempty"`
	HeartbeatAt     string `json:"heartbeatAt,omitempty"`
	ClaimedAt       string `json:"claimedAt,omitempty"`
	ReleasedAt      string `json:"releasedAt,omitempty"`
}

type WorkspaceLeaseAuthority struct {
	CanonicalRoot   string `json:"canonicalRoot"`
	LeaseToken      string `json:"leaseToken"`
	OwnerGeneration int64  `json:"ownerGeneration"`
	LeaseExpiresAt  string `json:"leaseExpiresAt"`
}

type ClaimWorkspaceParams struct {
	WorkspaceRoot string `json:"workspaceRoot"`
	OwnerID       string `json:"ownerId"`
	LeaseMs       int64  `json:"leaseMs"`
}

type HeartbeatWorkspaceParams struct {
	WorkspaceRoot   string `json:"workspaceRoot"`
	LeaseToken      string `json:"leaseToken"`
	OwnerGeneration int64  `json:"ownerGeneration"`
	LeaseMs         int64  `json:"leaseMs,omitempty"`
}

type ReleaseWorkspaceParams struct {
	WorkspaceRoot   string `json:"workspaceRoot"`
	LeaseToken      string `json:"leaseToken"`
	OwnerGeneration int64  `json:"ownerGeneration"`
}

type ShowWorkspaceParams struct {
	WorkspaceRoot string `json:"workspaceRoot"`
}

func (s *Service) ClaimWorkspace(p ClaimWorkspaceParams) (*WorkspaceLease, error) {
	leaseMs := p.LeaseMs
	if leaseMs <= 0 {
		return nil, validationError("leaseMs", "leaseMs must be greater than zero", "positive milliseconds", nil, "supply leaseMs")
	}
	owner := strings.TrimSpace(p.OwnerID)
	if owner == "" {
		return nil, validationError("ownerId", "ownerId is required", "ownerId", nil, "supply the workspace lease owner")
	}
	root, err := canonicalWorkspaceRoot(p.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	var out *WorkspaceLease
	err = withImmediateTx(s.db, func(tx *sql.Tx) error {
		lease, err := claimWorkspaceTx(tx, root, owner, leaseMs, s.now().UTC())
		if err != nil {
			return err
		}
		out = lease
		return nil
	})
	return out, err
}

func (s *Service) HeartbeatWorkspace(p HeartbeatWorkspaceParams) (*WorkspaceLease, error) {
	root, err := canonicalWorkspaceRoot(p.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	leaseMs := p.LeaseMs
	if leaseMs <= 0 {
		leaseMs = 5 * 60 * 1000
	}
	var out *WorkspaceLease
	err = withImmediateTx(s.db, func(tx *sql.Tx) error {
		current, err := workspaceLeaseByRootTx(tx, root)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if !workspaceLeaseMatches(current, p.LeaseToken, p.OwnerGeneration, now) {
			return actionLeaseConflictError(root)
		}
		expiresAt := now.Add(time.Duration(leaseMs) * time.Millisecond).Format(time.RFC3339)
		heartbeatAt := now.Format(time.RFC3339)
		if _, err := tx.Exec(`
			UPDATE workflow_workspace_leases
			SET lease_expires_at = ?, heartbeat_at = ?
			WHERE canonical_root = ?
		`, expiresAt, heartbeatAt, root); err != nil {
			return err
		}
		current.LeaseExpiresAt = expiresAt
		current.HeartbeatAt = heartbeatAt
		out = current
		return nil
	})
	return out, err
}

func (s *Service) ReleaseWorkspace(p ReleaseWorkspaceParams) (*WorkspaceLease, error) {
	root, err := canonicalWorkspaceRoot(p.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	var out *WorkspaceLease
	err = withImmediateTx(s.db, func(tx *sql.Tx) error {
		current, err := workspaceLeaseByRootTx(tx, root)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if !workspaceLeaseMatches(current, p.LeaseToken, p.OwnerGeneration, now) {
			return actionLeaseConflictError(root)
		}
		releasedAt := now.Format(time.RFC3339)
		if _, err := tx.Exec(`
			UPDATE workflow_workspace_leases
			SET lease_owner = NULL, lease_token = NULL, lease_expires_at = NULL,
			    heartbeat_at = NULL, released_at = ?
			WHERE canonical_root = ?
		`, releasedAt, root); err != nil {
			return err
		}
		current.LeaseOwner = ""
		current.LeaseToken = ""
		current.LeaseExpiresAt = ""
		current.HeartbeatAt = ""
		current.ReleasedAt = releasedAt
		out = current
		return nil
	})
	return out, err
}

func (s *Service) ShowWorkspace(p ShowWorkspaceParams) (*WorkspaceLease, error) {
	root, err := canonicalWorkspaceRoot(p.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	lease, err := workspaceLeaseByRootQuery(s.db, root)
	if err != nil {
		return nil, err
	}
	lease.LeaseToken = ""
	return lease, nil
}

func canonicalWorkspaceRoot(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", validationError("workspaceRoot", "workspaceRoot is required", "workspace root path", nil, "supply the physical worktree root")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", validationError("workspaceRoot", fmt.Sprintf("workspaceRoot must resolve to an absolute path: %v", err), "resolvable path", nil, "supply an existing physical worktree root")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", validationError("workspaceRoot", fmt.Sprintf("workspaceRoot must exist: %v", err), "existing directory", nil, "supply an existing physical worktree root")
	}
	if !info.IsDir() {
		return "", validationError("workspaceRoot", "workspaceRoot must be a directory", "directory", nil, "supply the physical worktree root")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", validationError("workspaceRoot", fmt.Sprintf("workspaceRoot symlink resolution failed: %v", err), "canonical path", nil, "supply an existing physical worktree root")
	}
	return filepath.Clean(resolved), nil
}

func claimWorkspaceTx(tx *sql.Tx, canonicalRoot, owner string, leaseMs int64, now time.Time) (*WorkspaceLease, error) {
	token, err := newLeaseToken()
	if err != nil {
		return nil, err
	}
	nowText := now.Format(time.RFC3339)
	expiresAt := now.Add(time.Duration(leaseMs) * time.Millisecond).Format(time.RFC3339)
	current, err := workspaceLeaseByRootTx(tx, canonicalRoot)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == sql.ErrNoRows {
		if _, err := tx.Exec(`
			INSERT INTO workflow_workspace_leases (
				canonical_root, lease_owner, lease_token, owner_generation,
				lease_expires_at, heartbeat_at, claimed_at
			)
			VALUES (?, ?, ?, 1, ?, ?, ?)
		`, canonicalRoot, owner, token, expiresAt, nowText, nowText); err != nil {
			return nil, err
		}
		return &WorkspaceLease{
			CanonicalRoot: canonicalRoot, LeaseOwner: owner, LeaseToken: token,
			OwnerGeneration: 1, LeaseExpiresAt: expiresAt, HeartbeatAt: nowText, ClaimedAt: nowText,
		}, nil
	}
	if workspaceLeaseActive(current, now) && current.LeaseOwner != owner {
		return nil, actionLeaseConflictError(canonicalRoot)
	}
	nextGeneration := current.OwnerGeneration + 1
	if _, err := tx.Exec(`
		UPDATE workflow_workspace_leases
		SET lease_owner = ?, lease_token = ?, owner_generation = ?,
		    lease_expires_at = ?, heartbeat_at = ?, claimed_at = ?, released_at = NULL
		WHERE canonical_root = ?
	`, owner, token, nextGeneration, expiresAt, nowText, nowText, canonicalRoot); err != nil {
		return nil, err
	}
	current.LeaseOwner = owner
	current.LeaseToken = token
	current.OwnerGeneration = nextGeneration
	current.LeaseExpiresAt = expiresAt
	current.HeartbeatAt = nowText
	current.ClaimedAt = nowText
	current.ReleasedAt = ""
	return current, nil
}

func workspaceLeaseByRootQuery(q queryer, canonicalRoot string) (*WorkspaceLease, error) {
	return scanWorkspaceLease(q.QueryRow(`
		SELECT canonical_root, COALESCE(lease_owner,''), COALESCE(lease_token,''),
		       COALESCE(owner_generation,0), COALESCE(lease_expires_at,''),
		       COALESCE(heartbeat_at,''), COALESCE(claimed_at,''), COALESCE(released_at,'')
		FROM workflow_workspace_leases
		WHERE canonical_root = ?
	`, canonicalRoot))
}

func workspaceLeaseByRootTx(tx *sql.Tx, canonicalRoot string) (*WorkspaceLease, error) {
	return workspaceLeaseByRootQuery(tx, canonicalRoot)
}

func scanWorkspaceLease(scanner runRowScanner) (*WorkspaceLease, error) {
	var lease WorkspaceLease
	if err := scanner.Scan(
		&lease.CanonicalRoot, &lease.LeaseOwner, &lease.LeaseToken,
		&lease.OwnerGeneration, &lease.LeaseExpiresAt, &lease.HeartbeatAt,
		&lease.ClaimedAt, &lease.ReleasedAt,
	); err != nil {
		return nil, err
	}
	return &lease, nil
}

func workspaceLeaseMatches(lease *WorkspaceLease, token string, generation int64, now time.Time) bool {
	if lease == nil || strings.TrimSpace(token) == "" || strings.TrimSpace(token) != lease.LeaseToken {
		return false
	}
	if generation <= 0 || generation != lease.OwnerGeneration {
		return false
	}
	return workspaceLeaseActive(lease, now)
}

func workspaceLeaseActive(lease *WorkspaceLease, now time.Time) bool {
	if lease == nil || strings.TrimSpace(lease.LeaseToken) == "" {
		return false
	}
	return leaseStillCurrent(lease.LeaseExpiresAt, now)
}

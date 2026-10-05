//go:build wrkq_local

package workflow

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Service) ListObligations(taskSelector string, includeClosed bool) ([]Obligation, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
	if err != nil {
		return nil, err
	}
	policy := ResolveWorkflowPolicy(tpl)
	obl, err := listObligations(s.db, inst.ID, includeClosed)
	if err != nil {
		return nil, err
	}
	ev, err := listInstanceEvidence(s.db, inst.ID)
	if err != nil {
		return nil, err
	}
	obl = policy.ProjectObligations(s, inst, obl, ev, includeClosed)
	return obl, nil
}

// obligationColumns is the SELECT list scanObligations reads, in scan order.
const obligationColumns = `id, instance_id, kind, COALESCE(owner_role,''), COALESCE(owner_principal_ref, owner_actor, ''),
	COALESCE(obligee_role,''), COALESCE(obligee_principal_ref, obligee_actor, ''), COALESCE(waive_role,''), COALESCE(waive_principal_ref, waive_actor, ''), COALESCE(no_self_waive,1),
	blocking, status, COALESCE(reason,''), COALESCE(satisfied_by_evidence_id,''),
	COALESCE(resolved_by_principal_ref, resolved_by_actor, ''), COALESCE(resolved_by_role,''), COALESCE(resolved_at,''), created_at, updated_at`

// queryObligations runs a workflow_obligations query whose WHERE/ORDER tail
// is clause.
func queryObligations(q rowsQueryer, clause string, args ...interface{}) ([]Obligation, error) {
	rows, err := q.Query(`SELECT `+obligationColumns+` FROM workflow_obligations `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanObligations(rows)
}

func listObligations(q rowsQueryer, instanceID string, includeClosed bool) ([]Obligation, error) {
	clause := `WHERE instance_id = ?`
	if !includeClosed {
		clause += ` AND status = 'open'`
	}
	return queryObligations(q, clause+` ORDER BY created_at, id`, instanceID)
}

func scanObligations(rows *sql.Rows) ([]Obligation, error) {
	var out []Obligation
	for rows.Next() {
		var o Obligation
		var blocking int
		var noSelfWaive int
		if err := rows.Scan(&o.ID, &o.InstanceID, &o.Kind, &o.OwnerRole, &o.OwnerPrincipalRef, &o.ObligeeRole, &o.ObligeePrincipalRef, &o.WaiveRole, &o.WaivePrincipalRef, &noSelfWaive, &blocking, &o.Status, &o.Reason, &o.SatisfiedByEvidenceID, &o.ResolvedByPrincipalRef, &o.ResolvedByRole, &o.ResolvedAt, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		o.Blocking = blocking == 1
		o.NoSelfWaive = noSelfWaive == 1
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Service) ShowObligation(id string) (*Obligation, error) {
	obl, err := queryObligations(s.db, `WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(obl) == 0 {
		return nil, fmt.Errorf("obligation not found: %s", id)
	}
	return &obl[0], nil
}

func (s *Service) SetObligationStatusWithAuthority(taskSelector, id, status, evidenceID, reason string, opts ObligationStatusOptions) (*Obligation, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	if status != "satisfied" && status != "waived" && status != "cancelled" {
		return nil, fmt.Errorf("invalid obligation status: %s", status)
	}
	now := s.now().Format(time.RFC3339)
	var out *Obligation
	err = withTx(s.db.DB, func(tx *sql.Tx) error {
		current, err := selectObligationTx(tx, id, inst.ID)
		if err != nil {
			return err
		}
		if !obligationStatusAllowed(*current, status, opts.PrincipalRef, opts.Role) {
			return roleDeniedError(inst.ID, "obligation:"+id+":"+status, opts.Role)
		}
		_, err = tx.Exec(`
			UPDATE workflow_obligations
			SET status = ?, satisfied_by_evidence_id = COALESCE(NULLIF(?, ''), satisfied_by_evidence_id),
			    reason = COALESCE(NULLIF(?, ''), reason), resolved_by_actor = ?, resolved_by_principal_ref = ?, resolved_by_role = ?, resolved_at = ?, updated_at = ?
			WHERE id = ? AND instance_id = ?
		`, status, evidenceID, reason, nullIfEmpty(opts.PrincipalRef), nullIfEmpty(opts.PrincipalRef), nullIfEmpty(opts.Role), now, now, id, inst.ID)
		if err != nil {
			return err
		}
		out, err = selectObligationTx(tx, id, inst.ID)
		return err
	})
	return out, err
}

func selectObligationTx(tx *sql.Tx, id, instanceID string) (*Obligation, error) {
	list, err := queryObligations(tx, `WHERE id = ? AND instance_id = ?`, id, instanceID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("obligation not found: %s", id)
	}
	return &list[0], nil
}

func obligationStatusAllowed(o Obligation, status, actor, role string) bool {
	actor = strings.TrimSpace(actor)
	role = strings.TrimSpace(role)
	if role == "system" || role == "supervisor" {
		return true
	}
	switch status {
	case "satisfied":
		if o.OwnerPrincipalRef != "" {
			return actor != "" && actor == o.OwnerPrincipalRef
		}
		if o.OwnerRole != "" {
			return role == o.OwnerRole
		}
		return actor != ""
	case "waived", "cancelled":
		if o.NoSelfWaive && actor != "" && o.OwnerPrincipalRef != "" && actor == o.OwnerPrincipalRef {
			return false
		}
		if o.WaivePrincipalRef != "" {
			return actor != "" && actor == o.WaivePrincipalRef
		}
		if o.WaiveRole != "" {
			return role == o.WaiveRole
		}
		return false
	default:
		return false
	}
}

func (s *Service) CreateObligation(taskSelector, kind, ownerRole, ownerActor string, blocking bool, reason string) (*Obligation, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	var out *Obligation
	err = withTx(s.db.DB, func(tx *sql.Tx) error {
		id, err := nextSeqID(tx, "workflow_obligation_seq", "obl")
		if err != nil {
			return err
		}
		blockingInt := 0
		if blocking {
			blockingInt = 1
		}
		_, err = tx.Exec(`
			INSERT INTO workflow_obligations (
				id, instance_id, kind, owner_role, owner_actor,
				obligee_role, waive_role, no_self_waive, blocking, status, reason
			) VALUES (?, ?, ?, ?, ?, 'workflow', 'system', 1, ?, 'open', ?)
		`, id, inst.ID, kind, nullIfEmpty(ownerRole), nullIfEmpty(ownerActor), blockingInt, nullIfEmpty(reason))
		if err != nil {
			return err
		}
		out, err = selectObligationTx(tx, id, inst.ID)
		return err
	})
	return out, err
}

// insertOutcomeObligationsTx opens the obligations a chosen transition
// outcome declares, defaulting the obligee to "workflow", the waiver to
// "system" when no waive principal is named, and no-self-waive to true.
func insertOutcomeObligationsTx(tx *sql.Tx, instanceID string, specs []ObligationCreateSpec, now string) ([]Obligation, error) {
	created := make([]Obligation, 0, len(specs))
	for _, ob := range specs {
		id, err := nextSeqID(tx, "workflow_obligation_seq", "obl")
		if err != nil {
			return nil, err
		}
		blocking := 0
		if ob.Blocking {
			blocking = 1
		}
		noSelfWaive := true
		if ob.NoSelfWaive != nil {
			noSelfWaive = *ob.NoSelfWaive
		}
		noSelfWaiveInt := 0
		if noSelfWaive {
			noSelfWaiveInt = 1
		}
		obligeeRole := strings.TrimSpace(ob.ObligeeRole)
		if obligeeRole == "" {
			obligeeRole = "workflow"
		}
		waiveRole := strings.TrimSpace(ob.WaiveRole)
		if waiveRole == "" && strings.TrimSpace(ob.WaivePrincipalRef) == "" {
			waiveRole = "system"
		}
		_, err = tx.Exec(`
			INSERT INTO workflow_obligations (
				id, instance_id, kind, owner_role, owner_actor, owner_principal_ref, obligee_role, obligee_actor, obligee_principal_ref,
				waive_role, waive_actor, waive_principal_ref, no_self_waive, blocking, status, reason, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, ?)
		`, id, instanceID, ob.Kind, nullIfEmpty(ob.OwnerRole), nullIfEmpty(ob.OwnerPrincipalRef), nullIfEmpty(ob.OwnerPrincipalRef), nullIfEmpty(obligeeRole), nullIfEmpty(ob.ObligeePrincipalRef), nullIfEmpty(ob.ObligeePrincipalRef), nullIfEmpty(waiveRole), nullIfEmpty(ob.WaivePrincipalRef), nullIfEmpty(ob.WaivePrincipalRef), noSelfWaiveInt, blocking, nullIfEmpty(ob.Reason), now, now)
		if err != nil {
			return nil, err
		}
		created = append(created, Obligation{
			ID: id, InstanceID: instanceID, Kind: ob.Kind, OwnerRole: ob.OwnerRole, OwnerPrincipalRef: ob.OwnerPrincipalRef,
			ObligeeRole: obligeeRole, ObligeePrincipalRef: ob.ObligeePrincipalRef, WaiveRole: waiveRole, WaivePrincipalRef: ob.WaivePrincipalRef,
			NoSelfWaive: noSelfWaive, Blocking: ob.Blocking, Status: "open", Reason: ob.Reason, CreatedAt: now, UpdatedAt: now,
		})
	}
	return created, nil
}

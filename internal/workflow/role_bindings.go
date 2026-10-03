//go:build wrkq_local

package workflow

import (
	"database/sql"
	"strings"
	"time"
)

func (s *Service) ListRoleBindings(taskSelector, instanceID string) ([]RoleBinding, error) {
	inst, err := s.ResolveInstance(taskSelector, instanceID)
	if err != nil {
		return nil, err
	}
	return listRoleBindingsForInstance(s.db, inst.ID)
}

func (s *Service) BindRole(opts RoleBindOptions) (*RoleBinding, error) {
	if err := validateRoleBindingInput(opts.Role, opts.PrincipalRef, opts.BindingMode); err != nil {
		return nil, err
	}
	mode := strings.TrimSpace(opts.BindingMode)
	if mode == "" {
		mode = "required"
	}
	var out *RoleBinding
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, opts.TaskSelector, opts.InstanceID)
		if err != nil {
			return err
		}
		now := s.now().Format(time.RFC3339)
		_, err = tx.Exec(`
			INSERT INTO workflow_role_bindings (instance_id, role, actor, principal_ref, delivery_ref, lane, binding_mode, bound_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(instance_id, role, actor) DO UPDATE SET
				principal_ref = excluded.principal_ref,
				delivery_ref = excluded.delivery_ref,
				lane = excluded.lane,
				binding_mode = excluded.binding_mode,
				bound_at = excluded.bound_at
		`, inst.ID, strings.TrimSpace(opts.Role), strings.TrimSpace(opts.PrincipalRef), strings.TrimSpace(opts.PrincipalRef), nullIfEmpty(opts.DeliveryRef), nullIfEmpty(opts.Lane), mode, now)
		if err != nil {
			return err
		}
		binding, err := getRoleBindingTx(tx, inst.ID, strings.TrimSpace(opts.Role), strings.TrimSpace(opts.PrincipalRef))
		if err != nil {
			return err
		}
		out = binding
		return nil
	})
	return out, err
}

func (s *Service) UnbindRole(taskSelector, instanceID, role, actor string) ([]RoleBinding, error) {
	role = strings.TrimSpace(role)
	actor = strings.TrimSpace(actor)
	if role == "" {
		return nil, validationError("role", "role is required", "non-empty role", nil, "supply role")
	}
	var out []RoleBinding
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, taskSelector, instanceID)
		if err != nil {
			return err
		}
		if actor == "" {
			_, err = tx.Exec(`DELETE FROM workflow_role_bindings WHERE instance_id = ? AND role = ?`, inst.ID, role)
		} else {
			_, err = tx.Exec(`DELETE FROM workflow_role_bindings WHERE instance_id = ? AND role = ? AND principal_ref = ?`, inst.ID, role, actor)
		}
		if err != nil {
			return err
		}
		out, err = listRoleBindingsForInstance(tx, inst.ID)
		return err
	})
	return out, err
}

func (s *Service) SetRoleBindings(taskSelector, instanceID string, roleMap map[string]string) ([]RoleBinding, error) {
	if roleMap == nil {
		roleMap = map[string]string{}
	}
	for role, actor := range roleMap {
		if err := validateRoleBindingInput(role, actor, "required"); err != nil {
			return nil, err
		}
	}
	var out []RoleBinding
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, taskSelector, instanceID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM workflow_role_bindings WHERE instance_id = ?`, inst.ID); err != nil {
			return err
		}
		now := s.now().Format(time.RFC3339)
		for role, actor := range roleMap {
			_, err := tx.Exec(`
				INSERT INTO workflow_role_bindings (instance_id, role, actor, principal_ref, binding_mode, bound_at)
				VALUES (?, ?, ?, ?, 'required', ?)
			`, inst.ID, strings.TrimSpace(role), strings.TrimSpace(actor), strings.TrimSpace(actor), now)
			if err != nil {
				return err
			}
		}
		out, err = listRoleBindingsForInstance(tx, inst.ID)
		return err
	})
	return out, err
}

func validateRoleBindingInput(role, actor, mode string) error {
	if strings.TrimSpace(role) == "" {
		return validationError("role", "role is required", "non-empty role", nil, "supply role")
	}
	if strings.TrimSpace(actor) == "" {
		return validationError("principalRef", "principalRef is required", "non-empty principal_ref", nil, "supply principal_ref")
	}
	switch strings.TrimSpace(mode) {
	case "", "required", "optional", "auto":
		return nil
	default:
		return validationError("bindingMode", "bindingMode must be required, optional, or auto", "required|optional|auto", []string{"required", "optional", "auto"}, "use a supported bindingMode")
	}
}

func listRoleBindingsForInstance(q rowsQueryer, instanceID string) ([]RoleBinding, error) {
	rows, err := q.Query(`
		SELECT instance_id, role, COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,''), binding_mode, bound_at
		FROM workflow_role_bindings
		WHERE instance_id = ?
		ORDER BY role, principal_ref
	`, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RoleBinding
	for rows.Next() {
		var binding RoleBinding
		if err := rows.Scan(&binding.InstanceID, &binding.Role, &binding.PrincipalRef, &binding.DeliveryRef, &binding.Lane, &binding.BindingMode, &binding.BoundAt); err != nil {
			return nil, err
		}
		out = append(out, binding)
	}
	return out, rows.Err()
}

func listRoleBindingsForInstanceRole(q rowsQueryer, instanceID, role string) ([]RoleBinding, error) {
	rows, err := q.Query(`
		SELECT instance_id, role, COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,''), binding_mode, bound_at
		FROM workflow_role_bindings
		WHERE instance_id = ? AND role = ?
		ORDER BY role, principal_ref
	`, instanceID, role)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RoleBinding
	for rows.Next() {
		var binding RoleBinding
		if err := rows.Scan(&binding.InstanceID, &binding.Role, &binding.PrincipalRef, &binding.DeliveryRef, &binding.Lane, &binding.BindingMode, &binding.BoundAt); err != nil {
			return nil, err
		}
		out = append(out, binding)
	}
	return out, rows.Err()
}

func getRoleBindingTx(tx *sql.Tx, instanceID, role, actor string) (*RoleBinding, error) {
	row := tx.QueryRow(`
		SELECT instance_id, role, COALESCE(principal_ref, actor, ''), COALESCE(delivery_ref,''), COALESCE(lane,''), binding_mode, bound_at
		FROM workflow_role_bindings
		WHERE instance_id = ? AND role = ? AND principal_ref = ?
	`, instanceID, role, actor)
	var binding RoleBinding
	if err := row.Scan(&binding.InstanceID, &binding.Role, &binding.PrincipalRef, &binding.DeliveryRef, &binding.Lane, &binding.BindingMode, &binding.BoundAt); err != nil {
		return nil, err
	}
	return &binding, nil
}

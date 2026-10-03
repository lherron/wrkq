//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Service) InstallTemplate(path, actor string, catalog *HookCatalog) (map[string]interface{}, error) {
	tpl, canonical, hash, err := LoadTemplateFile(path)
	if err != nil {
		return nil, err
	}
	return s.installTemplateCanonical(tpl, canonical, hash, actor, catalog, false)
}

// InstallTemplateContent installs a decoded template body without consulting
// the daemon filesystem.
func (s *Service) InstallTemplateContent(data []byte, actor string, catalog *HookCatalog) (map[string]interface{}, error) {
	tpl, canonical, hash, err := ParseTemplateContent(data)
	if err != nil {
		return nil, err
	}
	return s.installTemplateCanonical(tpl, canonical, hash, actor, catalog, false)
}

// installTemplateCanonical installs an already-parsed template. It is shared by
// the file-based InstallTemplate and the embedded built-in installer so both
// honor the same validation and idempotent-by-hash semantics.
//
// When supersede is true (embedded built-in installer only), a same-version
// hash change overwrites the stored definition/hash in place instead of
// erroring, so a rebuilt binary can evolve a built-in workflow. There is NO
// pinned-hash guard: old instances carrying the prior template hash may then
// evaluate under the NEW definition. That divergence is an explicitly accepted
// operational risk for embedded built-ins only (Lance, T-triage-spec-gate);
// correctness is asserted only for behavior evaluated after install, not for
// historical template-hash immutability. File-based InstallTemplate keeps the
// immutable same-id/version-hash-mismatch rejection (supersede=false).
func (s *Service) installTemplateCanonical(tpl *Template, canonical []byte, hash, actor string, catalog *HookCatalog, supersede bool) (map[string]interface{}, error) {
	if errs := ValidateTemplate(tpl, canonical, catalog); len(errs) > 0 {
		return nil, fmt.Errorf("invalid template: %s", strings.Join(errs, "; "))
	}
	catalogCanonical, catalogHash, err := canonicalHookCatalog(catalog)
	if err != nil {
		return nil, err
	}
	var existingHash, existingCatalogHash string
	err = s.db.QueryRow(`SELECT hash, COALESCE(hook_catalog_hash, '') FROM workflow_templates WHERE id = ? AND version = ?`, tpl.ID, tpl.Version).Scan(&existingHash, &existingCatalogHash)
	if err == nil {
		if existingHash != hash {
			if !supersede {
				return nil, fmt.Errorf("template %s@%s already installed with different hash", tpl.ID, tpl.Version)
			}
			if _, err := s.db.Exec(`UPDATE workflow_templates SET hash = ?, definition_json = ?, hook_catalog_json = ?, hook_catalog_hash = ? WHERE id = ? AND version = ?`,
				hash, string(canonical), nullIfEmpty(string(catalogCanonical)), nullIfEmpty(catalogHash), tpl.ID, tpl.Version); err != nil {
				return nil, err
			}
			return map[string]interface{}{"id": tpl.ID, "version": tpl.Version, "hash": hash, "installed": true, "superseded": true}, nil
		}
		if catalogHash != "" && existingCatalogHash != "" && existingCatalogHash != catalogHash {
			return nil, fmt.Errorf("template %s@%s already installed with different hook catalog hash", tpl.ID, tpl.Version)
		}
		if catalogHash != "" && existingCatalogHash == "" {
			if _, err := s.db.Exec(`UPDATE workflow_templates SET hook_catalog_json = ?, hook_catalog_hash = ? WHERE id = ? AND version = ?`, string(catalogCanonical), catalogHash, tpl.ID, tpl.Version); err != nil {
				return nil, err
			}
		}
		return map[string]interface{}{"id": tpl.ID, "version": tpl.Version, "hash": hash, "installed": false}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	_, err = s.db.Exec(`
		INSERT INTO workflow_templates (id, version, hash, definition_json, installed_by, installed_by_principal_ref, hook_catalog_json, hook_catalog_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, tpl.ID, tpl.Version, hash, string(canonical), emptyToNil(actor), emptyToNil(actor), nullIfEmpty(string(catalogCanonical)), nullIfEmpty(catalogHash))
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"id": tpl.ID, "version": tpl.Version, "hash": hash, "installed": true}, nil
}

func (s *Service) ListTemplates() ([]map[string]interface{}, error) {
	rows, err := s.db.Query(`
		SELECT id, version, hash, installed_at,
		       COALESCE(installed_by_principal_ref, installed_by),
		       discontinued_at, discontinued_by
		FROM workflow_templates
		ORDER BY id, version
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]interface{}
	for rows.Next() {
		var id, version, hash, installedAt string
		var installedBy, discontinuedAt, discontinuedBy sql.NullString
		if err := rows.Scan(&id, &version, &hash, &installedAt, &installedBy, &discontinuedAt, &discontinuedBy); err != nil {
			return nil, err
		}
		row := map[string]interface{}{"id": id, "version": version, "hash": hash, "installedAt": installedAt}
		if installedBy.Valid {
			row["installedBy"] = installedBy.String
		}
		if discontinuedAt.Valid {
			row["discontinuedAt"] = discontinuedAt.String
		}
		if discontinuedBy.Valid {
			row["discontinuedBy"] = discontinuedBy.String
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Service) ShowTemplateVersion(ref string) (*TemplateVersionInfo, error) {
	id, version, err := parseTemplateRef(ref)
	if err != nil {
		return nil, err
	}
	var definition, hash string
	var discontinuedAt, discontinuedBy sql.NullString
	if err := s.db.QueryRow(`
		SELECT definition_json, hash, discontinued_at, discontinued_by
		FROM workflow_templates
		WHERE id = ? AND version = ?
	`, id, version).Scan(&definition, &hash, &discontinuedAt, &discontinuedBy); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("template not found: %s", ref)
		}
		return nil, err
	}
	tpl, _, err := ParseTemplate([]byte(definition))
	if err != nil {
		return nil, err
	}
	return &TemplateVersionInfo{
		Template:       tpl,
		Hash:           hash,
		DiscontinuedAt: discontinuedAt.String,
		DiscontinuedBy: discontinuedBy.String,
	}, nil
}

func (s *Service) ShowTemplate(ref string) (*Template, string, error) {
	info, err := s.ShowTemplateVersion(ref)
	if err != nil {
		return nil, "", err
	}
	return info.Template, info.Hash, nil
}

func (s *Service) DiscontinueTemplate(id, version, actor string) error {
	return withImmediateTx(s.db, func(tx *sql.Tx) error {
		now := s.now().Format(time.RFC3339)
		result, err := tx.Exec(`
			UPDATE workflow_templates
			SET discontinued_at = ?, discontinued_by = ?
			WHERE id = ? AND version = ? AND discontinued_at IS NULL
		`, now, emptyToNil(actor), id, version)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 1 {
			return nil
		}
		var discontinuedAt string
		var discontinuedBy sql.NullString
		if err := tx.QueryRow(`SELECT discontinued_at, discontinued_by FROM workflow_templates WHERE id = ? AND version = ?`, id, version).Scan(&discontinuedAt, &discontinuedBy); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("template not found: %s@%s", id, version)
			}
			return err
		}
		state := fmt.Sprintf("template %s@%s is already discontinued at %s", id, version, discontinuedAt)
		if discontinuedBy.Valid {
			state += " by " + discontinuedBy.String
		}
		return validationError("workflow", state, "non-discontinued template version", nil, "reinstate the template version before discontinuing it again")
	})
}

func (s *Service) ReinstateTemplate(id, version string) error {
	return withImmediateTx(s.db, func(tx *sql.Tx) error {
		result, err := tx.Exec(`
			UPDATE workflow_templates
			SET discontinued_at = NULL, discontinued_by = NULL
			WHERE id = ? AND version = ? AND discontinued_at IS NOT NULL
		`, id, version)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 1 {
			return nil
		}
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM workflow_templates WHERE id = ? AND version = ?`, id, version).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("template not found: %s@%s", id, version)
		}
		return validationError("workflow", fmt.Sprintf("template %s@%s is already non-discontinued", id, version), "discontinued template version", nil, "discontinue the template version before reinstating it")
	})
}

// pinnedHookCatalog returns the template version's stored hook law only after
// verifying that the daemon's executable bundle has the same catalog identity.
// The configured catalog supplies the executable root, never replacement specs.
func (s *Service) pinnedHookCatalog(templateID, version string, configured *HookCatalog) (*HookCatalog, error) {
	var raw sql.NullString
	var storedHash sql.NullString
	if err := s.db.QueryRow(`SELECT hook_catalog_json, hook_catalog_hash FROM workflow_templates WHERE id = ? AND version = ?`, templateID, version).Scan(&raw, &storedHash); err != nil {
		return nil, err
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" || !storedHash.Valid || strings.TrimSpace(storedHash.String) == "" {
		return nil, fmt.Errorf("template %s@%s has no pinned hook catalog", templateID, version)
	}
	if configured == nil {
		return nil, fmt.Errorf("daemon hook catalog is not configured for template %s@%s", templateID, version)
	}
	_, configuredHash, err := canonicalHookCatalog(configured)
	if err != nil {
		return nil, err
	}
	if configuredHash != storedHash.String {
		return nil, fmt.Errorf("hook catalog hash mismatch for template %s@%s: stored %s, configured %s", templateID, version, storedHash.String, configuredHash)
	}
	var catalog HookCatalog
	if err := json.Unmarshal([]byte(raw.String), &catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func parseTemplateRef(ref string) (string, string, error) {
	parts := strings.Split(ref, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("template ref must be id@version")
	}
	return parts[0], parts[1], nil
}

// ParseTemplateRef validates and splits the explicit id@version registry
// address used by operator-facing template commands.
func ParseTemplateRef(ref string) (string, string, error) {
	return parseTemplateRef(ref)
}

func emptyToNil(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func showTemplateTx(q queryer, ref string) (*Template, string, error) {
	id, version, err := parseTemplateRef(ref)
	if err != nil {
		return nil, "", err
	}
	var definition, hash string
	if err := q.QueryRow(`SELECT definition_json, hash FROM workflow_templates WHERE id = ? AND version = ?`, id, version).Scan(&definition, &hash); err != nil {
		if err == sql.ErrNoRows {
			return nil, "", fmt.Errorf("template not found: %s", ref)
		}
		return nil, "", err
	}
	tpl, _, err := ParseTemplate([]byte(definition))
	return tpl, hash, err
}

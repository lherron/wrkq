package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func applyCampaignPortfolioMigration(t *testing.T, database *DB) {
	t.Helper()
	content, err := migrationsFS.ReadFile(filepath.Join("migrations", "000050_campaign_portfolio.sql"))
	if err != nil {
		t.Fatalf("read campaign portfolio migration: %v", err)
	}
	if err := database.applyMigration("000050_campaign_portfolio.sql", content); err != nil {
		t.Fatalf("apply campaign portfolio migration: %v", err)
	}
}

func TestCampaignPortfolioMigrationWidensStateAndPreservesContainerRows(t *testing.T) {
	database := openPreAdornmentsFixture(t)
	applyAdornmentsMigration(t, database)

	if _, err := database.Exec(`
		UPDATE containers
		   SET campaign_state = 'active', specification = 'legacy campaign spec'
		 WHERE uuid = ?
	`, adornmentsCampaignUUID); err != nil {
		t.Fatalf("seed active campaign: %v", err)
	}

	applyCampaignPortfolioMigration(t, database)

	var state, specification, labels sql.NullString
	if err := database.QueryRow(`
		SELECT campaign_state, specification, labels
		  FROM containers
		 WHERE uuid = ?
	`, adornmentsCampaignUUID).Scan(&state, &specification, &labels); err != nil {
		t.Fatalf("read migrated campaign: %v", err)
	}
	if !state.Valid || state.String != "active" ||
		!specification.Valid || specification.String != "legacy campaign spec" ||
		labels.Valid {
		t.Fatalf("migrated campaign state/spec/labels = %v/%v/%v", state, specification, labels)
	}
	if _, err := database.Exec(`
		UPDATE containers SET campaign_state = 'draft', labels = '["one"," one ","one"]'
		 WHERE uuid = ?
	`, adornmentsCampaignUUID); err != nil {
		t.Fatalf("store draft campaign labels: %v", err)
	}
	if _, err := database.Exec(`
		UPDATE containers SET campaign_state = 'paused' WHERE uuid = ?
	`, adornmentsCampaignUUID); err == nil {
		t.Fatal("invalid campaign state accepted")
	}

	var tableSQL string
	if err := database.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'containers'",
	).Scan(&tableSQL); err != nil {
		t.Fatalf("read containers schema: %v", err)
	}
	if !strings.Contains(tableSQL, "'draft'") || !strings.Contains(tableSQL, "labels TEXT") {
		t.Fatalf("containers schema missing campaign portfolio columns: %s", tableSQL)
	}

	rows, err := database.Query("PRAGMA foreign_key_list(tasks)")
	if err != nil {
		t.Fatalf("read task foreign keys: %v", err)
	}
	defer func() { _ = rows.Close() }()
	containerRefs := 0
	for rows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan task foreign key: %v", err)
		}
		if table == "containers" {
			containerRefs++
		}
	}
	if containerRefs < 2 {
		t.Fatalf("task container foreign keys = %d, want project+campaign refs intact", containerRefs)
	}
}

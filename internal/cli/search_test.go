package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

func TestSearchCommandRebuildAndSearchJSON(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	app := createTestApp(t, database, dbPath)
	app.Config.Search = config.SearchConfig{
		Enabled:        true,
		DBPath:         filepath.Join(t.TempDir(), "search.sqlite"),
		DenseProvider:  "hash",
		DenseDimension: 16,
		CandidateLimit: 20,
	}

	s := store.New(database)
	if _, err := s.Tasks.Create(app.ActorUUID, store.CreateParams{
		Slug:        "qwen-search",
		Title:       "Qwen search",
		Description: "Use qwen embeddings for local search.",
		ProjectUUID: "00000000-0000-0000-0000-000000000002",
		State:       "open",
		Priority:    2,
		Kind:        "task",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	rebuildBuf := &bytes.Buffer{}
	rebuildCmd := &cobra.Command{}
	rebuildCmd.SetOut(rebuildBuf)
	rebuildCmd.SetErr(rebuildBuf)
	if err := runIndexRebuild(app, rebuildCmd, nil); err != nil {
		t.Fatalf("runIndexRebuild failed: %v", err)
	}

	searchJSON = true
	searchState = ""
	searchKind = ""
	searchAssignee = ""
	searchLimit = 20
	searchCandidateLimit = 20
	searchExplain = true
	searchFresh = true
	defer func() {
		searchJSON = false
		searchExplain = false
		searchFresh = false
	}()

	searchBuf := &bytes.Buffer{}
	searchCmd := &cobra.Command{}
	searchCmd.SetOut(searchBuf)
	searchCmd.SetErr(searchBuf)
	if err := runSearch(app, searchCmd, []string{"qwen"}); err != nil {
		t.Fatalf("runSearch failed: %v", err)
	}

	var response struct {
		Results []struct {
			TaskID string `json:"task_id"`
			State  string `json:"state"`
		} `json:"results"`
	}
	if err := json.Unmarshal(searchBuf.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, searchBuf.String())
	}
	if len(response.Results) != 1 {
		t.Fatalf("expected 1 result, got %d: %s", len(response.Results), searchBuf.String())
	}
	if response.Results[0].State != "open" {
		t.Fatalf("expected open result, got %s", response.Results[0].State)
	}
}

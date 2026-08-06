//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

func TestParseLabelValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "single shorthand", raw: "overnight-ok", want: []string{"overnight-ok"}},
		{name: "shorthand trims drops empties", raw: "a, b,,a", want: []string{"a", "b", "a"}},
		{name: "empty clears", raw: "", want: []string{}},
		{name: "whitespace clears", raw: " \t ", want: []string{}},
		{name: "empty JSON clears", raw: "[]", want: []string{}},
		{
			name: "JSON is lossless",
			raw:  `  [" a ","a,b","a","a",""]  `,
			want: []string{" a ", "a,b", "a", "a", ""},
		},
		{name: "numeric JSON-looking shorthand", raw: "123", want: []string{"123"}},
		{name: "boolean JSON-looking shorthand", raw: "true", want: []string{"true"}},
		{name: "null JSON-looking shorthand", raw: "null", want: []string{"null"}},
		{name: "quoted JSON-looking shorthand", raw: `"a"`, want: []string{`"a"`}},
		{name: "object-like shorthand", raw: `{x}`, want: []string{`{x}`}},
		{name: "valid object JSON-looking shorthand", raw: `{"label":"a"}`, want: []string{`{"label":"a"}`}},
		{name: "malformed JSON", raw: `["a"`, wantErr: true},
		{name: "non-string element", raw: `["a",1]`, wantErr: true},
		{name: "null element", raw: `["a",null]`, wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseLabelValue(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseLabelValue(%q) succeeded: %#v", tc.raw, got)
				}
				if !strings.Contains(err.Error(), labelValueForms) {
					t.Fatalf("error %q does not name both accepted forms", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLabelValue(%q): %v", tc.raw, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseLabelValue(%q) = %#v, want %#v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestLabelWriteSurfacesLocalAndAuthenticatedRPC(t *testing.T) {
	for _, mode := range []string{"local", "authenticated-rpc"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			dbPath := newLabelWriteDB(t)
			locator := dbPath
			if mode == "authenticated-rpc" {
				locator = newAuthenticatedLabelWriteServer(t, dbPath)
			}

			mustRunLabelWriteCLI(t, locator, "mkdir", "labels-e2e")
			mustRunLabelWriteCLI(t, locator, "mkdir", "labels-e2e/wave")

			mustRunLabelWriteCLI(t, locator,
				"touch", "labels-e2e/task", "-t", "Task",
				"--labels", "a, b,,a",
			)
			assertTaskLabels(t, locator, "labels-e2e/task", []string{"a", "b", "a"})

			mustRunLabelWriteCLI(t, locator, "set", "labels-e2e/task", "--labels", "null")
			assertTaskLabels(t, locator, "labels-e2e/task", []string{"null"})

			lossless := []string{" a ", "a,b", "a", "a", ""}
			losslessJSON := `[" a ","a,b","a","a",""]`
			mustRunLabelWriteCLI(t, locator, "set", "labels-e2e/task", "--labels", losslessJSON)
			assertTaskLabels(t, locator, "labels-e2e/task", lossless)

			dryRun := mustRunLabelWriteCLI(t, locator,
				"set", "labels-e2e/task", "--labels", "dry, Run,,dry", "--dry-run",
			)
			var preview struct {
				DryRun bool `json:"dry_run"`
				Fields struct {
					Labels []string `json:"labels"`
				} `json:"fields"`
			}
			if err := json.Unmarshal([]byte(dryRun), &preview); err != nil {
				t.Fatalf("decode --dry-run: %v\n%s", err, dryRun)
			}
			if !preview.DryRun || !reflect.DeepEqual(preview.Fields.Labels, []string{"dry", "Run", "dry"}) {
				t.Fatalf("dry-run labels = %#v", preview)
			}
			assertTaskLabels(t, locator, "labels-e2e/task", lossless)

			mustRunLabelWriteCLI(t, locator, "set", "labels-e2e/task", "--title", "Renamed")
			assertTaskLabels(t, locator, "labels-e2e/task", lossless)

			mustRunLabelWriteCLI(t, locator, "set", "labels-e2e/task", "--labels", "")
			assertTaskLabels(t, locator, "labels-e2e/task", []string{})
			mustRunLabelWriteCLI(t, locator, "set", "labels-e2e/task", "--labels", losslessJSON)
			mustRunLabelWriteCLI(t, locator, "set", "labels-e2e/task", "--state", "archived")
			mustRunLabelWriteCLI(t, locator, "restore", "labels-e2e/task")
			assertTaskLabels(t, locator, "labels-e2e/task", lossless)

			mustRunLabelWriteCLI(t, locator, "set", "labels-e2e/task", "--state", "archived")
			mustRunLabelWriteCLI(t, locator, "restore", "labels-e2e/task", "--labels", "restore, Mixed,,restore")
			assertTaskLabels(t, locator, "labels-e2e/task", []string{"restore", "Mixed", "restore"})

			mustRunLabelWriteCLI(t, locator, "set", "labels-e2e/task", "--state", "archived")
			mustRunLabelWriteCLI(t, locator, "restore", "labels-e2e/task", "--labels", "")
			assertTaskLabels(t, locator, "labels-e2e/task", []string{})

			mustRunLabelWriteCLI(t, locator, "set", "labels-e2e/task", "--labels", "before-clear")
			mustRunLabelWriteCLI(t, locator, "set", "labels-e2e/task", "--state", "archived")
			mustRunLabelWriteCLI(t, locator, "restore", "labels-e2e/task", "--labels", "[]")
			assertTaskLabels(t, locator, "labels-e2e/task", []string{})

			mustRunLabelWriteCLI(t, locator, "touch", "labels-e2e/empty-clear", "--labels", "")
			assertTaskLabels(t, locator, "labels-e2e/empty-clear", []string{})
			mustRunLabelWriteCLI(t, locator, "touch", "labels-e2e/json-clear", "--labels", "[]")
			assertTaskLabels(t, locator, "labels-e2e/json-clear", []string{})

			mustRunLabelWriteCLI(t, locator, "set", "labels-e2e/task", "--labels", "safe")
			assertLabelWriteRefused(t, locator,
				[]string{"set", "labels-e2e/task", "--labels", `["bad",1]`},
				"labels-e2e/task", []string{"safe"},
			)
			assertLabelTouchRefused(t, locator, `["bad",null]`)

			converted := mustRunLabelWriteCLI(t, locator,
				"campaign", "convert", "labels-e2e/wave",
				"--labels", "nightly, Mixed,,nightly",
			)
			assertCampaignLabels(t, converted, []string{"nightly", "Mixed", "nightly"})

			edited := mustRunLabelWriteCLI(t, locator,
				"campaign", "edit", "labels-e2e/wave", "--labels", losslessJSON,
			)
			assertCampaignLabels(t, edited, lossless)

			unchanged := mustRunLabelWriteCLI(t, locator,
				"campaign", "edit", "labels-e2e/wave", "--description", "labels unchanged",
			)
			assertCampaignLabels(t, unchanged, lossless)

			assertCampaignEditRefused(t, locator, `["bad",null]`, lossless)

			emptyCleared := mustRunLabelWriteCLI(t, locator,
				"campaign", "edit", "labels-e2e/wave", "--labels", "",
			)
			assertCampaignLabels(t, emptyCleared, []string{})
			mustRunLabelWriteCLI(t, locator,
				"campaign", "edit", "labels-e2e/wave", "--labels", "before-clear",
			)
			jsonCleared := mustRunLabelWriteCLI(t, locator,
				"campaign", "edit", "labels-e2e/wave", "--labels", "[]",
			)
			assertCampaignLabels(t, jsonCleared, []string{})
		})
	}
}

func newLabelWriteDB(t *testing.T) string {
	t.Helper()
	catalogPath := filepath.Join(t.TempDir(), "empty-hook-catalog.json")
	if err := os.WriteFile(catalogPath, []byte(`{"schemaVersion":"wrkf.hook-catalog.v0","hooks":{}}`), 0o600); err != nil {
		t.Fatalf("write hook catalog: %v", err)
	}
	t.Setenv("WRKF_HOOK_CATALOG", catalogPath)
	dbPath := filepath.Join(t.TempDir(), "wrkq.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open label write DB: %v", err)
	}
	if err := database.Migrate(); err != nil {
		_ = database.Close()
		t.Fatalf("migrate label write DB: %v", err)
	}
	if _, err := store.New(database).Containers.Create(
		"00000000-0000-4000-8000-0000000000a0",
		store.ContainerCreateParams{Slug: "labels-project", Kind: "project"},
	); err != nil {
		_ = database.Close()
		t.Fatalf("create label write project: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close label write DB: %v", err)
	}
	return dbPath
}

func newAuthenticatedLabelWriteServer(t *testing.T, dbPath string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open authenticated RPC DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cfg := &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50}
	api, opts, err := bootstrap.Server(database, cfg)
	if err != nil {
		t.Fatalf("bootstrap authenticated RPC server: %v", err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)

	const token = "label-write-test-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, ok := rpcServer.HandleRequest(r.Context(), req)
		if !ok {
			http.Error(w, "unexpected RPC exit", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)
	t.Setenv("WRKQD_TOKEN", token)
	return "rpc://" + strings.TrimPrefix(server.URL, "http://")
}

func runLabelWriteCLI(t *testing.T, locator string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs(append([]string{
		"--db", locator,
		"--project", "labels-project",
		"--principal-ref", "agent:label-write-test",
	}, args...))
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := cmd.Execute()
	return output.String(), err
}

func mustRunLabelWriteCLI(t *testing.T, locator string, args ...string) string {
	t.Helper()
	out, err := runLabelWriteCLI(t, locator, args...)
	if err != nil {
		t.Fatalf("wrkq %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func assertTaskLabels(t *testing.T, locator, task string, want []string) {
	t.Helper()
	out := mustRunLabelWriteCLI(t, locator, "cat", task, "--json", "--one")
	var taskView struct {
		Labels string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(out), &taskView); err != nil {
		t.Fatalf("decode task read-back: %v\n%s", err, out)
	}
	var got []string
	if taskView.Labels != "" {
		if err := json.Unmarshal([]byte(taskView.Labels), &got); err != nil {
			t.Fatalf("decode stored task labels %q: %v", taskView.Labels, err)
		}
	} else {
		got = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s labels = %#v, want %#v", task, got, want)
	}
}

func assertCampaignLabels(t *testing.T, raw string, want []string) {
	t.Helper()
	var envelope struct {
		Container *campaignContainer `json:"container,omitempty"`
		Labels    []string           `json:"labels,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("decode campaign read-back: %v\n%s", err, raw)
	}
	got := envelope.Labels
	if envelope.Container != nil {
		got = envelope.Container.Labels
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("campaign labels = %#v, want %#v", got, want)
	}
}

func assertLabelWriteRefused(t *testing.T, locator string, args []string, task string, want []string) {
	t.Helper()
	out, err := runLabelWriteCLI(t, locator, args...)
	if err == nil {
		t.Fatalf("wrkq %s unexpectedly succeeded: %s", strings.Join(args, " "), out)
	}
	if !strings.Contains(err.Error(), labelValueForms) {
		t.Fatalf("refusal %q does not name accepted forms", err)
	}
	assertTaskLabels(t, locator, task, want)
}

func assertLabelTouchRefused(t *testing.T, locator, value string) {
	t.Helper()
	out, err := runLabelWriteCLI(t, locator,
		"touch", "labels-e2e/refused-touch", "--labels", value,
	)
	if err == nil {
		t.Fatalf("malformed touch unexpectedly succeeded: %s", out)
	}
	if !strings.Contains(err.Error(), labelValueForms) {
		t.Fatalf("touch refusal %q does not name accepted forms", err)
	}
	if out, err := runLabelWriteCLI(t, locator, "cat", "labels-e2e/refused-touch", "--json", "--one"); err == nil {
		t.Fatalf("malformed touch created a task: %s", out)
	}
}

func assertCampaignEditRefused(t *testing.T, locator, value string, want []string) {
	t.Helper()
	out, err := runLabelWriteCLI(t, locator,
		"campaign", "edit", "labels-e2e/wave", "--labels", value,
	)
	if err == nil {
		t.Fatalf("malformed campaign edit unexpectedly succeeded: %s", out)
	}
	if !strings.Contains(err.Error(), labelValueForms) {
		t.Fatalf("campaign refusal %q does not name accepted forms", err)
	}
	readBack := mustRunLabelWriteCLI(t, locator,
		"campaign", "edit", "labels-e2e/wave", "--description", "refusal preserved labels",
	)
	assertCampaignLabels(t, readBack, want)
}
package wrkqapi

import (
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/rpcidem"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/wrkfapi"
)

// API is the wrkq-namespace business surface. It owns task/comment mutation and
// the task↔workflow binding (wrkq.workflow.attach is a wrkq verb), delegating
// workflow state reads to the shared wrkfapi.API.
type API struct {
	db                  *db.DB
	store               *store.Store
	wf                  *wrkfapi.API
	defaultPrincipalRef string
	attachDir           string
	attachMaxMB         int

	// searchCfg is the SERVER's search/index host configuration. The server owns
	// the derived sidecar + dense embedder behind the wrkq.search.* / wrkq.index.*
	// methods (T-05114). Zero-value (Enabled=false) means the search host is not
	// configured and search/index methods report WRKQ_VALIDATION "search is
	// disabled".
	searchCfg SearchConfig

	// uploads tracks in-progress chunked byte uploads (wrkq.attachment.addBytes),
	// keyed by server-generated uploadId. Each session stages bytes into a temp
	// file under the attach dir; finalize atomically renames into place. The mutex
	// guards the map; per-session writes are serialized by the monotonic seq check.
	uploadsMu sync.Mutex
	uploads   map[string]*attachmentUpload
}

// New constructs a wrkq API over the given database. wf provides workflow
// instance/timeline access for the wrkq.workflow.* verbs (may be nil).
// attachDir/attachMaxMB carry the explicitly-configured attachment storage
// settings; an empty attachDir disables attachment writes (attachment.add
// returns WRKQ_VALIDATION rather than silently writing relative to cwd).
func New(database *db.DB, wf *wrkfapi.API, defaultPrincipalRef, attachDir string, attachMaxMB int, opts ...Option) *API {
	a := &API{
		db:                  database,
		store:               store.New(database),
		wf:                  wf,
		defaultPrincipalRef: defaultPrincipalRef,
		attachDir:           attachDir,
		attachMaxMB:         attachMaxMB,
		uploads:             map[string]*attachmentUpload{},
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Option configures optional API capabilities at construction time.
type Option func(*API)

// WithSearch wires the server-owned search/index host (sidecar + dense embedder)
// behind the wrkq.search.* / wrkq.index.* methods. Without it the search/index
// methods report WRKQ_VALIDATION "search is disabled".
func WithSearch(cfg SearchConfig) Option {
	return func(a *API) { a.searchCfg = cfg }
}

// ─── attribution ─────────────────────────────────────────────────────────────

// attributionFor resolves principal-only write attribution. The parameter name
// remains actor while older RPC DTOs are retired. Accepted non-empty values are
// agent:<id> or full agent ScopeRefs, both persisted as the durable agent:<id>
// principal. Empty uses the configured default_principal_ref; if none is
// configured the mutation fails.
func (a *API) attributionFor(actor string) (attribution.Attribution, error) {
	principalInput := strings.TrimSpace(actor)
	if principalInput == "" {
		principalInput = strings.TrimSpace(a.defaultPrincipalRef)
	}
	if principalInput == "" {
		return attribution.Attribution{}, NewValidationError(
			"principalRef is required; include principalRef \"agent:<id>\" or launch with --principal-ref agent:<id> / --as agent:<id>",
			map[string]any{
				"field":    "principalRef",
				"expected": "agent:<id>",
				"example":  "agent:cody",
			})
	}
	principal, err := attribution.NormalizeCanonical(principalInput)
	if err != nil {
		return attribution.Attribution{}, NewValidationError(
			"invalid principalRef: "+err.Error(),
			map[string]any{
				"field":        "principalRef",
				"principalRef": principalInput,
				"expected":     "agent:<id> or full agent ScopeRef",
				"example":      "agent:cody",
			})
	}
	return attribution.Attribution{PrincipalRef: principal}, nil
}

// ─── shared canonical request hashing ────────────────────────────────────────

// canonicalRequestHash is the single canonicalizer used by every mutating wrkq
// method for idempotency request-hashing. It normalizes the value to a
// key-sorted JSON document (Go sorts map keys on marshal) and hashes the
// result. Callers pass the param value with the idempotency key zeroed so the
// key itself is excluded from the hash. Do not hand-roll per-call marshaling
// elsewhere — route through here.
func canonicalRequestHash(v any) string {
	return rpcidem.CanonicalRequestHash(v)
}

// ─── idempotency ledger (wrkq_rpc_idempotency) ───────────────────────────────

// idempotentReplay looks up a persisted result for (namespace, key). It returns
// (result, true, nil) when a row exists with a matching request hash,
// (nil, false, nil) when no row exists, and a WRKQ_CONFLICT error when a row
// exists with a different canonical request hash.
func (a *API) idempotentReplay(namespace, key, requestHash string) (json.RawMessage, bool, error) {
	var storedHash, resultJSON string
	err := a.db.QueryRow(
		"SELECT request_hash, result_json FROM wrkq_rpc_idempotency WHERE namespace = ? AND idempotency_key = ?",
		namespace, key,
	).Scan(&storedHash, &resultJSON)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, NewInternalError(err)
	}
	if storedHash != requestHash {
		return nil, false, NewConflictError("idempotency key reused with a different request", map[string]any{
			"idempotencyKey": key,
		})
	}
	return json.RawMessage(resultJSON), true, nil
}

// idempotentStore persists the committed result for (namespace, key).
func (a *API) idempotentStore(namespace, key, requestHash string, result any) error {
	b, err := json.Marshal(result)
	if err != nil {
		return NewInternalError(err)
	}
	_, err = a.db.Exec(
		"INSERT INTO wrkq_rpc_idempotency (namespace, idempotency_key, request_hash, result_json) VALUES (?, ?, ?, ?)",
		namespace, key, requestHash, string(b),
	)
	if err != nil {
		return NewInternalError(err)
	}
	return nil
}

// ─── small helpers ───────────────────────────────────────────────────────────

// flexString accepts either a JSON string or array of strings.
type flexString []string

func (f *flexString) UnmarshalJSON(data []byte) error {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '[' {
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*f = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s != "" {
		*f = []string{s}
	}
	return nil
}

// toRFC3339 normalizes a stored timestamp string to RFC3339. SQLite columns use
// either strftime ISO-8601 ("...Z") or datetime() ("YYYY-MM-DD HH:MM:SS").
func toRFC3339(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	layouts := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return value
}

// parseLabels decodes a stored JSON labels array into a non-nil slice.
func parseLabels(raw string) []string {
	out := []string{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

// parseMeta decodes a stored JSON meta object into a non-nil map.
func parseMeta(raw string) map[string]any {
	out := map[string]any{}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

// metaString marshals a meta map to a JSON string pointer for storage.
func metaString(meta map[string]any) *string {
	if meta == nil {
		return nil
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

// scopeBind returns the scope ref for a SQL bind, mapping empty to nil.
func scopeBind(attr attribution.Attribution) any {
	if strings.TrimSpace(attr.ScopeRef) == "" {
		return nil
	}
	return attr.ScopeRef
}

// labelsString marshals a labels slice to a JSON string for storage.
func labelsString(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	b, err := json.Marshal(labels)
	if err != nil {
		return ""
	}
	return string(b)
}

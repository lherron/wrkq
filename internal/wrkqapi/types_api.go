//go:build wrkq_local

package wrkqapi

import (
	"sync"

	"github.com/lherron/wrkq/internal/db"

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

// Option configures optional API capabilities at construction time.
type Option func(*API)

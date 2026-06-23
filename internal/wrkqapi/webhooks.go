package wrkqapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

// Global webhook subscriptions live on the SINGLETON ROOT container (kind='root')
// and are inherited by every project via the container chain. This DEDICATED
// family (wrkq.webhook.add / remove / listView, T-05119 daedalus #10211) owns the
// root resolution, URL validation, idempotent add/remove delta, and the
// webhook_urls write — it is deliberately SEPARATE from wrkq.container.update,
// whose patch surface stays narrow ({slug?, title?} only) and rejects webhookUrls
// to avoid reopening the overbroad-mutation-sink it was kept narrow to avoid.

// WebhookListViewParams is the (empty) parameter set for wrkq.webhook.listView.
type WebhookListViewParams struct{}

// WebhookRow is one global webhook URL row. The mirror renders these as legacy
// {url:<value>} NDJSON when non-TTY (and as a plain URL line per row on a TTY).
type WebhookRow struct {
	URL string `json:"url"`
}

// WebhookMutateParams mirrors wrkq.webhook.add / wrkq.webhook.remove params. The
// URL is the single webhook target. ExpectETag is an OPTIONAL CAS over the root
// container's etag; absent (0) preserves the legacy no-CAS last-writer-wins
// behavior, so the CLI mirror — which never sends it — stays byte-for-byte with
// legacy. A raw RPC caller MAY pass it to reduce the concurrent last-writer-wins
// risk; a stale etag → WRKQ_CONFLICT.
type WebhookMutateParams struct {
	URL        string `json:"url"`
	ExpectETag int64  `json:"expectEtag,omitempty"`
	Actor      string `json:"actor,omitempty"`
}

// WebhookListView returns the global webhook URLs stored on the root container,
// in stored order. The mirror renders --json / NDJSON / TTY from these rows.
func (a *API) WebhookListView(ctx context.Context, _ WebhookListViewParams) ([]WebhookRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	urls, err := a.rootWebhookURLs()
	if err != nil {
		return nil, err
	}
	rows := make([]WebhookRow, 0, len(urls))
	for _, u := range urls {
		rows = append(rows, WebhookRow{URL: u})
	}
	return rows, nil
}

// WebhookAdd adds a global webhook URL to the root container (idempotent: a
// duplicate is a no-change). The URL is validated server-side (http/https with a
// host); an invalid URL → WRKQ_VALIDATION. The store records attribution and logs
// container.updated on a real change.
func (a *API) WebhookAdd(ctx context.Context, p WebhookMutateParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	url := strings.TrimSpace(p.URL)
	if !isValidWebhookURL(url) {
		return nil, NewValidationError("invalid webhook url: "+url, map[string]any{"field": "url"})
	}
	return a.mutateRootWebhooks([]string{url}, nil, p.ExpectETag, p.Actor)
}

// WebhookRemove removes a global webhook URL from the root container (idempotent:
// removing an absent URL is a no-change). Unlike add, the URL is NOT validated
// (legacy removes by exact match without a validity check). The store records
// attribution and logs container.updated on a real change.
func (a *API) WebhookRemove(ctx context.Context, p WebhookMutateParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	url := strings.TrimSpace(p.URL)
	return a.mutateRootWebhooks(nil, []string{url}, p.ExpectETag, p.Actor)
}

// mutateRootWebhooks resolves the root container, applies the idempotent add/remove
// delta, and writes webhook_urls through the store when changed. It returns the
// legacy MUTATION RESULT in MAP-ALPHABETICAL key order (built from a map so
// encoding/json sorts the keys): changed = {changed,count,target,webhook_urls};
// no-change = {changed,webhook_urls}.
func (a *API) mutateRootWebhooks(add, remove []string, expectEtag int64, actor string) (json.RawMessage, error) {
	rootUUID, err := store.RootContainerUUID(a.db)
	if err != nil {
		return nil, NewInternalError(err)
	}
	container, err := a.store.Containers.GetByUUID(rootUUID)
	if err != nil {
		return nil, NewInternalError(err)
	}

	newURLs, changed := applyWebhookDelta(container.WebhookURLs, add, remove)
	if !changed {
		return marshalAlpha(map[string]any{
			"changed":      false,
			"webhook_urls": newURLs,
		})
	}

	payload, merr := json.Marshal(newURLs)
	if merr != nil {
		return nil, NewInternalError(merr)
	}
	fields := map[string]interface{}{"webhook_urls": string(payload)}
	attr, aerr := a.attributionFor(actor)
	if aerr != nil {
		return nil, aerr
	}
	if _, uerr := a.store.Containers.UpdateFieldsWithAttribution(attr, rootUUID, fields, expectEtag); uerr != nil {
		return nil, mapWebhookStoreError(uerr)
	}

	target := strings.Join(append(append([]string{}, add...), remove...), ", ")
	return marshalAlpha(map[string]any{
		"changed":      true,
		"target":       target,
		"webhook_urls": newURLs,
		"count":        len(newURLs),
	})
}

// rootWebhookURLs reads the decoded webhook_urls slice off the root container.
func (a *API) rootWebhookURLs() ([]string, error) {
	rootUUID, err := store.RootContainerUUID(a.db)
	if err != nil {
		return nil, NewInternalError(err)
	}
	container, err := a.store.Containers.GetByUUID(rootUUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	return decodeWebhookURLs(container.WebhookURLs), nil
}

// marshalAlpha marshals m into JSON. encoding/json emits map keys in sorted
// (alphabetical) order, which is exactly the legacy webhook mutation key order.
func marshalAlpha(m map[string]any) (json.RawMessage, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, NewInternalError(err)
	}
	return json.RawMessage(b), nil
}

// mapWebhookStoreError maps the store error from the root webhook_urls write. The
// only structured case is the optional etag CAS (WRKQ_CONFLICT); everything else
// is an internal failure.
func mapWebhookStoreError(err error) error {
	if err == nil {
		return nil
	}
	var mismatch *domain.ETagMismatchError
	if errors.As(err, &mismatch) {
		return NewConflictError("container etag precondition failed", map[string]any{"currentEtag": mismatch.Actual})
	}
	return NewInternalError(err)
}

// applyWebhookDelta adds/removes URLs from an existing webhook_urls JSON string,
// returning the new list and whether it changed. Mirrors the legacy
// internal/cli applyWebhookDelta byte-for-byte (idempotent dedupe on add, exact
// match on remove, order preserved).
func applyWebhookDelta(existing *string, add, remove []string) ([]string, bool) {
	var urls []string
	if existing != nil && *existing != "" {
		_ = json.Unmarshal([]byte(*existing), &urls)
	}

	removeSet := make(map[string]bool, len(remove))
	for _, u := range remove {
		removeSet[strings.TrimSpace(u)] = true
	}

	filtered := urls[:0]
	for _, u := range urls {
		if !removeSet[u] {
			filtered = append(filtered, u)
		}
	}

	existingSet := make(map[string]bool, len(filtered))
	for _, u := range filtered {
		existingSet[u] = true
	}
	for _, u := range add {
		trimmed := strings.TrimSpace(u)
		if !existingSet[trimmed] {
			filtered = append(filtered, trimmed)
			existingSet[trimmed] = true
		}
	}

	changed := len(filtered) != len(urls)
	if !changed {
		for i := range filtered {
			if filtered[i] != urls[i] {
				changed = true
				break
			}
		}
	}

	if filtered == nil {
		filtered = []string{}
	}
	return filtered, changed
}

// decodeWebhookURLs decodes the stored webhook_urls JSON string into a slice,
// returning nil for empty/absent/malformed values (legacy behavior).
func decodeWebhookURLs(raw *string) []string {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil
	}
	var urls []string
	if err := json.Unmarshal([]byte(*raw), &urls); err != nil {
		return nil
	}
	return urls
}

// isValidWebhookURL accepts only http/https URLs with a host (legacy parity).
func isValidWebhookURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}

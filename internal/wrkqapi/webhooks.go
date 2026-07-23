package wrkqapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooksub"
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
	return a.mutateRootWebhooks([]webhooksub.Subscription{{URL: url}}, nil, p.ExpectETag, p.Actor)
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

// ContainerWebhookSet mirrors legacy `wrkq container set` webhook-url updates.
// This is intentionally separate from wrkq.container.update: global webhook
// mutation and per-container webhook mutation are reviewed compatibility
// surfaces, while container.update remains the narrow slug/title method.
func (a *API) ContainerWebhookSet(ctx context.Context, p ContainerWebhookSetParams) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}

	if p.All {
		if len(p.AddWebhookURLs) == 0 && len(p.RemoveWebhookURLs) == 0 {
			return nil, NewValidationError("--all requires --add-webhook-url or --remove-webhook-url", map[string]any{"field": "all"})
		}
		if strings.TrimSpace(p.Container) != "" {
			return nil, NewValidationError("--all cannot be used with a container argument", map[string]any{"field": "container"})
		}
		adds, verr := normalizeWebhookSubscriptions(p.AddWebhookURLs, "addWebhookUrls")
		if verr != nil {
			return nil, verr
		}
		containers, err := a.store.Containers.ListAll(false)
		if err != nil {
			return nil, NewInternalError(errors.New("failed to list containers: " + err.Error()))
		}
		updated := 0
		for _, c := range containers {
			newURLs, changed := applyWebhookDelta(c.WebhookURLs, adds, p.RemoveWebhookURLs)
			if !changed {
				continue
			}
			payload, err := webhooksub.Encode(newURLs)
			if err != nil {
				return nil, NewInternalError(errors.New("failed to encode webhook urls: " + err.Error()))
			}
			fields := map[string]interface{}{"webhook_urls": payload}
			if _, err := a.store.Containers.UpdateFieldsWithAttribution(attr, c.UUID, fields, 0); err != nil {
				return nil, NewInternalError(errors.New("failed to update container " + c.ID + ": " + err.Error()))
			}
			updated++
		}
		return marshalAlpha(map[string]any{"updated": updated, "all": true})
	}

	selector := strings.TrimSpace(p.Container)
	if selector == "" {
		return nil, NewValidationError("container argument required (or use --all)", map[string]any{"field": "container"})
	}
	containerUUID, containerPath, err := selectors.ResolveContainer(a.db, selector)
	if err != nil {
		return nil, newError(CodeNotFound, err.Error(), false, struct {
			Ref  string `json:"ref,omitempty"`
			Kind string `json:"kind,omitempty"`
		}{Ref: selector, Kind: "container"}, err)
	}

	var webhookURLs []webhooksub.Subscription
	if p.Replace {
		webhookURLs, err = normalizeWebhookSubscriptions(p.WebhookURLs, "webhookUrls")
		if err != nil {
			return nil, err
		}
		if webhookURLs == nil {
			webhookURLs = []webhooksub.Subscription{}
		}
	} else {
		if len(p.AddWebhookURLs) == 0 && len(p.RemoveWebhookURLs) == 0 {
			return nil, NewValidationError("no updates specified", map[string]any{"field": "webhookUrls"})
		}
		adds, verr := normalizeWebhookSubscriptions(p.AddWebhookURLs, "addWebhookUrls")
		if verr != nil {
			return nil, verr
		}
		container, err := a.store.Containers.GetByUUID(containerUUID)
		if err != nil {
			return nil, NewInternalError(err)
		}
		webhookURLs, _ = applyWebhookDelta(container.WebhookURLs, adds, p.RemoveWebhookURLs)
	}

	payload, err := webhooksub.Encode(webhookURLs)
	if err != nil {
		return nil, NewInternalError(errors.New("failed to encode webhook urls: " + err.Error()))
	}
	fields := map[string]interface{}{"webhook_urls": payload}
	if _, err := a.store.Containers.UpdateFieldsWithAttribution(attr, containerUUID, fields, p.ExpectETag); err != nil {
		return nil, mapWebhookStoreError(err)
	}

	return marshalAlpha(map[string]any{
		"container_uuid": containerUUID,
		"container_path": containerPath,
		"webhook_urls":   webhookURLs,
		"count":          len(webhookURLs),
		"updated":        true,
	})
}

// normalizeWebhookSubscriptions trims and validates every entry of a
// subscription list. The URL keeps the legacy http(s)-with-host rule; event
// names are only trimmed and required to be non-empty, matching the
// dispatcher's permissive matcher (class names like "container"/"task" AND
// exact event names both resolve there, so this layer must not narrow them).
func normalizeWebhookSubscriptions(raw []webhooksub.Subscription, field string) ([]webhooksub.Subscription, error) {
	if raw == nil {
		return nil, nil
	}
	subs := make([]webhooksub.Subscription, 0, len(raw))
	for _, sub := range raw {
		trimmed := strings.TrimSpace(sub.URL)
		if trimmed == "" {
			return nil, NewValidationError("webhook url cannot be empty", map[string]any{"field": field})
		}
		if !isValidWebhookURL(trimmed) {
			return nil, NewValidationError("invalid webhook url: "+trimmed, map[string]any{"field": field})
		}
		var events []string
		for _, ev := range sub.Events {
			e := strings.TrimSpace(ev)
			if e == "" {
				return nil, NewValidationError("webhook event cannot be empty", map[string]any{"field": field})
			}
			events = append(events, e)
		}
		subs = append(subs, webhooksub.Subscription{URL: trimmed, Events: events})
	}
	return subs, nil
}

// mutateRootWebhooks resolves the root container, applies the idempotent add/remove
// delta, and writes webhook_urls through the store when changed. It returns the
// legacy MUTATION RESULT in MAP-ALPHABETICAL key order (built from a map so
// encoding/json sorts the keys): changed = {changed,count,target,webhook_urls};
// no-change = {changed,webhook_urls}.
func (a *API) mutateRootWebhooks(
	add []webhooksub.Subscription,
	remove []string,
	expectEtag int64,
	actor string,
) (json.RawMessage, error) {
	// Validate the explicit actor BEFORE returning any mutation-family response —
	// including the idempotent no-change branch — so a malformed explicit actor
	// never yields a successful no-op. attributionFor rejects invalid non-empty
	// selectors with WRKQ_VALIDATION (daedalus #10291) and reduces valid full
	// ScopeRefs to their durable agent principal.
	attr, aerr := a.attributionFor(actor)
	if aerr != nil {
		return nil, aerr
	}
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

	payload, merr := webhooksub.Encode(newURLs)
	if merr != nil {
		return nil, NewInternalError(merr)
	}
	fields := map[string]interface{}{"webhook_urls": payload}
	if _, uerr := a.store.Containers.UpdateFieldsWithAttribution(attr, rootUUID, fields, expectEtag); uerr != nil {
		return nil, mapWebhookStoreError(uerr)
	}

	target := strings.Join(append(webhooksub.URLs(add), remove...), ", ")
	return marshalAlpha(map[string]any{
		"changed":      true,
		"target":       target,
		"webhook_urls": newURLs,
		"count":        len(newURLs),
	})
}

// rootWebhookURLs reads the URLs of the root container's subscriptions. The
// global webhook family is URL-only by design, so a structured entry written
// through `container set` on the root surfaces here as its bare URL (and is
// preserved verbatim by add/remove, which key on URL).
func (a *API) rootWebhookURLs() ([]string, error) {
	rootUUID, err := store.RootContainerUUID(a.db)
	if err != nil {
		return nil, NewInternalError(err)
	}
	container, err := a.store.Containers.GetByUUID(rootUUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	return webhooksub.URLs(webhooksub.Decode(container.WebhookURLs)), nil
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
func applyWebhookDelta(existing *string, add []webhooksub.Subscription, remove []string) ([]webhooksub.Subscription, bool) {
	subs := webhooksub.Decode(existing)

	removeSet := make(map[string]bool, len(remove))
	for _, u := range remove {
		removeSet[strings.TrimSpace(u)] = true
	}

	filtered := make([]webhooksub.Subscription, 0, len(subs)+len(add))
	index := make(map[string]int, len(subs))
	for _, sub := range subs {
		if removeSet[sub.URL] {
			continue
		}
		index[sub.URL] = len(filtered)
		filtered = append(filtered, sub)
	}

	for _, sub := range add {
		trimmed := strings.TrimSpace(sub.URL)
		if at, ok := index[trimmed]; ok {
			// Re-adding a known URL is still idempotent, but it RE-POINTS that
			// URL's event narrowing so `--add-webhook-url X --webhook-events …`
			// can retarget an existing subscription instead of duplicating it.
			filtered[at].Events = sub.Events
			continue
		}
		index[trimmed] = len(filtered)
		filtered = append(filtered, webhooksub.Subscription{URL: trimmed, Events: sub.Events})
	}

	return filtered, !webhooksub.Equal(filtered, subs)
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

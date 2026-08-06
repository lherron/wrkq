package wrkqapi

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

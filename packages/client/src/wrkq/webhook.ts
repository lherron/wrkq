/**
 * wrkq/webhook.ts — global webhook DTOs (wrkq.webhook.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

/**
 * Global webhook subscriptions live on the SINGLETON ROOT container and are
 * inherited by every project. This is a DEDICATED family (wrkq.webhook.add /
 * remove / listView, T-05119 daedalus #10211), deliberately separate from
 * wrkq.container.update (whose narrow patch surface rejects webhookUrls). Mirrors
 * docs/wrkq-wrkf-rpc.md §6.2 "Global webhook methods".
 */
export interface WrkqWebhookMutateParams {
  url: string;
  /** Optional root-container etag CAS; absent = legacy no-CAS. Stale → WRKQ_CONFLICT. */
  expectEtag?: number;
  actor?: string;
}

/**
 * One stored webhook_urls entry: a bare URL (every event family) or a URL
 * narrowed to event classes ("task" | "workflow" | "container", "*" / "all") or
 * exact event names. A list written as bare URLs stays bare on the wire.
 */
export type WrkqWebhookSubscription =
  string | { url: string; events?: string[] };

/**
 * The legacy MUTATION RESULT for wrkq.webhook.add / remove, in MAP-ALPHABETICAL
 * key order (this OVERRIDES the struct-field-order convention): a changed result
 * carries { changed, count, target, webhook_urls }; a no-change result carries
 * only { changed, webhook_urls }. count/target are present only when changed.
 * The global family is URL-only but PRESERVES structured entries written on the
 * root by `container set`, so webhook_urls may contain narrowed entries.
 */
export interface WrkqWebhookMutateResult {
  changed: boolean;
  count?: number;
  target?: string;
  webhook_urls: WrkqWebhookSubscription[];
}

export type WrkqWebhookListViewParams = Record<string, never>;

/** Legacy {url:<value>} row returned by wrkq.webhook.listView. */
export interface WrkqWebhookRow {
  url: string;
}

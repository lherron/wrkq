package workrpc

import "testing"

// TestCatViewInCatalogs guards daedalus's catView contract corrections (T-05090):
// the compatibility projection must be represented in BOTH the method catalog and
// the DTO catalog so ProtocolSchemaHash() detects added/removed method/DTO names
// and the reflected DTO field/tag shape registered for each DTO.
func TestCatViewInCatalogs(t *testing.T) {
	if !contains(methodCatalog, "wrkq.task.catView") {
		t.Error("wrkq.task.catView missing from methodCatalog (its name would not be in the schema-hash input)")
	}
	if !contains(dtoCatalog, "WrkqTaskCatView") {
		t.Error("WrkqTaskCatView missing from dtoCatalog (its name would not be in the schema-hash input)")
	}
	if !contains(methodCatalog, "wrkq.container.catView") {
		t.Error("wrkq.container.catView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqContainerCatView") {
		t.Error("WrkqContainerCatView missing from dtoCatalog")
	}
	if !contains(methodCatalog, "wrkq.comment.catView") {
		t.Error("wrkq.comment.catView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqCommentCatView") {
		t.Error("WrkqCommentCatView missing from dtoCatalog")
	}
	if !contains(methodCatalog, "wrkq.relation.listView") {
		t.Error("wrkq.relation.listView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "CatViewRelation") {
		t.Error("CatViewRelation missing from dtoCatalog")
	}
	if !contains(methodCatalog, "wrkq.comment.listView") {
		t.Error("wrkq.comment.listView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqCommentListView") {
		t.Error("WrkqCommentListView missing from dtoCatalog")
	}
	if !contains(methodCatalog, "wrkq.attachment.listView") {
		t.Error("wrkq.attachment.listView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqAttachmentListView") {
		t.Error("WrkqAttachmentListView missing from dtoCatalog")
	}
	if !contains(methodCatalog, "wrkq.task.lsView") {
		t.Error("wrkq.task.lsView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqLsListView") {
		t.Error("WrkqLsListView missing from dtoCatalog")
	}
	if !contains(methodCatalog, "wrkq.task.findListView") {
		t.Error("wrkq.task.findListView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqFindListView") {
		t.Error("WrkqFindListView missing from dtoCatalog")
	}
	if !contains(methodCatalog, "wrkq.history.listView") {
		t.Error("wrkq.history.listView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqHistoryListView") {
		t.Error("WrkqHistoryListView missing from dtoCatalog")
	}
	if !contains(dtoCatalog, "WrkqLogEvent") {
		t.Error("WrkqLogEvent missing from dtoCatalog")
	}
	// monitor + watch bounded-polling read models (T-05115 / T-05116): each new
	// method + DTO must be in BOTH catalogs so their NAMES enter the protocol
	// schema-hash input.
	if !contains(methodCatalog, "wrkq.history.tailView") {
		t.Error("wrkq.history.tailView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqHistoryTailView") {
		t.Error("WrkqHistoryTailView missing from dtoCatalog")
	}
	if !contains(dtoCatalog, "WrkqWatchEvent") {
		t.Error("WrkqWatchEvent missing from dtoCatalog")
	}
	if !contains(methodCatalog, "wrkq.monitor.eventsView") {
		t.Error("wrkq.monitor.eventsView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqMonitorEventsView") {
		t.Error("WrkqMonitorEventsView missing from dtoCatalog")
	}
	if !contains(dtoCatalog, "WrkqMonitorEvent") {
		t.Error("WrkqMonitorEvent missing from dtoCatalog")
	}
	if !contains(methodCatalog, "wrkq.monitor.stateView") {
		t.Error("wrkq.monitor.stateView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqMonitorStateView") {
		t.Error("WrkqMonitorStateView missing from dtoCatalog")
	}
	if !contains(methodCatalog, "wrkq.task.treeView") {
		t.Error("wrkq.task.treeView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqTreeView") {
		t.Error("WrkqTreeView missing from dtoCatalog")
	}
	// Byte-transfer boundary (T-05103): the new methods + DTOs must be in BOTH
	// catalogs so their NAMES enter the protocol schema-hash input.
	if !contains(methodCatalog, "wrkq.attachment.getBytes") {
		t.Error("wrkq.attachment.getBytes missing from methodCatalog")
	}
	if !contains(methodCatalog, "wrkq.attachment.addBytes") {
		t.Error("wrkq.attachment.addBytes missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqAttachmentBytes") {
		t.Error("WrkqAttachmentBytes missing from dtoCatalog")
	}
	if !contains(dtoCatalog, "WrkqAttachmentAddBytesResult") {
		t.Error("WrkqAttachmentAddBytesResult missing from dtoCatalog")
	}
	// Container update (T-05112): the new mutation method must be in methodCatalog
	// so its NAME enters the protocol schema-hash input. Its result reuses the
	// existing WrkqContainer DTO (already cataloged), so no new DTO entry is added.
	if !contains(methodCatalog, "wrkq.container.update") {
		t.Error("wrkq.container.update missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqContainer") {
		t.Error("WrkqContainer missing from dtoCatalog (wrkq.container.update result DTO)")
	}
	if !contains(methodCatalog, "wrkq.container.taskCounts") {
		t.Error("wrkq.container.taskCounts missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqContainerTaskCount") {
		t.Error("WrkqContainerTaskCount missing from dtoCatalog")
	}
	if !contains(dtoCatalog, "WrkqContainerTaskCounts") {
		t.Error("WrkqContainerTaskCounts missing from dtoCatalog")
	}
	for _, method := range []string{
		"wrkq.container.campaignActivate",
		"wrkq.container.campaignPortfolio",
	} {
		if !contains(methodCatalog, method) {
			t.Errorf("%s missing from methodCatalog", method)
		}
	}
	for _, dto := range []string{
		"WrkqCampaignProject",
		"WrkqCampaignFootprint",
		"WrkqCampaignPortfolioRow",
		"WrkqCampaignPortfolio",
	} {
		if !contains(dtoCatalog, dto) {
			t.Errorf("%s missing from dtoCatalog", dto)
		}
	}
	// Per-container webhook mutation is deliberately separate from
	// wrkq.container.update's narrow slug/title patch surface. Its result is the
	// legacy map-shaped `container set` output, so only the method name is cataloged.
	if !contains(methodCatalog, "wrkq.container.webhookSet") {
		t.Error("wrkq.container.webhookSet missing from methodCatalog")
	}
	if !contains(methodCatalog, "wrkq.container.archive") {
		t.Error("wrkq.container.archive missing from methodCatalog")
	}
	if !contains(methodCatalog, "wrkq.container.restore") {
		t.Error("wrkq.container.restore missing from methodCatalog")
	}
	// Global webhook family (T-05119): the DEDICATED mutation + list methods must be
	// in methodCatalog so their NAMES enter the protocol schema-hash input. The
	// listView row DTO (WebhookRow) is cataloged; the add/remove mutation result is
	// a map-alphabetical JSON object (no struct), pinned by the fingerprint test.
	if !contains(methodCatalog, "wrkq.webhook.add") {
		t.Error("wrkq.webhook.add missing from methodCatalog")
	}
	if !contains(methodCatalog, "wrkq.webhook.remove") {
		t.Error("wrkq.webhook.remove missing from methodCatalog")
	}
	if !contains(methodCatalog, "wrkq.webhook.listView") {
		t.Error("wrkq.webhook.listView missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WebhookRow") {
		t.Error("WebhookRow missing from dtoCatalog (wrkq.webhook.listView row DTO)")
	}
	// Server-owned deep copy (T-05111): the new mutation method + its DTOs must be
	// in BOTH catalogs so their NAMES enter the protocol schema-hash input.
	if !contains(methodCatalog, "wrkq.task.copy") {
		t.Error("wrkq.task.copy missing from methodCatalog")
	}
	if !contains(dtoCatalog, "WrkqTaskCopyParams") {
		t.Error("WrkqTaskCopyParams missing from dtoCatalog")
	}
	if !contains(dtoCatalog, "WrkqTaskCopyResult") {
		t.Error("WrkqTaskCopyResult missing from dtoCatalog")
	}
	// Handoff family (T-05117) + search + index family (T-05114): the new methods
	// must be in BOTH catalogs so their NAMES enter the protocol schema-hash input.
	// Index lifecycle outputs are map-shaped (no struct DTO).
	for _, m := range []string{
		"wrkq.handoff.create", "wrkq.handoff.get", "wrkq.handoff.listView", "wrkq.handoff.searchView", "wrkq.handoff.acknowledge",
		"wrkq.search.listView",
		"wrkq.index.status",
		"wrkq.index.update",
		"wrkq.index.rebuild",
		"wrkq.index.vacuum",
		"wrkq.index.pause",
		"wrkq.index.resume",
	} {
		if !contains(methodCatalog, m) {
			t.Errorf("%s missing from methodCatalog", m)
		}
	}
	for _, d := range []string{"WrkqHandoff", "WrkqHandoffCreateResult", "WrkqHandoffListResult", "WrkqHandoffSearchResult", "WrkqSearchListView", "WrkqSearchResult", "WrkqIndexStatus"} {
		if !contains(dtoCatalog, d) {
			t.Errorf("%s missing from dtoCatalog", d)
		}
	}
	for _, d := range []string{"WrkfWorkflowContentParams", "WrkfWorkflowDiffParams", "WrkfWorkflowInstallParams"} {
		if !contains(dtoCatalog, d) {
			t.Errorf("%s missing from dtoCatalog", d)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

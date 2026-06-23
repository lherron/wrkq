package workrpc

import "testing"

// TestCatViewInCatalogs guards daedalus's catView contract corrections (T-05090):
// the compatibility projection must be represented in BOTH the method catalog and
// the DTO catalog. NOTE: ProtocolSchemaHash() hashes catalog *names*, not field
// shapes — so these memberships make the hash detect added/removed method/DTO
// NAMES, not field-level drift. Field-shape drift is guarded separately by
// TestCatViewDTOFingerprint (internal/wrkqapi). True shape-hashing is a tracked
// follow-up (see docs/rpc-cli-migration.md).
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
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

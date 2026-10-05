/**
 * wrkq/attachment.ts — attachment DTOs (wrkq.attachment.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

export interface WrkqAttachmentAddParams {
  task: string;
  path: string;
  filename?: string;
  mimeType?: string;
  idempotencyKey?: string;
}

export interface WrkqAttachmentListParams {
  task: string;
  limit?: number;
  cursor?: string;
}

export interface WrkqAttachmentShowParams {
  id: string;
}

export interface WrkqAttachmentRemoveParams {
  id: string;
}

export interface WrkqAttachment {
  uuid: string;
  id: string;
  taskUuid: string;
  filename: string;
  relativePath?: string;
  mimeType?: string;
  sizeBytes: number;
  /** Content checksum (DB/CLI field name; not "sha256"). */
  checksum?: string;
  createdAt: string;
  createdByPrincipalRef?: string;
}

export interface WrkqAttachmentListResult {
  items: WrkqAttachment[];
  nextCursor?: string;
}

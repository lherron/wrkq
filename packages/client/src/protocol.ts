/**
 * protocol.ts — unified protocol constants + lifecycle (rpc.*) DTOs.
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §3, §4. Lifecycle is scoped under `rpc.*`.
 */

/** Unified protocol version (docs/wrkq-wrkf-rpc.md). */
export const PROTOCOL_VERSION = "2026-06-30";

export interface InitializeParams {
  protocolVersion?: string;
  client?: { name: string; version: string };
}

export interface InitializeResult {
  protocolVersion: string;
  /** SHA-256 of the canonical method catalog + DTO schema (entrypoint-stable). */
  protocolSchemaHash: string;
  server: {
    name: string;
    /** Dirty-bearing build version (for example a git-describe string). */
    version: string;
    /** Build source revision; "unknown" for unstamped development builds. */
    revision: string;
    pid: number;
    /** Diagnostic only: which binary launched the server ("wrkq" | "wrkf"). */
    entrypoint?: string;
  };
  database: {
    path: string;
    /** SHA-256 of the applied DB migration set (instance-specific). */
    migrationHash: string;
  };
  capabilities: {
    cancel: boolean;
    wrkq: boolean;
    wrkf: boolean;
    effectClaimLease: boolean;
    runExternalBinding: boolean;
    [k: string]: unknown;
  };
  /** Full registered method catalog. */
  methods: string[];
}

/**
 * stdio-transport.ts — real Transport over `wrkf rpc --stdio`.
 *
 * Spawns the binary with child_process.spawn (NO shell), wires stdout through
 * the JsonRpcChannel, keeps a bounded stderr tail for diagnostics, supports
 * AbortSignal, and rejects all pending requests when the process exits.
 *
 * Contract: docs/wrkf-rpc.md §1, §2, §7.
 * - Global flags (--db/--actor/--role/--hook-catalog) precede the `rpc --stdio`
 *   subcommand, matching the CLI surface.
 * - close() == graceful (stdin EOF == wrkf.exit, §2) then await exit.
 * - kill()  == immediate SIGKILL.
 * - Human CLI output is NEVER parsed; only NDJSON on stdout, logs on stderr.
 */

import { spawn, type ChildProcess } from "node:child_process";
import { JsonRpcChannel } from "./json-rpc-channel.js";
import type { JsonRpcRequest, JsonRpcResponse, Transport } from "./transport.js";

export interface StdioSpawnOptions {
  /** Path/name of the wrkf binary (e.g. "wrkf" or an absolute path). */
  command: string;
  /** argv after the global flags. Defaults to ["rpc", "--stdio"]. */
  args?: string[];
  /** --db */
  dbPath?: string;
  /** --actor */
  actor?: string;
  /** --role */
  role?: string;
  /** --hook-catalog */
  hookCatalogPath?: string;
  /** Abort to kill the process and reject all pending requests. */
  signal?: AbortSignal;
  /** Max bytes of stderr to retain for diagnostics (default 64 KiB). */
  stderrTailBytes?: number;
  /** Graceful-close timeout before escalating to SIGKILL (ms, default 5000). */
  closeTimeoutMs?: number;
  /** Extra environment for the child (merged over process.env). */
  env?: Record<string, string | undefined>;
  /** Working directory for the child. */
  cwd?: string;
}

export class StdioTransport implements Transport {
  private readonly child: ChildProcess;
  private readonly channel: JsonRpcChannel;
  private readonly stderrLimit: number;
  private readonly closeTimeoutMs: number;
  private stderrTail = "";
  private exited = false;
  private exitInfo: { code: number | null; signal: NodeJS.Signals | null } | null = null;
  private signal?: AbortSignal;
  private onAbort?: () => void;

  constructor(opts: StdioSpawnOptions) {
    this.stderrLimit = opts.stderrTailBytes ?? 64 * 1024;
    this.closeTimeoutMs = opts.closeTimeoutMs ?? 5000;

    const argv: string[] = [];
    if (opts.dbPath) argv.push("--db", opts.dbPath);
    if (opts.actor) argv.push("--actor", opts.actor);
    if (opts.role) argv.push("--role", opts.role);
    if (opts.hookCatalogPath) argv.push("--hook-catalog", opts.hookCatalogPath);
    argv.push(...(opts.args ?? ["rpc", "--stdio"]));

    this.child = spawn(opts.command, argv, {
      stdio: ["pipe", "pipe", "pipe"],
      shell: false, // never a shell — no injection, no human CLI parsing
      env: opts.env ? { ...process.env, ...opts.env } : process.env,
      cwd: opts.cwd,
    });

    const stdout = this.child.stdout!;
    const stderr = this.child.stderr!;
    const stdin = this.child.stdin!;
    stdout.setEncoding("utf8");
    stderr.setEncoding("utf8");

    this.channel = new JsonRpcChannel(
      (line) => {
        if (!stdin.writable) throw new Error("wrkf rpc stdin is not writable");
        stdin.write(line);
      },
      () => {
        // Protocol corruption already rejected pending requests; tear down.
        this.kill();
      },
    );

    stdout.on("data", (chunk: string) => this.channel.feed(chunk));
    stderr.on("data", (chunk: string) => {
      this.stderrTail = (this.stderrTail + chunk).slice(-this.stderrLimit);
    });

    this.child.on("exit", (code, signal) => {
      if (this.exited) return;
      this.exited = true;
      this.exitInfo = { code, signal };
      this.channel.rejectAll(
        new Error(
          `wrkf rpc process exited (code=${code}, signal=${signal})` +
            (this.stderrTail ? `\n--- stderr tail ---\n${this.stderrTail}` : ""),
        ),
      );
      this.detachAbort();
    });

    this.child.on("error", (err) => {
      this.channel.rejectAll(new Error(`wrkf rpc spawn error: ${err.message}`));
    });

    if (opts.signal) {
      this.signal = opts.signal;
      this.onAbort = () => this.kill();
      if (opts.signal.aborted) this.kill();
      else opts.signal.addEventListener("abort", this.onAbort, { once: true });
    }
  }

  request(frame: JsonRpcRequest): Promise<JsonRpcResponse> {
    return this.channel.send(frame);
  }

  /** Graceful: end stdin (EOF == wrkf.exit), await exit, escalate to kill on timeout. */
  async close(): Promise<void> {
    if (this.exited) return;
    try {
      this.child.stdin?.end();
    } catch {
      /* ignore */
    }
    await new Promise<void>((resolve) => {
      if (this.exited) return resolve();
      const timer = setTimeout(() => {
        this.kill();
        resolve();
      }, this.closeTimeoutMs);
      this.child.once("exit", () => {
        clearTimeout(timer);
        resolve();
      });
    });
  }

  /** Immediate, ungraceful termination. */
  kill(): void {
    if (this.exited) return;
    try {
      this.child.kill("SIGKILL");
    } catch {
      /* ignore */
    }
  }

  /** Last bytes of the child's stderr (bounded), for diagnostics. */
  get stderr(): string {
    return this.stderrTail;
  }

  get pid(): number | undefined {
    return this.child.pid;
  }

  get exitStatus(): { code: number | null; signal: NodeJS.Signals | null } | null {
    return this.exitInfo;
  }

  private detachAbort(): void {
    if (this.signal && this.onAbort) {
      this.signal.removeEventListener("abort", this.onAbort);
      this.onAbort = undefined;
    }
  }
}

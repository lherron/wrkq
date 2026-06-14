/**
 * stdio-transport.ts — real Transport over `wrkq rpc --stdio` / `wrkf rpc --stdio`.
 *
 * Bun-native: spawns the binary with `Bun.spawn` (NO shell), wires stdout
 * through the JsonRpcChannel, keeps a bounded stderr tail for diagnostics,
 * supports AbortSignal, and rejects all pending requests when the process
 * exits.
 *
 * Contract: docs/wrkq-wrkf-rpc.md §1, §4, §7.
 * - Global flags (--db/--actor/--role/--hook-catalog) precede the `rpc --stdio`
 *   subcommand, matching the CLI surface.
 * - close() == graceful (stdin EOF == rpc.exit) then await exit, escalate on
 *   timeout.
 * - kill()  == immediate SIGKILL.
 * - Human CLI output is NEVER parsed; only NDJSON on stdout, logs on stderr.
 */

import { JsonRpcChannel } from "./json-rpc-channel.js";
import type { JsonRpcRequest, JsonRpcResponse, Transport } from "./transport.js";

export interface StdioSpawnOptions {
  /** Path/name of the binary (e.g. "wrkq", "wrkf", or an absolute path). */
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

/** Basename without directory or extension (e.g. "/usr/bin/wrkf" -> "wrkf"). */
function baseName(command: string): string {
  const slash = Math.max(command.lastIndexOf("/"), command.lastIndexOf("\\"));
  const file = slash >= 0 ? command.slice(slash + 1) : command;
  const dot = file.lastIndexOf(".");
  return dot > 0 ? file.slice(0, dot) : file;
}

type BunSubprocess = {
  stdin: { write(chunk: string): void; flush?(): void; end(): void; readonly writable?: boolean };
  stdout: ReadableStream<Uint8Array>;
  stderr: ReadableStream<Uint8Array>;
  exited: Promise<number>;
  exitCode: number | null;
  pid: number;
  kill(signal?: number | NodeJS.Signals): void;
};

export class StdioTransport implements Transport {
  private readonly proc: BunSubprocess;
  private readonly channel: JsonRpcChannel;
  private readonly stderrLimit: number;
  private readonly closeTimeoutMs: number;
  private stderrTail = "";
  private exited = false;
  private exitCode: number | null = null;
  private signal?: AbortSignal;
  private onAbort?: () => void;

  constructor(opts: StdioSpawnOptions) {
    this.stderrLimit = opts.stderrTailBytes ?? 64 * 1024;
    this.closeTimeoutMs = opts.closeTimeoutMs ?? 5000;

    // The two entrypoints share the RPC server but NOT the global CLI flag
    // surface: `wrkq` attributes writes via `--as` and has no role/hook-catalog
    // flags, while `wrkf` uses `--actor`/`--role`/`--hook-catalog`. Map session
    // options to whichever the launched binary accepts; per-call `actor`/`role`
    // still travel in method params either way.
    const base = baseName(opts.command);
    const isWrkf = base === "wrkf";
    const argv: string[] = [];
    if (opts.dbPath) argv.push("--db", opts.dbPath);
    if (opts.actor) argv.push(isWrkf ? "--actor" : "--as", opts.actor);
    if (isWrkf && opts.role) argv.push("--role", opts.role);
    if (isWrkf && opts.hookCatalogPath) argv.push("--hook-catalog", opts.hookCatalogPath);
    argv.push(...(opts.args ?? ["rpc", "--stdio"]));

    const env = opts.env ? { ...process.env, ...opts.env } : process.env;

    // Bun.spawn: no shell, no injection, no human CLI parsing.
    this.proc = Bun.spawn([opts.command, ...argv], {
      stdin: "pipe",
      stdout: "pipe",
      stderr: "pipe",
      env: env as Record<string, string>,
      cwd: opts.cwd,
    }) as unknown as BunSubprocess;

    this.channel = new JsonRpcChannel(
      (line) => {
        try {
          this.proc.stdin.write(line);
          this.proc.stdin.flush?.();
        } catch (e) {
          throw new Error(`work rpc stdin write failed: ${(e as Error).message}`);
        }
      },
      () => {
        // Protocol corruption already rejected pending requests; tear down.
        this.kill();
      },
    );

    void this.pumpStdout();
    void this.pumpStderr();
    void this.watchExit();

    if (opts.signal) {
      this.signal = opts.signal;
      this.onAbort = () => this.kill();
      if (opts.signal.aborted) this.kill();
      else opts.signal.addEventListener("abort", this.onAbort, { once: true });
    }
  }

  private async pumpStdout(): Promise<void> {
    const decoder = new TextDecoder();
    try {
      for await (const chunk of this.proc.stdout) {
        this.channel.feed(decoder.decode(chunk, { stream: true }));
      }
    } catch {
      // stream torn down on exit/kill; exit watcher rejects pending.
    }
  }

  private async pumpStderr(): Promise<void> {
    const decoder = new TextDecoder();
    try {
      for await (const chunk of this.proc.stderr) {
        this.stderrTail = (this.stderrTail + decoder.decode(chunk, { stream: true })).slice(
          -this.stderrLimit,
        );
      }
    } catch {
      // ignore; diagnostics only.
    }
  }

  private async watchExit(): Promise<void> {
    let code: number | null = null;
    try {
      code = await this.proc.exited;
    } catch {
      code = null;
    }
    if (this.exited) return;
    this.exited = true;
    this.exitCode = code;
    this.channel.rejectAll(
      new Error(
        `work rpc process exited (code=${code})` +
          (this.stderrTail ? `\n--- stderr tail ---\n${this.stderrTail}` : ""),
      ),
    );
    this.detachAbort();
  }

  request(frame: JsonRpcRequest): Promise<JsonRpcResponse> {
    return this.channel.send(frame);
  }

  /** Graceful: end stdin (EOF == rpc.exit), await exit, escalate to kill on timeout. */
  async close(): Promise<void> {
    if (this.exited) return;
    try {
      this.proc.stdin.end();
    } catch {
      /* ignore */
    }
    await new Promise<void>((resolve) => {
      if (this.exited) return resolve();
      const timer = setTimeout(() => {
        this.kill();
        resolve();
      }, this.closeTimeoutMs);
      this.proc.exited.then(
        () => {
          clearTimeout(timer);
          resolve();
        },
        () => {
          clearTimeout(timer);
          resolve();
        },
      );
    });
  }

  /** Immediate, ungraceful termination. */
  kill(): void {
    if (this.exited) return;
    try {
      this.proc.kill();
    } catch {
      /* ignore */
    }
  }

  /** Last bytes of the child's stderr (bounded), for diagnostics. */
  get stderr(): string {
    return this.stderrTail;
  }

  get pid(): number | undefined {
    return this.proc.pid;
  }

  get exitStatus(): number | null {
    return this.exitCode;
  }

  private detachAbort(): void {
    if (this.signal && this.onAbort) {
      this.signal.removeEventListener("abort", this.onAbort);
      this.onAbort = undefined;
    }
  }
}

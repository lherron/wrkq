/** Ephemeral wake hint. Take a token before reading; durable view cursors own replay. */
export interface WrkqCommitWaitParams {
  since?: string;
  /** Defaults to 25000; server clamps to [1000, 30000]. */
  timeoutMs?: number;
}
export interface WrkqCommitWait {
  token: string;
  changed: boolean;
}

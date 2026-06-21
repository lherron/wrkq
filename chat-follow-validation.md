# Chat Follow Validation

## Goal

Validate that Codex waits on inter-agent chat in the token-efficient shape we expect:

1. Codex starts one local shell process with `exec_command`.
2. The shell process owns the wait for the other agent.
3. Codex only samples again when the shell tool returns or when a background poll returns.
4. The new `hrcchat` final-only wait mode emits no progress output while waiting and returns one compact terminal result.

This document is for validating behavior after the `hrcchat` quiet wait changes land.

## Expected Codex Sequence

For a long-running command started through Codex `exec_command`:

```text
t=0s      model calls exec_command
t<=30s    exec_command returns running session id
t~30s     model samples, calls write_stdin empty with 300000
t~330s    write_stdin returns if still running
t~330s    model samples again, maybe polls again
```

The first wakeup exists because Codex caps the initial `exec_command` yield. The later cadence comes from empty `write_stdin` background polling. The desired `hrcchat` behavior is to make each tool result small and final-only, so every model wakeup has minimal prompt cost.

## Validation Setup

Use two HRC agents or one agent plus a controllable test responder.

Recommended handles:

```bash
TARGET=clod@hrc-runtime:primary
```

Use a prompt that forces a delayed response so the wait crosses the initial Codex `exec_command` yield:

```text
Reply exactly "chat-follow validation done" after about 90 seconds.
```

For a longer background-poll validation, delay the response past 5 minutes.

## Baseline: Current Streaming Follow

Before validating the new quiet path, capture the current behavior with the existing streaming mode:

```bash
hrcchat dm "$TARGET" --follow 10s - <<'EOF'
Reply exactly "chat-follow validation done" after about 90 seconds.
EOF
```

Expected baseline behavior:

- `hrcchat` emits progress while waiting.
- Codex captures progress output as tool output.
- If the process is still running after the initial exec yield, Codex receives a running session id and must poll the shell session.
- The final response is present, but the captured output is larger than needed for Codex.

This baseline is not the desired final behavior. It exists to prove the validation harness can observe a delayed chat response.

## Quiet Final-Only Validation

After the new CLI mode lands, validate the Codex-friendly path with the final flag names chosen by the implementation. The expected shape is:

```bash
hrcchat dm "$TARGET" --wait response --timeout 20m --quiet --json - <<'EOF'
Reply exactly "chat-follow validation done" after about 90 seconds.
EOF
```

If the implementation exposes a shared wait command instead, use the equivalent send-then-wait form:

```bash
MESSAGE_JSON=$(hrcchat dm "$TARGET" --json - <<'EOF'
Reply exactly "chat-follow validation done" after about 90 seconds.
EOF
)

MESSAGE_ID=$(printf '%s\n' "$MESSAGE_JSON" | jq -r '.messageId')
hrcchat wait "msg:$MESSAGE_ID" --until response-or-idle --timeout 20m --quiet --json
```

Expected quiet behavior:

- No stdout output before the terminal JSON object.
- No stderr progress, heartbeat, or status output while waiting.
- The command exits after the correlated response arrives or the timeout fires.
- The final JSON is one object, not NDJSON.
- The JSON reports the terminal status and enough correlation data to audit what matched.

Representative success:

```json
{
  "status": "responded",
  "sentMessageId": "msg_...",
  "target": "clod@hrc-runtime:primary",
  "elapsedMs": 90000,
  "correlation": {
    "mode": "reply_to",
    "afterSeq": 12345
  },
  "response": {
    "messageId": "msg_...",
    "from": "clod@hrc-runtime:primary",
    "text": "chat-follow validation done"
  }
}
```

Representative timeout:

```json
{
  "status": "timeout",
  "sentMessageId": "msg_...",
  "target": "clod@hrc-runtime:primary",
  "elapsedMs": 1200000,
  "lastSeq": 12345
}
```

## Codex Tool-Cadence Check

Run the quiet final-only command from a Codex session and watch the tool cadence.

For a 90 second delayed response:

- Codex should issue `exec_command` at `t=0`.
- Codex should regain control at `t<=30s` if the process is still waiting.
- Codex should then issue an empty `write_stdin` poll with a long background yield.
- The `write_stdin` call should return when the response arrives, not at the full 300 second mark.
- The next model sample should see only the compact final JSON.

For a response delayed beyond 5 minutes:

- Codex should issue `exec_command` at `t=0`.
- Codex should poll with empty `write_stdin` around `t~30s`.
- If the command is still running, that poll should return around `t~330s`.
- Codex may sample and issue another empty `write_stdin` poll.
- Intermediate tool outputs should remain small and should not contain progress stream lines.

## Assertions

Pass criteria:

- Existing `--follow` and `--stacked` still stream progress as before.
- The new quiet path does not change existing streaming semantics.
- Quiet wait mode emits no progress output before terminal completion.
- Quiet wait mode returns a single final JSON object.
- Success output identifies the outgoing message, target, elapsed time, correlation mode, and response text.
- Timeout output identifies the outgoing message, target, elapsed time, and resume/inspection cursor.
- In Codex, the model only wakes on tool return or background poll return; chat progress does not force model sampling.

Fail indicators:

- Any progress line appears on stdout or stderr while `--quiet` wait is active.
- The final-only path emits NDJSON progress frames.
- Timeout output lacks a message id or cursor.
- The response match is ambiguous and the JSON does not report fallback correlation.
- Implementing the quiet path changes `--follow` or `--stacked` output.

## Notes

The point of this validation is not to eliminate Codex's first `exec_command` wakeup. Without changing Codex source, that wakeup remains. The point is to ensure every later wakeup is sparse, quiet, and machine-actionable, with `hrcchat` doing the chat polling locally instead of forcing the model to reason over progress output.

import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const repoRoot = process.cwd();
const wrkfcliDir = join(repoRoot, "internal", "wrkfcli");
const rootGoPath = join(wrkfcliDir, "root.go");
const actionGoPath = join(wrkfcliDir, "action.go");

function read(path: string): string {
  return readFileSync(path, "utf8");
}

describe("T-05622 wrkf action command builder split", () => {
  test("moves action command construction out of root.go into focused action builders", () => {
    // T-05622 is a refactor contract: root.go should remain the root wiring and
    // bootstrap surface, while the wrkf action family owns its builder code.
    const rootSource = read(rootGoPath);

    expect(
      rootSource,
      "root.go must still wire the action command into the root command",
    ).toContain("rootCmd.AddCommand(actionCmd())");
    expect(
      rootSource,
      "root.go must not retain the large actionCmd implementation after the split",
    ).not.toMatch(/\nfunc\s+actionCmd\s*\(\)\s+\*cobra\.Command\s*\{/);

    expect(
      existsSync(actionGoPath),
      "action command construction must live in internal/wrkfcli/action.go",
    ).toBe(true);

    const actionSource = read(actionGoPath);
    expect(
      actionSource,
      "the focused action file must define the action command parent builder",
    ).toMatch(/\nfunc\s+actionCmd\s*\(\)\s+\*cobra\.Command\s*\{/);

    for (const builder of [
      "actionNextCmd",
      "actionClaimCmd",
      "actionStartCmd",
      "actionBindCmd",
      "actionCompleteCmd",
      "actionSettleCmd",
      "actionFailCmd",
      "actionHeartbeatCmd",
      "actionShowCmd",
      "actionListCmd",
    ]) {
      expect(
        actionSource,
        `action.go must expose a focused ${builder} builder`,
      ).toMatch(new RegExp(`\\nfunc\\s+${builder}\\s*\\(.*\\)\\s+\\*cobra\\.Command\\s*\\{`));
    }

    for (const optionType of [
      "actionStartOptions",
      "actionNextOptions",
      "actionClaimOptions",
      "actionEvidenceOptions",
      "actionTransitionOptions",
      "actionSettleOptions",
      "actionListOptions",
    ]) {
      expect(
        actionSource,
        `action.go must replace shared action flag locals with ${optionType}`,
      ).toMatch(new RegExp(`\\ntype\\s+${optionType}\\s+struct\\s*\\{`));
    }

    expect(
      actionSource,
      "claim must preserve the existing action lease default",
    ).toContain(`"lease-ms", 300000, "Lease duration in milliseconds"`);
    expect(
      actionSource,
      "settle must preserve the existing terminal settlement default",
    ).toContain(`"result", "completed", "Terminal settlement result"`);
  });
});

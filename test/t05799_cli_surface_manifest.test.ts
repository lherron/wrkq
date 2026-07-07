import { afterEach, describe, expect, test } from "bun:test";
import { existsSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const repoRoot = process.cwd();
const manifestPath = join(repoRoot, "internal", "rpccli", "cli_surface_manifest.json");
const tempGoTestPath = join(repoRoot, "internal", "rpccli", "t05799_watch_visibility_test.go");

afterEach(() => {
  rmSync(tempGoTestPath, { force: true });
});

describe("T-05799 CLI surface manifest", () => {
  test("publishes the Cobra-derived root command manifest with watch still visible", () => {
    expect(existsSync(manifestPath), "expected an embedded/generated CLI surface manifest").toBe(true);

    const manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as {
      root?: { commands?: Array<{ name?: string; hidden?: boolean }> };
    };
    const commands = manifest.root?.commands ?? [];
    const watch = commands.find((command) => command.name === "watch");

    expect(watch, "manifest must include the existing public root command `watch`").toBeDefined();
    expect(watch?.hidden, "`watch` must not be suppressed to satisfy the manifest guard").toBe(false);
  });

  test("root help advertises watch and rpccli does not hide existing commands", () => {
    // This generated Go unit test exercises the actual Cobra tree instead of
    // trusting source text; it guards the defect addendum from T-05799.
    writeFileSync(
      tempGoTestPath,
      `package rpccli

import (
	"bytes"
	"strings"
	"testing"
)

func TestT05799WatchCommandRemainsVisibleInRootHelp(t *testing.T) {
	root := NewRootCmdFor("wrkq")
	var stdout bytes.Buffer
	root.SetArgs([]string{"--help"})
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatalf("root help returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "watch") {
		t.Fatalf("root help must advertise watch; output:\\n%s", output)
	}
	cmd, _, err := root.Find([]string{"watch"})
	if err != nil || cmd == nil || cmd.Name() != "watch" {
		t.Fatalf("watch command must remain addressable, got cmd=%v err=%v", cmd, err)
	}
	if cmd.Hidden || !cmd.IsAvailableCommand() {
		t.Fatalf("watch command must remain visible and available: hidden=%v available=%v", cmd.Hidden, cmd.IsAvailableCommand())
	}
}
`,
    );

    const result = Bun.spawnSync({
      cmd: ["go", "test", "./internal/rpccli", "-run", "^TestT05799WatchCommandRemainsVisibleInRootHelp$"],
      cwd: repoRoot,
      stdout: "pipe",
      stderr: "pipe",
    });
    const output = `${result.stdout.toString()}${result.stderr.toString()}`;
    expect(output).not.toContain("no tests to run");
    expect(result.exitCode, output).toBe(0);

    const hiddenShortcuts = readdirSync(join(repoRoot, "internal", "rpccli"))
      .filter((name) => name.endsWith(".go"))
      .flatMap((name) => {
        const path = join(repoRoot, "internal", "rpccli", name);
        const source = readFileSync(path, "utf8");
        return [...source.matchAll(/Hidden\s*:\s*true|\.Hidden\s*=\s*true|MarkHidden/g)].map(
          (match) => `${name}:${match.index ?? 0}:${match[0]}`,
        );
      });

    expect(hiddenShortcuts).toEqual([]);
  });
});

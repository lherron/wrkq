import { afterEach, describe, expect, test } from "bun:test";
import {
	mkdirSync,
	mkdtempSync,
	readFileSync,
	rmSync,
	writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	type LockProbe,
	type SyncRunner,
	dirtyLockSummary,
	downstreamConsumers,
	syncCommand,
	syncDownstream,
} from "../scripts/sync-downstream";

const fixtureRoots: string[] = [];

afterEach(() => {
	for (const root of fixtureRoots.splice(0)) {
		rmSync(root, { force: true, recursive: true });
	}
});

function createFixture(): string {
	const root = mkdtempSync(join(tmpdir(), "wrkq-sync-downstream-"));
	fixtureRoots.push(root);
	for (const consumer of downstreamConsumers) {
		const checkout = join(root, consumer.directory);
		mkdirSync(checkout, { recursive: true });
		writeFileSync(
			join(checkout, "package.json"),
			`${JSON.stringify(
				{
					name: consumer.packageName,
					scripts: { "sync:wrkq": "bun scripts/sync-wrkq-from-verdaccio.ts" },
				},
				null,
				2,
			)}\n`,
		);
	}
	return root;
}

describe("sync-downstream", () => {
	test("syncs the four owned @wrkq/client consumers in stable order with --pull", async () => {
		expect(downstreamConsumers.map(({ directory }) => directory)).toEqual([
			"hrc-runtime",
			"agent-control-plane",
			"taskboard",
			"agent-loop",
		]);

		const root = createFixture();
		const calls: string[] = [];
		const runner: SyncRunner = (consumer, command) => {
			calls.push(`${consumer.directory}:${command.join(" ")}`);
			return { status: 0 };
		};

		await syncDownstream(root, runner);
		expect(calls).toEqual([
			"hrc-runtime:run sync:wrkq -- --pull",
			"agent-control-plane:run sync:wrkq -- --pull",
			"taskboard:run sync:wrkq -- --pull",
			"agent-loop:run sync:wrkq -- --pull",
		]);
		expect(syncCommand).toEqual(["run", "sync:wrkq", "--", "--pull"]);
	});

	test("validates every checkout before running any sync", async () => {
		const root = createFixture();
		rmSync(join(root, "agent-loop"), { force: true, recursive: true });
		const calls: string[] = [];

		await expect(
			syncDownstream(root, (consumer) => {
				calls.push(consumer.directory);
				return { status: 0 };
			}),
		).rejects.toThrow(/agent-loop: configured checkout is not a directory/);
		expect(calls).toEqual([]);
	});

	test("fails clearly on invalid package metadata", async () => {
		const cases = [
			{
				body: "{",
				expected: /package\.json is invalid JSON/,
			},
			{
				body: JSON.stringify({
					name: "wrong-package",
					scripts: { "sync:wrkq": "bun sync.ts" },
				}),
				expected: /package\.json name must be "agent-loop"/,
			},
			{
				body: JSON.stringify({ name: "agent-loop", scripts: {} }),
				expected: /must define a non-empty "sync:wrkq" script/,
			},
		];

		for (const { body, expected } of cases) {
			const root = createFixture();
			writeFileSync(join(root, "agent-loop", "package.json"), body);
			await expect(syncDownstream(root, () => ({ status: 0 }))).rejects.toThrow(
				expected,
			);
		}
	});

	test("reports the failing consumer and exit code", async () => {
		const root = createFixture();
		await expect(
			syncDownstream(root, (consumer) => ({
				status: consumer.directory === "taskboard" ? 17 : 0,
			})),
		).rejects.toThrow(/taskboard: "sync:wrkq --pull" failed with exit code 17/);
	});

	// T-07629: an install here must not write git history in a repo it does not
	// own. The sync leaves bun.lock dirty and names every repo it dirtied.
	test("never runs git in a consumer checkout", async () => {
		const root = createFixture();
		const commands: string[] = [];
		const runner: SyncRunner = (_consumer, command) => {
			commands.push(command.join(" "));
			return { status: 0 };
		};

		await syncDownstream(root, runner, () => false);

		expect(commands).toEqual([
			"run sync:wrkq -- --pull",
			"run sync:wrkq -- --pull",
			"run sync:wrkq -- --pull",
			"run sync:wrkq -- --pull",
		]);
		// The driver only ever spawns git read-only, to detect the dirty lock.
		const source = readFileSync(
			join(import.meta.dir, "..", "scripts", "sync-downstream.ts"),
			"utf8",
		);
		expect(source).not.toMatch(/"commit"|"add"/);
		expect(source.match(/spawnSync\("git", \[[^\]]*\]/g)).toEqual([
			'spawnSync("git", ["status", "--porcelain", "--", "bun.lock"]',
		]);
	});

	test("leaves the lockfile dirty and names each repo it dirtied", async () => {
		const root = createFixture();
		const lines: string[] = [];
		const log = console.log;
		console.log = (line: string) => {
			lines.push(line);
		};
		const probe: LockProbe = (consumer) =>
			consumer.directory === "taskboard" || consumer.directory === "agent-loop";

		try {
			await syncDownstream(root, () => ({ status: 0 }), probe);
		} finally {
			console.log = log;
		}

		expect(lines).toEqual([
			"[sync-downstream] bun.lock updated in taskboard — commit it with your next landing",
			"[sync-downstream] bun.lock updated in agent-loop — commit it with your next landing",
		]);
	});

	test("says nothing when no consumer lockfile changed", async () => {
		const root = createFixture();
		const lines: string[] = [];
		const log = console.log;
		console.log = (line: string) => {
			lines.push(line);
		};

		try {
			await syncDownstream(root, () => ({ status: 0 }), () => false);
		} finally {
			console.log = log;
		}

		expect(lines).toEqual([]);
		expect(dirtyLockSummary([])).toEqual([]);
	});
});

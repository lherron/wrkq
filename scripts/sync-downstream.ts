import { spawnSync } from "node:child_process";
import { readFile, stat } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";

export type DownstreamConsumer = {
	directory: string;
	label: string;
	packageName: string;
};

export type ResolvedDownstreamConsumer = DownstreamConsumer & {
	path: string;
};

export type SyncRunner = (
	consumer: ResolvedDownstreamConsumer,
	command: readonly string[],
) => { status: number | null; stderr?: string; stdout?: string };

export const syncCommand = ["run", "sync:wrkq", "--", "--pull"] as const;

export const downstreamConsumers: readonly DownstreamConsumer[] = [
	{ directory: "hrc-runtime", label: "hrc-sync", packageName: "hrc-runtime" },
	{
		directory: "agent-control-plane",
		label: "acp-sync",
		packageName: "agent-control-plane",
	},
	{ directory: "taskboard", label: "taskboard-sync", packageName: "webwrkq" },
	{
		directory: "agent-loop",
		label: "agent-loop-sync",
		packageName: "agent-loop",
	},
];

type PackageManifest = {
	name?: unknown;
	scripts?: Record<string, unknown>;
};

const wrkqRoot = resolve(import.meta.dir, "..");
const defaultDownstreamRoot = dirname(wrkqRoot);

function errorMessage(
	consumer: ResolvedDownstreamConsumer,
	detail: string,
): Error {
	return new Error(
		`sync-downstream: ${consumer.directory}: ${detail} (${consumer.path})`,
	);
}

async function validateConsumer(
	consumer: ResolvedDownstreamConsumer,
): Promise<void> {
	const checkout = await stat(consumer.path).catch(() => undefined);
	if (!checkout?.isDirectory()) {
		throw errorMessage(consumer, "configured checkout is not a directory");
	}

	const manifestPath = join(consumer.path, "package.json");
	const raw = await readFile(manifestPath, "utf8").catch((error: unknown) => {
		throw errorMessage(
			consumer,
			`cannot read package.json: ${error instanceof Error ? error.message : String(error)}`,
		);
	});

	let manifest: PackageManifest;
	try {
		manifest = JSON.parse(raw) as PackageManifest;
	} catch (error) {
		throw errorMessage(
			consumer,
			`package.json is invalid JSON: ${error instanceof Error ? error.message : String(error)}`,
		);
	}

	if (manifest.name !== consumer.packageName) {
		throw errorMessage(
			consumer,
			`package.json name must be ${JSON.stringify(consumer.packageName)}, got ${JSON.stringify(manifest.name)}`,
		);
	}
	if (
		typeof manifest.scripts?.["sync:wrkq"] !== "string" ||
		manifest.scripts["sync:wrkq"].trim() === ""
	) {
		throw errorMessage(
			consumer,
			'package.json must define a non-empty "sync:wrkq" script',
		);
	}
}

function emitPrefixed(
	prefix: string,
	output: string | undefined,
	write: (line: string) => void,
): void {
	if (!output) return;
	for (const line of output.trimEnd().split("\n")) write(`[${prefix}] ${line}`);
}

const defaultRunner: SyncRunner = (consumer, command) => {
	const result = spawnSync("bun", command, {
		cwd: consumer.path,
		encoding: "utf8",
		env: process.env,
		stdio: "pipe",
	});
	return {
		status: result.status,
		stderr: result.stderr,
		stdout: result.stdout,
	};
};

export async function syncDownstream(
	root = process.env.WRKQ_DOWNSTREAM_ROOT ?? defaultDownstreamRoot,
	runner: SyncRunner = defaultRunner,
): Promise<void> {
	const consumers = downstreamConsumers.map((consumer) => ({
		...consumer,
		path: join(resolve(root), consumer.directory),
	}));

	// Validate the complete inventory before mutating any checkout.
	await Promise.all(consumers.map(validateConsumer));

	for (const consumer of consumers) {
		const result = runner(consumer, syncCommand);
		emitPrefixed(consumer.label, result.stdout, console.log);
		emitPrefixed(consumer.label, result.stderr, console.error);
		if (result.status !== 0) {
			throw errorMessage(
				consumer,
				`"sync:wrkq --pull" failed with exit code ${result.status ?? "unknown"}`,
			);
		}
	}
}

if (import.meta.main) {
	await syncDownstream().catch((error: unknown) => {
		console.error(error instanceof Error ? error.message : String(error));
		process.exit(1);
	});
}

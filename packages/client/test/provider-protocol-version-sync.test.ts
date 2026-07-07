/**
 * T-05875 red bar: the checked-in wrkq capability provider manifest must
 * advertise the same rpc.initialize protocol version accepted by the Go
 * workrpc server.
 */

import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const repoRoot = resolve(import.meta.dir, "../../..");

function readRepoFile(path: string) {
  return readFileSync(resolve(repoRoot, path), "utf8");
}

function runtimeProtocolVersion() {
  const source = readRepoFile("internal/workrpc/doc.go");
  const match = source.match(/const\s+ProtocolVersion\s*=\s*"([^"]+)"/);
  expect(match, "internal/workrpc/doc.go declares ProtocolVersion").not.toBeNull();
  return match?.[1] ?? "";
}

function providerInitializeProtocolVersions(manifest: string) {
  return [...manifest.matchAll(/protocolVersion:\s*"([^"]+)"/g)].map((match) => match[1]);
}

function providerSourceProtocolVersion(manifest: string) {
  const match = manifest.match(/x-source:\s*\n\s+protocolVersion:\s*\n\s+value:\s*"([^"]+)"/);
  expect(match, "cap/provider.wrkq.yaml declares x-source.protocolVersion.value").not.toBeNull();
  return match?.[1] ?? "";
}

describe("wrkq provider protocol version contract", () => {
  test("provider manifest lifecycle and metadata match workrpc.ProtocolVersion", () => {
    const manifest = readRepoFile("cap/provider.wrkq.yaml");
    const expected = runtimeProtocolVersion();
    const initializeVersions = providerInitializeProtocolVersions(manifest);

    expect(
      initializeVersions,
      "every binding lifecycle rpc.initialize params.protocolVersion must match workrpc.ProtocolVersion",
    ).toEqual([expected]);
    expect(
      providerSourceProtocolVersion(manifest),
      "x-source.protocolVersion.value must match workrpc.ProtocolVersion",
    ).toBe(expected);
  });
});

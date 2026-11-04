import { expect, test } from "bun:test";
import { createClient } from "../src/client";
import type { JsonRpcRequest, Transport } from "../src/transport";

test("caller resolves once: component, environment, seat, unknown", async () => {
 for (const [componentName, env, want] of [
  ["component", { PRAESIDIUM_CALLER: "parent", ASP_HANDLE: "seat" }, "component"],
  [undefined, { PRAESIDIUM_CALLER: "parent", ASP_HANDLE: "seat" }, "parent"],
  [undefined, { PRAESIDIUM_CALLER: "", ASP_HANDLE: "seat@wrkq:primary" }, "seat@wrkq:primary"],
  [undefined, { PRAESIDIUM_CALLER: "", ASP_HANDLE: "" }, process.stdin.isTTY ? "tty" : "unknown"],
  [undefined, { PRAESIDIUM_CALLER: "bad value", ASP_HANDLE: "seat" }, "unknown"],
 ] as const) {
  const frames: JsonRpcRequest[] = [];
  const transport: Transport = {request: async f => {frames.push(f);return {jsonrpc:"2.0",id:f.id,result:{}}},close:async()=>{},kill:()=>{}};
  const options = {componentName, env: {...env} as Record<string,string>, transport, autoInitialize:false};
  const client = await createClient(options);
  options.env.PRAESIDIUM_CALLER = "changed";
  await client.call("probe");await client.call("probe");
  expect(frames.map(f => (f as unknown as {caller:string}).caller)).toEqual([want,want]);
 }
});

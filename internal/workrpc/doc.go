// Package workrpc will provide the unified JSON-RPC 2.0 server for the
// wrkq+wrkf protocol (protocol version 2026-06-30).
//
// This package replaces internal/wrkfrpc for the unified protocol. It exposes
// a shared registry and lifecycle for both wrkq.* and wrkf.* method families.
package workrpc

// ProtocolVersion is the unified RPC protocol version this package will implement.
const ProtocolVersion = "2026-06-30"

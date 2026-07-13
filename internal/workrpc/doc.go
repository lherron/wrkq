// Package workrpc provides the unified JSON-RPC 2.0 server for the wrkq+wrkf
// protocol. It exposes a shared registry and lifecycle for both wrkq.* and
// wrkf.* method families.
package workrpc

// ProtocolVersion is the unified RPC protocol version implemented by this package.
const ProtocolVersion = "2026-06-30"

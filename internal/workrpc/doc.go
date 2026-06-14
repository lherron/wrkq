// Package workrpc will provide the unified JSON-RPC 2.0 server for the
// wrkq+wrkf protocol (protocol version 2026-06-14).
//
// This package is the planned replacement for internal/wrkfrpc. It will expose
// a shared registry and lifecycle for both wrkq.* and wrkf.* method families.
//
// Status: stub — registry and server are not yet implemented. See P1 (T-04424).
// The registry contract tests in this package define the acceptance criteria.
package workrpc

// ProtocolVersion is the unified RPC protocol version this package will implement.
const ProtocolVersion = "2026-06-14"

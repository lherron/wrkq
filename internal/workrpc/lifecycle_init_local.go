//go:build wrkq_local

package workrpc

import "os"

// newInitializeResult builds the server side of the rpc.initialize handshake
// from server-owned RegistryOptions, so it exists only in server builds
// (T-07090). Portable clients consume initializeResult; they never build it.
func newInitializeResult(opts RegistryOptions, methods []string) initializeResult {
	version := opts.ServerVersion
	if version == "" {
		version = "dev"
	}
	revision := opts.ServerRevision
	if revision == "" {
		revision = "unknown"
	}
	entrypoint := opts.Entrypoint
	if entrypoint == "" {
		entrypoint = "wrkq"
	}
	return initializeResult{
		ProtocolVersion:    ProtocolVersion,
		ProtocolSchemaHash: ProtocolSchemaHash(),
		Server: serverInfo{
			Name:       "wrkq-wrkf-rpc",
			Version:    version,
			Revision:   revision,
			PID:        os.Getpid(),
			Entrypoint: entrypoint,
		},
		Database: databaseInfo{
			Path:          opts.DatabasePath,
			MigrationHash: opts.MigrationHash,
		},
		Capabilities: defaultCapabilities(),
		Methods:      methods,
	}
}

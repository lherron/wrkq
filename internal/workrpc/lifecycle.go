package workrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/db"
)

type Capabilities struct {
	Cancel             bool `json:"cancel"`
	Wrkq               bool `json:"wrkq"`
	Wrkf               bool `json:"wrkf"`
	EffectClaimLease   bool `json:"effectClaimLease"`
	RunExternalBinding bool `json:"runExternalBinding"`
}

func defaultCapabilities() Capabilities {
	return Capabilities{
		Cancel:             true,
		Wrkq:               true,
		Wrkf:               true,
		EffectClaimLease:   true,
		RunExternalBinding: true,
	}
}

func ProtocolSchemaHash() string {
	h := sha256.New()
	_, _ = fmt.Fprintln(h, "protocolVersion:"+ProtocolVersion)
	for _, method := range MethodCatalog() {
		_, _ = fmt.Fprintln(h, "method:"+method)
	}
	for _, code := range ErrorCodeCatalog() {
		_, _ = fmt.Fprintln(h, "error:"+code)
	}
	for _, dto := range dtoCatalog {
		_, _ = fmt.Fprintln(h, "dto:"+dto)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func MigrationHash(database *db.DB) string {
	if database == nil {
		return zeroHash()
	}
	applied, pending, err := database.MigrationStatus()
	if err != nil {
		return zeroHash()
	}
	h := sha256.New()
	for _, migration := range applied {
		_, _ = fmt.Fprintln(h, "applied:"+migration)
	}
	for _, migration := range pending {
		_, _ = fmt.Fprintln(h, "pending:"+migration)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func zeroHash() string {
	return "sha256:" + strings.Repeat("0", 64)
}

type initializeResult struct {
	ProtocolVersion    string       `json:"protocolVersion"`
	ProtocolSchemaHash string       `json:"protocolSchemaHash"`
	Server             serverInfo   `json:"server"`
	Database           databaseInfo `json:"database"`
	Capabilities       Capabilities `json:"capabilities"`
	Methods            []string     `json:"methods"`
}

type serverInfo struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	PID        int    `json:"pid"`
	Entrypoint string `json:"entrypoint"`
}

type databaseInfo struct {
	Path          string `json:"path"`
	MigrationHash string `json:"migrationHash"`
}

func newInitializeResult(opts RegistryOptions, methods []string) initializeResult {
	version := opts.ServerVersion
	if version == "" {
		version = "dev"
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

package workrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
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
		if typ, ok := dtoSchemaTypes[dto]; ok {
			_, _ = fmt.Fprintln(h, "dto-shape:"+dtoSchemaFingerprint(dto, typ))
		} else {
			_, _ = fmt.Fprintln(h, "dto-shape-missing:"+dto)
		}
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
	Revision   string `json:"revision"`
	PID        int    `json:"pid"`
	Entrypoint string `json:"entrypoint"`
}

type databaseInfo struct {
	Path          string `json:"path"`
	MigrationHash string `json:"migrationHash"`
}

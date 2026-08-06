//go:build wrkq_local

package workrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/lherron/wrkq/internal/db"
)

// MigrationHash is diagnostic server identity derived from the local migration
// ledger, so it exists only in builds that link the database (T-07090).
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

package store

import "database/sql"

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

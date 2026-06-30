package cli

import "github.com/lherron/wrkq/internal/attribution"

func scopeBind(attr attribution.Attribution) interface{} {
	if attr.ScopeRef == "" {
		return nil
	}
	return attr.ScopeRef
}

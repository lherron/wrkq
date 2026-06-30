package store

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
)

func (s *Store) attributionFromActorUUID(actorUUID string) attribution.Attribution {
	actorUUID = strings.TrimSpace(actorUUID)
	if actorUUID == "" {
		return attribution.Attribution{}
	}
	return attribution.Attribution{PrincipalRef: "agent:" + actorUUID}
}

func requireAttribution(attr attribution.Attribution) error {
	if err := attribution.ValidatePrincipalRef(attr.PrincipalRef); err != nil {
		return fmt.Errorf("invalid attribution principal: %w", err)
	}
	return nil
}

func legacyActorSQL(attr attribution.Attribution) interface{} {
	return nil
}

func principalSQL(attr attribution.Attribution) interface{} {
	if strings.TrimSpace(attr.PrincipalRef) == "" {
		return nil
	}
	return attr.PrincipalRef
}

func scopeSQL(attr attribution.Attribution) interface{} {
	if strings.TrimSpace(attr.ScopeRef) == "" {
		return nil
	}
	return attr.ScopeRef
}

func actorUUIDPtr(attr attribution.Attribution) *string {
	return nil
}

func eventActorUUID(attr attribution.Attribution) *string {
	return actorUUIDPtr(attr)
}

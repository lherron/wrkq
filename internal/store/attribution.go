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

	attr := attribution.Attribution{LegacyActorUUID: &actorUUID}
	var slug, id string
	if err := s.db.QueryRow("SELECT slug, id FROM actors WHERE uuid = ? LIMIT 1", actorUUID).Scan(&slug, &id); err == nil {
		attr.PrincipalRef = "agent:" + slug
		attr.LegacyActorID = id
		return attr
	}
	attr.PrincipalRef = "agent:" + actorUUID
	return attr
}

func requireAttribution(attr attribution.Attribution) error {
	if err := attribution.ValidatePrincipalRef(attr.PrincipalRef); err != nil {
		return fmt.Errorf("invalid attribution principal: %w", err)
	}
	return nil
}

func legacyActorSQL(attr attribution.Attribution) interface{} {
	if attr.LegacyActorUUID == nil || strings.TrimSpace(*attr.LegacyActorUUID) == "" {
		return nil
	}
	return *attr.LegacyActorUUID
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
	if attr.LegacyActorUUID == nil || strings.TrimSpace(*attr.LegacyActorUUID) == "" {
		return nil
	}
	return attr.LegacyActorUUID
}

func eventActorUUID(attr attribution.Attribution) *string {
	return actorUUIDPtr(attr)
}

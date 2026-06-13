package cli

import "github.com/lherron/wrkq/internal/attribution"

func attributionFromLegacyActor(actorUUID string) attribution.Attribution {
	attr := attribution.Attribution{PrincipalRef: "agent:" + actorUUID}
	if actorUUID != "" {
		attr.LegacyActorUUID = &actorUUID
	}
	return attr
}

func legacyActorBind(attr attribution.Attribution) interface{} {
	if attr.LegacyActorUUID == nil || *attr.LegacyActorUUID == "" {
		return nil
	}
	return *attr.LegacyActorUUID
}

func scopeBind(attr attribution.Attribution) interface{} {
	if attr.ScopeRef == "" {
		return nil
	}
	return attr.ScopeRef
}

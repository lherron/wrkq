package rpccli

import (
	"context"
	"encoding/json"
	"strings"
)

// wrkcAdhocIdentity is presentation data composed from the existing member and
// log read models. It deliberately stays out of WrkqRoom: pair identity is a
// wrkc rendering concern, not a second stored room name.
type wrkcAdhocIdentity struct {
	Members []string
	Last    string
}

func loadWrkcAdhocIdentity(ctx context.Context, tr Transport, common map[string]any, room roomWire, includeLast bool) (*wrkcAdhocIdentity, error) {
	if room.Kind != "adhoc" {
		return nil, nil
	}
	identity := &wrkcAdhocIdentity{Members: []string{}}
	params := wrkcIdentityParams(common)
	params["room"] = room.Key
	raw, err := tr.Call(ctx, "wrkq.room.membersView", params)
	if err != nil {
		return nil, err
	}
	var members roomMembersViewWire
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, err
	}
	for _, member := range members.Items {
		if member.LeftAt == nil {
			identity.Members = append(identity.Members, member.MemberRef)
		}
	}
	if !includeLast || room.MessageCount == 0 {
		return identity, nil
	}
	params = wrkcIdentityParams(common)
	params["room"] = room.Key
	params["limit"] = 1
	raw, err = tr.Call(ctx, "wrkq.room.logView", params)
	if err != nil {
		return nil, err
	}
	var log roomLogViewWire
	if err := json.Unmarshal(raw, &log); err != nil {
		return nil, err
	}
	if len(log.Items) > 0 {
		identity.Last = clipWrkcFirstLine(log.Items[len(log.Items)-1].Body, 80)
	}
	return identity, nil
}

func loadWrkcAdhocIdentities(ctx context.Context, tr Transport, common map[string]any, rooms []roomWire) (map[string]wrkcAdhocIdentity, error) {
	identities := map[string]wrkcAdhocIdentity{}
	for _, room := range rooms {
		identity, err := loadWrkcAdhocIdentity(ctx, tr, common, room, true)
		if err != nil {
			return nil, err
		}
		if identity != nil {
			identities[room.Key] = *identity
		}
	}
	return identities, nil
}

func wrkcIdentityParams(params map[string]any) map[string]any {
	result := make(map[string]any, 4)
	for _, key := range []string{"principalRef", "scopeRef"} {
		if value, ok := params[key]; ok {
			result[key] = value
		}
	}
	return result
}

func clipWrkcFirstLine(body string, limit int) string {
	line, _, _ := strings.Cut(body, "\n")
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return line
}

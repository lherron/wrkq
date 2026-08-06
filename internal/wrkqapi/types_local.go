//go:build wrkq_local

package wrkqapi

import (
	"encoding/json"
	"fmt"
)

func (p *TaskCreateParams) UnmarshalJSON(b []byte) error {
	type taskCreateParams TaskCreateParams
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if _, ok := raw["actor"]; ok {
		return fmt.Errorf("unsupported task create field %q; use principalRef", "actor")
	}
	var out taskCreateParams
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*p = TaskCreateParams(out)
	return nil
}

func (p *TaskPatch) UnmarshalJSON(b []byte) error {
	type taskPatch TaskPatch
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	allowed := map[string]bool{
		"slug": true, "title": true, "description": true, "specification": true, "outcome": true, "state": true,
		"priority": true, "kind": true, "riskClass": true, "parentTask": true, "labels": true, "meta": true, "metaRaw": true,
		"assigneePrincipalRef": true, "requestedBy": true, "assignedProject": true, "resolution": true,
		"dueAt": true, "startAt": true, "causedBy": true, "campaign": true,
	}
	for key := range raw {
		if !allowed[key] {
			return fmt.Errorf("unsupported task patch field %q", key)
		}
	}
	var out taskPatch
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*p = TaskPatch(out)
	return nil
}

// AttachmentByteChunkBytes is the server's preferred raw-bytes-per-chunk on read.
// 1 MiB raw → ~1.34 MiB base64, comfortably under the 8 MiB frame cap with JSON
// envelope overhead.
const AttachmentByteChunkBytes = 1 << 20

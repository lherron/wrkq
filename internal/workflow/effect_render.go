//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

func nextEffectSequenceTx(tx *sql.Tx, instanceID string) (int64, error) {
	var seq int64
	err := tx.QueryRow(`SELECT COALESCE(MAX(sequence), 0) + 1 FROM workflow_effects WHERE instance_id = ?`, instanceID).Scan(&seq)
	return seq, err
}

var unresolvedEffectTokenRE = regexp.MustCompile(`\{[A-Za-z][A-Za-z0-9_]*\}`)

func effectRenderReplacements(ctx effectRenderContext, kind string) map[string]string {
	return map[string]string{
		"{instanceId}":                 ctx.instance.ID,
		"{taskUuid}":                   ctx.instance.TaskUUID,
		"{taskRef}":                    ctx.instance.TaskRef,
		"{revision}":                   fmt.Sprint(ctx.instance.Revision),
		"{outcome}":                    ctx.outcomeID,
		"{kind}":                       kind,
		"{sequence}":                   fmt.Sprint(ctx.sequence),
		"{runId}":                      ctx.runID,
		"{sourceImplementActionRunId}": ctx.runID,
	}
}

func renderEffectTemplateString(value string, replacements map[string]string) (string, error) {
	rendered := value
	for token, replacement := range replacements {
		if strings.Contains(rendered, token) && replacement == "" {
			return "", fmt.Errorf("effect template token %s resolved empty", token)
		}
		rendered = strings.ReplaceAll(rendered, token, replacement)
	}
	if unresolved := unresolvedEffectTokenRE.FindString(rendered); unresolved != "" {
		return "", fmt.Errorf("unresolved effect template token %s", unresolved)
	}
	return rendered, nil
}

func renderEffectTemplateValue(value interface{}, replacements map[string]string) (interface{}, error) {
	switch v := value.(type) {
	case string:
		return renderEffectTemplateString(v, replacements)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i := range v {
			rendered, err := renderEffectTemplateValue(v[i], replacements)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, child := range v {
			renderedKey, err := renderEffectTemplateString(key, replacements)
			if err != nil {
				return nil, err
			}
			rendered, err := renderEffectTemplateValue(child, replacements)
			if err != nil {
				return nil, err
			}
			out[renderedKey] = rendered
		}
		return out, nil
	default:
		return value, nil
	}
}

func renderEffectSpec(ef EffectSpec, ctx effectRenderContext) (EffectSpec, string, error) {
	rendered := ef
	replacements := effectRenderReplacements(ctx, ef.Kind)
	var err error
	rendered.Kind, err = renderEffectTemplateString(rendered.Kind, replacements)
	if err != nil {
		return EffectSpec{}, "", err
	}
	replacements = effectRenderReplacements(ctx, rendered.Kind)
	rendered.Role, err = renderEffectTemplateString(rendered.Role, replacements)
	if err != nil {
		return EffectSpec{}, "", err
	}
	rendered.Reason, err = renderEffectTemplateString(rendered.Reason, replacements)
	if err != nil {
		return EffectSpec{}, "", err
	}
	rendered.SemanticKey, err = renderEffectTemplateString(rendered.SemanticKey, replacements)
	if err != nil {
		return EffectSpec{}, "", err
	}
	if rendered.Data != nil {
		data, err := renderEffectTemplateValue(rendered.Data, replacements)
		if err != nil {
			return EffectSpec{}, "", err
		}
		rendered.Data = data.(map[string]interface{})
	}
	semanticKey, err := effectSemanticKey(ctx, rendered)
	if err != nil {
		return EffectSpec{}, "", err
	}
	return rendered, semanticKey, nil
}

func effectSemanticKey(ctx effectRenderContext, ef EffectSpec) (string, error) {
	key := strings.TrimSpace(ef.SemanticKey)
	if key == "" && ef.Data != nil {
		if raw, ok := ef.Data["semanticKey"]; ok {
			if s, ok := raw.(string); ok {
				key = strings.TrimSpace(s)
			}
		}
	}
	if key == "" {
		key = fmt.Sprintf("rev:%d:outcome:%s:seq:%d:kind:%s", ctx.instance.Revision, ctx.outcomeID, ctx.sequence, ef.Kind)
	}
	return renderEffectTemplateString(key, effectRenderReplacements(ctx, ef.Kind))
}

// enqueueRenderedEffectsTx renders each spec against inst and inserts it as
// a pending effect at the next per-instance sequence, keyed by its semantic
// key so a replayed render cannot enqueue twice.
func enqueueRenderedEffectsTx(tx *sql.Tx, inst Instance, specs []EffectSpec, outcomeID, runID, now string) ([]Effect, error) {
	created := make([]Effect, 0, len(specs))
	for _, spec := range specs {
		id, err := nextSeqID(tx, "workflow_effect_seq", "eff")
		if err != nil {
			return nil, err
		}
		seq, err := nextEffectSequenceTx(tx, inst.ID)
		if err != nil {
			return nil, err
		}
		rendered, semanticKey, err := renderEffectSpec(spec, effectRenderContext{instance: inst, outcomeID: outcomeID, runID: runID, sequence: seq})
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(rendered)
		if err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%s:%s", inst.ID, semanticKey)
		if _, err := tx.Exec(`
			INSERT INTO workflow_effects (id, instance_id, revision, sequence, kind, payload_json, status, idempotency_key, semantic_key, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)
		`, id, inst.ID, inst.Revision, seq, rendered.Kind, string(payload), key, semanticKey, now, now); err != nil {
			return nil, err
		}
		created = append(created, Effect{
			ID: id, InstanceID: inst.ID, Revision: inst.Revision, Sequence: seq, Kind: rendered.Kind,
			Payload: json.RawMessage(payload), Status: "pending", IdempotencyKey: key, SemanticKey: semanticKey,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	return created, nil
}

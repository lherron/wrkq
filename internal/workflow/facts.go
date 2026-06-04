package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var supportedFactTypes = map[string]bool{
	"string":  true,
	"boolean": true,
	"number":  true,
	"integer": true,
	"array":   true,
}

var supportedArrayItemTypes = map[string]bool{
	"string":  true,
	"boolean": true,
	"number":  true,
	"integer": true,
}

type parsedEvidenceFacts struct {
	Raw    json.RawMessage
	Fields map[string]json.RawMessage
}

type evidenceMatchResult struct {
	OK      bool
	Latest  *Evidence
	Detail  string
	Missing bool
}

func validateFactsContracts(tpl *Template) []string {
	var errs []string
	for kind, spec := range tpl.EvidenceKinds {
		errs = append(errs, validateFactsContract("evidence kind "+kind, spec.Facts)...)
	}
	for _, tr := range tpl.Transitions {
		for _, req := range tr.Requires {
			if req.Evidence == nil {
				continue
			}
			errs = append(errs, validateEvidenceFactSelector(tpl, fmt.Sprintf("transition %s requires %s", tr.ID, req.Evidence.Kind), req.Evidence.Kind, req.Evidence.Facts)...)
		}
		for _, guard := range tr.Guards {
			errs = append(errs, validatePredicateFactSelectors(tpl, fmt.Sprintf("transition %s guard", tr.ID), guard)...)
		}
		for _, out := range tr.Outcomes {
			errs = append(errs, validatePredicateFactSelectors(tpl, fmt.Sprintf("transition %s outcome %s", tr.ID, out.ID), out.When)...)
		}
	}
	for checkID, check := range tpl.Checks {
		if check.Predicate != nil {
			errs = append(errs, validatePredicateFactSelectors(tpl, fmt.Sprintf("check %s predicate", checkID), *check.Predicate)...)
		}
	}
	return errs
}

func validateFactsContract(prefix string, contract *FactsContract) []string {
	if contract == nil {
		return nil
	}
	var errs []string
	props := contract.Properties
	for i, name := range contract.Required {
		if strings.TrimSpace(name) == "" {
			errs = append(errs, fmt.Sprintf("%s facts.required[%d] is empty", prefix, i))
			continue
		}
		if _, ok := props[name]; !ok {
			errs = append(errs, fmt.Sprintf("%s facts.required[%d] references undeclared property %q", prefix, i, name))
		}
	}
	keys := make([]string, 0, len(props))
	for name := range props {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		prop := props[name]
		propPrefix := fmt.Sprintf("%s facts.properties.%s", prefix, name)
		if !supportedFactTypes[prop.Type] {
			errs = append(errs, fmt.Sprintf("%s has invalid type %q", propPrefix, prop.Type))
		}
		if prop.ItemsType != "" {
			if prop.Type != "array" {
				errs = append(errs, fmt.Sprintf("%s itemsType is only valid for array properties", propPrefix))
			} else if !supportedArrayItemTypes[prop.ItemsType] {
				errs = append(errs, fmt.Sprintf("%s has invalid itemsType %q", propPrefix, prop.ItemsType))
			}
		}
		if prop.MaxLength < 0 {
			errs = append(errs, fmt.Sprintf("%s maxLength must be non-negative", propPrefix))
		}
		if prop.MaxItems < 0 {
			errs = append(errs, fmt.Sprintf("%s maxItems must be non-negative", propPrefix))
		}
		for _, raw := range prop.Enum {
			value, err := decodeJSONValue(raw)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s enum value is invalid JSON", propPrefix))
				continue
			}
			if err := validateFactPropertyValue(prop, value, raw, true); err != nil {
				errs = append(errs, fmt.Sprintf("%s enum value %s does not match type %s", propPrefix, string(raw), prop.Type))
			}
		}
	}
	return errs
}

func validatePredicateFactSelectors(tpl *Template, prefix string, p Predicate) []string {
	var errs []string
	if p.EvidenceExists != nil {
		errs = append(errs, validateEvidenceFactSelector(tpl, prefix+" evidenceExists", p.EvidenceExists.Kind, p.EvidenceExists.Facts)...)
	}
	for i, child := range p.All {
		errs = append(errs, validatePredicateFactSelectors(tpl, fmt.Sprintf("%s all[%d]", prefix, i), child)...)
	}
	for i, child := range p.Any {
		errs = append(errs, validatePredicateFactSelectors(tpl, fmt.Sprintf("%s any[%d]", prefix, i), child)...)
	}
	if p.Not != nil {
		errs = append(errs, validatePredicateFactSelectors(tpl, prefix+" not", *p.Not)...)
	}
	return errs
}

func validateEvidenceFactSelector(tpl *Template, prefix, kind string, facts map[string]json.RawMessage) []string {
	if len(facts) == 0 {
		return nil
	}
	var errs []string
	for key, raw := range facts {
		value, err := decodeJSONValue(raw)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s fact %s is invalid JSON", prefix, key))
			continue
		}
		if err := validateFlatFactValue(value); err != nil {
			errs = append(errs, fmt.Sprintf("%s fact %s %s", prefix, key, err.Error()))
			continue
		}
	}
	spec, ok := tpl.EvidenceKinds[kind]
	if !ok || spec.Facts == nil {
		return errs
	}
	for key, raw := range facts {
		prop, ok := spec.Facts.Properties[key]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s fact %s, but %s does not declare that fact", prefix, key, kind))
			continue
		}
		value, err := decodeJSONValue(raw)
		if err != nil {
			continue
		}
		if err := validateFactPropertyValue(prop, value, raw, false); err != nil {
			errs = append(errs, fmt.Sprintf("%s fact %s %s", prefix, key, err.Error()))
		}
	}
	return errs
}

func parseAndValidateEvidenceFacts(kind string, input string, spec *KindSpec) (*parsedEvidenceFacts, error) {
	trimmed := strings.TrimSpace(input)
	var parsed *parsedEvidenceFacts
	if trimmed != "" {
		fields := map[string]json.RawMessage{}
		dec := json.NewDecoder(strings.NewReader(trimmed))
		dec.UseNumber()
		if err := dec.Decode(&fields); err != nil {
			return nil, fmt.Errorf("evidence %s facts must be a JSON object", kind)
		}
		if dec.More() {
			return nil, fmt.Errorf("evidence %s facts must contain one JSON object", kind)
		}
		if trailingToken(dec) {
			return nil, fmt.Errorf("evidence %s facts must contain one JSON object", kind)
		}
		canonical, err := canonicalJSON([]byte(trimmed))
		if err != nil {
			return nil, fmt.Errorf("evidence %s facts must be valid JSON", kind)
		}
		parsed = &parsedEvidenceFacts{Raw: canonical, Fields: fields}
		for key, raw := range fields {
			value, err := decodeJSONValue(raw)
			if err != nil {
				return nil, fmt.Errorf("evidence %s facts.%s must be valid JSON", kind, key)
			}
			if err := validateFlatFactValue(value); err != nil {
				return nil, fmt.Errorf("evidence %s facts.%s %s", kind, key, err.Error())
			}
		}
	}
	if spec == nil || spec.Facts == nil {
		return parsed, nil
	}
	fields := map[string]json.RawMessage{}
	if parsed != nil {
		fields = parsed.Fields
	}
	for _, name := range spec.Facts.Required {
		if _, ok := fields[name]; !ok {
			return nil, fmt.Errorf("evidence %s missing required fact %s", kind, name)
		}
	}
	for name, raw := range fields {
		prop, ok := spec.Facts.Properties[name]
		if !ok {
			continue
		}
		value, err := decodeJSONValue(raw)
		if err != nil {
			return nil, fmt.Errorf("evidence %s facts.%s must be valid JSON", kind, name)
		}
		if err := validateFactPropertyValue(prop, value, raw, false); err != nil {
			return nil, fmt.Errorf("evidence %s facts.%s %s", kind, name, err.Error())
		}
	}
	return parsed, nil
}

func trailingToken(dec *json.Decoder) bool {
	var extra interface{}
	return dec.Decode(&extra) != io.EOF
}

func validateFlatFactValue(value interface{}) error {
	switch v := value.(type) {
	case map[string]interface{}:
		return fmt.Errorf("must be flat; nested objects are not supported")
	case []interface{}:
		for _, item := range v {
			switch item.(type) {
			case map[string]interface{}, []interface{}:
				return fmt.Errorf("must be flat; arrays may contain scalar values only")
			}
		}
		return nil
	default:
		return nil
	}
}

func validateFactPropertyValue(prop FactProperty, value interface{}, raw json.RawMessage, enumOnly bool) error {
	if !factTypeMatches(prop.Type, value) {
		return fmt.Errorf("must be %s", prop.Type)
	}
	if prop.Type == "string" && prop.MaxLength > 0 {
		s := value.(string)
		if utf8.RuneCountInString(s) > prop.MaxLength {
			return fmt.Errorf("must be at most %d characters", prop.MaxLength)
		}
	}
	if prop.Type == "array" {
		arr := value.([]interface{})
		if prop.MaxItems > 0 && len(arr) > prop.MaxItems {
			return fmt.Errorf("must contain at most %d items", prop.MaxItems)
		}
		if prop.ItemsType != "" {
			for _, item := range arr {
				if !factTypeMatches(prop.ItemsType, item) {
					return fmt.Errorf("items must be %s", prop.ItemsType)
				}
			}
		}
	}
	if !enumOnly && len(prop.Enum) > 0 {
		rawCanon, err := canonicalJSON(raw)
		if err != nil {
			return fmt.Errorf("must be valid JSON")
		}
		for _, allowed := range prop.Enum {
			allowedCanon, err := canonicalJSON(allowed)
			if err == nil && bytes.Equal(rawCanon, allowedCanon) {
				return nil
			}
		}
		return fmt.Errorf("must be one of %s", enumList(prop.Enum))
	}
	return nil
}

func factTypeMatches(typ string, value interface{}) bool {
	switch typ {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		n, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := strconv.ParseFloat(n.String(), 64)
		return err == nil
	case "integer":
		n, ok := value.(json.Number)
		return ok && jsonNumberIsInteger(n)
	case "array":
		_, ok := value.([]interface{})
		return ok
	default:
		return false
	}
}

func jsonNumberIsInteger(n json.Number) bool {
	rat, ok := new(big.Rat).SetString(n.String())
	return ok && rat.IsInt()
}

func enumList(values []json.RawMessage) string {
	parts := make([]string, 0, len(values))
	for _, raw := range values {
		parts = append(parts, string(raw))
	}
	return strings.Join(parts, ", ")
}

func decodeJSONValue(raw json.RawMessage) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value interface{}
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func canonicalJSON(raw []byte) (json.RawMessage, error) {
	value, err := decodeJSONValue(raw)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

func latestEvidenceByKind(ev []Evidence, kind string) (Evidence, bool) {
	for i := len(ev) - 1; i >= 0; i-- {
		if ev[i].Kind == kind {
			return ev[i], true
		}
	}
	return Evidence{}, false
}

func evidenceFactsMatch(e Evidence, required map[string]json.RawMessage) (bool, string) {
	if len(required) == 0 {
		return true, ""
	}
	if len(e.Facts) == 0 {
		return false, fmt.Sprintf("latest evidence %s has no facts", e.ID)
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(e.Facts, &fields); err != nil {
		return false, fmt.Sprintf("latest evidence %s has invalid facts", e.ID)
	}
	keys := make([]string, 0, len(required))
	for key := range required {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		want, err := canonicalJSON(required[key])
		if err != nil {
			return false, fmt.Sprintf("required fact %s is invalid", key)
		}
		gotRaw, ok := fields[key]
		if !ok {
			return false, fmt.Sprintf("latest evidence %s missing fact %s", e.ID, key)
		}
		got, err := canonicalJSON(gotRaw)
		if err != nil {
			return false, fmt.Sprintf("latest evidence %s fact %s is invalid", e.ID, key)
		}
		if !bytes.Equal(got, want) {
			return false, fmt.Sprintf("latest %s=%s from %s", key, string(got), e.ID)
		}
	}
	return true, ""
}

func matchEvidenceRequirement(ev []Evidence, req EvidenceRequirementSpec) evidenceMatchResult {
	latest, ok := latestEvidenceByKind(ev, req.Kind)
	if !ok {
		return evidenceMatchResult{OK: false, Missing: true, Detail: fmt.Sprintf("required evidence %s%s is missing", req.Kind, factsRequirementLabel(req.Facts))}
	}
	if ok, detail := evidenceFactsMatch(latest, req.Facts); !ok {
		return evidenceMatchResult{OK: false, Latest: &latest, Detail: fmt.Sprintf("required evidence %s%s is blocked; %s", req.Kind, factsRequirementLabel(req.Facts), detail)}
	}
	return evidenceMatchResult{OK: true, Latest: &latest}
}

func factsRequirementLabel(facts map[string]json.RawMessage) string {
	if len(facts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(facts))
	for key := range facts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+string(facts[key]))
	}
	return " with facts " + strings.Join(parts, ",")
}

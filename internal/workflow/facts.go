//go:build wrkq_local

package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
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

func validateFactsContracts(tpl *Template) []string {
	var errs []string
	for kind, spec := range tpl.EvidenceKinds {
		errs = append(errs, validateFactsContract("evidence kind "+kind, spec.Facts)...)
		errs = append(errs, validateProducibleBySpec(tpl, kind, spec)...)
		errs = append(errs, validateLinkageRefSpec(tpl, kind, spec)...)
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

// validateProducibleBySpec validates E1 template declarations at install: each
// producer entry must be non-empty and name a declared role or a reserved role
// (system/supervisor).
func validateProducibleBySpec(tpl *Template, kind string, spec KindSpec) []string {
	if len(spec.ProducibleBy) == 0 {
		return nil
	}
	var errs []string
	seen := map[string]bool{}
	for i, role := range spec.ProducibleBy {
		trimmed := strings.TrimSpace(role)
		if trimmed == "" {
			errs = append(errs, fmt.Sprintf("evidence kind %s producibleBy[%d] is empty", kind, i))
			continue
		}
		if seen[trimmed] {
			errs = append(errs, fmt.Sprintf("evidence kind %s producibleBy lists %q more than once", kind, trimmed))
		}
		seen[trimmed] = true
		if _, ok := tpl.Roles[trimmed]; !ok && trimmed != "system" && trimmed != "supervisor" {
			errs = append(errs, fmt.Sprintf("evidence kind %s producibleBy references unknown role %q", kind, trimmed))
		}
	}
	return errs
}

// validateLinkageRefSpec validates E3 template declarations at install: each
// ref must declare a top-level JSON Pointer path and, when set, a declared
// resolvesToKind.
func validateLinkageRefSpec(tpl *Template, kind string, spec KindSpec) []string {
	if len(spec.LinkageRefs) == 0 {
		return nil
	}
	var errs []string
	seen := map[string]bool{}
	for i, ref := range spec.LinkageRefs {
		path := strings.TrimSpace(ref.Path)
		switch {
		case path == "":
			errs = append(errs, fmt.Sprintf("evidence kind %s linkageRefs[%d].path is required", kind, i))
		case !strings.HasPrefix(path, "/"):
			errs = append(errs, fmt.Sprintf("evidence kind %s linkageRefs[%d].path %q must be a JSON Pointer beginning with '/'", kind, i, path))
		case strings.Count(path, "/") != 1:
			errs = append(errs, fmt.Sprintf("evidence kind %s linkageRefs[%d].path %q must be a single top-level pointer (no nested segments)", kind, i, path))
		default:
			if seen[path] {
				errs = append(errs, fmt.Sprintf("evidence kind %s linkageRefs declares path %q more than once", kind, path))
			}
			seen[path] = true
		}
		if ref.ResolvesToKind != "" {
			if _, ok := tpl.EvidenceKinds[ref.ResolvesToKind]; !ok {
				errs = append(errs, fmt.Sprintf("evidence kind %s linkageRefs[%d].resolvesToKind references unknown evidence kind %q", kind, i, ref.ResolvesToKind))
			}
		}
		if ref.Latest && ref.ResolvesToKind == "" {
			errs = append(errs, fmt.Sprintf("evidence kind %s linkageRefs[%d].latest requires resolvesToKind (latest of which kind?)", kind, i))
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
		if prop.MinLength < 0 {
			errs = append(errs, fmt.Sprintf("%s minLength must be non-negative", propPrefix))
		}
		if prop.MinLength > 0 && prop.Type != "string" {
			errs = append(errs, fmt.Sprintf("%s minLength is only valid for string properties", propPrefix))
		}
		if prop.MaxLength < 0 {
			errs = append(errs, fmt.Sprintf("%s maxLength must be non-negative", propPrefix))
		}
		if prop.MaxLength > 0 && prop.MinLength > prop.MaxLength {
			errs = append(errs, fmt.Sprintf("%s minLength must not exceed maxLength", propPrefix))
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
			return nil, validationError("facts", fmt.Sprintf("evidence %s facts must be a JSON object%s", kind, jsonLocationSuffix(err)), "JSON object", nil, "pass --facts as a single JSON object, e.g. --facts '{\"verdict\":\"ready\"}'")
		}
		if dec.More() || trailingToken(dec) {
			return nil, validationError("facts", fmt.Sprintf("evidence %s facts must contain one JSON object", kind), "single JSON object", nil, "remove any trailing tokens after the JSON object")
		}
		canonical, err := canonicalJSON([]byte(trimmed))
		if err != nil {
			return nil, validationError("facts", fmt.Sprintf("evidence %s facts must be valid JSON%s", kind, jsonLocationSuffix(err)), "valid JSON", nil, "fix the JSON syntax in --facts")
		}
		parsed = &parsedEvidenceFacts{Raw: canonical, Fields: fields}
		for key, raw := range fields {
			value, err := decodeJSONValue(raw)
			if err != nil {
				return nil, validationError("facts."+key, fmt.Sprintf("evidence %s facts.%s must be valid JSON", kind, key), "valid JSON", nil, "")
			}
			if err := validateFlatFactValue(value); err != nil {
				return nil, validationError("facts."+key, fmt.Sprintf("evidence %s facts.%s %s", kind, key, err.Error()), "", nil, "")
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
			prop := spec.Facts.Properties[name]
			allowed := enumStrings(prop)
			fix := fmt.Sprintf("add facts.%s", name)
			if len(allowed) > 0 {
				fix = fmt.Sprintf("add facts.%s, one of [%s]", name, strings.Join(allowed, "|"))
			}
			return nil, validationError("facts."+name, fmt.Sprintf("evidence %s missing required fact %s", kind, name), expectedForProp(prop), allowed, fix)
		}
	}
	for name, raw := range fields {
		prop, ok := spec.Facts.Properties[name]
		if !ok {
			continue
		}
		value, err := decodeJSONValue(raw)
		if err != nil {
			return nil, validationError("facts."+name, fmt.Sprintf("evidence %s facts.%s must be valid JSON", kind, name), "valid JSON", nil, "")
		}
		if err := validateFactPropertyValue(prop, value, raw, false); err != nil {
			allowed := enumStrings(prop)
			fix := ""
			if len(allowed) > 0 {
				fix = fmt.Sprintf("set facts.%s to one of [%s]", name, strings.Join(allowed, "|"))
			}
			return nil, validationError("facts."+name, fmt.Sprintf("evidence %s facts.%s %s", kind, name, err.Error()), expectedForProp(prop), allowed, fix)
		}
	}
	return parsed, nil
}

// jsonLocationSuffix returns a " at offset N" hint when err is a JSON syntax
// error, so the agent can locate the malformed token (F4).
func jsonLocationSuffix(err error) string {
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		return fmt.Sprintf(" (at byte offset %d)", syn.Offset)
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Offset > 0 {
		return fmt.Sprintf(" (at byte offset %d)", typeErr.Offset)
	}
	return ""
}

// enumStrings renders a fact property's enum as plain strings for fix hints.
func enumStrings(prop FactProperty) []string {
	out := make([]string, 0, len(prop.Enum))
	for _, raw := range prop.Enum {
		s := strings.TrimSpace(string(raw))
		if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
			s = s[1 : len(s)-1]
		}
		out = append(out, s)
	}
	return out
}

func expectedForProp(prop FactProperty) string {
	if prop.Type == "" {
		return ""
	}
	if len(prop.Enum) > 0 {
		return fmt.Sprintf("%s one of [%s]", prop.Type, strings.Join(enumStrings(prop), "|"))
	}
	return prop.Type
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
	if prop.Type == "string" && (prop.MinLength > 0 || prop.MaxLength > 0) {
		s := value.(string)
		n := utf8.RuneCountInString(s)
		if prop.MinLength > 0 && n < prop.MinLength {
			return fmt.Errorf("must be at least %d characters", prop.MinLength)
		}
		if prop.MaxLength > 0 && n > prop.MaxLength {
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

// validateProducibleBy enforces E1 supplied-role conformance: if the kind
// declares producers, the supplied role must be one of them. An empty/unset
// role is rejected when producers are declared. There is no implicit bypass —
// "system" must be listed explicitly to be allowed.
func validateProducibleBy(kind string, spec *KindSpec, role string) error {
	if spec == nil || len(spec.ProducibleBy) == 0 {
		return nil
	}
	r := strings.TrimSpace(role)
	if r != "" {
		for _, allowed := range spec.ProducibleBy {
			if allowed == r {
				return nil
			}
		}
	}
	return kindRoleDeniedError(kind, role, spec.ProducibleBy)
}

// validateLinkageRefs enforces E3: each declared linkage ref must resolve to a
// live evidence id on the same instance (and of ResolvesToKind when set).
func validateLinkageRefs(existing []Evidence, spec *KindSpec, dataRaw json.RawMessage) error {
	if spec == nil || len(spec.LinkageRefs) == 0 {
		return nil
	}
	dataObj := map[string]json.RawMessage{}
	if len(dataRaw) > 0 {

		_ = json.Unmarshal(dataRaw, &dataObj)
	}
	for _, ref := range spec.LinkageRefs {
		key := strings.TrimPrefix(ref.Path, "/")
		raw, present := dataObj[key]
		if !present {
			if ref.Required {
				return linkageUnresolvedError(ref.Path, "", ref.ResolvesToKind, "")
			}
			continue
		}
		var id string
		if err := json.Unmarshal(raw, &id); err != nil || strings.TrimSpace(id) == "" {
			return linkageUnresolvedError(ref.Path, strings.Trim(string(raw), `"`), ref.ResolvesToKind, "is not a non-empty string evidence id")
		}
		match := findEvidenceByID(existing, id)
		if match == nil {
			return linkageUnresolvedError(ref.Path, id, ref.ResolvesToKind, "")
		}
		if ref.ResolvesToKind != "" && match.Kind != ref.ResolvesToKind {
			return linkageUnresolvedError(ref.Path, id, ref.ResolvesToKind, fmt.Sprintf("resolves to kind %s, expected %s", match.Kind, ref.ResolvesToKind))
		}
		if ref.Latest {

			if latest, ok := latestEvidenceByKind(existing, ref.ResolvesToKind); ok && latest.ID != id {
				return linkageStaleError(ref.Path, id, ref.ResolvesToKind, latest.ID)
			}
		}
	}
	return nil
}

func findEvidenceByID(ev []Evidence, id string) *Evidence {
	for i := range ev {
		if ev[i].ID == id {
			return &ev[i]
		}
	}
	return nil
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

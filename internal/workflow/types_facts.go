package workflow

import "encoding/json"

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

package wrkqapi

import (
	"encoding/json"
	"strings"
)

// flexString accepts either a JSON string or array of strings. It is a DTO
// field shape rather than server machinery, so it stays in the portable
// (untagged) leaf alongside the types that embed it (T-07090).
type flexString []string

func (f *flexString) UnmarshalJSON(data []byte) error {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '[' {
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*f = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s != "" {
		*f = []string{s}
	}
	return nil
}

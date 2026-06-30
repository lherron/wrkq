package rpccli

import "strings"

// splitCausedBy splits a comma-separated --caused-by input into trimmed,
// non-empty tokens. The server owns format validation, existence checks, and
// de-duplication; the mirror only forwards the raw friendly IDs.
func splitCausedBy(input string) []string {
	toks := []string{}
	for _, raw := range strings.Split(input, ",") {
		if t := strings.TrimSpace(raw); t != "" {
			toks = append(toks, t)
		}
	}
	return toks
}

package useragent

import (
	"encoding/json"
	"os"
	"regexp"

	"golang.org/x/term"
)

var callerToken = regexp.MustCompile(`^[A-Za-z0-9._:@/+-]{1,128}$`)

// Caller accepts attribution, never authority. Invalid names and non-string
// envelope values are unknown; they must not prevent a request being served.
func Caller(value any) string {
	if raw, ok := value.(json.RawMessage); ok {
		var decoded any
		if json.Unmarshal(raw, &decoded) != nil {
			return "unknown"
		}
		value = decoded
	}
	name, ok := value.(string)
	if !ok || !callerToken.MatchString(name) {
		return "unknown"
	}
	return name
}

// ResolveCaller snapshots the nearest named component or seat. Wrkq binaries
// have no component name of their own and pass an inherited hint through.
func ResolveCaller(component string) string {
	return CallerForEnv(component, os.Getenv, term.IsTerminal(int(os.Stdin.Fd())))
}

// CallerForEnv also lets subprocess clients use their effective child environment.
func CallerForEnv(component string, getenv func(string) string, terminal bool) string {
	for _, value := range []string{component, getenv("PRAESIDIUM_CALLER"), getenv("ASP_HANDLE")} {
		if value != "" {
			return Caller(value)
		}
	}
	if terminal {
		return "tty"
	}
	return "unknown"
}

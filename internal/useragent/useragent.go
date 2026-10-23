// Package useragent names wrkq's first-party HTTP clients. Services log the
// leading `<name>/<version>` token of every request's User-Agent (service-api
// §3, logging L3), so each outbound request carries it.
package useragent

import (
	"path"
	"runtime/debug"
	"sync"
)

// Version is injected by scripts/ldflags.sh for shipped wrkq binaries.
var Version = ""

// String is the User-Agent for this process: `wrkq/<version>` inside wrkq's
// own binaries, otherwise `<main module>/<version>` so an embedding program
// (hcs links pkg/client) names itself rather than posing as wrkq.
var String = sync.OnceValue(func() string {
	if Version != "" {
		return "wrkq/" + Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Path == "" {
		return "wrkq/dev"
	}
	name := path.Base(info.Main.Path)
	if info.Main.Path == "github.com/lherron/wrkq" {
		name = "wrkq"
	}
	version := info.Main.Version
	if version == "" || version == "(devel)" {
		version = "dev"
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				version = s.Value[:7]
			}
		}
	}
	return name + "/" + version
})

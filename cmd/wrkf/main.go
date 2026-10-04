package main

import (
	"os"

	"github.com/lherron/wrkq/internal/clifunnel"
	"github.com/lherron/wrkq/internal/wrkfcli"
)

func main() {
	if err := wrkfcli.Execute(); err != nil {
		if !wrkfcli.IsReported(err) {
			clifunnel.Report(os.Stderr, err, clifunnel.ErrorModeFor(os.Args[1:]), "WRKF")
		}
		os.Exit(wrkfcli.ExitCodeForError(err))
	}
}

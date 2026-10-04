package main

import (
	"os"

	"github.com/lherron/wrkq/internal/clifunnel"
	"github.com/lherron/wrkq/internal/rpccli"
)

func main() {
	if err := rpccli.ExecuteWrkc(); err != nil {
		if !rpccli.ErrorAlreadyReported(err) {
			clifunnel.Report(os.Stderr, err, clifunnel.ErrorModeFor(os.Args[1:]), "WRKQ")
		}
		os.Exit(rpccli.ExitCodeForError(err))
	}
}

package main

import (
	"os"

	"github.com/lherron/wrkq/internal/admincli"
	"github.com/lherron/wrkq/internal/clifunnel"
)

func main() {
	if err := admincli.ExecuteAdmin(); err != nil {
		if !admincli.ErrorAlreadyReported(err) {
			clifunnel.Report(os.Stderr, err, clifunnel.ErrorModeFor(os.Args[1:]), "WRKQ")
		}
		os.Exit(admincli.ExitCodeForError(err))
	}
}

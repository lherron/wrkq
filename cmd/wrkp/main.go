package main

import (
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/rpccli"
)

func main() {
	if err := rpccli.ExecuteWrkp(); err != nil {
		if !rpccli.ErrorAlreadyReported(err) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(rpccli.ExitCodeForError(err))
	}
}

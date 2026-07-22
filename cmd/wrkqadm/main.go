package main

import (
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/admincli"
)

func main() {
	if err := admincli.ExecuteAdmin(); err != nil {
		if !admincli.ErrorAlreadyReported(err) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(admincli.ExitCodeForError(err))
	}
}

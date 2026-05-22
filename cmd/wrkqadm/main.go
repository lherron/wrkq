package main

import (
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/cli"
)

func main() {
	if err := cli.ExecuteAdmin(); err != nil {
		if !cli.ErrorAlreadyReported(err) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(cli.ExitCodeForError(err))
	}
}

package main

import (
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/wrkfcli"
)

func main() {
	if err := wrkfcli.Execute(); err != nil {
		if !wrkfcli.IsReported(err) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

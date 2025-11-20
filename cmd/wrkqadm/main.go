package main

import (
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/cli"
)

func main() {
	if err := cli.ExecuteAdmin(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

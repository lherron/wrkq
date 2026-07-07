package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/rpccli"
)

func main() {
	out := flag.String("out", "internal/rpccli/cli_surface_manifest.json", "path to write the CLI surface manifest")
	commandName := flag.String("command", "wrkq", "root command name")
	flag.Parse()

	body, err := rpccli.BuildCLISurfaceManifestJSON(*commandName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate CLI surface manifest: %v\n", err)
		os.Exit(1)
	}
	body = append(body, '\n')
	if err := os.WriteFile(*out, body, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *out, err)
		os.Exit(1)
	}
}

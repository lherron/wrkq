package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/cli"
)

func main() {
	addr := flag.String("addr", os.Getenv("WRKQD_ADDR"), "Listen address (default 127.0.0.1:7171)")
	unixPath := flag.String("unix", os.Getenv("WRKQD_UNIX"), "Listen on unix socket path")
	token := flag.String("token", os.Getenv("WRKQD_TOKEN"), "Shared token for local auth")
	dbPath := flag.String("db", "", "Database path override (defaults to config)")
	flag.Parse()

	opts := cli.DaemonOptions{
		Addr:   *addr,
		Unix:   *unixPath,
		Token:  *token,
		DBPath: *dbPath,
	}

	if err := cli.ServeDaemon(opts); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

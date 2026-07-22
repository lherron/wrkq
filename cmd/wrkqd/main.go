package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/wrkqd"
)

func main() {
	addr := flag.String("addr", os.Getenv("WRKQD_ADDR"), "Listen address (default 127.0.0.1:7171)")
	unixPath := flag.String("unix", os.Getenv("WRKQD_UNIX"), "Listen on unix socket path")
	token := flag.String("token", os.Getenv("WRKQD_TOKEN"), "Shared token for local auth")
	dbPath := flag.String("db", "", "Database path override (defaults to config)")
	unsafeNoToken := flag.Bool("unsafe-no-token", false, "Allow non-loopback listen without a token (dev only)")
	nodeTokens := flag.String("node-tokens", os.Getenv("WRKQD_NODE_TOKENS"), "Per-node bearer tokens (nodeId=token,nodeId=token); supersedes --token")
	nodeTokensFile := flag.String("node-tokens-file", os.Getenv("WRKQD_NODE_TOKENS_FILE"), "File of per-node bearer tokens, one nodeId=token per line")
	flag.Parse()

	opts := wrkqd.DaemonOptions{
		Addr:           *addr,
		Unix:           *unixPath,
		Token:          *token,
		DBPath:         *dbPath,
		UnsafeNoToken:  *unsafeNoToken,
		NodeTokens:     *nodeTokens,
		NodeTokensFile: *nodeTokensFile,
	}

	if err := wrkqd.ServeDaemon(opts); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

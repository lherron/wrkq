package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/wrkqd"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		usageOut := io.Writer(os.Stderr)
		if helpRequested(os.Args[2:]) {
			usageOut = os.Stdout
		}
		if err := printVersion(os.Args[2:], os.Stdout, usageOut); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(2)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		if err := printVersion(nil, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(2)
		}
		return
	}

	addr := flag.String("addr", os.Getenv("WRKQD_ADDR"), "Listen address (default 127.0.0.1:7171)")
	unixPath := flag.String("unix", os.Getenv("WRKQD_UNIX"), "Listen on unix socket path")
	token := flag.String("token", os.Getenv("WRKQD_TOKEN"), "Shared token for local auth")
	dbPath := flag.String("db", "", "Database path override (defaults to config)")
	unsafeNoToken := flag.Bool("unsafe-no-token", false, "Allow non-loopback listen without a token (dev only)")
	nodeTokens := flag.String("node-tokens", os.Getenv("WRKQD_NODE_TOKENS"), "Per-node bearer tokens (nodeId=token,nodeId=token); supersedes --token")
	nodeTokensFile := flag.String("node-tokens-file", os.Getenv("WRKQD_NODE_TOKENS_FILE"), "File of per-node bearer tokens, one nodeId=token per line")
	// CLI standard §1: -h/--help anywhere is inert. The flag package stops at
	// the first positional and would take "--help" as a value of a string flag,
	// so `wrkqd foo --help` or `wrkqd -db --help` would start a daemon. Scan
	// first; help goes to stdout and exits 0.
	if helpRequested(os.Args[1:]) {
		flag.CommandLine.SetOutput(os.Stdout)
		flag.Usage()
		return
	}
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

func printVersion(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("wrkqd version", flag.ContinueOnError)
	flags.SetOutput(errOut)
	asJSON := flags.Bool("json", false, "Output as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("wrkqd version accepts no positional arguments")
	}

	if *asJSON {
		payload := struct {
			Version            string `json:"version"`
			BuildRevision      string `json:"build_revision"`
			BuildDate          string `json:"build_date"`
			ProtocolVersion    string `json:"protocol_version"`
			ProtocolSchemaHash string `json:"protocol_schema_hash"`
		}{
			Version:            wrkqd.Version,
			BuildRevision:      wrkqd.GitCommit,
			BuildDate:          wrkqd.BuildDate,
			ProtocolVersion:    workrpc.ProtocolVersion,
			ProtocolSchemaHash: workrpc.ProtocolSchemaHash(),
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(payload)
	}

	fmt.Fprintf(out, "wrkqd version %s\n", wrkqd.Version)
	fmt.Fprintf(out, "  revision:        %s\n", wrkqd.GitCommit)
	fmt.Fprintf(out, "  built:           %s\n", wrkqd.BuildDate)
	fmt.Fprintf(out, "  protocol:        %s\n", workrpc.ProtocolVersion)
	fmt.Fprintf(out, "  protocol schema: %s\n", workrpc.ProtocolSchemaHash())
	return nil
}

// helpRequested reports whether a help flag appears anywhere before a "--"
// terminator, in any spelling the flag package accepts.
func helpRequested(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--":
			return false
		case "-h", "-help", "--help", "--h":
			return true
		}
	}
	return false
}

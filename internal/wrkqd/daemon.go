package wrkqd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/nodeauth"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// DaemonOptions configures the wrkqd daemon.
type DaemonOptions struct {
	Addr          string
	Unix          string
	Token         string
	DBPath        string
	PIDPath       string
	UnsafeNoToken bool
	// NodeTokens maps bearer tokens to logical nodeIds inline
	// (`nodeId=token,nodeId=token`); NodeTokensFile reads the same grammar
	// from disk. Either one enables per-node identity, which supersedes the
	// shared Token.
	NodeTokens     string
	NodeTokensFile string
}

// ServeDaemon starts the wrkqd daemon.
func ServeDaemon(opts DaemonOptions) error {
	cfg, err := config.LoadWithDBOverride(opts.DBPath, true)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	nodes, err := loadNodeRegistry(opts)
	if err != nil {
		return err
	}

	if cfg.RemoteEndpoint != "" {
		return fmt.Errorf("wrkqd requires a local database path; WRKQ_DB_PATH and --db must not be rpc:// locators")
	}
	if cfg.DBPath == "" {
		return config.MissingDatabasePathError()
	}

	release, err := db.AcquireServingLease(cfg.DBPath)
	if err != nil {
		return err
	}
	defer release()

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if err := database.RequiresMigrationError(); err != nil {
		_ = database.Close()
		return err
	}
	defer func() { _ = database.Close() }()

	if opts.PIDPath != "" {
		if err := writePIDFile(opts.PIDPath); err != nil {
			return err
		}
		defer func() { _ = os.Remove(opts.PIDPath) }()
	}
	api, rpcOpts, err := bootstrap.DaemonServer(database, cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize workrpc registry: %w", err)
	}
	rpcOpts.ServerVersion = Version
	rpcOpts.ServerRevision = GitCommit
	// Handlers run concurrently and without per-request stdout isolation
	// (T-09997); a stray handler print goes to stderr, as it always did.
	os.Stdout = os.Stderr
	rpcServer := workrpc.NewServer(io.Discard)
	workrpc.RegisterAPI(rpcServer, api, rpcOpts)

	server := &daemonServer{
		db:       database,
		cfg:      cfg,
		token:    opts.Token,
		nodes:    nodes,
		workrpc:  rpcServer,
		rpcToken: opts.Token,
	}
	server.startSearchIndexer()

	mux := http.NewServeMux()
	server.registerRoutes(mux)

	httpServer := &http.Server{
		Handler:      mux,
		ReadTimeout:  workrpc.HTTPResponseTimeout,
		WriteTimeout: workrpc.HTTPResponseTimeout,
	}

	if opts.Unix != "" {
		_ = os.Remove(opts.Unix)
		listener, err := net.Listen("unix", opts.Unix)
		if err != nil {
			return fmt.Errorf("failed to listen on unix socket: %w", err)
		}
		defer func() { _ = listener.Close() }()
		return serveHTTPWithSignals(httpServer, listener)
	}

	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:7171"
	}
	if opts.Unix == "" && opts.Token == "" && !nodes.Enabled() && !opts.UnsafeNoToken && !isLoopbackListenAddr(addr) {
		return fmt.Errorf("refusing to bind wrkqd on non-loopback address %s without --token; pass --unsafe-no-token for explicit dev-only override", addr)
	}
	httpServer.Addr = addr

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	return serveHTTPWithSignals(httpServer, listener)
}

func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writePIDFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create pid directory: %w", err)
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

func serveHTTPWithSignals(server *http.Server, listener net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	sigCh := make(chan os.Signal, 1)
	signalNotify(sigCh)
	defer signalStop(sigCh)

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
			return err
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

var (
	signalNotify = func(ch chan<- os.Signal) {
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	}
	signalStop = signal.Stop
)

type daemonServer struct {
	db       *db.DB
	cfg      *config.Config
	token    string
	nodes    *nodeauth.Registry
	workrpc  *workrpc.Server
	rpcToken string
}

// loadNodeRegistry builds the per-node token registry from whichever source
// the operator configured. Configuring both an inline spec and a file is a
// config error rather than a silent precedence rule.
func loadNodeRegistry(opts DaemonOptions) (*nodeauth.Registry, error) {
	spec := strings.TrimSpace(opts.NodeTokens)
	file := strings.TrimSpace(opts.NodeTokensFile)
	switch {
	case spec != "" && file != "":
		return nil, fmt.Errorf("configure node tokens inline or by file, not both")
	case file != "":
		return nodeauth.LoadFile(file)
	case spec != "":
		return nodeauth.ParseSpec(spec)
	default:
		return nil, nil
	}
}

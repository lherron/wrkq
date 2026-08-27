package rpccli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

const defaultWrkqLaunchdLabel = "com.praesidium.wrkq-server"

type serverRuntimeStatus struct {
	Running            bool   `json:"running"`
	PID                *int   `json:"pid"`
	PIDAlive           bool   `json:"pidAlive"`
	PIDPath            string `json:"pidPath"`
	Endpoint           string `json:"endpoint,omitempty"`
	EndpointResponsive bool   `json:"endpointResponsive,omitempty"`
	SocketPath         string `json:"socketPath,omitempty"`
	SocketResponsive   bool   `json:"socketResponsive,omitempty"`
	LaunchdLabel       string `json:"launchdLabel,omitempty"`
	LaunchdLoaded      bool   `json:"launchdLoaded"`
	BinaryPath         string `json:"binaryPath,omitempty"`
	BinaryCDHash       string `json:"binaryCDHash,omitempty"`
	RunningCDHash      string `json:"runningCDHash,omitempty"`
	BinaryStale        bool   `json:"binaryStale"`
}

type serverOptions struct {
	addr          string
	unixPath      string
	token         string
	dbPath        string
	json          bool
	foreground    bool
	daemon        bool
	unsafeNoToken bool
	timeoutMS     int
	force         bool
}

func newServerCmd() *cobra.Command {
	opts := &serverOptions{
		addr:     os.Getenv("WRKQD_ADDR"),
		unixPath: os.Getenv("WRKQD_UNIX"),
		token:    os.Getenv("WRKQD_TOKEN"),
	}
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Manage the wrkq daemon server",
	}
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the wrkq daemon server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServerStart(cmd, opts)
		},
	}
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the wrkq daemon server in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServerServe(cmd, opts)
		},
	}
	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the wrkq daemon server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServerStop(cmd, opts)
		},
	}
	restartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the wrkq daemon server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServerRestart(cmd, opts)
		},
	}
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show wrkq daemon server status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServerStatus(cmd, opts)
		},
	}
	healthCmd := &cobra.Command{
		Use:   "health",
		Short: "Check wrkq daemon server health",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServerHealth(cmd, opts)
		},
	}
	cmd.AddCommand(startCmd, serveCmd, stopCmd, restartCmd, statusCmd, healthCmd)

	cmd.PersistentFlags().StringVar(&opts.addr, "addr", opts.addr, "Listen/status address")
	cmd.PersistentFlags().StringVar(&opts.unixPath, "unix", opts.unixPath, "Listen/status Unix socket path")
	cmd.PersistentFlags().StringVar(&opts.token, "token", opts.token, "Shared token for local auth")
	cmd.PersistentFlags().StringVar(&opts.dbPath, "db-path", "", "Database path override")
	cmd.PersistentFlags().BoolVar(&opts.unsafeNoToken, "unsafe-no-token", false, "Allow non-loopback listen without a token (dev only)")

	startCmd.Flags().BoolVar(&opts.foreground, "foreground", false, "Run in the foreground when launchd is not loaded")
	startCmd.Flags().BoolVar(&opts.daemon, "daemon", false, "Run as a background process when launchd is not loaded")
	startCmd.Flags().IntVar(&opts.timeoutMS, "timeout-ms", 30000, "Startup timeout in milliseconds")

	stopCmd.Flags().IntVar(&opts.timeoutMS, "timeout-ms", 5000, "Shutdown timeout in milliseconds")
	stopCmd.Flags().BoolVar(&opts.force, "force", false, "Escalate to SIGKILL if graceful shutdown times out")

	restartCmd.Flags().BoolVar(&opts.foreground, "foreground", false, "Run in the foreground when launchd is not loaded")
	restartCmd.Flags().BoolVar(&opts.daemon, "daemon", false, "Run as a background process when launchd is not loaded")
	restartCmd.Flags().IntVar(&opts.timeoutMS, "timeout-ms", 30000, "Shutdown/startup timeout in milliseconds")
	restartCmd.Flags().BoolVar(&opts.force, "force", false, "Escalate to SIGKILL if graceful shutdown times out")

	statusCmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func runServerStart(cmd *cobra.Command, opts *serverOptions) error {
	applyServerDBFlag(cmd, opts)
	mode, err := resolveServerMode(opts, "daemon")
	if err != nil {
		return err
	}
	status := collectWrkqServerStatus(opts)
	if status.Running {
		return fmt.Errorf("daemon already running at %s (pid %s)", statusTarget(status), formatOptionalPID(status.PID))
	}
	if owner := detectWrkqLaunchdOwner(); owner != nil {
		probe := launchdProbeOptions(opts, owner)
		if err := launchctlKickstart(owner, false); err != nil {
			return err
		}
		if err := waitForServerAnswer(probe, time.Duration(opts.timeoutMS)*time.Millisecond); err != nil {
			return launchdLifecycleError("start", detectWrkqLaunchdOwner(), err)
		}
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return writeServerLifecycleJSON(cmd, "started", "launchd", collectWrkqServerStatus(probe))
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "wrkq: daemon started via launchd (%s)\n", owner.serviceTarget)
		return nil
	}
	if mode == "foreground" {
		return serveWrkqServer(opts)
	}
	if err := daemonizeWrkqServer(opts, time.Duration(opts.timeoutMS)*time.Millisecond); err != nil {
		return err
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return writeServerLifecycleJSON(cmd, "started", mode, collectWrkqServerStatus(opts))
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "wrkq: daemon started")
	return nil
}

func runServerServe(cmd *cobra.Command, opts *serverOptions) error {
	applyServerDBFlag(cmd, opts)
	status := collectWrkqServerStatus(opts)
	if status.Running {
		return fmt.Errorf("daemon already running at %s (pid %s)", statusTarget(status), formatOptionalPID(status.PID))
	}
	return serveWrkqServer(opts)
}

func runServerStop(cmd *cobra.Command, opts *serverOptions) error {
	status := collectWrkqServerStatus(opts)
	if !status.Running && status.PID == nil {
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return writeServerLifecycleJSON(cmd, "not_running", "", status)
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "wrkq: daemon is not running")
		return nil
	}
	if owner := detectWrkqLaunchdOwner(); owner != nil {
		return fmt.Errorf("daemon is supervised by launchd (%s); launchd will respawn it. To stop permanently: launchctl unload -w ~/Library/LaunchAgents/%s.plist", owner.serviceTarget, owner.label)
	}
	if err := stopWrkqServerProcess(opts, time.Duration(opts.timeoutMS)*time.Millisecond, opts.force); err != nil {
		return err
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return writeServerLifecycleJSON(cmd, "stopped", "", collectWrkqServerStatus(opts))
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "wrkq: daemon stopped")
	return nil
}

func runServerRestart(cmd *cobra.Command, opts *serverOptions) error {
	applyServerDBFlag(cmd, opts)
	mode, err := resolveServerMode(opts, "daemon")
	if err != nil {
		return err
	}
	if owner := detectWrkqLaunchdOwner(); owner != nil {
		probe := launchdProbeOptions(opts, owner)
		if err := restartLaunchdDaemon(cmd, owner); err != nil {
			return err
		}
		// A live pid is not evidence of a working daemon: launchd SIGKILLs a
		// respawn that fails the pinned code requirement, and the process dies
		// before it can log. Report success only once it answers.
		if err := waitForServerAnswer(probe, time.Duration(opts.timeoutMS)*time.Millisecond); err != nil {
			return launchdLifecycleError("restart", detectWrkqLaunchdOwner(), err)
		}
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return writeServerLifecycleJSON(cmd, "restarted", "launchd", collectWrkqServerStatus(probe))
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "wrkq: daemon restarted via launchd (%s)\n", owner.serviceTarget)
		return nil
	}
	if err := stopWrkqServerProcess(opts, time.Duration(opts.timeoutMS)*time.Millisecond, opts.force); err != nil && !errors.Is(err, errWrkqServerNotRunning) {
		return err
	}
	if mode == "foreground" {
		return serveWrkqServer(opts)
	}
	if err := daemonizeWrkqServer(opts, time.Duration(opts.timeoutMS)*time.Millisecond); err != nil {
		return err
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return writeServerLifecycleJSON(cmd, "restarted", mode, collectWrkqServerStatus(opts))
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "wrkq: daemon restarted")
	return nil
}

func runServerStatus(cmd *cobra.Command, opts *serverOptions) error {
	status := collectWrkqServerStatus(opts)
	annotateBinaryIdentity(&status, detectWrkqLaunchdOwner())
	if opts.json || !isStdoutTTY(cmd.OutOrStdout()) {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(status)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "WRKQ Server Status\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  running:     %s\n", yesNo(status.Running))
	fmt.Fprintf(cmd.OutOrStdout(), "  pid:         %s\n", formatOptionalPID(status.PID))
	fmt.Fprintf(cmd.OutOrStdout(), "  pid alive:   %s\n", yesNo(status.PIDAlive))
	fmt.Fprintf(cmd.OutOrStdout(), "  pid file:    %s\n", status.PIDPath)
	if status.Endpoint != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  endpoint:    %s%s\n", status.Endpoint, responsiveSuffix(status.EndpointResponsive))
	}
	if status.SocketPath != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  socket:      %s%s\n", status.SocketPath, responsiveSuffix(status.SocketResponsive))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  launchd:     %s\n", loadedNotLoaded(status.LaunchdLoaded))
	if status.BinaryPath != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  binary:      %s\n", status.BinaryPath)
	}
	if status.BinaryStale {
		fmt.Fprintf(cmd.OutOrStdout(), "  ⚠️  installed binary (%s) is not the image the daemon is running (%s)\n", shortCDHash(status.BinaryCDHash), shortCDHash(status.RunningCDHash))
		fmt.Fprintf(cmd.OutOrStdout(), "      launchd will SIGKILL the next respawn; run 'wrkq server restart'\n")
	}
	return nil
}

func runServerHealth(cmd *cobra.Command, opts *serverOptions) error {
	owner := detectWrkqLaunchdOwner()
	authorized, err := checkWrkqServerHealth(launchdProbeOptions(opts, owner), 2*time.Second)
	if err != nil {
		return err
	}
	if err := checkInstalledBinaryLoaded(owner); err != nil {
		return err
	}
	payload := map[string]string{"status": "ok"}
	if !authorized {
		payload["auth"] = "unauthorized"
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(payload)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "ok")
	if !authorized {
		fmt.Fprintln(cmd.ErrOrStderr(), "wrkq: the daemon answered but rejected this caller's token; health checked liveness only")
	}
	return nil
}

func writeServerLifecycleJSON(cmd *cobra.Command, action, mode string, status serverRuntimeStatus) error {
	payload := map[string]any{
		"action": action,
		"status": status,
	}
	if mode != "" {
		payload["mode"] = mode
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

func serveWrkqServer(opts *serverOptions) error {
	if err := writePIDFile(wrkqServerPIDPath()); err != nil {
		return err
	}
	bin, err := resolveWrkqdBinary()
	if err != nil {
		_ = os.Remove(wrkqServerPIDPath())
		return err
	}
	args := []string{bin}
	if opts.addr != "" {
		args = append(args, "--addr", opts.addr)
	}
	if opts.unixPath != "" {
		args = append(args, "--unix", opts.unixPath)
	}
	if opts.token != "" {
		args = append(args, "--token", opts.token)
	}
	if opts.unsafeNoToken {
		args = append(args, "--unsafe-no-token")
	}
	if opts.dbPath != "" {
		args = append(args, "--db", opts.dbPath)
	}
	return syscall.Exec(bin, args, os.Environ())
}

func resolveWrkqdBinary() (string, error) {
	if override := os.Getenv("WRKQD_BIN"); override != "" {
		return override, nil
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "wrkqd")
		if st, statErr := os.Stat(sibling); statErr == nil && !st.IsDir() {
			return sibling, nil
		}
	}
	if path, err := exec.LookPath("wrkqd"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("wrkqd not found on PATH")
}

func applyServerDBFlag(cmd *cobra.Command, opts *serverOptions) {
	if opts.dbPath != "" {
		return
	}
	if dbFlag := cmd.Flag("db"); dbFlag != nil {
		opts.dbPath = dbFlag.Value.String()
	}
}

func collectWrkqServerStatus(opts *serverOptions) serverRuntimeStatus {
	pidPath := wrkqServerPIDPath()
	pid := readWrkqPIDFile(pidPath)
	pidAlive := pid != nil && processAlive(*pid)
	owner := detectWrkqLaunchdOwner()
	if owner != nil {
		opts = launchdProbeOptions(opts, owner)
	}
	if !pidAlive && owner != nil && owner.pid != nil {
		pid = owner.pid
		pidAlive = processAlive(*pid)
	}
	if !pidAlive && opts.unixPath == "" {
		if listenerPID := discoverTCPListenerPID(resolvedServerAddr(opts)); listenerPID != nil {
			pid = listenerPID
			pidAlive = processAlive(*pid)
		}
	}

	status := serverRuntimeStatus{
		PID:           pid,
		PIDAlive:      pidAlive,
		PIDPath:       pidPath,
		LaunchdLabel:  wrkqLaunchdLabel(),
		LaunchdLoaded: owner != nil,
	}
	if opts.unixPath != "" {
		status.SocketPath = opts.unixPath
		status.SocketResponsive = isUnixSocketResponsive(opts.unixPath, 200*time.Millisecond)
		status.Running = status.SocketResponsive && (pid == nil || pidAlive)
		return status
	}
	status.Endpoint = "http://" + resolvedServerAddr(opts)
	status.EndpointResponsive = isTCPResponsive(resolvedServerAddr(opts), 200*time.Millisecond)
	status.Running = status.EndpointResponsive && (pid == nil || pidAlive)
	return status
}

func resolveServerMode(opts *serverOptions, defaultMode string) (string, error) {
	if opts.foreground && opts.daemon {
		return "", fmt.Errorf("--foreground and --daemon are mutually exclusive")
	}
	if opts.foreground {
		return "foreground", nil
	}
	if opts.daemon {
		return "daemon", nil
	}
	return defaultMode, nil
}

func resolvedServerAddr(opts *serverOptions) string {
	if opts.addr != "" {
		return opts.addr
	}
	return "127.0.0.1:7171"
}

func wrkqRuntimeDir() string {
	if dir := os.Getenv("WRKQ_RUNTIME_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err == nil {
		praesidium := filepath.Join(home, "praesidium")
		if _, statErr := os.Stat(praesidium); statErr == nil {
			return filepath.Join(praesidium, "var", "run", "wrkq")
		}
		return filepath.Join(home, ".local", "state", "wrkq", "run")
	}
	return filepath.Join(os.TempDir(), "wrkq")
}

func wrkqServerPIDPath() string {
	return filepath.Join(wrkqRuntimeDir(), "wrkq-server.pid")
}

func wrkqServerLogPath() string {
	if path := os.Getenv("WRKQ_LOG_PATH"); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err == nil {
		praesidium := filepath.Join(home, "praesidium")
		if _, statErr := os.Stat(praesidium); statErr == nil {
			return filepath.Join(praesidium, "var", "logs", "wrkq-server.log")
		}
	}
	return filepath.Join(wrkqRuntimeDir(), "wrkq-server.log")
}

func wrkqLaunchdLabel() string {
	if label := os.Getenv("WRKQ_LAUNCHD_LABEL"); label != "" {
		return label
	}
	return defaultWrkqLaunchdLabel
}

// launchdProbeOptions resolves the endpoint the launchd job actually binds.
// The canonical daemon binds a node address passed as --addr in its plist, so
// probing the 127.0.0.1 default reports a healthy daemon as down and removes
// the one signal that would catch a dead one.
func launchdProbeOptions(opts *serverOptions, owner *launchdOwner) *serverOptions {
	if owner == nil || opts.addr != "" || opts.unixPath != "" {
		return opts
	}
	addr, unixPath := owner.endpoint()
	if addr == "" && unixPath == "" {
		return opts
	}
	probe := *opts
	probe.addr = addr
	probe.unixPath = unixPath
	return &probe
}

// restartLaunchdDaemon reloads the job rather than kickstarting it, so launchd
// re-derives the code requirement from the binary that is on disk now. A
// rebuilt wrkqd has a fresh adhoc cdhash and is SIGKILLed on every respawn
// inside the existing job.
func restartLaunchdDaemon(cmd *cobra.Command, owner *launchdOwner) error {
	if owner.plistPath != "" {
		return relaunchLaunchdJob(owner)
	}
	if identity := inspectLaunchdBinary(owner); identity.stale() {
		return fmt.Errorf("launchd job %s reports no plist path, so it cannot be bootstrapped again, and %s (%s) is not the image the running daemon loaded (%s); kickstarting it would respawn into a SIGKILL. Reinstall the plist (just install-launchd), then retry", owner.serviceTarget, identity.path, shortCDHash(identity.onDisk), shortCDHash(identity.running))
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "wrkq: launchd job %s reports no plist path; falling back to kickstart -k\n", owner.serviceTarget)
	return launchctlKickstart(owner, true)
}

// waitForServerAnswer blocks until the daemon answers over its endpoint.
func waitForServerAnswer(opts *serverOptions, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if serverAnswers(opts) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for the daemon to answer at %s", timeout, statusTarget(collectWrkqServerStatus(opts)))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// serverAnswers reports whether the daemon replied to an HTTP request at all.
// A 401 still proves the process is alive and serving, which is what a
// lifecycle command needs to verify; token correctness is `server health` work.
func serverAnswers(opts *serverOptions) bool {
	client, req, err := newServerHealthRequest(opts, 2*time.Second)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

func launchdLifecycleError(action string, owner *launchdOwner, cause error) error {
	return fmt.Errorf("daemon did not answer after %s: %w%s", action, cause, launchdFailureDetail(owner))
}

// annotateBinaryIdentity records whether the installed daemon binary is the
// image the running daemon actually loaded.
func annotateBinaryIdentity(status *serverRuntimeStatus, owner *launchdOwner) {
	if owner == nil {
		return
	}
	identity := inspectLaunchdBinary(owner)
	status.BinaryPath = identity.path
	status.BinaryCDHash = identity.onDisk
	status.RunningCDHash = identity.running
	status.BinaryStale = identity.stale()
}

// checkInstalledBinaryLoaded fails a daemon that is answering now but has been
// armed to die on its next respawn by a rebuild underneath it.
func checkInstalledBinaryLoaded(owner *launchdOwner) error {
	if owner == nil {
		return nil
	}
	identity := inspectLaunchdBinary(owner)
	if !identity.stale() {
		return nil
	}
	return fmt.Errorf("daemon is answering, but the installed %s (%s) is not the image it is running (%s): launchd will SIGKILL the next respawn for failing the pinned code requirement. Run 'wrkq server restart'", identity.path, shortCDHash(identity.onDisk), shortCDHash(identity.running))
}

func shortCDHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	if hash == "" {
		return "unknown"
	}
	return hash
}

func daemonizeWrkqServer(opts *serverOptions, timeout time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve executable: %w", err)
	}
	args := []string{"server", "serve"}
	if opts.addr != "" {
		args = append(args, "--addr", opts.addr)
	}
	if opts.unixPath != "" {
		args = append(args, "--unix", opts.unixPath)
	}
	if opts.token != "" {
		args = append(args, "--token", opts.token)
	}
	if opts.unsafeNoToken {
		args = append(args, "--unsafe-no-token")
	}
	if opts.dbPath != "" {
		args = append(args, "--db-path", opts.dbPath)
	}

	logPath := wrkqServerLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.Command(exe, args...)
	cmd.Env = os.Environ()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		return err
	}
	return waitForWrkqServer(opts, timeout)
}

func waitForWrkqServer(opts *serverOptions, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status := collectWrkqServerStatus(opts)
		if status.Running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for wrkq server at %s", statusTarget(collectWrkqServerStatus(opts)))
}

var errWrkqServerNotRunning = errors.New("wrkq server is not running")

func stopWrkqServerProcess(opts *serverOptions, timeout time.Duration, force bool) error {
	status := collectWrkqServerStatus(opts)
	if status.PID == nil {
		if status.EndpointResponsive || status.SocketResponsive {
			return fmt.Errorf("daemon is responsive at %s, but no pid file exists at %s", statusTarget(status), status.PIDPath)
		}
		return errWrkqServerNotRunning
	}
	pid := *status.PID
	if !processAlive(pid) {
		_ = os.Remove(status.PIDPath)
		return errWrkqServerNotRunning
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) && !collectWrkqServerStatus(opts).EndpointResponsive && !collectWrkqServerStatus(opts).SocketResponsive {
			_ = os.Remove(status.PIDPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !force {
		return fmt.Errorf("timed out waiting for daemon pid %d to stop; retry with --force to send SIGKILL", pid)
	}
	if err := process.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	_ = os.Remove(status.PIDPath)
	return nil
}

func writePIDFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create pid directory: %w", err)
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

func readWrkqPIDFile(path string) *int {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return nil
	}
	return &pid
}

func discoverTCPListenerPID(addr string) *int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return nil
	}
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err == nil && pid > 0 {
			return &pid
		}
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func isTCPResponsive(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func isUnixSocketResponsive(path string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func newServerHealthRequest(opts *serverOptions, timeout time.Duration) (*http.Client, *http.Request, error) {
	client := &http.Client{Timeout: timeout}
	url := "http://" + resolvedServerAddr(opts) + "/v1/health"
	if opts.unixPath != "" {
		dialer := &net.Dialer{Timeout: timeout}
		client.Transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", opts.unixPath)
			},
		}
		url = "http://unix/v1/health"
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	// A node running per-node bearer tokens rejects an absent or wrong token,
	// so fall back to the same credential the CLI transport resolves.
	token := opts.token
	if token == "" {
		token = remoteTokenFromEnv()
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return client, req, nil
}

// checkWrkqServerHealth reports whether the daemon is serving. A 401 is not a
// health failure: it proves the daemon answered and only says this caller holds
// no usable token. Failing on it would report a healthy canonical daemon as
// broken, and would mask the stale-binary signal behind the same exit code.
func checkWrkqServerHealth(opts *serverOptions, timeout time.Duration) (authorized bool, err error) {
	client, req, err := newServerHealthRequest(opts, timeout)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("wrkq server is not responsive at %s: %w", statusTarget(collectWrkqServerStatus(opts)), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized && opts.token == "" {
		_ = resp.Body.Close()
		req.Header.Set("Authorization", "Bearer dev")
		resp, err = client.Do(req)
		if err != nil {
			return false, fmt.Errorf("wrkq server is not responsive at %s: %w", statusTarget(collectWrkqServerStatus(opts)), err)
		}
		defer func() { _ = resp.Body.Close() }()
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusUnauthorized:
		return false, nil
	default:
		return false, fmt.Errorf("wrkq server health returned HTTP %d", resp.StatusCode)
	}
}

func statusTarget(status serverRuntimeStatus) string {
	if status.SocketPath != "" {
		return status.SocketPath
	}
	if status.Endpoint != "" {
		return status.Endpoint
	}
	return "(unknown)"
}

func formatOptionalPID(pid *int) string {
	if pid == nil {
		return "(none)"
	}
	return strconv.Itoa(*pid)
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}

func loadedNotLoaded(ok bool) string {
	if ok {
		return "loaded"
	}
	return "not loaded"
}

func responsiveSuffix(ok bool) string {
	if ok {
		return " (responsive)"
	}
	return " (down)"
}

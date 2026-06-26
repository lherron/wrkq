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
	"runtime"
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
}

type serverOptions struct {
	addr       string
	unixPath   string
	token      string
	dbPath     string
	json       bool
	foreground bool
	daemon     bool
	timeoutMS  int
	force      bool
}

type launchdOwner struct {
	label         string
	domain        string
	serviceTarget string
	pid           *int
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

	startCmd.Flags().BoolVar(&opts.foreground, "foreground", false, "Run in the foreground when launchd is not loaded")
	startCmd.Flags().BoolVar(&opts.daemon, "daemon", false, "Run as a background process when launchd is not loaded")
	startCmd.Flags().IntVar(&opts.timeoutMS, "timeout-ms", 5000, "Startup timeout in milliseconds")

	stopCmd.Flags().IntVar(&opts.timeoutMS, "timeout-ms", 5000, "Shutdown timeout in milliseconds")
	stopCmd.Flags().BoolVar(&opts.force, "force", false, "Escalate to SIGKILL if graceful shutdown times out")

	restartCmd.Flags().BoolVar(&opts.foreground, "foreground", false, "Run in the foreground when launchd is not loaded")
	restartCmd.Flags().BoolVar(&opts.daemon, "daemon", false, "Run as a background process when launchd is not loaded")
	restartCmd.Flags().IntVar(&opts.timeoutMS, "timeout-ms", 5000, "Shutdown/startup timeout in milliseconds")
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
		if err := launchctlKickstart(owner, false); err != nil {
			return err
		}
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return writeServerLifecycleJSON(cmd, "started", "launchd", collectWrkqServerStatus(opts))
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
		if err := launchctlKickstart(owner, true); err != nil {
			return err
		}
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return writeServerLifecycleJSON(cmd, "restarted", "launchd", collectWrkqServerStatus(opts))
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
	return nil
}

func runServerHealth(cmd *cobra.Command, opts *serverOptions) error {
	if err := checkWrkqServerHealth(opts, 2*time.Second); err != nil {
		return err
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"status": "ok"})
	}
	fmt.Fprintln(cmd.OutOrStdout(), "ok")
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

func detectWrkqLaunchdOwner() *launchdOwner {
	if runtime.GOOS != "darwin" {
		return nil
	}
	uid := os.Getuid()
	label := wrkqLaunchdLabel()
	domain := fmt.Sprintf("gui/%d", uid)
	target := domain + "/" + label
	out, err := exec.Command("launchctl", "print", target).CombinedOutput()
	if err != nil {
		return nil
	}
	return &launchdOwner{
		label:         label,
		domain:        domain,
		serviceTarget: target,
		pid:           parseLaunchdPID(string(out)),
	}
}

func parseLaunchdPID(output string) *int {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "pid = ") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "pid = "))
		pid, err := strconv.Atoi(raw)
		if err == nil && pid > 0 {
			return &pid
		}
	}
	return nil
}

func launchctlKickstart(owner *launchdOwner, kill bool) error {
	args := []string{"kickstart"}
	if kill {
		args = append(args, "-k")
	}
	args = append(args, owner.serviceTarget)
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return fmt.Errorf("launchctl kickstart failed: %s", detail)
		}
		return fmt.Errorf("launchctl kickstart failed: %w", err)
	}
	return nil
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

func checkWrkqServerHealth(opts *serverOptions, timeout time.Duration) error {
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
		return err
	}
	if opts.token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("wrkq server is not responsive at %s: %w", statusTarget(collectWrkqServerStatus(opts)), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized && opts.token == "" {
		_ = resp.Body.Close()
		req.Header.Set("Authorization", "Bearer dev")
		resp, err = client.Do(req)
		if err != nil {
			return fmt.Errorf("wrkq server is not responsive at %s: %w", statusTarget(collectWrkqServerStatus(opts)), err)
		}
		defer func() { _ = resp.Body.Close() }()
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("wrkq server health returned HTTP %d", resp.StatusCode)
	}
	return nil
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

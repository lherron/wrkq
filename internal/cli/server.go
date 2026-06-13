package cli

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

type launchdOwner struct {
	label         string
	domain        string
	serviceTarget string
	pid           *int
}

var (
	serverAddr       string
	serverUnixPath   string
	serverToken      string
	serverDBPath     string
	serverJSON       bool
	serverForeground bool
	serverDaemon     bool
	serverTimeoutMS  int
	serverForce      bool
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage the wrkq daemon server",
}

var serverStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the wrkq daemon server",
	RunE:  runServerStart,
}

var serverServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the wrkq daemon server in the foreground",
	RunE:  runServerServe,
}

var serverStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the wrkq daemon server",
	RunE:  runServerStop,
}

var serverRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the wrkq daemon server",
	RunE:  runServerRestart,
}

var serverStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show wrkq daemon server status",
	RunE:  runServerStatus,
}

var serverHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check wrkq daemon server health",
	RunE:  runServerHealth,
}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.AddCommand(serverStartCmd, serverServeCmd, serverStopCmd, serverRestartCmd, serverStatusCmd, serverHealthCmd)

	serverCmd.PersistentFlags().StringVar(&serverAddr, "addr", os.Getenv("WRKQD_ADDR"), "Listen/status address")
	serverCmd.PersistentFlags().StringVar(&serverUnixPath, "unix", os.Getenv("WRKQD_UNIX"), "Listen/status Unix socket path")
	serverCmd.PersistentFlags().StringVar(&serverToken, "token", os.Getenv("WRKQD_TOKEN"), "Shared token for local auth")
	serverCmd.PersistentFlags().StringVar(&serverDBPath, "db-path", "", "Database path override")

	serverStartCmd.Flags().BoolVar(&serverForeground, "foreground", false, "Run in the foreground when launchd is not loaded")
	serverStartCmd.Flags().BoolVar(&serverDaemon, "daemon", false, "Run as a background process when launchd is not loaded")
	serverStartCmd.Flags().IntVar(&serverTimeoutMS, "timeout-ms", 5000, "Startup timeout in milliseconds")

	serverStopCmd.Flags().IntVar(&serverTimeoutMS, "timeout-ms", 5000, "Shutdown timeout in milliseconds")
	serverStopCmd.Flags().BoolVar(&serverForce, "force", false, "Escalate to SIGKILL if graceful shutdown times out")

	serverRestartCmd.Flags().BoolVar(&serverForeground, "foreground", false, "Run in the foreground when launchd is not loaded")
	serverRestartCmd.Flags().BoolVar(&serverDaemon, "daemon", false, "Run as a background process when launchd is not loaded")
	serverRestartCmd.Flags().IntVar(&serverTimeoutMS, "timeout-ms", 5000, "Shutdown/startup timeout in milliseconds")
	serverRestartCmd.Flags().BoolVar(&serverForce, "force", false, "Escalate to SIGKILL if graceful shutdown times out")

	serverStatusCmd.Flags().BoolVar(&serverJSON, "json", false, "Output as JSON")
}

func runServerStart(cmd *cobra.Command, args []string) error {
	applyServerDBFlag(cmd)
	mode, err := resolveServerMode("daemon")
	if err != nil {
		return err
	}
	status := collectWrkqServerStatus()
	if status.Running {
		return fmt.Errorf("daemon already running at %s (pid %s)", statusTarget(status), formatOptionalPID(status.PID))
	}
	if owner := detectWrkqLaunchdOwner(); owner != nil {
		if err := launchctlKickstart(owner, false); err != nil {
			return err
		}
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return writeServerLifecycleJSON(cmd, "started", "launchd", collectWrkqServerStatus())
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "wrkq: daemon started via launchd (%s)\n", owner.serviceTarget)
		return nil
	}
	if mode == "foreground" {
		return serveWrkqServer()
	}
	if err := daemonizeWrkqServer(time.Duration(serverTimeoutMS) * time.Millisecond); err != nil {
		return err
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return writeServerLifecycleJSON(cmd, "started", mode, collectWrkqServerStatus())
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "wrkq: daemon started")
	return nil
}

func runServerServe(cmd *cobra.Command, args []string) error {
	applyServerDBFlag(cmd)
	status := collectWrkqServerStatus()
	if status.Running {
		return fmt.Errorf("daemon already running at %s (pid %s)", statusTarget(status), formatOptionalPID(status.PID))
	}
	return serveWrkqServer()
}

func runServerStop(cmd *cobra.Command, args []string) error {
	status := collectWrkqServerStatus()
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
	if err := stopWrkqServerProcess(time.Duration(serverTimeoutMS)*time.Millisecond, serverForce); err != nil {
		return err
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return writeServerLifecycleJSON(cmd, "stopped", "", collectWrkqServerStatus())
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "wrkq: daemon stopped")
	return nil
}

func runServerRestart(cmd *cobra.Command, args []string) error {
	applyServerDBFlag(cmd)
	mode, err := resolveServerMode("daemon")
	if err != nil {
		return err
	}
	if owner := detectWrkqLaunchdOwner(); owner != nil {
		if err := launchctlKickstart(owner, true); err != nil {
			return err
		}
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return writeServerLifecycleJSON(cmd, "restarted", "launchd", collectWrkqServerStatus())
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "wrkq: daemon restarted via launchd (%s)\n", owner.serviceTarget)
		return nil
	}
	if err := stopWrkqServerProcess(time.Duration(serverTimeoutMS)*time.Millisecond, serverForce); err != nil && !errors.Is(err, errWrkqServerNotRunning) {
		return err
	}
	if mode == "foreground" {
		return serveWrkqServer()
	}
	if err := daemonizeWrkqServer(time.Duration(serverTimeoutMS) * time.Millisecond); err != nil {
		return err
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return writeServerLifecycleJSON(cmd, "restarted", mode, collectWrkqServerStatus())
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "wrkq: daemon restarted")
	return nil
}

func runServerStatus(cmd *cobra.Command, args []string) error {
	status := collectWrkqServerStatus()
	if serverJSON || !isStdoutTTY(cmd.OutOrStdout()) {
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

func runServerHealth(cmd *cobra.Command, args []string) error {
	if err := checkWrkqServerHealth(2 * time.Second); err != nil {
		return err
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"status": "ok"})
	}
	fmt.Fprintln(cmd.OutOrStdout(), "ok")
	return nil
}

func writeServerLifecycleJSON(cmd *cobra.Command, action, mode string, status serverRuntimeStatus) error {
	payload := map[string]interface{}{
		"action": action,
		"status": status,
	}
	if mode != "" {
		payload["mode"] = mode
	}
	return writeJSONOutput(cmd.OutOrStdout(), outputSelection{}, payload)
}

func serveWrkqServer() error {
	return ServeDaemon(DaemonOptions{
		Addr:    resolvedServerAddr(),
		Unix:    serverUnixPath,
		Token:   serverToken,
		DBPath:  serverDBPath,
		PIDPath: wrkqServerPIDPath(),
	})
}

func applyServerDBFlag(cmd *cobra.Command) {
	if serverDBPath != "" {
		return
	}
	if dbFlag := cmd.Flag("db"); dbFlag != nil {
		serverDBPath = dbFlag.Value.String()
	}
}

func collectWrkqServerStatus() serverRuntimeStatus {
	pidPath := wrkqServerPIDPath()
	pid := readWrkqPIDFile(pidPath)
	pidAlive := pid != nil && processAlive(*pid)
	owner := detectWrkqLaunchdOwner()
	if !pidAlive && owner != nil && owner.pid != nil {
		pid = owner.pid
		pidAlive = processAlive(*pid)
	}
	if !pidAlive && serverUnixPath == "" {
		if listenerPID := discoverTCPListenerPID(resolvedServerAddr()); listenerPID != nil {
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
	if serverUnixPath != "" {
		status.SocketPath = serverUnixPath
		status.SocketResponsive = isUnixSocketResponsive(serverUnixPath, 200*time.Millisecond)
		status.Running = status.SocketResponsive && (pid == nil || pidAlive)
		return status
	}
	status.Endpoint = "http://" + resolvedServerAddr()
	status.EndpointResponsive = isTCPResponsive(resolvedServerAddr(), 200*time.Millisecond)
	status.Running = status.EndpointResponsive && (pid == nil || pidAlive)
	return status
}

func resolveServerMode(defaultMode string) (string, error) {
	if serverForeground && serverDaemon {
		return "", fmt.Errorf("--foreground and --daemon are mutually exclusive")
	}
	if serverForeground {
		return "foreground", nil
	}
	if serverDaemon {
		return "daemon", nil
	}
	return defaultMode, nil
}

func resolvedServerAddr() string {
	if serverAddr != "" {
		return serverAddr
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

func daemonizeWrkqServer(timeout time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve executable: %w", err)
	}
	args := []string{"server", "serve"}
	if serverAddr != "" {
		args = append(args, "--addr", serverAddr)
	}
	if serverUnixPath != "" {
		args = append(args, "--unix", serverUnixPath)
	}
	if serverToken != "" {
		args = append(args, "--token", serverToken)
	}
	if serverDBPath != "" {
		args = append(args, "--db-path", serverDBPath)
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
	return waitForWrkqServer(timeout)
}

func waitForWrkqServer(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status := collectWrkqServerStatus()
		if status.Running {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for wrkq server at %s", statusTarget(collectWrkqServerStatus()))
}

var errWrkqServerNotRunning = errors.New("wrkq server is not running")

func stopWrkqServerProcess(timeout time.Duration, force bool) error {
	status := collectWrkqServerStatus()
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
		if !processAlive(pid) && !collectWrkqServerStatus().EndpointResponsive && !collectWrkqServerStatus().SocketResponsive {
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

func checkWrkqServerHealth(timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	url := "http://" + resolvedServerAddr() + "/v1/health"
	if serverUnixPath != "" {
		dialer := &net.Dialer{Timeout: timeout}
		client.Transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", serverUnixPath)
			},
		}
		url = "http://unix/v1/health"
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if serverToken != "" {
		req.Header.Set("Authorization", "Bearer "+serverToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("wrkq server is not responsive at %s: %w", statusTarget(collectWrkqServerStatus()), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized && serverToken == "" {
		_ = resp.Body.Close()
		req.Header.Set("Authorization", "Bearer dev")
		resp, err = client.Do(req)
		if err != nil {
			return fmt.Errorf("wrkq server is not responsive at %s: %w", statusTarget(collectWrkqServerStatus()), err)
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

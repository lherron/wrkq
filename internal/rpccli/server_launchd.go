package rpccli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// launchd restarts are a bootout + bootstrap cycle rather than `kickstart -k`.
// launchd derives a Lightweight Code Requirement (LWCR) pinning the cdhash of
// the binary present when the job was bootstrapped. `go build` emits a fresh
// adhoc signature on every build, so a rebuilt wrkqd no longer satisfies the
// pinned requirement and every respawn inside the existing job is SIGKILLed
// with OS_REASON_CODESIGNING. Only bootout + bootstrap re-derives the
// requirement from the binary that is on disk now.
//
// `launchctl bootout` is asynchronous: it returns before the job has finished
// unloading, and bootstrapping into a still-unloading job fails with
// "Bootstrap failed: 5: Input/output error", leaving the daemon neither booted
// out nor loaded. Every bootstrap here therefore waits for the job to actually
// disappear first.
var (
	launchdUnloadTimeout    = 45 * time.Second
	launchdBootstrapTimeout = 20 * time.Second
)

// launchdPollInterval is how often the unload/bootstrap loops poll launchd.
// Tests shorten it.
var launchdPollInterval = 250 * time.Millisecond

// runLaunchctl executes launchctl and returns its combined output. Tests
// replace it with a fake so the bootout/bootstrap ordering can be asserted
// without touching the real launchd domain.
var runLaunchctl = func(args ...string) (string, error) {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	return string(out), err
}

// runCodesign reads code-signing information for a target, which is either a
// filesystem path or "+<pid>" for a running process. codesign writes its
// display output to stderr, so combined output is required.
var runCodesign = func(target string) (string, error) {
	out, err := exec.Command("codesign", "-dvvv", target).CombinedOutput()
	return string(out), err
}

type launchdOwner struct {
	label          string
	domain         string
	serviceTarget  string
	pid            *int
	plistPath      string
	program        string
	arguments      []string
	environment    map[string]string
	lastExitCode   string
	lastExitReason string
	properties     string
}

// binaryIdentity compares the on-disk daemon binary with the image the running
// daemon actually loaded. They diverge whenever the binary is rebuilt under a
// live daemon, which is the state that arms the codesigning kill on the next
// respawn.
type binaryIdentity struct {
	path       string
	onDisk     string
	running    string
	comparable bool
}

func (b binaryIdentity) stale() bool {
	return b.comparable && b.onDisk != b.running
}

func detectWrkqLaunchdOwner() *launchdOwner {
	if runtime.GOOS != "darwin" {
		return nil
	}
	uid := os.Getuid()
	label := wrkqLaunchdLabel()
	domain := fmt.Sprintf("gui/%d", uid)
	target := domain + "/" + label
	out, err := runLaunchctl("print", target)
	if err != nil {
		return nil
	}
	owner := parseLaunchdPrint(out)
	owner.label = label
	owner.domain = domain
	owner.serviceTarget = target
	return owner
}

// parseLaunchdPrint reads the block-structured output of `launchctl print`.
// Top-level scalars (path, program, pid, last exit code/reason, properties) and
// the `arguments` and `environment` blocks are all the daemon lifecycle needs.
func parseLaunchdPrint(output string) *launchdOwner {
	owner := &launchdOwner{environment: map[string]string{}}
	var stack []string
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if line == "}" || line == "};" {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if strings.HasSuffix(line, "= {") {
			name := strings.TrimSpace(strings.TrimSuffix(line, "= {"))
			stack = append(stack, name)
			continue
		}
		// `launchctl print` wraps everything in one block named for the
		// service target, so the job's own scalars sit one level in.
		block := ""
		if len(stack) > 0 {
			block = stack[len(stack)-1]
		}
		switch block {
		case "arguments":
			owner.arguments = append(owner.arguments, line)
			continue
		case "environment":
			// Deliberately not "inherited environment" or "default
			// environment": those belong to launchd, not to the job.
			if key, value, ok := strings.Cut(line, " => "); ok {
				owner.environment[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
			continue
		}
		if len(stack) > 1 {
			continue
		}
		key, value, ok := strings.Cut(line, " = ")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "path":
			owner.plistPath = value
		case "program":
			owner.program = value
		case "pid":
			if pid, err := strconv.Atoi(value); err == nil && pid > 0 {
				owner.pid = &pid
			}
		case "last exit code":
			owner.lastExitCode = value
		case "last exit reason":
			owner.lastExitReason = value
		case "properties":
			owner.properties = value
		}
	}
	return owner
}

// programPath is the binary launchd executes for this job.
func (o *launchdOwner) programPath() string {
	if o == nil {
		return ""
	}
	if o.program != "" {
		return o.program
	}
	if len(o.arguments) > 0 {
		return o.arguments[0]
	}
	return ""
}

// endpoint reports the address the job actually binds, which is frequently not
// the 127.0.0.1 default: the canonical daemon binds a node address passed as
// `--addr` in the plist. Probing the default instead of the job's real endpoint
// reports a healthy daemon as down.
func (o *launchdOwner) endpoint() (addr string, unixPath string) {
	if o == nil {
		return "", ""
	}
	for i, arg := range o.arguments {
		flag, inline, hasInline := strings.Cut(arg, "=")
		value := ""
		switch {
		case hasInline:
			value = inline
		case i+1 < len(o.arguments):
			value = o.arguments[i+1]
		}
		if value == "" {
			continue
		}
		switch strings.TrimLeft(flag, "-") {
		case "addr":
			addr = value
		case "unix":
			unixPath = value
		}
	}
	if addr == "" {
		addr = o.environment["WRKQD_ADDR"]
	}
	if unixPath == "" {
		unixPath = o.environment["WRKQD_UNIX"]
	}
	return addr, unixPath
}

// inspectLaunchdBinary compares the cdhash of the on-disk program with the
// cdhash of the image the running process loaded. `codesign -dvvv +<pid>`
// reports the loaded image, so a binary replaced under a live daemon shows up
// as a mismatch.
func inspectLaunchdBinary(owner *launchdOwner) binaryIdentity {
	identity := binaryIdentity{path: owner.programPath()}
	if runtime.GOOS != "darwin" || identity.path == "" {
		return identity
	}
	identity.onDisk = codesignCDHash(identity.path)
	if owner.pid != nil {
		identity.running = codesignCDHash("+" + strconv.Itoa(*owner.pid))
	}
	identity.comparable = identity.onDisk != "" && identity.running != ""
	return identity
}

func codesignCDHash(target string) string {
	out, err := runCodesign(target)
	if err != nil && out == "" {
		return ""
	}
	return parseCDHash(out)
}

func parseCDHash(output string) string {
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if value, ok := strings.CutPrefix(line, "CDHash="); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func launchdJobLoaded(target string) bool {
	_, err := runLaunchctl("print", target)
	return err == nil
}

// relaunchLaunchdJob boots the job out, waits for launchd to finish unloading
// it, and bootstraps it again so the code requirement is re-derived from the
// binary on disk now.
func relaunchLaunchdJob(owner *launchdOwner) error {
	if owner.plistPath == "" {
		return fmt.Errorf("launchd job %s does not report a plist path; cannot bootstrap it again", owner.serviceTarget)
	}
	if _, err := os.Stat(owner.plistPath); err != nil {
		return fmt.Errorf("launchd job %s references plist %s which is not readable (%v); refusing to boot the job out with no way to bootstrap it again", owner.serviceTarget, owner.plistPath, err)
	}
	if out, err := runLaunchctl("bootout", owner.serviceTarget); err != nil && launchdJobLoaded(owner.serviceTarget) {
		return fmt.Errorf("launchctl bootout %s failed: %s", owner.serviceTarget, launchctlDetail(out, err))
	}
	if err := waitForLaunchdUnload(owner.serviceTarget, launchdUnloadTimeout); err != nil {
		return err
	}
	return bootstrapLaunchdJob(owner)
}

// waitForLaunchdUnload blocks until launchd no longer knows the job. bootout is
// asynchronous, and bootstrapping before the teardown completes fails and
// leaves the service down.
func waitForLaunchdUnload(target string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if !launchdJobLoaded(target) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("launchd job %s was still loaded %s after bootout; not bootstrapping into a half-unloaded job. Wait for it to unload, then run: launchctl bootstrap <domain> <plist>", target, timeout)
		}
		time.Sleep(launchdPollInterval)
	}
}

// bootstrapLaunchdJob retries because launchd can still report a transient
// "Input/output error" in the moments after a job disappears from print.
func bootstrapLaunchdJob(owner *launchdOwner) error {
	deadline := time.Now().Add(launchdBootstrapTimeout)
	var detail string
	for {
		out, err := runLaunchctl("bootstrap", owner.domain, owner.plistPath)
		if err == nil {
			return nil
		}
		detail = launchctlDetail(out, err)
		if launchdJobLoaded(owner.serviceTarget) {
			// Someone (or a racing bootstrap) already brought it back.
			return nil
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(launchdPollInterval)
	}
	return fmt.Errorf("launchctl bootstrap %s %s failed: %s\nThe job is booted out and the daemon is DOWN. Recover with: launchctl bootstrap %s %s", owner.domain, owner.plistPath, detail, owner.domain, owner.plistPath)
}

func launchctlKickstart(owner *launchdOwner, kill bool) error {
	args := []string{"kickstart"}
	if kill {
		args = append(args, "-k")
	}
	args = append(args, owner.serviceTarget)
	out, err := runLaunchctl(args...)
	if err != nil {
		return fmt.Errorf("launchctl kickstart failed: %s", launchctlDetail(out, err))
	}
	return nil
}

func launchctlDetail(out string, err error) string {
	detail := strings.TrimSpace(out)
	if detail != "" {
		return detail
	}
	if err != nil {
		return err.Error()
	}
	return "unknown error"
}

// launchdFailureDetail explains why a job that was just (re)started is not
// answering. The codesigning kill leaves nothing in the daemon log because the
// process dies before it can write, so launchd's exit reason is the only signal.
func launchdFailureDetail(owner *launchdOwner) string {
	if owner == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nlaunchd job %s:", owner.serviceTarget)
	if owner.pid != nil {
		fmt.Fprintf(&b, "\n  pid              = %d", *owner.pid)
	}
	if owner.lastExitCode != "" {
		fmt.Fprintf(&b, "\n  last exit code   = %s", owner.lastExitCode)
	}
	if owner.lastExitReason != "" {
		fmt.Fprintf(&b, "\n  last exit reason = %s", owner.lastExitReason)
	}
	if strings.Contains(owner.lastExitReason, "CODESIGNING") {
		fmt.Fprintf(&b, "\n  launchd is killing the daemon for failing the code requirement it pinned at bootstrap.")
		fmt.Fprintf(&b, "\n  Recover with: launchctl bootout %s; then bootstrap it again from %s", owner.serviceTarget, owner.plistPath)
	}
	return b.String()
}

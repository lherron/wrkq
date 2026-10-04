package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DatabaseServingError is an offline-migration refusal, not SQLite contention.
// Job can be loaded without a PID: KeepAlive may spawn a pre-guard daemon.
type DatabaseServingError struct {
	Path string
	PID  int
	Job  string
}

func (e *DatabaseServingError) Error() string {
	return fmt.Sprintf("MIGRATION_DATABASE_SERVING: refusing migration of %s: wrkqd pid=%d job=%s. Back up the DB, then run OFFLINE: launchctl bootout gui/%d/%s; wait until launchctl print reports the job absent and the daemon exits; wrkqadm --db %q migrate; launchctl bootstrap gui/%d ~/Library/LaunchAgents/%s.plist. For a standalone daemon, stop pid %d and disable its launcher before migrating, then start it again.", e.Path, e.PID, e.Job, os.Getuid(), migrationJobLabel(), e.Path, os.Getuid(), migrationJobLabel(), e.PID)
}

// OpenForMigration excludes daemon startup before Open's WAL pragmas and keeps
// the lease through Close. Use for init and applying migrations.
func OpenForMigration(path string) (*DB, error) {
	lease, err := acquireMigrationLease(path)
	if err != nil {
		return nil, err
	}
	database, err := Open(strings.TrimSuffix(lease.Name(), ".migration-lock"))
	if err != nil {
		_ = lease.Close()
		return nil, err
	}
	database.migrationLease = lease
	return database, nil
}

// AcquireServingLease is held from before SQLite opens until after it closes.
// The sidecar is never unlinked: replacing its inode would split the lock.
func AcquireServingLease(path string) (func(), error) {
	path, err := lifecyclePath(path)
	if err != nil {
		return nil, err
	}
	lease, err := lockLifecycle(path, syscall.LOCK_SH)
	if err != nil {
		return nil, err
	}
	return func() { _ = lease.Close() }, nil
}

func lifecyclePath(path string) (string, error) {
	if path == "" || strings.ContainsAny(path, "?\x00") || strings.HasPrefix(path, "file:") || path == ":memory:" {
		return "", fmt.Errorf("DATABASE_PATH_UNSUPPORTED: migration/daemon requires a plain local file path: %q", path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
			return "", fmt.Errorf("DATABASE_HARDLINK_UNSUPPORTED: %s has multiple names; SQLite WAL and migration leases require one file identity", absolute)
		}
		return filepath.EvalSymlinks(absolute)
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if _, err := os.Lstat(absolute); err == nil {
		return "", fmt.Errorf("DATABASE_PATH_UNSUPPORTED: dangling symlink %s", absolute)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0755); err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func lockLifecycle(path string, mode int) (*os.File, error) {
	lease, err := os.OpenFile(path+".migration-lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_LIFECYCLE_INSPECTION_FAILED: %w", err)
	}
	if err := syscall.Flock(int(lease.Fd()), mode|syscall.LOCK_NB); err != nil {
		_ = lease.Close()
		return nil, fmt.Errorf("DATABASE_LIFECYCLE_BUSY: daemon/migration lease for %s is held; stop the daemon and disable its launcher before migrating: %w", path, err)
	}
	return lease, nil
}

func acquireMigrationLease(path string) (*os.File, error) {
	path, err := lifecyclePath(path)
	if err != nil {
		return nil, err
	}
	if err := refuseLoadedMigrationJob(path); err != nil {
		return nil, err
	}
	lease, err := lockLifecycle(path, syscall.LOCK_EX)
	if err != nil {
		if probe := refuseServingProcess(path); probe != nil {
			return nil, probe
		}
		if probe := refuseServingProcess(path + ".migration-lock"); probe != nil {
			var serving *DatabaseServingError
			if errors.As(probe, &serving) {
				serving.Path = path
			}
			return nil, probe
		}
		return nil, err
	}
	// Inspect under the lease: guarded daemons cannot start during this probe.
	if err := refuseServingProcess(path); err != nil {
		_ = lease.Close()
		return nil, err
	}
	return lease, nil
}

func (db *DB) guardMigration() (func(), error) {
	if db.migrationLease != nil {
		return func() {}, nil
	}
	lease, err := acquireMigrationLease(db.path)
	if err != nil {
		return nil, err
	}
	return func() { _ = lease.Close() }, nil
}

// Inspect in a child process. Opening/closing the SQLite file ourselves could
// release this process's SQLite POSIX locks (SQLite howtocorrupt.html §2.2).
func refuseServingProcess(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lsof", "+c0", "-w", "-Fpc", "--", path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(output) == 0 && stderr.Len() == 0 && ctx.Err() == nil {
			return nil
		}
		return fmt.Errorf("DATABASE_LIFECYCLE_INSPECTION_FAILED: cannot inspect holders of %s with lsof: %w (%s)", path, err, strings.TrimSpace(stderr.String()))
	}
	pid := 0
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "p") {
			pid, _ = strconv.Atoi(line[1:])
		}
		if strings.HasPrefix(line, "c") && pid != os.Getpid() && (line[1:] == "wrkqd" || line[1:] == "wrkq" || strings.HasSuffix(line[1:], "-wrkqd")) {
			return &DatabaseServingError{Path: path, PID: pid, Job: "standalone"}
		}
	}
	return nil
}

func migrationJobLabel() string {
	if label := os.Getenv("WRKQ_LAUNCHD_LABEL"); label != "" {
		return label
	}
	return "com.praesidium.wrkq-server"
}

type migrationJob struct {
	path string
	pid  int
}

func refuseLoadedMigrationJob(path string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), migrationJobLabel())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "launchctl", "print", target).CombinedOutput()
	if err != nil {
		if ctx.Err() == nil && strings.Contains(string(out), "Could not find service") {
			return nil
		}
		return fmt.Errorf("DATABASE_LIFECYCLE_INSPECTION_FAILED: cannot inspect launchd job %s: %w", target, err)
	}
	job, err := parseMigrationJob(string(out))
	if err != nil {
		return fmt.Errorf("DATABASE_LIFECYCLE_INSPECTION_FAILED: loaded job %s database unresolved: %w", target, err)
	}
	same := filepath.Clean(job.path) == path
	if a, err := os.Stat(job.path); err == nil {
		if b, err := os.Stat(path); err == nil {
			same = os.SameFile(a, b)
		}
	}
	if same {
		return &DatabaseServingError{Path: path, PID: job.pid, Job: target}
	}
	return nil
}

// Mirrors scripts/resolve-job-db.sh: only job argv/environment, never the
// inspecting shell's config. Relative paths require the job's working directory.
func parseMigrationJob(output string) (migrationJob, error) {
	job := migrationJob{}
	env := map[string]string{}
	var args, stack []string
	directory := ""
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasSuffix(line, "= {") {
			stack = append(stack, strings.TrimSpace(strings.TrimSuffix(line, "= {")))
			continue
		}
		if line == "}" || line == "};" {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if len(stack) == 0 {
			continue
		}
		switch stack[len(stack)-1] {
		case "arguments":
			args = append(args, line)
			continue
		case "environment":
			if k, v, ok := strings.Cut(line, " => "); ok {
				env[k] = v
			}
			continue
		}
		if len(stack) != 1 {
			continue
		}
		if k, v, ok := strings.Cut(line, " = "); ok {
			switch k {
			case "pid":
				job.pid, _ = strconv.Atoi(v)
			case "working directory":
				directory = v
			}
		}
	}
	for i, arg := range args {
		flag, value, inline := strings.Cut(arg, "=")
		if flag != "--db" && flag != "-db" {
			continue
		}
		if !inline {
			if i+1 == len(args) {
				return job, fmt.Errorf("bare --db")
			}
			value = args[i+1]
		}
		job.path = value
	}
	if job.path == "" {
		if env["WRKQ_DB"] != "" && env["WRKQ_DB_PATH"] != "" && env["WRKQ_DB"] != env["WRKQ_DB_PATH"] {
			return job, fmt.Errorf("conflicting job DB environment")
		}
		job.path = env["WRKQ_DB"]
		if job.path == "" {
			job.path = env["WRKQ_DB_PATH"]
		}
		if job.path == "" && env["WRKQ_DB_PATH_FILE"] != "" {
			file := env["WRKQ_DB_PATH_FILE"]
			if !filepath.IsAbs(file) {
				return job, fmt.Errorf("relative WRKQ_DB_PATH_FILE")
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return job, err
			}
			job.path = strings.TrimSpace(string(data))
		}
	}
	if job.path == "" || strings.HasPrefix(job.path, "rpc://") || strings.Contains(job.path, "?") {
		return job, fmt.Errorf("job has no plain local DB path")
	}
	if !filepath.IsAbs(job.path) {
		if !filepath.IsAbs(directory) {
			return job, fmt.Errorf("relative DB without absolute job working directory")
		}
		job.path = filepath.Join(directory, job.path)
	}
	job.path = filepath.Clean(job.path)
	return job, nil
}

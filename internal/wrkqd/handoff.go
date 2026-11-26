package wrkqd

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const listenFDEnv = "WRKQD_LISTEN_FD"

func takeInheritedListener() (*os.File, error) {
	value, ok := os.LookupEnv(listenFDEnv)
	if !ok {
		return nil, nil
	}
	if err := os.Unsetenv(listenFDEnv); err != nil {
		return nil, err
	}
	fd, err := strconv.Atoi(value)
	if err != nil || fd < 3 {
		return nil, fmt.Errorf("invalid %s value %q", listenFDEnv, value)
	}
	syscall.CloseOnExec(fd)
	return os.NewFile(uintptr(fd), "inherited listener"), nil
}

func duplicateListener(listener net.Listener) (*os.File, error) {
	switch listener := listener.(type) {
	case *net.TCPListener:
		return listener.File()
	case *net.UnixListener:
		listener.SetUnlinkOnClose(false)
		file, err := listener.File()
		if err != nil {
			listener.SetUnlinkOnClose(true)
		}
		return file, err
	default:
		return nil, fmt.Errorf("unsupported listener type %T", listener)
	}
}

// ExecHandoff consumes the listening descriptor returned by ServeDaemon and
// replaces wrkqd with the executable now at its original path.
func ExecHandoff(file *os.File) error {
	defer func() { _ = file.Close() }()
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable for handoff: %w", err)
	}
	fd := file.Fd()
	if _, err := unix.FcntlInt(fd, unix.F_SETFD, 0); err != nil {
		return fmt.Errorf("make listener fd inheritable: %w", err)
	}
	env := append(os.Environ(), listenFDEnv+"="+strconv.Itoa(int(fd)))
	logHandoff("daemon.handoff.exec", map[string]string{"executable": exe})
	return fmt.Errorf("exec handoff: %w", syscall.Exec(exe, os.Args, env))
}

func logHandoff(event string, data map[string]string) {
	writeHTTPLogLine(requestLogLine{
		Level: "info", Component: "daemon", Event: event, Data: data,
		TS: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
	})
}

//go:build darwin

package rpccli

import (
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// darwin /dev/ptmx ioctls (grantpt/unlockpt/ptsname equivalents). Used ONLY by
// the parity harness to attach a real terminal to a CLI's stdout so the legacy
// TTY-only render path (e.g. `relation ls` table) can be byte-compared against
// the mirror. No third-party pty dependency is pulled in for this.
const (
	tiocPtyGrant = 0x20007454 // TIOCPTYGRANT
	tiocPtyGname = 0x40807453 // TIOCPTYGNAME
	tiocPtyUnlk  = 0x20007452 // TIOCPTYUNLK
)

func ptyIoctl(fd, req, arg uintptr) syscall.Errno {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg)
	return e
}

// openPTY allocates a pseudo-terminal pair (master, slave-name).
func openPTY(t *testing.T) (master *os.File, slaveName string) {
	t.Helper()
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("pty unavailable (open /dev/ptmx): %v", err)
	}
	if e := ptyIoctl(ptmx.Fd(), tiocPtyGrant, 0); e != 0 {
		_ = ptmx.Close()
		t.Skipf("pty unavailable (grantpt): %v", e)
	}
	if e := ptyIoctl(ptmx.Fd(), tiocPtyUnlk, 0); e != 0 {
		_ = ptmx.Close()
		t.Skipf("pty unavailable (unlockpt): %v", e)
	}
	buf := make([]byte, 128)
	if e := ptyIoctl(ptmx.Fd(), tiocPtyGname, uintptr(unsafe.Pointer(&buf[0]))); e != 0 {
		_ = ptmx.Close()
		t.Skipf("pty unavailable (ptsname): %v", e)
	}
	name := string(buf)
	for i, b := range buf {
		if b == 0 {
			name = string(buf[:i])
			break
		}
	}
	return ptmx, name
}

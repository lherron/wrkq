//go:build darwin

package rpccli

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// processStartTime reads this process's kernel start time so the profile can
// charge exec/dyld/runtime-init cost that happens before main runs.
func processStartTime() (time.Time, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		return time.Time{}, false
	}
	tv := kp.Proc.P_starttime
	return time.Unix(tv.Sec, int64(tv.Usec)*1000), true
}

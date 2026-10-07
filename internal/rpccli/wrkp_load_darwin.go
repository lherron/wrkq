package rpccli

import (
	"encoding/binary"
	"errors"

	"golang.org/x/sys/unix"
)

// readLoad1 reads the 1-minute load average from the vm.loadavg sysctl: a
// struct loadavg { fixpt_t ldavg[3]; long fscale; } in host byte order.
func readLoad1() (float64, error) {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return 0, err
	}
	if len(raw) < 24 {
		return 0, errors.New("short vm.loadavg")
	}
	ldavg := binary.LittleEndian.Uint32(raw[0:4])
	fscale := binary.LittleEndian.Uint64(raw[16:24])
	if fscale == 0 {
		return 0, errors.New("vm.loadavg fscale is zero")
	}
	return float64(ldavg) / float64(fscale), nil
}

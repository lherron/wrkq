//go:build !darwin

package rpccli

import (
	"os"
	"strconv"
	"strings"
)

// readLoad1 reads the 1-minute load average from /proc/loadavg; elsewhere it
// fails and the run carries no load.
func readLoad1() (float64, error) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(raw)), " ")
	return strconv.ParseFloat(first, 64)
}

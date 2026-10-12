//go:build !darwin

package rpccli

import "time"

func processStartTime() (time.Time, bool) { return time.Time{}, false }

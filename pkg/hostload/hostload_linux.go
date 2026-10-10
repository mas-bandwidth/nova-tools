//go:build linux

package hostload

import (
	"strconv"
	"strings"
)

// ParseProcLoadavg is the one-minute load average, the first field of
// /proc/loadavg ("0.52 0.58 0.59 1/389 12345").
func ParseProcLoadavg(s string) (float64, bool) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

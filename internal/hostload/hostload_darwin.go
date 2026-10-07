//go:build darwin

package hostload

import (
	"strconv"
	"strings"
)

// ParseIostat is the busy percent of the last line of darwin's `iostat -c 2 -w 1 -n 0`,
// 100 minus the id column of the second reading (the first is the average since boot,
// so one reading alone is not a second).
func ParseIostat(s string) (float64, bool) {
	var idle float64
	n := 0
	for _, line := range strings.Split(s, "\n") {
		id, ok := iostatIdle(line)
		if !ok {
			continue
		}
		idle = id
		n++
	}
	if n < 2 {
		return 0, false
	}
	return 100 - idle, true
}

// iostatIdle is the id column of one data row. iostat prints us, sy and id each
// %3.0f on some releases, so an idle of 100 runs into the column before it
// ("  0  0100"). Read complete whitespace-separated fields first; only a packed
// row needs the first nine characters cut in threes. Cutting a spaced row first
// can truncate its idle value ("  0  0 100" becomes idle 10).
func iostatIdle(line string) (float64, bool) {
	cols := strings.Fields(line)
	if len(cols) < 3 || !iostatCols(cols) {
		if len(line) < 9 {
			return 0, false
		}
		cols = make([]string, 0, 3)
		for i := 0; i < 9; i += 3 {
			cols = append(cols, strings.TrimSpace(line[i:i+3]))
		}
		if !iostatCols(cols) {
			return 0, false
		}
	}
	id, _ := strconv.Atoi(cols[2])
	return float64(id), true
}

// iostatCols says three words are the us, sy and id of a row: whole percents, id at most 100.
func iostatCols(c []string) bool {
	if len(c) < 3 {
		return false
	}
	for _, w := range c[:3] {
		if v, err := strconv.Atoi(w); err != nil || v < 0 || v > 100 {
			return false
		}
	}
	return true
}

// ParseVMLoadavg is the one-minute load average from darwin's vm.loadavg,
// struct loadavg {uint32 ldavg[3]; long fscale}: 24 bytes on 64-bit darwin,
// little-endian. The sysctl reply loses trailing NULs, so a short reply is
// padded back to 24 bytes.
func ParseVMLoadavg(b []byte) (float64, bool) {
	if len(b) > 24 {
		return 0, false
	}
	buf := make([]byte, 24)
	copy(buf, b)
	le32 := func(p []byte) uint64 {
		return uint64(p[0]) | uint64(p[1])<<8 | uint64(p[2])<<16 | uint64(p[3])<<24
	}
	scale := le32(buf[16:20]) | le32(buf[20:24])<<32
	if scale == 0 {
		return 0, false
	}
	return float64(le32(buf[0:4])) / float64(scale), true
}

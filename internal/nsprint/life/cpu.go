package life

import (
	"strconv"
	"strings"
	"sync"
)

// The CPU cell (Glenn 2026-09-26 9:35 AM ET, Activity Monitor beside the
// table: "You are not yet normalizing by cores"): the beat carries the
// machine's CPU busy time as a percent of every core over the interval since
// the last sample, the number Activity Monitor and top print, not the load
// average over cores (a load average counts waiting threads and passes 100%).
// Linux reads /proc/stat; darwin reads top's CPU usage line; elsewhere the
// cell is empty and the table falls back to load1 / ncpu.

// cpuSample is cumulative busy and total ticks (Linux) or one instantaneous
// busy percent (darwin, where total is 0).
type cpuSample struct {
	busy, total uint64
	pct         float64
	ok          bool
}

var cpuMu sync.Mutex
var cpuLast cpuSample

// CPUBusyNow is the CPU busy percent since the previous call, "" when the
// platform cannot measure or on the first call (no interval yet).
func CPUBusyNow() string {
	cpuMu.Lock()
	defer cpuMu.Unlock()
	cur := cpuRead()
	prev := cpuLast
	cpuLast = cur
	pct, ok := cpuBusy(prev, cur)
	if !ok {
		return ""
	}
	return strconv.FormatFloat(pct, 'f', 1, 64)
}

// cpuBusy is the busy percent between two samples: ticks on Linux, the
// current sample's own percent on darwin.
func cpuBusy(prev, cur cpuSample) (float64, bool) {
	if !cur.ok {
		return 0, false
	}
	if cur.total == 0 {
		return cur.pct, true
	}
	if !prev.ok || cur.total <= prev.total {
		return 0, false
	}
	return float64(cur.busy-prev.busy) / float64(cur.total-prev.total) * 100, true
}

// parseProcStat is the cpu line of /proc/stat: user nice system idle iowait
// irq softirq steal; busy is everything but idle and iowait.
func parseProcStat(s string) cpuSample {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		var vals []uint64
		for _, x := range f[1:] {
			v, err := strconv.ParseUint(x, 10, 64)
			if err != nil {
				return cpuSample{}
			}
			vals = append(vals, v)
		}
		var total uint64
		for _, v := range vals {
			total += v
		}
		idle := vals[3]
		if len(vals) > 4 {
			idle += vals[4]
		}
		return cpuSample{busy: total - idle, total: total, ok: true}
	}
	return cpuSample{}
}

// parseTopCPU is darwin top's "CPU usage: a% user, b% sys, c% idle" line;
// busy is 100 minus idle.
func parseTopCPU(s string) cpuSample {
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "CPU usage:") {
			continue
		}
		for _, part := range strings.Split(strings.TrimPrefix(line, "CPU usage:"), ",") {
			f := strings.Fields(part)
			if len(f) == 2 && f[1] == "idle" {
				idle, err := strconv.ParseFloat(strings.TrimSuffix(f[0], "%"), 64)
				if err != nil {
					return cpuSample{}
				}
				return cpuSample{pct: 100 - idle, ok: true}
			}
		}
	}
	return cpuSample{}
}

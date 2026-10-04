// Package hostload measures how busy a machine is, as a percent of all its
// cores: the CPU busy percent over an interval (the number Activity Monitor
// and top print), or, where that cannot be measured, the one-minute load
// average over the logical cores. A load average counts threads waiting as
// well as running, so it passes 100% on a machine that is not saturated; it is
// the fallback only.
//
// Linux reads /proc/stat and /proc/loadavg. darwin asks top for its CPU usage
// line (a process of its own, so at most once every TopEvery) and reads
// vm.loadavg with sysctl, in Go. Elsewhere only a given value is known.
//
// A member does not measure at its beat: a Sampler takes the busy percent once a
// second (Linux: /proc/stat counters, a read of a file; darwin: iostat, which takes
// its own second at about 4 ms of CPU, where top costs 300) and a Ring keeps
// the last ten, so the beat can carry the highest of them.
//
// Every read of the machine is a Source function, so a caller measures with
// Local and a test with its own inputs; Measure is a pure function of the
// source, the state the previous measurement left, and the clock.
package hostload

import (
	"math"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// MaxPercent caps a measured percent: a load average over cores has no upper
// bound, and a cell wants a number that fits.
const MaxPercent = 1000.0

// TopEvery is the least time between two runs of top on darwin: between them
// the last reading stands.
const TopEvery = 10 * time.Second

// The ways a percent was measured.
const (
	HowCPU   = "cpu"   // CPU busy percent over an interval
	HowLoad1 = "load1" // one-minute load average over the logical cores
)

// Ticks is one reading of the kernel's cumulative CPU counters: busy is every
// tick but idle and iowait, total is every tick.
type Ticks struct {
	Busy  uint64 `json:"busy"`
	Total uint64 `json:"total"`
}

// State is what one measurement leaves for the next: the last CPU counters
// (Linux, where the busy percent is the change between two readings), and
// the last top reading and when it was taken (darwin).
type State struct {
	Ticks  *Ticks    `json:"ticks,omitempty"`
	TopAt  time.Time `json:"top_at,omitzero"`
	TopPct float64   `json:"top_pct,omitempty"`
}

// Source is how a machine is read. A nil function is a reading this machine
// does not have.
type Source struct {
	// NCPU is the logical cores; zero is runtime.NumCPU.
	NCPU int
	// ProcStat is the text of /proc/stat (Linux).
	ProcStat func() (string, error)
	// Top is the output of top's one-sample header (darwin).
	Top func() (string, error)
	// CPUSecond is the busy percent over the next second, taking that second
	// (darwin: iostat). The Sampler's source where there is no ProcStat.
	CPUSecond func() (float64, error)
	// Load1 is the one-minute load average.
	Load1 func() (float64, bool)
}

// Local is this machine's source.
func Local() Source { return localSource() }

// Measure is the machine's load as a percent of all its cores at now, how it
// was measured, and the state for the next measurement. ok is false when
// nothing could be measured.
func Measure(src Source, prev State, now time.Time) (pct float64, how string, next State, ok bool) {
	next = prev
	switch {
	case src.ProcStat != nil:
		if text, err := src.ProcStat(); err == nil {
			if cur, good := ParseProcStat(text); good {
				next.Ticks = &cur
				if prev.Ticks != nil {
					if p, good := BusyPercent(*prev.Ticks, cur); good {
						return capped(p), HowCPU, next, true
					}
				}
			}
		}
	case src.Top != nil:
		if !prev.TopAt.IsZero() && now.Sub(prev.TopAt) >= 0 && now.Sub(prev.TopAt) < TopEvery {
			return capped(prev.TopPct), HowCPU, next, true
		}
		if text, err := src.Top(); err == nil {
			if p, good := ParseTopCPU(text); good {
				next.TopAt, next.TopPct = now, p
				return capped(p), HowCPU, next, true
			}
		}
	}
	if src.Load1 != nil {
		if l, good := src.Load1(); good {
			return LoadPercent(l, src.NCPU), HowLoad1, next, true
		}
	}
	return 0, "", next, false
}

// LoadPercent is a load average over the logical cores as a percent, capped
// at MaxPercent; ncpu zero or less is runtime.NumCPU.
func LoadPercent(load1 float64, ncpu int) float64 {
	if ncpu <= 0 {
		ncpu = runtime.NumCPU()
	}
	return capped(load1 / float64(ncpu) * 100)
}

func capped(p float64) float64 {
	switch {
	case math.IsNaN(p) || p < 0:
		return 0
	case p > MaxPercent:
		return MaxPercent
	}
	return p
}

// BusyPercent is the busy percent between two readings of the counters:
// false when the counters did not move forward (the same reading, or a
// reboot between them).
func BusyPercent(prev, cur Ticks) (float64, bool) {
	if cur.Total <= prev.Total || cur.Busy < prev.Busy {
		return 0, false
	}
	return float64(cur.Busy-prev.Busy) / float64(cur.Total-prev.Total) * 100, true
}

// ParseProcStat is the cpu line of /proc/stat: user nice system idle iowait
// irq softirq steal ...; busy is every tick but idle and iowait.
func ParseProcStat(s string) (Ticks, bool) {
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		var vals []uint64
		for _, x := range f[1:] {
			v, err := strconv.ParseUint(x, 10, 64)
			if err != nil {
				return Ticks{}, false
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
		return Ticks{Busy: total - idle, Total: total}, true
	}
	return Ticks{}, false
}

// ParseTopCPU is darwin top's "CPU usage: a% user, b% sys, c% idle" line;
// busy is 100 minus idle.
func ParseTopCPU(s string) (float64, bool) {
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "CPU usage:") {
			continue
		}
		for _, part := range strings.Split(strings.TrimPrefix(line, "CPU usage:"), ",") {
			f := strings.Fields(part)
			if len(f) == 2 && f[1] == "idle" {
				idle, good := topPercent(f[0])
				if !good || idle > 100 {
					return 0, false
				}
				return 100 - idle, true
			}
		}
	}
	return 0, false
}

// topPercent is one of top's percents, "86.3%": top prints the hundredths as a
// bare integer, so a leading zero is lost: 86.3 is 86.03, and 86.30 prints as
// 86.30. The digits after the point are always the hundredths.
func topPercent(w string) (float64, bool) {
	whole, frac, _ := strings.Cut(strings.TrimSuffix(w, "%"), ".")
	n, err := strconv.ParseUint(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	v := float64(n)
	if frac != "" {
		h, err := strconv.ParseUint(frac, 10, 64)
		if err != nil || len(frac) > 2 {
			return 0, false
		}
		v += float64(h) / 100
	}
	return v, true
}

package hostload

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func near(a, b float64) bool { return math.Abs(a-b) < 0.05 }

// TestProcStatBusyPercent: two /proc/stat readings give busy over total ticks
// as a percent; one reading alone gives nothing; counters that went back (a
// reboot) give nothing.
func TestProcStatBusyPercent(t *testing.T) {
	t.Parallel()
	a, ok := ParseProcStat("cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 1 2 3 4 5 6 7 8 9 10\n")
	require.True(t, ok && a.Busy == 200 && a.Total == 1000, "reading a = %+v %v", a, ok)
	b, _ := ParseProcStat("cpu  400 0 200 900 100 0 0 0 0 0\n")
	// busy 200 -> 600 (+400) over total 1000 -> 1600 (+600): 66.7%
	pct, ok := BusyPercent(a, b)
	require.True(t, ok && near(pct, 66.67), "busy = %v %v, want 66.7", pct, ok)
	_, ok = BusyPercent(a, a)
	require.False(t, ok, "the same reading twice has no interval and must not report")
	_, ok = BusyPercent(b, a)
	require.False(t, ok, "counters that went back must not report")
	_, ok = ParseProcStat("intr 1 2 3\n")
	require.False(t, ok, "no cpu line must not report")
	_, ok = ParseProcStat("cpu  1 2 x 4 5\n")
	require.False(t, ok, "a cpu line with a word that is not a number must not report")
}

// TestTopCPULine: darwin's top line gives 100 minus idle.
func TestTopCPULine(t *testing.T) {
	t.Parallel()
	pct, ok := ParseTopCPU("Processes: 900 total\nCPU usage: 13.22% user, 37.47% sys, 49.31% idle\nSharedLibs: 1M\n")
	require.True(t, ok, "top busy = %v %v, want 50.69", pct, ok)
	require.True(t, near(pct, 50.69), "top busy = %v %v, want 50.69", pct, ok)
	_, ok = ParseTopCPU("no usage line\n")
	require.False(t, ok, "a missing line must not report")
	_, ok = ParseTopCPU("CPU usage: 1% user, 2% sys, x% idle\n")
	require.False(t, ok, "an idle that is not a number must not report")
}

// TestProcLoadavg: Linux's /proc/loadavg gives its first field.
func TestProcLoadavg(t *testing.T) {
	t.Parallel()
	v, ok := ParseProcLoadavg("0.52 0.58 0.59 1/389 12345\n")
	require.True(t, ok && v == 0.52, "load1 = %v %v, want 0.52", v, ok)
	v, ok = ParseProcLoadavg("21.07 19.50 18.00 30/2000 999\n")
	require.True(t, ok && v == 21.07, "load1 = %v %v, want 21.07", v, ok)
	for _, bad := range []string{"", "  \n", "x 1 2", "-1 0 0"} {
		_, ok := ParseProcLoadavg(bad)
		require.False(t, ok, "%q must not report", bad)
	}
}

// vmLoadavg is darwin's struct loadavg as sysctl returns it: three fixed-point
// averages and the scale, with its trailing NULs cut as syscall.Sysctl cuts
// them.
func vmLoadavg(scale uint64, avgs ...float64) []byte {
	b := make([]byte, 24)
	for i, a := range avgs {
		binary.LittleEndian.PutUint32(b[4*i:], uint32(a*float64(scale)))
	}
	binary.LittleEndian.PutUint64(b[16:], scale)
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return b
}

// TestVMLoadavg: darwin's vm.loadavg gives the first average over the scale,
// with the reply's cut trailing NULs padded back.
func TestVMLoadavg(t *testing.T) {
	t.Parallel()
	v, ok := ParseVMLoadavg(vmLoadavg(2048, 17.36, 21.31, 19.56))
	require.True(t, ok && near(v, 17.36), "load1 = %v %v, want 17.36", v, ok)
	v, ok = ParseVMLoadavg(vmLoadavg(2048, 1.5))
	require.True(t, ok && v == 1.5, "load1 = %v %v, want 1.5", v, ok)
	_, ok = ParseVMLoadavg(nil)
	require.False(t, ok, "an empty reply has no scale and must not report")
	_, ok = ParseVMLoadavg(make([]byte, 25))
	require.False(t, ok, "a reply longer than the struct must not report")
}

// TestMeasureLinux: the first reading has no interval, so it falls back to the
// load average over the cores; the next gives the CPU busy percent between the
// two readings.
func TestMeasureLinux(t *testing.T) {
	t.Parallel()
	stat := "cpu  100 0 100 700 100 0 0 0 0 0\n"
	src := Source{NCPU: 8,
		ProcStat: func() (string, error) { return stat, nil },
		Load1:    func() (float64, bool) { return 2, true }}
	pct, how, st, ok := Measure(src, State{}, t0)
	require.True(t, ok && how == HowLoad1 && pct == 25 && st.Ticks != nil && st.Ticks.Total == 1000, "first = %v %s %+v %v, want 25%% by load1 and the counters kept", pct, how, st, ok)
	stat = "cpu  400 0 200 900 100 0 0 0 0 0\n"
	pct, how, st, ok = Measure(src, st, t0.Add(time.Second))
	require.True(t, ok && how == HowCPU && near(pct, 66.67) && st.Ticks.Total == 1600, "second = %v %s %+v %v, want 66.7%% by cpu", pct, how, st, ok)
}

// TestMeasureDarwin: top runs at most once every TopEvery; between runs the
// last reading stands; a top that fails falls back to the load average.
func TestMeasureDarwin(t *testing.T) {
	t.Parallel()
	runs := 0
	line := "CPU usage: 10.00% user, 20.00% sys, 70.00% idle\n"
	src := Source{NCPU: 4,
		Top:   func() (string, error) { runs++; return line, nil },
		Load1: func() (float64, bool) { return 6, true }}
	pct, how, st, ok := Measure(src, State{}, t0)
	require.True(t, ok && how == HowCPU && near(pct, 30) && runs == 1, "first = %v %s %v runs=%d, want 30%% by cpu from one top", pct, how, ok, runs)
	line = "CPU usage: 50.00% user, 40.00% sys, 10.00% idle\n"
	pct, _, st, _ = Measure(src, st, t0.Add(TopEvery-time.Second))
	require.True(t, near(pct, 30), "within TopEvery = %v runs=%d, want the last reading and no new top", pct, runs)
	require.Equal(t, 1, runs, "within TopEvery = %v runs=%d, want the last reading and no new top", pct, runs)
	pct, _, st, _ = Measure(src, st, t0.Add(TopEvery))
	require.True(t, near(pct, 90), "after TopEvery = %v runs=%d, want 90%% from a second top", pct, runs)
	require.Equal(t, 2, runs, "after TopEvery = %v runs=%d, want 90%% from a second top", pct, runs)
	src.Top = func() (string, error) { return "", errors.New("no top") }
	pct, how, _, ok = Measure(src, st, t0.Add(3*TopEvery))
	require.True(t, ok && how == HowLoad1 && pct == 150, "failed top = %v %s %v, want 150%% by load1", pct, how, ok)
}

// TestMeasureCapsAndNothing: a load average far past the cores is capped; a
// machine with no reading measures nothing.
func TestMeasureCapsAndNothing(t *testing.T) {
	t.Parallel()
	src := Source{NCPU: 1, Load1: func() (float64, bool) { return 500, true }}
	pct, _, _, ok := Measure(src, State{}, t0)
	require.True(t, ok && pct == MaxPercent, "pct = %v %v, want the cap %v", pct, ok, MaxPercent)
	_, _, _, ok = Measure(Source{}, State{}, t0)
	require.False(t, ok, "a source with no reading must measure nothing")
}

// TestLocalMeasures: this machine, on Linux and darwin, measures a percent in
// range (the first reading on Linux is the load average).
func TestLocalMeasures(t *testing.T) {
	t.Parallel()
	src := Local()
	if src.ProcStat == nil && src.Top == nil && src.Load1 == nil {
		t.Skip("this platform reads nothing")
	}
	// top is a process; the test reads the load average alone.
	src.Top, src.ProcStat = nil, nil
	pct, how, _, ok := Measure(src, State{}, time.Now())
	require.True(t, ok && how == HowLoad1 && pct >= 0 && pct <= MaxPercent, "local = %v %s %v", pct, how, ok)
}

package life

import "testing"

// TestProcStatBusyPercent: two /proc/stat samples give busy over total ticks
// as a percent; the first sample alone gives nothing.
func TestProcStatBusyPercent(t *testing.T) {
	t.Parallel()
	a := parseProcStat("cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 1 2 3 4 5 6 7 8 9 10\n")
	b := parseProcStat("cpu  400 0 200 900 100 0 0 0 0 0\n")
	if !a.ok || a.busy != 200 || a.total != 1000 {
		t.Fatalf("sample a = %+v", a)
	}
	if _, ok := cpuBusy(cpuSample{}, a); ok {
		t.Fatal("a first sample has no interval and must not report")
	}
	// busy 200 -> 600 (+400) over total 1000 -> 1600 (+600): 66.7%
	pct, ok := cpuBusy(a, b)
	if !ok || pct < 66.6 || pct > 66.7 {
		t.Fatalf("busy = %v %v, want 66.7", pct, ok)
	}
	if s := parseProcStat("intr 1 2 3\n"); s.ok {
		t.Fatal("no cpu line must not report")
	}
}

// TestTopCPULine: darwin's top line gives 100 minus idle.
func TestTopCPULine(t *testing.T) {
	t.Parallel()
	s := parseTopCPU("Processes: 900 total\nCPU usage: 13.22% user, 37.47% sys, 49.31% idle\nSharedLibs: 1M\n")
	pct, ok := cpuBusy(cpuSample{}, s)
	if !s.ok || !ok || pct < 50.68 || pct > 50.70 {
		t.Fatalf("top busy = %v %v (%+v), want 50.69", pct, ok, s)
	}
	if parseTopCPU("no usage line\n").ok {
		t.Fatal("a missing line must not report")
	}
}

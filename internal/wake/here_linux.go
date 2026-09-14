//go:build linux

package wake

import "syscall"

// sysinfo(2) returns procs BESIDE the load averages -- the same call that
// already answers load=, one struct, no path opened and no directory read.
//
// It is the kernel's nr_threads, a TASK count and not a process count, so a
// Linux procs= and a Darwin procs= are different numbers about the same bench
// and must not be compared across platforms. Each is a load number read against
// its own cpus=, which is all rule 18 asks of it.
func sysinfoOnce() (*syscall.Sysinfo_t, bool) {
	var si syscall.Sysinfo_t
	if err := syscall.Sysinfo(&si); err != nil {
		return nil, false
	}
	return &si, true
}

func loadAverage() ([3]float64, bool) {
	var out [3]float64
	si, ok := sysinfoOnce()
	if !ok {
		return out, false
	}
	const scale = 65536.0
	for i := 0; i < 3; i++ {
		out[i] = float64(si.Loads[i]) / scale
	}
	return out, true
}

func processCount() (int, bool) {
	si, ok := sysinfoOnce()
	if !ok {
		return 0, false
	}
	return int(si.Procs), true
}

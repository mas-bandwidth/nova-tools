//go:build !darwin && !linux

package life

// loadavg1 cannot measure here; the host row's load prints "-".
func loadavg1() (float64, bool) { return 0, false }

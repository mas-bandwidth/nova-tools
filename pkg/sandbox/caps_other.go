//go:build !linux && !darwin

package sandbox

// machineRAM is unknown here: the caps are SizedCaps with no memory bound of the machine.
func machineRAM() int64 { return 0 }

// machineSlice is unknown here: no cgroup limits.
func machineSlice() (int64, int) { return 0, 0 }

package sandbox

import "golang.org/x/sys/unix"

// machineRAM is the hw.memsize sysctl in bytes, 0 when it cannot be read.
func machineRAM() int64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil || n > 1<<62 {
		return 0
	}
	return int64(n)
}

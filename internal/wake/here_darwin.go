//go:build darwin

package wake

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

// kinfoProcSize is sizeof(kinfo_proc) on 64-bit Darwin. It is in no
// standard-library type, so it is a NAMED CONSTANT here -- because a test
// cannot be written from words that leave it unstated.
const kinfoProcSize = 648

// loadAverage reads vm.loadavg: three fixed-point values and the scale they are
// fixed against.
func loadAverage() ([3]float64, bool) {
	var out [3]float64
	raw, err := syscall.Sysctl("vm.loadavg")
	if err != nil || len(raw) < 20 {
		return out, false
	}
	b := []byte(raw)
	scale := float64(binary.LittleEndian.Uint32(b[16:20]))
	if scale == 0 {
		return out, false
	}
	for i := 0; i < 3; i++ {
		out[i] = float64(binary.LittleEndian.Uint32(b[i*4:i*4+4])) / scale
	}
	return out, true
}

// processCount asks sysctl for kern.proc.all WITH A NIL BUFFER, which returns
// the byte length the answer WOULD need and nothing else; the count is that
// length divided by sizeof(kinfo_proc). No entry is fetched.
//
// The kernel's answer to a nil buffer is an ESTIMATE carrying room for
// processes that may start before the real read, so the count may run a little
// high. That is the reading and not a defect: procs= is a load number, the
// estimate errs in the direction of BUSIER, and a window deciding whether this
// bench is quiet is better served by the high number than the low one.
func processCount() (int, bool) {
	mib := [3]int32{1, 14, 0} // CTL_KERN, KERN_PROC, KERN_PROC_ALL
	var n uintptr
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), 3, 0, uintptr(unsafe.Pointer(&n)), 0, 0)
	if errno != 0 || n == 0 {
		return 0, false
	}
	return int(n) / kinfoProcSize, true
}

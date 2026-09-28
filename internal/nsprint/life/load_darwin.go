package life

import (
	"encoding/binary"
	"syscall"
)

// loadavg1 is the one-minute load from sysctl vm.loadavg: struct loadavg
// {uint32 ldavg[3]; long fscale}, 24 bytes on 64-bit Darwin. syscall.Sysctl
// drops one trailing NUL, so the reply is padded back to 24 bytes.
func loadavg1() (float64, bool) {
	s, err := syscall.Sysctl("vm.loadavg")
	if err != nil {
		return 0, false
	}
	b := []byte(s)
	for len(b) < 24 {
		b = append(b, 0)
	}
	scale := binary.LittleEndian.Uint64(b[16:24])
	if scale == 0 {
		return 0, false
	}
	return float64(binary.LittleEndian.Uint32(b[0:4])) / float64(scale), true
}

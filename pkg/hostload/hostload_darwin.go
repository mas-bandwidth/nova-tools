//go:build darwin

package hostload

// ParseVMLoadavg is the one-minute load average from darwin's vm.loadavg,
// struct loadavg {uint32 ldavg[3]; long fscale}: 24 bytes on 64-bit darwin,
// little-endian. The sysctl reply loses trailing NULs, so a short reply is
// padded back to 24 bytes.
func ParseVMLoadavg(b []byte) (float64, bool) {
	if len(b) > 24 {
		return 0, false
	}
	buf := make([]byte, 24)
	copy(buf, b)
	le32 := func(p []byte) uint64 {
		return uint64(p[0]) | uint64(p[1])<<8 | uint64(p[2])<<16 | uint64(p[3])<<24
	}
	scale := le32(buf[16:20]) | le32(buf[20:24])<<32
	if scale == 0 {
		return 0, false
	}
	return float64(le32(buf[0:4])) / float64(scale), true
}

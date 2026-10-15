package main

import (
	"encoding/binary"
	"syscall"
)

func localBox() Box {
	return Box{
		Load1: func() (float64, bool) {
			s, err := syscall.Sysctl("vm.loadavg")
			if err != nil {
				return 0, false
			}
			return parseDarwinLoadavg(s)
		},
		Mem: func() (uint64, uint64, bool) {
			total, ok1 := sysctlUint("hw.memsize")
			pages, ok2 := sysctlUint("vm.page_free_count")
			size, ok3 := sysctlUint("hw.pagesize")
			return pages * size, total, ok1 && ok2 && ok3
		},
		WiredCap: func() (uint64, bool) {
			mib, ok := sysctlUint("iogpu.wired_limit_mb")
			return mib, ok && mib > 0
		},
	}
}

// parseDarwinLoadavg is the one-minute load from the vm.loadavg sysctl, a struct loadavg:
// three uint32 ldavg, four bytes of padding, then a 64-bit fscale, little-endian, 24 bytes.
// syscall.Sysctl returns the raw bytes as a string with one trailing zero byte cut, so a
// 23-byte value is padded back; load1 is ldavg[0] / fscale.
func parseDarwinLoadavg(s string) (float64, bool) {
	const size = 24
	if len(s) != size && len(s) != size-1 {
		return 0, false
	}
	var b [size]byte
	copy(b[:], s)
	fscale := binary.LittleEndian.Uint64(b[16:])
	if fscale == 0 {
		return 0, false
	}
	return float64(binary.LittleEndian.Uint32(b[0:])) / float64(fscale), true
}

// sysctlUint reads an integer sysctl. syscall.Sysctl hands back the value's raw
// little-endian bytes with one trailing zero byte dropped, so the bytes are padded back.
func sysctlUint(name string) (uint64, bool) {
	s, err := syscall.Sysctl(name)
	if err != nil || len(s) > 8 {
		return 0, false
	}
	var b [8]byte
	copy(b[:], s)
	return binary.LittleEndian.Uint64(b[:]), true
}

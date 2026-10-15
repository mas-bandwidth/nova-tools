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

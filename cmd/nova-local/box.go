package main

import (
	"encoding/binary"
	"strconv"
	"strings"
)

// Box is the machine's facts serve and status print, read at the moment of the call and
// never cached (rules 8 and 10): the one-minute load average, the free and total memory
// in bytes, and the live GPU wired-memory cap in MiB where the platform has one. A nil
// function, or ok false, is a fact this machine does not report; a test supplies its own.
type Box struct {
	Load1    func() (float64, bool)
	Mem      func() (free, total uint64, ok bool)
	WiredCap func() (mib uint64, ok bool)
}

// LocalBox is this machine's box.
func LocalBox() Box { return localBox() }

// parseProcLoadavg is the one-minute load from the bytes of /proc/loadavg, whose first
// field is load1. A missing, malformed or negative field is not a reading.
func parseProcLoadavg(b []byte) (float64, bool) {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil || v < 0 || v != v {
		return 0, false
	}
	return v, true
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

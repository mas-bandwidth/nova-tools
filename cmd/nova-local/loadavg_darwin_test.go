//go:build darwin

package main

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
)

// darwinLoadavg builds the bytes of struct loadavg as the kernel returns them: three
// uint32 ldavg, four bytes of padding, then a 64-bit fscale, little-endian.
func darwinLoadavg(l1, l2, l3 uint32, fscale uint64) string {
	b := make([]byte, 24)
	binary.LittleEndian.PutUint32(b[0:], l1)
	binary.LittleEndian.PutUint32(b[4:], l2)
	binary.LittleEndian.PutUint32(b[8:], l3)
	binary.LittleEndian.PutUint64(b[16:], fscale)
	return string(b)
}

func TestLoadAvgParses(t *testing.T) {
	t.Parallel()

	full := darwinLoadavg(2048, 1024, 512, 2048) // load1 1.0
	cut := full[:23]                             // syscall.Sysctl drops one trailing NUL; fscale's top byte is zero
	darwin := []struct {
		name string
		in   string
		want float64
		ok   bool
	}{
		{"full 24 bytes", full, 1.0, true},
		{"last byte cut", cut, 1.0, true},
		{"fraction", darwinLoadavg(3072, 0, 0, 2048), 1.5, true},
		{"zero load", darwinLoadavg(0, 0, 0, 2048), 0, true},
		{"zero fscale", darwinLoadavg(2048, 0, 0, 0), 0, false},
		{"too short", full[:12], 0, false},
		{"too long", full + "x", 0, false},
		{"empty", "", 0, false},
	}
	for _, c := range darwin {
		got, ok := parseDarwinLoadavg(c.in)
		assert.Equal(t, c.ok, ok, "darwin %s", c.name)
		assert.Equal(t, c.want, got, "darwin %s", c.name)
	}
}

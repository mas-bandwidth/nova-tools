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

	linux := []struct {
		name string
		in   string
		want float64
		ok   bool
	}{
		{"typical", "0.52 0.58 0.59 1/467 12345\n", 0.52, true},
		{"busy", "12.75 3.00 1.00 9/1000 1\n", 12.75, true},
		{"no newline", "0.01 0.02 0.03", 0.01, true},
		{"empty", "", 0, false},
		{"not a number", "abc 1 2 3/4 5\n", 0, false},
		{"negative", "-1.0 0 0 1/1 1\n", 0, false},
	}
	for _, c := range linux {
		got, ok := parseProcLoadavg([]byte(c.in))
		assert.Equal(t, c.ok, ok, "linux %s", c.name)
		assert.Equal(t, c.want, got, "linux %s", c.name)
	}

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

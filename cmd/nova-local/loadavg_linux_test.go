//go:build linux

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
}

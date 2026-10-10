package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBenchsizeCoverVersion8 pins Version8: the main path truncates a string
// longer than eight characters to its first eight, and the refusal path
// returns a string of eight or fewer characters unchanged. The function is a
// pure length gate on the tool's identity; there is no store, clock, socket or
// subprocess to fake.
func TestBenchsizeCoverVersion8(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"long truncates to eight", "novatoolse", "novatool"},
		{"exactly eight unchanged", "novatool", "novatool"},
		{"short unchanged", "nova", "nova"},
		{"empty unchanged", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Version8(tc.in))
		})
	}
}

//go:build linux

package hostload

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProcLoadavg: Linux's /proc/loadavg gives its first field.
func TestProcLoadavg(t *testing.T) {
	t.Parallel()
	v, ok := ParseProcLoadavg("0.52 0.58 0.59 1/389 12345\n")
	require.True(t, ok && v == 0.52, "load1 = %v %v, want 0.52", v, ok)
	v, ok = ParseProcLoadavg("21.07 19.50 18.00 30/2000 999\n")
	require.True(t, ok && v == 21.07, "load1 = %v %v, want 21.07", v, ok)
	for _, bad := range []string{"", "  \n", "x 1 2", "-1 0 0"} {
		_, ok := ParseProcLoadavg(bad)
		require.False(t, ok, "%q must not report", bad)
	}
}

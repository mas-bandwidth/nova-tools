//go:build darwin

package hostload

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// vmLoadavg is darwin's struct loadavg as sysctl returns it: three fixed-point
// averages and the scale, with its trailing NULs cut as syscall.Sysctl cuts
// them.
func vmLoadavg(scale uint64, avgs ...float64) []byte {
	b := make([]byte, 24)
	for i, a := range avgs {
		binary.LittleEndian.PutUint32(b[4*i:], uint32(a*float64(scale)))
	}
	binary.LittleEndian.PutUint64(b[16:], scale)
	for len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return b
}

// TestVMLoadavg: darwin's vm.loadavg gives the first average over the scale,
// with the reply's cut trailing NULs padded back.
func TestVMLoadavg(t *testing.T) {
	t.Parallel()
	v, ok := ParseVMLoadavg(vmLoadavg(2048, 17.36, 21.31, 19.56))
	require.True(t, ok && near(v, 17.36), "load1 = %v %v, want 17.36", v, ok)
	v, ok = ParseVMLoadavg(vmLoadavg(2048, 1.5))
	require.True(t, ok && v == 1.5, "load1 = %v %v, want 1.5", v, ok)
	_, ok = ParseVMLoadavg(nil)
	require.False(t, ok, "an empty reply has no scale and must not report")
	_, ok = ParseVMLoadavg(make([]byte, 25))
	require.False(t, ok, "a reply longer than the struct must not report")
}

// TestIostatSecond: darwin's iostat -c 2 prints the average since boot and then the
// second; busy is 100 minus the second's idle.
func TestIostatSecond(t *testing.T) {
	t.Parallel()
	out := "      cpu    load average\n us sy id   1m   5m   15m\n 12 20 68  8.18 8.05 7.81\n  3  6 90  8.18 8.05 7.81\n"
	pct, ok := ParseIostat(out)
	require.True(t, ok && pct == 10, "iostat busy = %v %v, want 10", pct, ok)
	_, ok = ParseIostat("      cpu    load average\n us sy id   1m   5m   15m\n 12 20 68  8.18 8.05 7.81\n")
	require.False(t, ok, "the since-boot line alone is not a second")
	_, ok = ParseIostat("")
	require.False(t, ok, "no output must not report")
}

// TestIostatIdleOneHundred: iostat prints each of us, sy and id %3.0f, so an idle of 100
// is "  0  0100" and the row does not split on spaces; it is a second 0% busy, not a
// failed reading.
func TestIostatIdleOneHundred(t *testing.T) {
	t.Parallel()
	head := "      cpu    load average\n us sy id   1m   5m   15m\n 12 20 68  8.18 8.05 7.81\n"
	pct, ok := ParseIostat(head + "  0  0100  8.18 8.05 7.81\n")
	require.True(t, ok && pct == 0, "idle 100 = %v %v, want 0", pct, ok)
	pct, ok = ParseIostat(head + "  0  1 99  8.18 8.05 7.81\n")
	require.True(t, ok && pct == 1, "idle 99 = %v %v, want 1", pct, ok)
	pct, ok = ParseIostat(head + "100  0  0  8.18 8.05 7.81\n")
	require.True(t, ok && pct == 100, "idle 0 = %v %v, want 100", pct, ok)
	pct, ok = ParseIostat("12 20 68 8.18\n3 6 90 8.18\n")
	require.True(t, ok && pct == 10, "a row split on spaces = %v %v, want 10", pct, ok)
}

// Both spaced and packed columns preserve the entire idle value. The spaced
// three-digit value must not be truncated to its first two digits.
func TestIostatColumnLayouts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, row string
		busy      float64
	}{
		{"spaced idle hundred", "  0  0 100  8.18 8.05 7.81", 0},
		{"spaced idle ninety eight", "  1  1  98  8.18 8.05 7.81", 2},
		{"wide spaced idle hundred", "   0   0 100  8.18 8.05 7.81", 0},
		{"packed idle hundred", "  0  0100  8.18 8.05 7.81", 0},
		{"packed system hundred", "  0100  0  8.18 8.05 7.81", 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			busy, ok := ParseIostat("      cpu    load average\n us sy id   1m   5m   15m\n 12 20 68  8.18 8.05 7.81\n" + tc.row + "\n")
			require.True(t, ok)
			require.Equal(t, tc.busy, busy)
		})
	}
}

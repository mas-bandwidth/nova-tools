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
	require.True(t, ok, "load1 = %v %v, want 17.36", v, ok)
	require.True(t, near(v, 17.36), "load1 = %v %v, want 17.36", v, ok)
	v, ok = ParseVMLoadavg(vmLoadavg(2048, 1.5))
	require.True(t, ok, "load1 = %v %v, want 1.5", v, ok)
	require.Equal(t, 1.5, v, "load1 = %v %v, want 1.5", v, ok)
	_, ok = ParseVMLoadavg(nil)
	require.False(t, ok, "an empty reply has no scale and must not report")
	_, ok = ParseVMLoadavg(make([]byte, 25))
	require.False(t, ok, "a reply longer than the struct must not report")
}

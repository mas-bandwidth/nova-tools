package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMemTotalReadsTheKernelsKilobytes: MemTotal is kB, the answer bytes; no line is 0.
func TestMemTotalReadsTheKernelsKilobytes(t *testing.T) {
	t.Parallel()
	assert.Equal(t, int64(32617624)<<10, memTotal("MemTotal:       32617624 kB\nMemFree:  100 kB\n"))
	assert.Equal(t, int64(0), memTotal("MemFree:  100 kB\n"))
	assert.Equal(t, int64(0), memTotal("MemTotal: lots kB\n"))
}

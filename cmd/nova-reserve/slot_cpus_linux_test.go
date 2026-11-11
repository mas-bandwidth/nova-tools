package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsolatedCPUsAreNotUsable(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		allowed  []int
		isolated string
		want     string
	}{
		{"none isolated", span(0, 15), "\n", "0-15"},
		{"empty file", span(0, 15), "", "0-15"},
		{"1-15 of 64", span(0, 63), "1-15\n", "0,16-63"},
		{"a list", span(0, 5), "1,3-4\n", "0,2,5"},
		{"isolated outside the allowed set", append(span(0, 3), span(8, 11)...), "4-7", "0-3,8-11"},
	} {
		usable, err := withoutIsolated(c.allowed, c.isolated)
		require.NoError(t, err, c.name)
		assert.Equal(t, c.want, cpuList(usable), c.name)
	}
	_, err := withoutIsolated(span(0, 3), "0-3\n")
	assert.EqualError(t, err, "every CPU this process may use (0-3) is isolated")
	_, err = withoutIsolated(span(0, 3), "1-x\n")
	assert.EqualError(t, err, `/sys/devices/system/cpu/isolated holds "1-x", not a CPU list`)
}

// TestHostWithIsolatedCPUsSharesTheRest: 64 CPUs with 1-15 isolated, 8 slots.
func TestHostWithIsolatedCPUsSharesTheRest(t *testing.T) {
	t.Parallel()
	usable, err := withoutIsolated(span(0, 63), "1-15\n")
	require.NoError(t, err)
	var got []string
	for k := 1; k <= 8; k++ {
		got = append(got, cpuList(share(usable, 8, k)))
	}
	assert.Equal(t, []string{"0,16-20", "21-26", "27-32", "33-38", "39-44", "45-50", "51-56", "57-63"}, got)
}

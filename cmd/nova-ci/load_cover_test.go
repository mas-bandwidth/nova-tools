package main

import (
	"errors"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// load_cover_test.go reaches hostLoad, the one function of load.go the unit
// tier left at 0.0%: its main path on this host is the /proc/loadavg read, a
// plain file, so no subprocess, network or store stands in the way. A refusal
// of hostLoad itself is not reachable in a unit test here -- the read cannot
// be injected, the darwin refusal needs the sysctl subprocess and windows a
// GOOS hostLoad does not take -- so the refusal is covered through the seam
// hostLoad is built on, loadFrom's read argument.

// TestLoadCoverHostLoadMeasuresThisHost pins hostLoad's main path: the load it
// returns is known, never negative, over the machine's logical CPUs, with no
// reason, and its CI-LOAD line is the known shape over those CPUs.
func TestLoadCoverHostLoadMeasuresThisHost(t *testing.T) {
	t.Parallel()

	got := hostLoad()
	require.True(t, got.Known, "hostLoad = %+v, want known: this host has a load average", got)
	assert.Equal(t, runtime.NumCPU(), got.CPUs, "hostLoad = %+v, want the machine's logical CPUs", got)
	assert.GreaterOrEqual(t, got.Avg, 0.0, "hostLoad = %+v, want a load average, never negative", got)
	assert.Empty(t, got.Why, "hostLoad = %+v, want no reason when the load is known", got)
	assert.Equal(t, got.Avg/float64(got.CPUs), got.PerCPU(), "hostLoad = %+v, want the per-cpu figure over NumCPU", got)
	assert.Contains(t, got.LoadLine(), "CI-LOAD load=", "hostLoad = %+v, want the CI-LOAD line a run prints: %q", got, got.LoadLine())
	assert.Contains(t, got.LoadLine(), "cpus="+strconv.Itoa(runtime.NumCPU()), "hostLoad = %+v, want the CPU count in the line: %q", got, got.LoadLine())
}

// TestLoadCoverARefusedLoadIsUnknownWithItsReason pins the refusal hostLoad
// reaches through its seam: a read that fails, or a figure that does not
// parse, is unknown with the reason and the CPU count carried, and the CI-LOAD
// line says so. The unparsable and negative figures reached no test before
// this file; the failed read repeats the one shape through one table.
func TestLoadCoverARefusedLoadIsUnknownWithItsReason(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		raw  string
		err  error
		want string
	}{
		{name: "a failed read", raw: "", err: errors.New("sysctl -n vm.loadavg: the read failed"), want: "the read failed"},
		{name: "a figure that does not parse", raw: "{ 1.0 x 0.5 }", want: `"x" is not a load`},
		{name: "a negative figure", raw: "-1.5 -2.5", want: `"-1.5" is not a load`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := loadFrom("linux", 4, func(string) (string, error) { return tc.raw, tc.err })
			assert.False(t, got.Known, "%s: got %+v, want unknown with its reason", tc.name, got)
			assert.Equal(t, 4, got.CPUs, "%s: got %+v, want the CPU count carried", tc.name, got)
			assert.Contains(t, got.Why, tc.want, "%s: got %+v, want the reason", tc.name, got)
			assert.Contains(t, got.LoadLine(), "load=unknown", "%s: got %q, want the line that says so", tc.name, got.LoadLine())
		})
	}
}

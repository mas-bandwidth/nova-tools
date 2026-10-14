package up_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSeatExampleUsesTheBusPortAndARefusedDryRunHasNoMayHaveWritten pins
// docs/SPEC-UP.md: the --seat example teaches the bus on its own port (the
// house runs the sprint store on 6380 and the bus on 6381), and a refused
// --seat --dry-run is a dry run, so the skeleton never appends its
// "may have written" diagnostic.
func TestSeatExampleUsesTheBusPortAndARefusedDryRunHasNoMayHaveWritten(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	code, out, errs := f.nova("linux", "-h")
	require.Equal(t, 0, code, out+errs)
	assert.Contains(t, out+errs, "--bus-redis 127.0.0.1:6381")
	assert.NotContains(t, out+errs, "--bus-redis 127.0.0.1:6380")

	getenv := func(string) string { return "" }
	code, out, errs = newFake(t).novaWithEnv("linux", getenv, "--seat", "--dry-run")
	require.Equal(t, 2, code, out+errs)
	assert.NotContains(t, out+errs, "may have written")
}

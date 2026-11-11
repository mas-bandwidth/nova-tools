//go:build functional

package main

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFunctionalRunPinsTheSlotsCPUs: the command sees only its slot's share of
// the CPUs this test may use, and an exclusive one sees all of them.
func TestFunctionalRunPinsTheSlotsCPUs(t *testing.T) {
	t.Parallel()
	usable, err := usableCPUs()
	require.NoError(t, err)
	bin, dir := build(t), t.TempDir()
	out, err := exec.Command(bin, "set", "--slots", "2", "--dir", dir).CombinedOutput()
	require.NoError(t, err, "%s", out)
	for _, c := range []struct {
		args []string
		want []int
		held string
	}{
		{nil, share(usable, 2, 1), "HELD mode=shared slot=1/2 pid="},
		{[]string{"--exclusive"}, usable, "HELD mode=exclusive slot=all pid="},
	} {
		cmd := slotCmd(t, bin, dir, "cpus", c.args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		require.NoError(t, err, "%s", stderr.String())
		assert.Equal(t, strconv.Itoa(len(c.want)), strings.TrimSpace(string(stdout)), c.held)
		assert.Contains(t, stderr.String(), c.held)
		assert.Contains(t, stderr.String(), " cpus="+cpuList(c.want)+"\n")
		t.Log(strings.TrimSpace(stderr.String()), "; the command counts", strings.TrimSpace(string(stdout)), "CPUs")
	}
}

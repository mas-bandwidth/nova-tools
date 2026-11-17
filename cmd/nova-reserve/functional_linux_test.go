//go:build functional && linux

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

func TestFunctionalRunPinsDisjointCPUsAndSteersEnv(t *testing.T) {
	t.Parallel()
	usable, err := usableCPUs()
	require.NoError(t, err)
	bin, dir := build(t), t.TempDir()
	cmd := exec.Command(bin, "set", "--dir", dir, "--cpu", strconv.Itoa(len(usable)), "--memory", "16", "--cores", strconv.Itoa(len(usable)))
	cmd.Env = cleanChildEnv()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)

	want := max(1, min(2, len(usable)))
	run1 := runChildCmd(t, bin, dir, "cpus", "--needs", "cpu "+strconv.Itoa(want)+", memory 1")
	var stderr bytes.Buffer
	run1.Stderr = &stderr
	stdout, err := run1.Output()
	require.NoError(t, err, "%s", stderr.String())
	wStr := strconv.Itoa(want)
	assert.Equal(t, wStr+" "+wStr+" "+wStr, strings.TrimSpace(string(stdout)))
	assert.Contains(t, stderr.String(), "cpu="+wStr+" memory=1 cores="+wStr+" pid=")
	assert.Contains(t, stderr.String(), " cpus=")

	// Exclusive run gets all usable CPUs.
	stderr.Reset()
	ex := runChildCmd(t, bin, dir, "cpus", "--exclusive")
	ex.Stderr = &stderr
	stdout, err = ex.Output()
	require.NoError(t, err, "%s", stderr.String())
	allStr := strconv.Itoa(len(usable))
	assert.Equal(t, allStr+" "+allStr+" "+allStr, strings.TrimSpace(string(stdout)))
	assert.Contains(t, stderr.String(), " cpus="+cpuList(usable)+"\n")
}

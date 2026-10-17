package main

import (
	"bytes"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBareCapsAreTheFlagsOrTheMachines: the bare form's caps are --max-procs and
// --max-mem as given, and the machine's sized caps for one not given
// (docs/SPEC-SANDBOX.md "wall-caps-processes.w1").
func TestBareCapsAreTheFlagsOrTheMachines(t *testing.T) {
	t.Parallel()
	sizedProcs, sizedMem := sandbox.MachineCaps()
	procs, mem, bad := bareCaps("", "")
	require.Empty(t, bad)
	assert.Equal(t, sizedProcs, procs)
	assert.Equal(t, sizedMem, mem)
	procs, mem, bad = bareCaps("2048", "24g")
	require.Empty(t, bad)
	assert.Equal(t, 2048, procs)
	assert.Equal(t, int64(24)<<30, mem)
	procs, mem, bad = bareCaps("300", "")
	require.Empty(t, bad)
	assert.Equal(t, 300, procs)
	assert.Equal(t, sizedMem, mem, "a cap not given is the machine's")
}

// TestBareCapsRefuseABadValueByName: each bad value is its own refusal, named.
func TestBareCapsRefuseABadValueByName(t *testing.T) {
	t.Parallel()
	_, _, bad := bareCaps("0", "lots")
	require.Len(t, bad, 2)
	assert.Equal(t, "bad_max_procs", bad[0].Reason)
	assert.Equal(t, "bad_max_mem", bad[1].Reason)
}

// TestTheBareFormRefusesABadCapBeforeItRuns: a bad cap refuses the run whole, exit 125,
// and nothing starts.
func TestTheBareFormRefusesABadCapBeforeItRuns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := run([]string{"--write", dir, "--max-procs", "-3", "--", "/bin/sh", "-c", "true"}, nil, &out, &errb, []string{"HOME=" + dir})
	assert.Equal(t, sandbox.ExitRefused, code, errb.String())
	assert.Contains(t, errb.String(), "reason=bad_max_procs")
	assert.NotContains(t, errb.String(), "SANDBOX OK")
}

// TestProbeRefusesTheCapFlags: probe proves a wall, not a tree's caps; the flags are the
// bare form's and run's, and probe names them as such.
func TestProbeRefusesTheCapFlags(t *testing.T) {
	t.Parallel()
	f := parseVerb("probe", []string{"--write", "/tmp", "--max-procs", "4", "--max-mem", "1g"})
	bad := notForThisVerb("probe", "nova-sandbox probe -h", map[string]bool{"--max-procs": f.maxProcs != "", "--max-mem": f.maxMem != ""}, ownerOf)
	require.Len(t, bad, 2)
	assert.Contains(t, bad[0].Text, "--max-procs is not a flag of probe; it is the bare form and run's")
}

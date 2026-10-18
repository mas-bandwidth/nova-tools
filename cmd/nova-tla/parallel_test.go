package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/tlc"
)

// --parallel is the form nova-sprint's own runner had (its Makefile passes
// TLC_PARALLEL through it): independent cases at once, 1 to half the CPUs, a
// quarter of them by default.

func TestRunRunsIndependentCasesInParallelAndRecordsThemInPlanOrder(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	var runs []tlc.Run
	dir := filepath.Join(t.TempDir(), "out")
	r := call(t, scriptedTLC(t, map[string]int{"MCA": 0, "MCABroken": 12, "MCAStale": 13}, &runs), "run", "--root", root, "--jar", jar, "--dir", dir, "--group", "alpha", "--timeout", "100s")
	require.Equal(t, 0, r.code, "the default width (a quarter of the 8 CPUs) ran: %+v", r)
	assert.Empty(t, r.stderr)
	assert.Len(t, runs, 2)
	recs, err := tlc.ReadRecordsFile(filepath.Join(dir, tlc.RunsFile))
	require.NoError(t, err)
	require.Len(t, recs, 2)
	assert.Equal(t, "MCA.cfg", recs[0].Config, "the records are in plan order")
	assert.Equal(t, "MCABroken.cfg", recs[1].Config, "the records are in plan order")
}

func TestRunRefusesAParallelWidthOutsideTheCPUs(t *testing.T) {
	t.Parallel()
	root, jar := checkout(t)
	for _, width := range []string{"5", "-1"} {
		r := call(t, nil, "run", "--root", root, "--jar", jar, "--dir", t.TempDir(), "--group", "alpha", "--parallel", width)
		assert.Equal(t, 2, r.code, "--parallel %s: %+v", width, r)
		assert.Contains(t, r.stderr, "parallel must be between 1 and 4 (half the 8 CPUs)", width)
		assert.Contains(t, r.stderr, "nothing was run", width)
	}
	r := call(t, nil, "run", "--root", root, "--dir", t.TempDir(), "--bench", "bench-a", "--parallel", "2")
	assert.Equal(t, 2, r.code, "--parallel with --bench: %+v", r)
	assert.Contains(t, r.stderr, "--parallel is not taken with --bench")
}

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// scratchDenialChild is a finished read whose run log is the native log of a reader the wall
// refused a shell write to `denied` (rc=0, as the fleet's reads ended on 2026-10-20), and whose
// own RESULT.md says `result` (empty: none).
func scratchDenialChild(t *testing.T, denied, result string) *nativeChild {
	t.Helper()
	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	line := "/usr/bin/bash: line 1: " + denied + ": Permission denied"
	d, ok := swarm.ShellDenied([]byte(line + "\n"))
	require.True(t, ok, "the shell line is a denial")
	log := "STAGE OK bench=b1\nFRAME OK secs=1\nWALL REFUSED denied /usr/bin/bash task=c1.r3.hulk step=-\n" +
		"NATIVE REFUSED: " + swarm.ShellDenialReason("c1.r3.hulk", job, "landlock", 0, d) + "\n"
	write(t, filepath.Join(dir, "c1.native.log"), log)
	if result != "" {
		write(t, filepath.Join(dir, "results", "RESULT.md"), result)
	}
	done := make(chan struct{})
	close(done)
	return &nativeChild{card: "c1", read: true, logPath: filepath.Join(dir, "c1.native.log"), results: filepath.Join(dir, "results"), job: job, done: done}
}

// A READ THAT REACHED ITS VERDICT IS NOT THROWN AWAY FOR A SCRATCH WRITE (2026-10-20: 18 fleet
// reads in 3 hours returned "no verdict (ran=false verdict="ok"): environment: the wall refused
// a write outside the job", half of them a reader redirecting a log to /tmp/cfg.log). The
// reader exited 0 and its own RESULT.md carries the verdict, so the verdict is kept and the
// refusal rides on it as an environment finding.
func TestAReadKeepsItsVerdictPastAScratchWallRefusal(t *testing.T) {
	t.Parallel()
	for _, denied := range []string{"/tmp/cfg.log", "/tmp_nope", "/var/tmp/x"} {
		c := scratchDenialChild(t, denied, "rev: abc\nverdict: ok\n## One line\nAll gates pass\n")
		r := c.Result()
		assert.True(t, r.Ran, "%s: a read that reached its verdict past a scratch refusal ran", denied)
		assert.Empty(t, r.End, "%s: the read is no environment end", denied)
		assert.Equal(t, "ok", r.Verdict, denied)
		assert.Contains(t, r.Report, "environment: the wall refused a scratch write to "+denied, denied)
		assert.Contains(t, r.Report, "All gates pass", denied)
	}
	broken := scratchDenialChild(t, "/tmp/cfg.log", "rev: abc\nverdict: broken\n## One line\nsdk/a.cpp:12 leaks\n")
	r := broken.Result()
	assert.True(t, r.Ran)
	assert.Equal(t, "broken", r.Verdict)
	assert.Contains(t, r.Report, "sdk/a.cpp:12 leaks")
}

// A denial of anything that is not scratch -- a tool a gate runs, like the bench's own
// nova-sprint or tlacheck -- leaves the gate unproven, and a read with no verdict of its own
// has nothing to keep: both stay the reader's environment end, run again and never a verdict.
func TestAReadStaysAnEnvironmentEndPastANonScratchRefusal(t *testing.T) {
	t.Parallel()
	tool := scratchDenialChild(t, "/home/nova/.local/bin/nova-sprint", "rev: abc\nverdict: ok\n## One line\nAll gates pass\n")
	r := tool.Result()
	assert.False(t, r.Ran)
	assert.Equal(t, member.EndEnvironment, r.End, "a denied tool is never a kept verdict")

	none := scratchDenialChild(t, "/tmp/cfg.log", "")
	r = none.Result()
	assert.False(t, r.Ran)
	assert.Equal(t, member.EndEnvironment, r.End, "a scratch refusal with no verdict of the reader's own keeps nothing")
}

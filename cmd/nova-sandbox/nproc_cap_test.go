//go:build linux

// The wall's process and memory caps, docs/SPEC-SANDBOX.md "wall-caps-processes.w1". The
// fork bomb runs the real tool as a subprocess for the reason wall_linux_test.go gives: a
// Landlock domain cannot be lifted, so a Run() in the test binary would wall the binary.
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveInGroup is the processes of group pgid that are still running. A zombie is a
// process that has died and not been collected, which is no survivor.
func liveInGroup(t *testing.T, pgid int) []int {
	t.Helper()
	ents, err := os.ReadDir("/proc")
	require.NoError(t, err)
	var live []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(raw)
		f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(f) > 2 && f[0] != "Z" && f[2] == strconv.Itoa(pgid) {
			live = append(live, pid)
		}
	}
	return live
}

// TestWallCapsAForkBomb: a shell loop that forks until refused, itself capped at 1,000,
// runs under the wall; the run ends reporting runaway: <n> processes with n over the
// default cap of 256, and no process of the tree is left.
func TestWallCapsAForkBomb(t *testing.T) {
	t.Parallel()
	needLandlock(t)
	j := newJob(t)
	pidFile := filepath.Join(j.write, "leader")
	script := `echo $$ > ` + pidFile + `; i=0; while [ $i -lt 1000 ]; do sleep 60 & i=$((i+1)); done; wait`
	code, _, errOut := j.wall(t, script)

	m := regexp.MustCompile(`runaway: (\d+) processes`).FindStringSubmatch(errOut)
	require.NotNil(t, m, "no runaway line; the bomb was not bounded (exit %d): %s", code, errOut)
	n, _ := strconv.Atoi(m[1])
	assert.Greater(t, n, sandbox.DefaultMaxProcs, "the line names a count over the cap")
	assert.Equal(t, sandbox.ExitRunaway, code, "the run ends with the runaway status")

	raw, err := os.ReadFile(pidFile)
	require.NoError(t, err, "the leader never ran: %s", errOut)
	pgid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	assert.Empty(t, liveInGroup(t, pgid), "a process of the killed tree survives")
}

// TestWallCapsLeaveANormalRunAlone: a run under the cap is not touched, and says nothing.
func TestWallCapsLeaveANormalRunAlone(t *testing.T) {
	t.Parallel()
	needLandlock(t)
	j := newJob(t)
	code, _, errOut := j.wall(t, `i=0; while [ $i -lt 20 ]; do sleep 0 & i=$((i+1)); done; wait; exit 7`)
	assert.Equal(t, 7, code, errOut)
	assert.NotContains(t, errOut, "runaway")
}

// TestPolicyOverNamesTheCapPast: the verdict is a function of the counts and the caps.
func TestPolicyOverNamesTheCapPast(t *testing.T) {
	t.Parallel()
	p := &sandbox.Policy{MaxProcs: 4, MaxMem: 100}
	_, hit := p.Over(sandbox.Usage{Procs: 4, RSS: 100})
	assert.False(t, hit, "at the cap is not past it")
	line, hit := p.Over(sandbox.Usage{Procs: 5})
	assert.True(t, hit)
	assert.Equal(t, "runaway: 5 processes (cap 4)", line)
	line, hit = p.Over(sandbox.Usage{Procs: 1, RSS: 101})
	assert.True(t, hit)
	assert.Equal(t, "runaway: 101 bytes of memory (cap 100)", line)
	_, hit = (&sandbox.Policy{}).Over(sandbox.Usage{Procs: 1 << 20, RSS: 1 << 50})
	assert.False(t, hit, "a policy built by hand with no caps is unbounded")
}

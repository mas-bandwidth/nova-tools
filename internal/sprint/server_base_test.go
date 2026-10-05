package sprint_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// baseTwin is a twin of the server's repository: a bare origin with the sprint base and a
// side branch cut from it, the base moved on after the cut, and the server's clone of origin
// that the check fetches into.
type baseTwin struct {
	origin, clone, base string
	baseCommit, side    string // a commit on the base, and the side branch's tip
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=twin", "-c", "user.email=twin@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func newBaseTwin(t *testing.T) *baseTwin {
	t.Helper()
	dir := t.TempDir()
	tw := &baseTwin{origin: filepath.Join(dir, "origin.git"), clone: filepath.Join(dir, "clone"), base: "sprint/base"}
	work := filepath.Join(dir, "work")
	runGit(t, dir, "init", "-q", "--bare", tw.origin)
	runGit(t, dir, "init", "-q", work)
	commit := func(msg string) string {
		require.NoError(t, os.WriteFile(filepath.Join(work, "f"), []byte(msg), 0o644))
		runGit(t, work, "add", "f")
		runGit(t, work, "commit", "-q", "-m", msg)
		return runGit(t, work, "rev-parse", "HEAD")
	}
	runGit(t, work, "checkout", "-q", "-b", tw.base)
	tw.baseCommit = commit("on the base")
	runGit(t, work, "checkout", "-q", "-b", "side")
	tw.side = commit("off the base")
	runGit(t, work, "checkout", "-q", tw.base)
	commit("the base moves on")
	runGit(t, work, "remote", "add", "origin", tw.origin)
	runGit(t, work, "push", "-q", "origin", tw.base, "side")
	runGit(t, dir, "clone", "-q", tw.origin, tw.clone)
	return tw
}

// stamped is a binary whose version verb prints line.
func stamped(t *testing.T, line string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "nova-sprint")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo '" + line + "'; exit 0; fi\nexit 2\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	return bin
}

func stampOf(commit string) string {
	return "nova-sprint 20261005120000-" + commit[:12] + " linux/amd64 go1.24.0"
}

// TestServerSwitchRefusesABinaryBuiltOffTheSprintBase: the server is built from the sprint
// base and nothing else (docs/SPEC-SPRINT.md section 14, "server-from-base-only.w1"). On a
// twin repository a binary stamped from a side branch is refused, naming its commit, the base
// and the remedy; one stamped from the base is on it, however far the base has moved; one
// with no source commit, or built from an edited tree, is refused the same way; and the tick
// keeps one judgment while the running server is off the base, closed when it is back on.
func TestServerSwitchRefusesABinaryBuiltOffTheSprintBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tw := newBaseTwin(t)

	t.Run("a binary stamped from a side branch is refused", func(t *testing.T) {
		bin := stamped(t, stampOf(tw.side))
		b, err := sprint.CheckServerBinary(ctx, bin, tw.clone, tw.base)
		require.NoError(t, err)
		assert.False(t, b.On)
		assert.Equal(t, tw.side, b.Commit)
		why := b.Refusal(bin)
		assert.Contains(t, why, "commit "+tw.side[:12])
		assert.Contains(t, why, "not an ancestor of origin/"+tw.base)
		assert.Contains(t, why, "remedy: build nova-sprint from origin/"+tw.base+" at its tip, then run: nova-sprint server switch")
	})

	t.Run("a binary stamped from the base is on it", func(t *testing.T) {
		b, err := sprint.CheckServerBinary(ctx, stamped(t, stampOf(tw.baseCommit)), tw.clone, tw.base)
		require.NoError(t, err)
		assert.True(t, b.On, b.Why)
		assert.Equal(t, tw.baseCommit, b.Commit)
	})

	t.Run("the source revision extra is read whole", func(t *testing.T) {
		line := "nova-sprint v1.2.3 linux/amd64 go1.24.0 repo=github.com/mas-bandwidth/nova-tools revision=" + tw.baseCommit + " dirty=false build_host=bench"
		b, err := sprint.CheckServerBinary(ctx, stamped(t, line), tw.clone, tw.base)
		require.NoError(t, err)
		assert.True(t, b.On, b.Why)
	})

	t.Run("a binary with no source commit is refused the same way", func(t *testing.T) {
		for line, why := range map[string]string{
			"nova-sprint devel linux/amd64 go1.24.0":  "names no source commit",
			"nova-sprint v1.2.3 linux/amd64 go1.24.0": "names no source commit",
			"nova-sprint v1.2.3 linux/amd64 go1.24.0 repo=r revision=" + tw.baseCommit + " dirty=true build_host=h": "built from an edited tree",
			"nova-sprint 20261005120000-" + tw.baseCommit[:12] + "-dirty x/y z":                                     "built from an edited tree",
			"usage: nova-sprint <verb>":       "is not a version line",
			stampOf(strings.Repeat("ab", 20)): "is in no branch fetched from origin",
		} {
			bin := stamped(t, line)
			b, err := sprint.CheckServerBinary(ctx, bin, tw.clone, tw.base)
			require.NoError(t, err, line)
			assert.False(t, b.On, line)
			assert.Contains(t, b.Refusal(bin), why, line)
			assert.Contains(t, b.Refusal(bin), "not from the sprint base origin/"+tw.base, line)
		}
	})

	t.Run("a check that cannot be made is an error, never a pass", func(t *testing.T) {
		bin := stamped(t, stampOf(tw.baseCommit))
		_, err := sprint.CheckServerBinary(ctx, bin, tw.clone, "")
		assert.ErrorContains(t, err, "is not a branch name")
		_, err = sprint.CheckServerBinary(ctx, bin, "", tw.base)
		assert.ErrorContains(t, err, "no clone")
		_, err = sprint.CheckServerBinary(ctx, bin, tw.clone, "no-such-base")
		assert.ErrorContains(t, err, "git fetch origin no-such-base")
	})

	t.Run("a running server off the base raises one judgment, closed when it is back on", func(t *testing.T) {
		s := &sprint.Snapshot{Now: time.Date(2026, 10, 5, 9, 20, 0, 0, time.UTC), Coordinator: "coordinator"}
		off, err := sprint.CheckServerBinary(ctx, stamped(t, stampOf(tw.side)), tw.clone, tw.base)
		require.NoError(t, err)

		p, _ := sprint.TickServerBase(s, sprint.TickReq{}, &off)
		require.Len(t, p.Notes, 1)
		n := p.Notes[0]
		assert.Equal(t, sprint.Judgment, n.Kind)
		assert.Equal(t, sprint.NServerOffBase, n.Type)
		assert.Contains(t, n.What, "commit "+tw.side[:12])
		assert.Contains(t, n.What, "origin/"+tw.base)
		assert.Contains(t, n.What, "remedy: build nova-sprint from origin/"+tw.base)
		n.ID = "j1"
		s.Open = []sprint.Open{{Key: sprint.OpenKey(n.ID, sprint.StreamSubject("")), Note: n}}

		p, _ = sprint.TickServerBase(s, sprint.TickReq{}, &off)
		assert.Empty(t, p.Notes, "one judgment while it stands, never one a tick")
		assert.Empty(t, p.Closes)

		p, _ = sprint.TickServerBase(s, sprint.TickReq{}, nil)
		assert.Empty(t, p.Notes, "a tick with no check made raises nothing")
		assert.Empty(t, p.Closes, "and closes nothing")

		on, err := sprint.CheckServerBinary(ctx, stamped(t, stampOf(tw.baseCommit)), tw.clone, tw.base)
		require.NoError(t, err)
		p, _ = sprint.TickServerBase(s, sprint.TickReq{}, &on)
		assert.Empty(t, p.Notes)
		require.Len(t, p.Closes, 1, "back on the base closes it")
		assert.Equal(t, "j1", p.Closes[0].Note.ID)
	})
}

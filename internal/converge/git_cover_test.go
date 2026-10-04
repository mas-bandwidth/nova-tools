// Unit coverage for the RealGit paths the per-function coverage table showed
// at zero: run, RevBefore and Show. Everything runs in-process: the refusals
// are reached through the Bin seam -- a name this machine has no binary for,
// so exec refuses before any child starts -- and through a deadline already in
// the past, which exec.Cmd.Start returns from before it forks. The success
// paths need a child process and are not reached here: no sleeps, no real
// time, no network, no subprocess, no Redis or Postgres.
package converge

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goneGit is a binary name no machine has. exec's lookup of it fails before a
// child could start, so a refusal reached through it is reached with nothing
// but a name.
const goneGit = "converge-cover-no-such-git"

// pastDeadline is a context whose deadline is a fixed instant already behind
// it, so a child it would start is refused before it forks and the refusal is
// the deadline's.
func pastDeadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0).UTC())
	t.Cleanup(cancel)
	return ctx
}

func TestGitCoverRunRefusesWhenTheDeadlineHasPassed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rows := []struct {
		name string
		bin  string
	}{
		{name: "an empty Bin names git", bin: ""},
		{name: "a blank Bin names git", bin: "   "},
		{name: "a named Bin is kept as written", bin: filepath.Join(dir, "git-fake")},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			g := RealGit{Dir: dir, Timeout: 4 * time.Second, Bin: row.bin}
			out, err := g.run(pastDeadline(t), "rev-list", "-1", "HEAD")
			assert.Equal(t, "", out, "a refusal carries no output, got %q", out)
			require.Error(t, err, "run must refuse a deadline already passed")
			assert.Equal(t, "git rev-list ran past --timeout 4s and was killed", err.Error(),
				"the refusal names the verb, the bound and the kill")
		})
	}
}

func TestGitCoverRunRefusesWhenTheBinaryIsMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rows := []struct {
		name string
		args []string
	}{
		{name: "the rev-list ask", args: []string{"rev-list", "-1", "--before=2026-10-04T09:00:00Z", "HEAD"}},
		{name: "the show ask", args: []string{"show", "abc123456789:" + SpecCIPath}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			g := RealGit{Dir: dir, Timeout: time.Minute, Bin: goneGit}
			out, err := g.run(context.Background(), row.args...)
			assert.Equal(t, "", out, "a refusal carries no output, got %q", out)
			require.Error(t, err, "run must refuse a binary it cannot find")
			assert.Contains(t, err.Error(), "git "+row.args[0]+" in "+dir,
				"the refusal names the verb and the directory it ran in")
			assert.Contains(t, err.Error(), "executable file not found in $PATH",
				"the refusal carries the child's own reason")
			assert.NotContains(t, err.Error(), "ran past --timeout",
				"the binary was missing, never started, so this is not the timeout refusal")
		})
	}
}

func TestGitCoverRevBeforeRefusesWhenGitDoes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	before := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)

	t.Run("a binary this machine does not have is a refusal naming the ask", func(t *testing.T) {
		t.Parallel()
		g := RealGit{Dir: dir, Timeout: time.Minute, Bin: goneGit}
		rev, err := g.RevBefore(context.Background(), before)
		assert.Equal(t, "", rev, "a refusal carries no revision, got %q", rev)
		require.Error(t, err, "RevBefore must refuse through run's error")
		assert.Contains(t, err.Error(), "git rev-list in "+dir,
			"the refusal is run's, naming the rev-list ask and the checkout")
		assert.NotContains(t, err.Error(), "no commit in",
			"an empty history is a different refusal; this one never reached git")
	})

	t.Run("a deadline already passed is the timeout refusal", func(t *testing.T) {
		t.Parallel()
		g := RealGit{Dir: dir, Timeout: 4 * time.Second}
		rev, err := g.RevBefore(pastDeadline(t), before)
		assert.Equal(t, "", rev, "a refusal carries no revision, got %q", rev)
		require.Error(t, err, "RevBefore must refuse a deadline already passed")
		assert.Equal(t, "git rev-list ran past --timeout 4s and was killed", err.Error(),
			"RevBefore carries run's refusal unchanged")
	})
}

func TestGitCoverShowRefusesWhenGitDoes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rows := []struct {
		name string
		ctx  func(t *testing.T) context.Context
		want string
	}{
		{
			name: "a binary this machine does not have is a refusal naming the read",
			ctx:  func(t *testing.T) context.Context { return context.Background() },
			want: "executable file not found in $PATH",
		},
		{
			name: "a deadline already passed is the timeout refusal",
			ctx:  pastDeadline,
			want: "ran past --timeout 4s and was killed",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			g := RealGit{Dir: dir, Timeout: 4 * time.Second, Bin: goneGit}
			out, err := g.Show(row.ctx(t), "abc123456789", SpecCIPath)
			assert.Equal(t, "", out, "a refusal carries no content, got %q", out)
			require.Error(t, err, "Show must refuse through run's error")
			assert.Contains(t, err.Error(), "git show", "the refusal names the show ask")
			assert.Contains(t, err.Error(), row.want)
		})
	}
}

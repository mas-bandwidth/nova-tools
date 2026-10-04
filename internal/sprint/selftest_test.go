package sprint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSprint stands in for this binary's verbs in the canned card's flow: every verb
// answers 0, finish remembers the head it names, and land is the lander under test.
// A good lander merges that head (--no-ff) onto origin's base in the --repo-dir clone
// and pushes it, as land does; a broken one says LAND OK and pushes nothing, as the
// release build that broke the lander did.
type fakeSprint struct {
	t      *testing.T
	git    func(ctx context.Context, dir string, args ...string) (string, error)
	broken bool
	head   string
	ran    []string
}

func (f *fakeSprint) verb(ctx context.Context, args []string) (string, int) {
	f.ran = append(f.ran, args[0])
	flag := func(name string) string {
		if i := slices.Index(args, name); i >= 0 && i+1 < len(args) {
			return args[i+1]
		}
		return ""
	}
	switch args[0] {
	case "finish":
		f.head = flag("--head")
	case "land":
		if f.broken {
			return "LAND OK stream=s1 cards=1\n", 0
		}
		dir, base := flag("--repo-dir"), flag("--base")
		for _, g := range [][]string{
			{"fetch", "-q", "origin"},
			{"switch", "-q", "--detach", "origin/" + base},
			{"merge", "-q", "--no-ff", "-m", SelftestLanding(), f.head},
			{"push", "-q", "origin", "HEAD:refs/heads/" + base},
		} {
			if out, err := f.git(ctx, dir, g...); err != nil {
				return out + err.Error(), 2
			}
		}
		return "LAND OK stream=s1 cards=1\n", 0
	}
	return "OK\n", 0
}

// switchRig is a server's install in the test's own directory: the binary the
// supervisor runs, a new build beside it, a fake service manager that counts its
// restarts, the server's log as lines handed out one poll at a time, and a clock that
// moves only when the switch sleeps.
type switchRig struct {
	install, build string
	restarts       int
	polls          [][]string
	now            time.Time
}

func newSwitchRig(t *testing.T) *switchRig {
	t.Helper()
	dir := t.TempDir()
	r := &switchRig{install: filepath.Join(dir, "bin", "nova-sprint"), build: filepath.Join(dir, "nova-sprint.new"), now: time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)}
	require.NoError(t, os.MkdirAll(filepath.Dir(r.install), 0o755))
	require.NoError(t, os.WriteFile(r.install, []byte("old build"), 0o755))
	require.NoError(t, os.WriteFile(r.build, []byte("new build"), 0o755))
	return r
}

func (r *switchRig) sw(selftest error) Switch {
	return Switch{
		Binary: r.build, Install: r.install, Keep: r.install + ".prev",
		Selftest: func(context.Context, string) error { return selftest },
		Restart:  func(context.Context) error { r.restarts++; return nil },
		Lines: func() ([]string, error) {
			if len(r.polls) == 0 {
				return nil, nil
			}
			l := r.polls[0]
			r.polls = r.polls[1:]
			return l, nil
		},
		Window: 10 * time.Minute, Every: time.Minute,
		Now:   func() time.Time { return r.now },
		Sleep: func(_ context.Context, d time.Duration) error { r.now = r.now.Add(d); return nil },
	}
}

func (r *switchRig) installed(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(r.install)
	require.NoError(t, err)
	return string(b)
}

// selftest land lands the canned card (the help's walkthrough) on a bare origin and a
// scratch clone in the test's own directory, green on a good lander and red on a broken
// one; server switch keeps the previous binary, installs the new one only after its
// selftest is green, and rolls back to the kept one when a land fails in the window
// (docs/SPEC-SPRINT.md section 14, switching the server's binary).
func TestSelftestLandLandsACannedCardAndSwitchRollsBack(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		broken bool
	}{{"a good lander lands the canned card", false}, {"a broken lander is red", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			git := SelftestGit(dir)
			f := &fakeSprint{t: t, git: git, broken: tc.broken}
			res := Selftest{Dir: dir, Git: git, Verb: f.verb}.Land(context.Background())
			assert.Contains(t, f.ran, "land", "the flow runs the lander")
			if tc.broken {
				assert.False(t, res.OK, "a lander that pushed nothing is red: %+v", res)
				assert.Contains(t, res.Why, SelftestLanding(), "red names the landing the base lacks")
				return
			}
			require.True(t, res.OK, "%+v", res)
			assert.Equal(t, f.head, res.Head)
			subjects, err := git(context.Background(), dir, "-C", SelftestOrigin, "log", "--first-parent", "--format=%s", SelftestBase)
			require.NoError(t, err)
			assert.Equal(t, SelftestLanding(), strings.Split(subjects, "\n")[0], "origin's base holds the landing")
		})
	}
	t.Run("a verb that fails is red at its line", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		noGit := func(context.Context, string, ...string) (string, error) { return "", nil }
		res := Selftest{Dir: dir, Git: noGit, Verb: func(_ context.Context, args []string) (string, int) {
			if args[0] == "tick" {
				return "TICK FAILED", 2
			}
			return "", 0
		}}.Land(context.Background())
		assert.False(t, res.OK)
		assert.Equal(t, "nova-sprint tick", res.Line)
		assert.Contains(t, res.Why, "TICK FAILED")
	})
	t.Run("a green switch keeps the old binary and confirms on a landing", func(t *testing.T) {
		t.Parallel()
		r := newSwitchRig(t)
		r.polls = [][]string{nil, {"15:01:02 LAND REFUSED stream=s1 cards=1 base=main tip=- ids=s1-4 fact=conflict"}, {"15:02:02 LAND OK stream=s1 cards=2 base=main tip=abc ids=s1-5..s1-6"}}
		res := r.sw(nil).Run(context.Background())
		assert.Equal(t, SwitchOK, res.Status, "%+v", res)
		assert.True(t, res.Landed, "a landing in the window confirms the binary")
		assert.Equal(t, "new build", r.installed(t))
		kept, err := os.ReadFile(r.install + ".prev")
		require.NoError(t, err)
		assert.Equal(t, "old build", string(kept), "the previous binary is kept")
		assert.Equal(t, 1, r.restarts)
	})
	t.Run("a failed land in the window rolls back", func(t *testing.T) {
		t.Parallel()
		for _, line := range []string{
			"15:03:00 LAND FAILED the merge queue could not be read: EOF; nothing was landed",
			"15:03:00 LAND REFUSED stream=s1 cards=1 base=main tip=- ids=s1-4 reason=git_fetch_failed",
		} {
			r := newSwitchRig(t)
			r.polls = [][]string{nil, nil, {"15:02:59 TICK 41", line}}
			res := r.sw(nil).Run(context.Background())
			assert.Equal(t, SwitchRolledBack, res.Status, "%s: %+v", line, res)
			assert.Equal(t, line, res.Land)
			assert.Equal(t, "old build", r.installed(t), "%s: the kept binary is back", line)
			assert.Equal(t, 2, r.restarts, "%s: restarted on the new binary, then on the old", line)
		}
	})
	t.Run("a quiet window keeps the new binary, a failure after it is not watched", func(t *testing.T) {
		t.Parallel()
		r := newSwitchRig(t)
		for range 11 { // the window's polls, at 0 to 10 minutes
			r.polls = append(r.polls, nil)
		}
		r.polls = append(r.polls, []string{"LAND FAILED too late"})
		res := r.sw(nil).Run(context.Background())
		assert.Equal(t, SwitchOK, res.Status, "%+v", res)
		assert.False(t, res.Landed)
		assert.Equal(t, "new build", r.installed(t))
	})
	t.Run("a red selftest installs nothing", func(t *testing.T) {
		t.Parallel()
		r := newSwitchRig(t)
		res := r.sw(errors.New("SELFTEST FAILED land")).Run(context.Background())
		assert.Equal(t, SwitchRefused, res.Status)
		assert.Contains(t, res.Why, "SELFTEST FAILED land")
		assert.Equal(t, "old build", r.installed(t))
		assert.NoFileExists(t, r.install+".prev")
		assert.Zero(t, r.restarts)
	})
	t.Run("rollback puts the kept binary back", func(t *testing.T) {
		t.Parallel()
		r := newSwitchRig(t)
		res := r.sw(nil).Rollback(context.Background())
		assert.Equal(t, SwitchRefused, res.Status, "nothing kept, nothing to roll back to")
		r.polls = [][]string{{"LAND OK stream=s1"}}
		require.Equal(t, SwitchOK, r.sw(nil).Run(context.Background()).Status)
		res = r.sw(nil).Rollback(context.Background())
		assert.Equal(t, SwitchRolledBack, res.Status, "%+v", res)
		assert.Equal(t, "old build", r.installed(t))
		assert.Equal(t, 2, r.restarts)
	})
}

package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWinOps records the windows wall's calls, in order, and fails the step it is told to.
type fakeWinOps struct {
	calls    []string
	failAt   string
	released int
}

func (f *fakeWinOps) LabelLow(dir string) error {
	f.calls = append(f.calls, "label "+filepath.Base(dir))
	if f.failAt == "label" {
		return errors.New("access denied")
	}
	return nil
}

func (f *fakeWinOps) LowToken() (uintptr, func(), error) {
	f.calls = append(f.calls, "token")
	if f.failAt == "token" {
		return 0, nil, errors.New("no")
	}
	return 42, func() { f.released++ }, nil
}

func (f *fakeWinOps) JoinJob(n uint32, mem int64) error {
	f.calls = append(f.calls, "job")
	if f.failAt == "job" {
		return errors.New("no")
	}
	return nil
}

func winAbs(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `C:\w\` + name
	}
	return "/w/" + name
}

// TestTheWindowsWallLabelsEveryWriteRootThenTokensThenJoins: label every write root, the
// tmp and the home (each once, without case), then the token, then the job, and only then
// is a token handed back. The process cap counts this process, which is in the job too.
func TestTheWindowsWallLabelsEveryWriteRootThenTokensThenJoins(t *testing.T) {
	t.Parallel()

	p := &Policy{Writes: []string{winAbs(t, "job"), winAbs(t, "cache")}, Tmp: winAbs(t, "tmp"), Home: winAbs(t, "JOB"), MaxProcs: 64, MaxMem: 1 << 30}
	plan, err := planWinWall(p, winGuard{})
	require.NoError(t, err)
	assert.Equal(t, uint32(65), plan.ActiveProcesses)
	assert.Equal(t, int64(1<<30), plan.MemoryBytes)
	if runtime.GOOS == "windows" {
		assert.Len(t, plan.Label, 3, "JOB and job are one directory on Windows: %v", plan.Label)
	}
	f := &fakeWinOps{}
	tok, release, ref := prepareWinWall(f, p, winGuard{})
	require.Nil(t, ref)
	assert.Equal(t, uintptr(42), tok)
	n := len(f.calls)
	assert.Equal(t, []string{"token", "job"}, f.calls[n-2:], "the token and the job come after every label: %v", f.calls)
	for _, c := range f.calls[:n-2] {
		assert.True(t, strings.HasPrefix(c, "label "), c)
	}
	release()
	assert.Equal(t, 1, f.released)
}

// TestTheWindowsWallRefusesAtTheFirstStepThatFails: a failed label, token or job is a
// refusal and nothing after it is asked; a failed job releases the token it made.
func TestTheWindowsWallRefusesAtTheFirstStepThatFails(t *testing.T) {
	t.Parallel()

	p := &Policy{Writes: []string{winAbs(t, "job")}, Tmp: winAbs(t, "tmp")}
	for step, want := range map[string][]string{
		"label": {"label job"},
		"token": {"label job", "label tmp", "token"},
		"job":   {"label job", "label tmp", "token", "job"},
	} {
		f := &fakeWinOps{failAt: step}
		_, _, ref := prepareWinWall(f, p, winGuard{})
		require.NotNil(t, ref, step)
		assert.Equal(t, "sandbox_failed", ref.Reason, step)
		assert.Equal(t, want, f.calls, step)
		if step == "job" {
			assert.Equal(t, 1, f.released, "a failed job releases the token")
		}
	}
	// no write root at all is no wall: a low child could write nowhere, and the run is refused
	_, _, ref := prepareWinWall(&fakeWinOps{}, &Policy{}, winGuard{})
	require.NotNil(t, ref)
	// a relative write root is refused before anything is labelled
	f := &fakeWinOps{}
	_, _, ref = prepareWinWall(f, &Policy{Writes: []string{"rel"}}, winGuard{})
	require.NotNil(t, ref)
	assert.Empty(t, f.calls)
	// no caps leaves the job's limits unset
	plan, err := planWinWall(&Policy{Writes: []string{winAbs(t, "job")}}, winGuard{})
	require.NoError(t, err)
	assert.Zero(t, plan.ActiveProcesses)
	assert.Zero(t, plan.MemoryBytes)
}

// TestTheWindowsWallRefusesARootThatIsOrHoldsAProtectedTree: a volume, the profile, the
// member's temp, or an ancestor of one is never labelled low (the security read of #6037).
func TestTheWindowsWallRefusesARootThatIsOrHoldsAProtectedTree(t *testing.T) {
	t.Parallel()

	g := winGuard{Protected: []string{winAbs(t, "users/me"), winAbs(t, "tmp/me")}}
	for _, root := range []string{winAbs(t, "users/me"), winAbs(t, "users"), filepath.Dir(winAbs(t, "x")), winAbs(t, "tmp"), winAbs(t, "USERS/ME")} {
		f := &fakeWinOps{}
		_, _, ref := prepareWinWall(f, &Policy{Writes: []string{root}}, g)
		require.NotNil(t, ref, "%s was accepted as a write root", root)
		assert.Empty(t, f.calls, "%s: nothing is labelled before the refusal", root)
	}
	vol := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		vol = `C:\`
	}
	_, err := planWinWall(&Policy{Writes: []string{vol}}, winGuard{})
	assert.Error(t, err, "a volume root is refused")
	// a root beside the protected trees, or under one of them, is fine
	_, err = planWinWall(&Policy{Writes: []string{winAbs(t, "users/me/jobs/1"), winAbs(t, "slots/1")}}, g)
	assert.NoError(t, err)
}

// TestTheLabelWalkNeverFollowsAReparsePoint: every real entry is labelled, a reparse point
// under the root is skipped and not descended into, and a root that is one is refused. The
// reparse test is the seam, so this runs on any host over a temp tree.
func TestTheLabelWalkNeverFollowsAReparsePoint(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, d := range []string{"a/b", "junction/inside"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "f.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "link.lnk"), []byte("x"), 0o644))
	reparse := map[string]bool{filepath.Join(root, "junction"): true, filepath.Join(root, "link.lnk"): true}
	isRP := func(p string) (bool, error) { return reparse[p], nil }
	var labelled []string
	skipped, err := labelLowTree(root, isRP, func(p string) error {
		rel, _ := filepath.Rel(root, p)
		labelled = append(labelled, filepath.ToSlash(rel))
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, skipped)
	assert.Equal(t, []string{".", "a", "a/b", "a/f.txt"}, labelled, "nothing at or under a reparse point is labelled")
	// a root that is a reparse point is refused and nothing is labelled
	labelled = nil
	_, err = labelLowTree(filepath.Join(root, "junction"), isRP, func(p string) error { labelled = append(labelled, p); return nil })
	assert.ErrorIs(t, err, errReparseRoot)
	assert.Empty(t, labelled)
	// a label that fails stops the walk with that error
	_, err = labelLowTree(root, isRP, func(string) error { return errors.New("denied") })
	assert.EqualError(t, err, "denied")
}

package member

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// benchGitEnv keeps the bench's git config out of Verify's own git calls (gitIn sets
// its own environment): both files are read from the empty device, as the card asks.
// t.Setenv forbids t.Parallel, so these tests run in order.
func benchGitEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
}

// recordingRun is a ScriptVerifier.Run that records the argv and directory it was
// handed and does nothing else.
type recordingRun struct {
	mu   sync.Mutex
	dirs []string
	argv [][]string
	fn   func(context.Context, string, []string) error // nil: do nothing
}

func (r *recordingRun) run(ctx context.Context, dir string, argv []string) error {
	r.mu.Lock()
	r.dirs = append(r.dirs, dir)
	r.argv = append(r.argv, append([]string(nil), argv...))
	fn := r.fn
	r.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, dir, argv)
}

func emptyDir(t *testing.T, dir string) bool {
	t.Helper()
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	return len(ents) == 0
}

// Verify refuses a class with no program and a packet with no head before it touches
// git (docs/SPEC-SPRINT.md, the script read; member.go, Verify).
func TestAScriptReadWithNoProgramOrNoHeadIsRefused(t *testing.T) {
	benchGitEnv(t)
	repo := newScriptRepo(t)
	v := ScriptVerifier{Mirror: repo.dir, Temp: t.TempDir(), Run: program}

	ok, why := v.Verify(Packet{Card: "r1", Head: "x"}, cardhdr.Class{})
	assert.False(t, ok)
	assert.Equal(t, "the card names no program", why)

	ok, why = v.Verify(Packet{Card: "r1"}, mustClass(t, scriptBrief))
	assert.False(t, ok)
	assert.Equal(t, "the read names no head", why)
}

// A head or a base head the mirror does not hold fails the read with git's own words,
// and no program is run (member.go, Verify).
func TestAScriptReadWithACommitTheMirrorLacksFails(t *testing.T) {
	benchGitEnv(t)
	repo := newScriptRepo(t)
	made := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	missing := strings.Repeat("0", 40)
	rec := &recordingRun{}
	v := ScriptVerifier{Mirror: repo.dir, Temp: t.TempDir(), Run: rec.run}

	ok, why := v.Verify(Packet{Card: "r1", Head: missing, BaseHead: repo.start}, mustClass(t, scriptBrief))
	assert.False(t, ok)
	assert.Contains(t, why, "git diff")

	ok, why = v.Verify(Packet{Card: "r1", Head: made, BaseHead: missing}, mustClass(t, scriptBrief))
	assert.False(t, ok)
	assert.Empty(t, rec.dirs, "git failed before any program ran")
}

// With no base head the start commit is the merge base of the head with the work's
// base branch, resolved as origin/<b> when only that ref exists (member.go, Verify).
func TestAScriptReadWithoutABaseHeadResolvesTheMergeBaseOnOrigin(t *testing.T) {
	benchGitEnv(t)
	repo := newScriptRepo(t)
	made := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	gitIn(t, repo.dir, "update-ref", "refs/remotes/origin/base", repo.start)
	v := ScriptVerifier{Mirror: repo.dir, Temp: t.TempDir(), Run: program}

	ok, why := v.Verify(Packet{Card: "r1", Head: made, WorkBase: "base"}, mustClass(t, scriptBrief))
	assert.True(t, ok, why)
}

// Git that is not there fails the read with git's error and leaves no checkout under
// Temp (member.go, Verify; docs/SPEC-SPRINT.md, the script read).
func TestAScriptReadWithNoGitBinaryLeavesNoCheckout(t *testing.T) {
	benchGitEnv(t)
	repo := newScriptRepo(t)
	made := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	temp := t.TempDir()
	v := ScriptVerifier{Mirror: repo.dir, Temp: temp, Git: filepath.Join(t.TempDir(), "no-such-git"), Run: program}

	ok, why := v.Verify(Packet{Card: "r1", Head: made, BaseHead: repo.start}, mustClass(t, scriptBrief))
	assert.False(t, ok)
	assert.Contains(t, why, "git")
	assert.True(t, emptyDir(t, temp), "no script-read-* directory was left behind")
}

// A passing read hands Run the program's argv split as the class's Script and a
// directory under Temp, names the short start, short head and byte count in its why,
// and leaves Temp empty (member.go, Verify).
func TestAPassingScriptReadRunsTheProgramUnderTempAndNamesWhatItCompared(t *testing.T) {
	benchGitEnv(t)
	repo := newScriptRepo(t)
	made := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	temp := t.TempDir()
	rec := &recordingRun{fn: program}
	v := ScriptVerifier{Mirror: repo.dir, Temp: temp, Run: rec.run}

	ok, why := v.Verify(Packet{Card: "r1", Head: made, BaseHead: repo.start}, mustClass(t, scriptBrief))
	require.True(t, ok, why)
	require.Len(t, rec.argv, 1)
	assert.Equal(t, strings.Fields(mustClass(t, scriptBrief).Script), rec.argv[0])
	assert.True(t, strings.HasPrefix(rec.dirs[0], temp+string(filepath.Separator)), "the run's directory is under Temp: %s", rec.dirs[0])
	assert.Contains(t, why, short(repo.start))
	assert.Contains(t, why, short(made))
	assert.Contains(t, why, "identical")
	assert.True(t, emptyDir(t, temp), "the checkout is removed after the read")
}

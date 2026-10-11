package member

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// noGitConfig keeps the bench's own git config away from Verify's git calls; gitIn sets
// these for its own calls, the verifier does not. These tests are deliberately not
// parallel: t.Setenv cannot be used in a parallel test, and the package's parallel tests
// resume only after every sequential test here has finished.
func noGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
}

// The script read (member.go Verify; docs/SPEC-SPRINT.md, the script read): a script
// reader checks the head's diff against the program's, byte for byte.

// A card with no program and a packet with no head are each refused by name
// (member.go:2421, member.go:2423).
func TestAScriptReadNeedsAProgramAndAHead(t *testing.T) {
	noGitConfig(t)
	v := ScriptVerifier{}
	ok, why := v.Verify(Packet{Head: "abc"}, cardhdr.Class{})
	assert.False(t, ok)
	assert.Equal(t, "the card names no program", why)

	ok, why = v.Verify(Packet{}, mustClass(t, scriptBrief))
	assert.False(t, ok)
	assert.Equal(t, "the read names no head", why)
}

// A start or a head the mirror does not hold is refused with git's own words, and the
// program never runs (member.go:2451).
func TestAScriptReadRefusesACommitTheMirrorDoesNotHold(t *testing.T) {
	noGitConfig(t)
	repo := newScriptRepo(t)
	made := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	ran := false
	v := ScriptVerifier{Mirror: repo.dir, Temp: t.TempDir(), Run: func(context.Context, string, []string) error {
		ran = true
		return nil
	}}
	class := mustClass(t, scriptBrief)

	ok, why := v.Verify(Packet{Head: strings.Repeat("0", 40), BaseHead: repo.start}, class)
	assert.False(t, ok)
	assert.Contains(t, why, "git diff")
	assert.False(t, ran, "a head the mirror does not hold runs no program")

	ok, why = v.Verify(Packet{Head: made, BaseHead: strings.Repeat("1", 40)}, class)
	assert.False(t, ok)
	assert.Contains(t, why, "git diff")
	assert.False(t, ran, "a start the mirror does not hold runs no program")
}

// With no base head the merge base of the head and the work's base branch is the start,
// and a base branch the mirror holds only as origin/<b> resolves (member.go:2436).
func TestAScriptReadTakesTheOriginBranchAsTheWorkBase(t *testing.T) {
	noGitConfig(t)
	repo := newScriptRepo(t)
	made := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	gitIn(t, repo.dir, "update-ref", "refs/remotes/origin/dev", repo.start)
	v := ScriptVerifier{Mirror: repo.dir, Temp: t.TempDir(), Run: program}
	ok, why := v.Verify(Packet{Card: "r1", Head: made, WorkBase: "dev", Brief: scriptBrief}, mustClass(t, scriptBrief))
	assert.True(t, ok, why)
}

// A git that cannot be run is refused with its error and leaves no checkout under Temp
// (member.go:2430, member.go:2458).
func TestAScriptReadWithNoGitLeavesNothingUnderTemp(t *testing.T) {
	noGitConfig(t)
	repo := newScriptRepo(t)
	made := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	temp := t.TempDir()
	v := ScriptVerifier{Mirror: repo.dir, Temp: temp, Git: filepath.Join(temp, "no-such-git"), Run: program}
	ok, why := v.Verify(Packet{Head: made, BaseHead: repo.start, Brief: scriptBrief}, mustClass(t, scriptBrief))
	assert.False(t, ok)
	assert.Contains(t, why, "git diff")
	entries, err := os.ReadDir(temp)
	require.NoError(t, err)
	assert.Empty(t, entries, "no script-read-* checkout is left behind")
}

// The ok read hands Run the program's argv and a directory under Temp, names the short
// start, the short head and the byte count, and clears Temp (member.go:2476).
func TestAScriptReadRunsTheProgramUnderTempAndClearsIt(t *testing.T) {
	noGitConfig(t)
	repo := newScriptRepo(t)
	made := repo.head(t, "made", "HELLO\nWORLD\n", nil)
	temp := t.TempDir()
	var gotDir string
	var gotArgv []string
	v := ScriptVerifier{Mirror: repo.dir, Temp: temp, Run: func(_ context.Context, dir string, argv []string) error {
		gotDir, gotArgv = dir, argv
		return program(context.Background(), dir, argv)
	}}
	ok, why := v.Verify(Packet{Card: "r1", Head: made, BaseHead: repo.start, Brief: scriptBrief}, mustClass(t, scriptBrief))
	require.True(t, ok, why)
	assert.Equal(t, []string{"upcase"}, gotArgv, "the program's argv is the class's Script, split")
	assert.True(t, strings.HasPrefix(gotDir, temp), "the program runs in a directory under Temp")
	assert.Contains(t, why, short(repo.start))
	assert.Contains(t, why, short(made))
	assert.Contains(t, why, "identical")
	entries, err := os.ReadDir(temp)
	require.NoError(t, err)
	assert.Empty(t, entries, "Temp is empty after an ok read")
}

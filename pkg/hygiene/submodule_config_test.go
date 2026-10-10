package hygiene

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// findAt returns the finding with this token at this path, or nil. It exists because a
// range can draw more than one out-of-path finding and `has` answers with the first.
func findAt(fs []Finding, token, at string) *Finding {
	for i := range fs {
		if fs[i].Token == token && fs[i].At == at {
			return &fs[i]
		}
	}
	return nil
}

// hygiene-finds-a-gitlink-despite-ignore-submodules-config: the checked repository
// controls its own `.git/config`, and `diff.ignoreSubmodules=all` there makes `git diff`
// omit an added mode-160000 gitlink entirely. configOpts blanks the global and system
// config but not this local setting, so the raw read came back with no entry for the
// gitlink: modeFinding's explicit submodule refusal and checkPaths never ran, and a range
// that added a gitlink reported clean. The check reads the change; the subject does not
// choose which change it sees (hygiene.go modeFinding/rawDiff and configOpts).
//
// A gitlink is written with `update-index --cacheinfo` because a real submodule needs a
// second repository or a network, and the fixture keeps neither.
func TestCheckFindsGitlinkDespiteIgnoreSubmodulesConfig(t *testing.T) {
	t.Parallel()

	// card builds a range with two changes outside the declared sign/** paths: a mode
	// 160000 gitlink at other/vendor, and an ordinary edit to other/other.go beside
	// it. The ordinary edit is the control that the same config cannot hide real
	// work.
	card := func(t *testing.T) string {
		t.Helper()
		dir := lab(t)
		git(t, dir, "checkout", "-q", "-b", "card")
		write(t, dir, "other/other.go", "package other\n\nfunc F() {}\n")
		git(t, dir, "add", "-A")
		sha := git(t, dir, "rev-parse", "HEAD")
		git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+sha+",other/vendor")
		git(t, dir, "commit", "-q", "-m", "a gitlink and an ordinary change")
		return dir
	}

	assertFindings := func(t *testing.T, dir string) {
		t.Helper()
		fs := check(t, dir, Options{})
		sub := findAt(fs, "stray-file", "other/vendor")
		require.NotNil(t, sub, "the gitlink drew no stray-file finding: %v", tokens(fs))
		require.Contains(t, sub.Why, "submodule", "why=%q, want it to name the submodule", sub.Why)
		require.NotNil(t, findAt(fs, "out-of-path", "other/vendor"), "the gitlink drew no out-of-path finding: %v", tokens(fs))
		require.NotNil(t, findAt(fs, "out-of-path", "other/other.go"), "the ordinary change drew no out-of-path finding: %v", tokens(fs))
	}

	t.Run("default config control", func(t *testing.T) {
		assertFindings(t, card(t))
	})

	t.Run("local diff.ignoreSubmodules=all", func(t *testing.T) {
		dir := card(t)
		git(t, dir, "config", "diff.ignoreSubmodules", "all")
		assertFindings(t, dir)
	})
}

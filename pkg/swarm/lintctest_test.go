package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testgit"
)

// The typed header's `TEST: ctest <TestName>` (cardhdr.ParseTest, carried from nova-sprint
// 76d71f25) reaches both consumers of the TEST line as a CMake test, never as a Go one:
// the header lint holds no Go test name or package to it, and the brief's base check looks
// for an add_test of that name in the CMake files at the base, refusing a base with no
// CMakeLists.txt.

// cmakeTwin is briefTwin with a CMakeLists.txt at sprint/s1 declaring ExistingTest in the
// NAME form and BareName in the bare form.
func cmakeTwin(t *testing.T) (dir, sha string) {
	t.Helper()
	dir, _ = briefTwin(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CMakeLists.txt"), []byte("cmake_minimum_required(VERSION 3.16)\nproject(demo CXX)\nadd_test(NAME ExistingTest COMMAND demo_existing)\nadd_test(BareName COMMAND demo)\n"), 0o644))
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = testgit.Environ("GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %v\n%s", args, err, out)
		return strings.TrimSpace(string(out))
	}
	git("add", "CMakeLists.txt")
	git("commit", "-q", "-m", "cmake")
	return dir, git("rev-parse", "HEAD")
}

func TestTheHeaderLintTakesACtestLineAsACMakeTest(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"DecodeAll", "rocketnet.decode_all", "suite/one-two_3.4"} {
		fs := LintCardHeader(twinBrief("example.com/m", map[string]string{"TEST": "ctest " + name}, ""), nil, true)
		assert.Empty(t, findingsFor(fs, "test-named"), "TEST: ctest %s: %+v", name, fs)
	}
}

func TestLintBriefHoldsACtestTestAtTheBase(t *testing.T) {
	t.Parallel()
	repo, sha := cmakeTwin(t)
	bb := BriefBase{Repo: repo, Sha: sha, Listed: []string{"sprint/s1"}}
	lint := func(name string) []CardHeaderFinding {
		fs, _ := LintBrief(twinBrief(repo, map[string]string{"TEST": "ctest " + name}, ""), bb)
		return findingsFor(fs, "donewhen-test-name")
	}
	assert.Empty(t, lint("NewCtestTest"), "a ctest the base does not define can be red there")
	for _, name := range []string{"ExistingTest", "BareName"} {
		got := lint(name)
		require.Len(t, got, 1, "%s is defined at the base", name)
		assert.Contains(t, got[0].Excerpt, "TEST "+name+" exists in at ", "a ctest has no package to name")
	}
}

func TestLintBriefRefusesACtestWhereTheBaseHasNoCMakeLists(t *testing.T) {
	t.Parallel()
	repo, sha := briefTwin(t)
	bb := BriefBase{Repo: repo, Sha: sha, Listed: []string{"sprint/s1"}}
	fs, _ := LintBrief(twinBrief(repo, map[string]string{"TEST": "ctest Absent"}, ""), bb)
	got := findingsFor(fs, "donewhen-test-name")
	require.Len(t, got, 1, "%+v", fs)
	assert.Contains(t, got[0].Excerpt, "names a CMake test")
	assert.Contains(t, got[0].Excerpt, "no CMakeLists.txt at the base")
}

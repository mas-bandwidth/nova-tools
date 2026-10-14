package swarm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initCMakeRepo makes a throwaway git repository under t.TempDir() holding a CMakeLists.txt
// with one existing ctest test and a source file, and returns its directory and base commit.
func initCMakeRepo(t *testing.T) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	git("init", "-q", "-b", "main")
	write("CMakeLists.txt", "cmake_minimum_required(VERSION 3.16)\nproject(demo CXX)\nadd_test(NAME ExistingTest COMMAND demo_existing)\n")
	write("src/main.cpp", "int main() { return 0; }\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	return dir, git("rev-parse", "HEAD")
}

// ctestCard is a brief whose TEST line is `ctest <name>` and whose PATHS names the source
// file, so only the donewhen-test-name check varies with the name.
func ctestCard(sha, name string) []byte {
	return []byte("RESULT: c1 sha=" + sha + " tier: heavy\n" +
		"REPO: example.com/m\n" +
		"BASE: sprint/x\n" +
		"base-sha: " + sha + "\n" +
		"PATHS: src/main.cpp\n" +
		"KIND: fix\n" +
		"TEST: ctest " + name + "\n" +
		"\n" +
		"THE TASK.\n")
}

// A ctest TEST that is absent at the base lints clean; one the base already defines is
// refused as a Go test that already exists is.
func TestLintBriefHoldsACtestTestAtBase(t *testing.T) {
	t.Parallel()
	dir, sha := initCMakeRepo(t)
	bb := BriefBase{Repo: dir, Sha: sha}

	absent, _ := LintBrief(ctestCard(sha, "NewCtestTest"), bb)
	assert.Empty(t, findingsWith(absent, "donewhen-test-name"), "%+v", absent)

	present, _ := LintBrief(ctestCard(sha, "ExistingTest"), bb)
	got := findingsWith(present, "donewhen-test-name")
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Excerpt, "exists")

	// the bare `add_test(<name>` form is found too.
	dir2, sha2 := initCMakeRepo(t)
	require.NoError(t, func() error {
		return os.WriteFile(filepath.Join(dir2, "CMakeLists.txt"), []byte("add_test(BareName COMMAND demo)\n"), 0o644)
	}())
	cmd := exec.Command("git", "-C", dir2, "commit", "-q", "-am", "bare")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	require.NoError(t, cmd.Run())
	bareSha := strings.TrimSpace(mustGit(t, dir2, "rev-parse", "HEAD"))
	_ = sha2
	found, _ := LintBrief(ctestCard(bareSha, "BareName"), BriefBase{Repo: dir2, Sha: bareSha})
	assert.Len(t, findingsWith(found, "donewhen-test-name"), 1)
}

// A ctest TEST in a repository with no CMakeLists.txt at the base is refused with the
// reason.
func TestLintBriefRefusesACtestWithNoCMakeLists(t *testing.T) {
	t.Parallel()
	dir, sha := initBaseRepo(t)
	bb := BriefBase{Repo: dir, Sha: sha}
	got := findingsWith(mustLint(t, ctestCard(sha, "Absent"), bb), "donewhen-test-name")
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Excerpt, "no CMakeLists.txt")
}

func mustLint(t *testing.T, raw []byte, bb BriefBase) []CardHeaderFinding {
	t.Helper()
	fs, _ := LintBrief(raw, bb)
	return fs
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

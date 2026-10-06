package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// TestGenerateFromCommitsWritesOneRelandBriefPerCommit is one pre-linted re-land
// brief per commit, oldest first, so a brief directory lands in commit order.
// PATHS are that commit's files. The gate is that commit's packages plus
// ./internal/ci/. The commits are not in the checkout the briefs are cut from:
// a re-land is how those files come back.
func TestGenerateFromCommitsWritesOneRelandBriefPerCommit(t *testing.T) {
	t.Parallel()
	dir, git := commitFixture(t)
	shas := strings.Fields(git("log", "--reverse", "--format=%H", "dev..landed"))
	require.Len(t, shas, 5)
	_, err := os.Stat(filepath.Join(dir, "internal", "alpha", "a.go"))
	require.True(t, os.IsNotExist(err), "the base tree does not hold the commit's files")

	out := filepath.Join(t.TempDir(), "cards")
	exit, stdout, stderr := runCard("generate", "--from", "commits", "--range", "dev..landed", "--repo-dir", dir, "--out", out)
	require.Equal(t, 0, exit, "stdout:\n%s\nstderr:\n%s", stdout, stderr)
	assert.Contains(t, stdout, "CARDS OK dir="+out+" cards=5 waves=2 tier=pro")
	assert.NotContains(t, stdout, "shared-paths")
	assert.NotContains(t, stdout, "LINT DRIFT")

	id := func(i int, sha string) string {
		return fmt.Sprintf("reland-%04d-%s", i, sha[:12])
	}
	want := strings.Join([]string{
		"id\tfile\ttest\twave\tdeps",
		id(1, shas[0]) + "\tinternal/alpha/a.go\tinternal/alpha TestAlphaHolds\t1\t-",
		id(2, shas[1]) + "\tcmd/beta/main.go\tcmd/beta TestBetaRuns\t1\t-",
		id(3, shas[2]) + "\tinternal/alpha/a.go\tinternal/alpha TestAlphaHolds\t2\t" + id(1, shas[0]),
		id(4, shas[3]) + "\tinternal/gamma/g.go\tinternal/gamma TestRelandHolds\t1\t-",
		id(5, shas[4]) + "\tinternal/gamma/g.go\tinternal/gamma TestRelandHolds\t2\t" + id(4, shas[3]),
	}, "\n") + "\n"
	manifest, err := os.ReadFile(filepath.Join(out, "manifest.tsv"))
	require.NoError(t, err)
	assert.Equal(t, want, string(manifest))

	names, err := filepath.Glob(filepath.Join(out, "*.md"))
	require.NoError(t, err)
	var got []string
	for _, n := range names {
		got = append(got, strings.TrimSuffix(filepath.Base(n), ".md"))
	}
	sort.Strings(got)
	assert.Equal(t, []string{id(1, shas[0]), id(2, shas[1]), id(3, shas[2]), id(4, shas[3]), id(5, shas[4])}, got, "file names sort oldest first, which is how add --brief-dir lands them")

	read := func(n int, sha string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(out, id(n, sha)+".md"))
		require.NoError(t, err)
		return string(raw)
	}
	gate := "go test -count=1 -timeout 600s "
	one := read(1, shas[0])
	assert.Contains(t, one, "\nPATHS: internal/alpha/a.go, internal/alpha/a_test.go\n")
	assert.Contains(t, one, "\nTEST: internal/alpha TestAlphaHolds\n")
	assert.Contains(t, one, gate+"./internal/alpha/ ./internal/ci/")
	assert.Contains(t, one, shas[0])
	assert.Contains(t, one, "cherry-pick")
	assert.Contains(t, one, "finish with no change when the code already does it")
	assert.Contains(t, one, "REPO: example/repo\nBASE: dev\n")
	assert.Contains(t, one, "tier: pro")
	assert.NotContains(t, one, "No test of this commit")
	assert.NotContains(t, one, shas[1])

	two := read(2, shas[1])
	assert.Contains(t, two, "\nPATHS: cmd/beta/main.go, cmd/beta/main_test.go, docs/note.md, internal/beta/b.go\n")
	assert.Contains(t, two, "\nTEST: cmd/beta TestBetaRuns\n")
	assert.Contains(t, two, gate+"./cmd/beta/ ./internal/beta/ ./internal/ci/")
	assert.NotContains(t, two, "./docs/")
	assert.Contains(t, two, "\nDEPENDS-ON: -\n")

	three := read(3, shas[2])
	assert.Contains(t, three, "\nPATHS: internal/alpha/a.go\n")
	assert.Contains(t, three, "\nDEPENDS-ON: "+id(1, shas[0])+"\n")
	assert.Contains(t, three, gate+"./internal/alpha/ ./internal/ci/")

	four := read(4, shas[3])
	assert.Contains(t, four, "\nPATHS: internal/gamma/g.go\n")
	assert.Contains(t, four, "\nTEST: internal/gamma TestRelandHolds\n")
	assert.Contains(t, four, "No test of this commit")
	assert.Contains(t, four, gate+"./internal/gamma/ ./internal/ci/")

	// --paths keeps the commits that touch the glob, still oldest first.
	alpha := filepath.Join(t.TempDir(), "alpha")
	exit, stdout, stderr = runCard("generate", "--from", "commits", "--range", "dev..landed", "--paths", "internal/alpha/**", "--repo-dir", dir, "--out", alpha)
	require.Equal(t, 0, exit, "stdout:\n%s\nstderr:\n%s", stdout, stderr)
	assert.Contains(t, stdout, "cards=2 waves=2 tier=pro")
	raw, err := os.ReadFile(filepath.Join(alpha, "manifest.tsv"))
	require.NoError(t, err)
	assert.Equal(t, strings.Join([]string{
		"id\tfile\ttest\twave\tdeps",
		id(1, shas[0]) + "\tinternal/alpha/a.go\tinternal/alpha TestAlphaHolds\t1\t-",
		id(2, shas[2]) + "\tinternal/alpha/a.go\tinternal/alpha TestAlphaHolds\t2\t" + id(1, shas[0]),
	}, "\n")+"\n", string(raw))

	// --file is the same commits in any order; the briefs come out oldest first.
	list := filepath.Join(t.TempDir(), "commits.txt")
	var b strings.Builder
	b.WriteString("# newest first\n")
	for i := len(shas) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, "%s\n", shas[i])
	}
	require.NoError(t, os.WriteFile(list, []byte(b.String()), 0o644))
	fromFile := filepath.Join(t.TempDir(), "file")
	exit, stdout, stderr = runCard("generate", "--from", "commits", "--file", list, "--repo-dir", dir, "--out", fromFile)
	require.Equal(t, 0, exit, "stdout:\n%s\nstderr:\n%s", stdout, stderr)
	raw, err = os.ReadFile(filepath.Join(fromFile, "manifest.tsv"))
	require.NoError(t, err)
	assert.Equal(t, want, string(raw))

	exit, _, stderr = runCard("generate", "--from", "commits", "--repo-dir", dir, "--out", filepath.Join(t.TempDir(), "neither"))
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, "--range")
	exit, _, stderr = runCard("generate", "--from", "commits", "--range", "dev..landed", "--file", list, "--repo-dir", dir, "--out", filepath.Join(t.TempDir(), "both"))
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, "not both")
	exit, _, stderr = runCard("generate", "--from", "commits", "--file", list, "--paths", "internal/alpha/**", "--repo-dir", dir, "--out", filepath.Join(t.TempDir(), "paths"))
	assert.Equal(t, 2, exit)
	assert.Contains(t, stderr, "--paths")
}

// commitFixture is a branch dev at a base commit and a branch landed five
// commits later. dev is what is checked out, so the later files are not in
// the work tree. Dates increase so a list of shas sorts into this order.
func commitFixture(t *testing.T) (dir string, git func(args ...string) string) {
	t.Helper()
	dir = t.TempDir()
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(goenv.Clean(os.Environ()),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return strings.TrimSpace(string(out))
	}
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
	}
	commit := func(date, message string) {
		t.Helper()
		git("add", "-A")
		cmd := exec.Command("git", "-C", dir, "commit", "-q", "-m", message)
		// Dates stay on the shared identity so a newest-first list still sorts oldest first.
		cmd.Env = goenv.Clean(testgit.Environ("GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date))
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git commit %s: %s", message, out)
	}
	git("init", "-q", "-b", "dev")
	git("remote", "add", "origin", "git@example.com:example/repo.git")
	write("internal/ci/ci_test.go", "package ci\n")
	commit("2020-01-01T00:00:00Z", "base")
	base := git("rev-parse", "HEAD")

	write("internal/alpha/a.go", "package alpha\n")
	write("internal/alpha/a_test.go", "package alpha\n\nfunc TestAlphaHolds(t *testing.T) {}\n")
	commit("2020-01-02T00:00:00Z", "alpha holds a count")
	write("cmd/beta/main.go", "package main\n")
	write("cmd/beta/main_test.go", "package main\n\nfunc TestBetaRuns(t *testing.T) {}\n")
	write("docs/note.md", "a note\n")
	write("internal/beta/b.go", "package beta\n")
	commit("2020-01-03T00:00:00Z", "beta command")
	write("internal/alpha/a.go", "package alpha\n\nconst n = 2\n")
	commit("2020-01-04T00:00:00Z", "alpha again")
	write("internal/gamma/g.go", "package gamma\n")
	commit("2020-01-05T00:00:00Z", "gamma added")
	git("rm", "-q", "internal/gamma/g.go")
	commit("2020-01-06T00:00:00Z", "gamma removed")

	git("branch", "-f", "landed", "HEAD")
	git("reset", "--hard", base)
	require.Equal(t, "dev", git("symbolic-ref", "--quiet", "--short", "HEAD"))
	return dir, git
}

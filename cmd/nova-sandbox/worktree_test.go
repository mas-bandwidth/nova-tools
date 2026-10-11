package main

// The worktree verb's tests, written first from docs/SPEC-SANDBOX.md's numbered
// list: a fake forge, a fake `git` behind the tool's one seam, a fake clock and
// a fake process probe, so no test touches a network, a real forge or the clock.

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGit is the tool's `git` for one test: it materialises a linked worktree as
// a real directory with a real .git file, so the assertions run against the disk
// and not against a mock's bookkeeping.
type fakeGit struct {
	repo  string
	trees map[string]string
	dirty map[string]bool
	log   []string
}

func newFakeGit(repo string) *fakeGit {
	return &fakeGit{repo: repo, trees: map[string]string{}, dirty: map[string]bool{}}
}

func (g *fakeGit) run(dir string, args ...string) (string, error) {
	g.log = append(g.log, strings.Join(args, " "))
	if len(args) == 0 {
		return "", fmt.Errorf("git: no args")
	}
	switch args[0] {
	case "rev-parse":
		if len(args) > 1 && args[1] == "--is-inside-work-tree" {
			if dir == g.repo {
				return "true\n", nil
			}
			return "", fmt.Errorf("fatal: not a git repository")
		}
		if len(args) > 1 && args[1] == "HEAD" {
			if sha, ok := g.trees[dir]; ok {
				return sha + "\n", nil
			}
			return "", fmt.Errorf("fatal: cannot resolve HEAD")
		}
	case "remote":
		return "https://github.com/mas-bandwidth/nova-tools.git\n", nil
	case "fetch":
		return "", nil
	case "status":
		if g.dirty[dir] {
			return " M dirty.go\n", nil
		}
		return "", nil
	case "worktree":
		if len(args) < 2 {
			return "", fmt.Errorf("fatal: worktree wants a subcommand")
		}
		switch args[1] {
		case "add":
			path, sha := args[len(args)-2], args[len(args)-1]
			if _, ok := g.trees[path]; ok {
				return "", fmt.Errorf("fatal: '%s' already exists", path)
			}
			if err := os.MkdirAll(path, 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: "+filepath.Join(g.repo, ".git", "worktrees", "fake")+"\n"), 0o644); err != nil {
				return "", err
			}
			g.trees[path] = sha
			return "Preparing worktree (detached HEAD)\n", nil
		case "remove":
			path := args[len(args)-1]
			delete(g.trees, path)
			_ = os.RemoveAll(path)
			return "", nil
		case "list":
			var b strings.Builder
			paths := make([]string, 0, len(g.trees))
			for p := range g.trees {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			for _, p := range paths {
				fmt.Fprintf(&b, "worktree %s\nHEAD %s\ndetached\n\n", p, g.trees[p])
			}
			return b.String(), nil
		}
	}
	return "", fmt.Errorf("fake git: unhandled %q in %s", strings.Join(args, " "), dir)
}

func (g *fakeGit) addCount() int {
	n := 0
	for _, l := range g.log {
		if strings.HasPrefix(l, "worktree add") {
			n++
		}
	}
	return n
}

// fakeForge is the forge client seam's test double.
type fakeForge struct{ byID map[int]worktreePR }

func (f *fakeForge) PR(id int) (worktreePR, error) {
	pr, ok := f.byID[id]
	if !ok {
		return worktreePR{}, errNoPR
	}
	return pr, nil
}

func (j wjob) useFakeGit(g *fakeGit) { j.ws.Git = g.run }

// errForge is the forge seam for the failure tests: every call answers one
// error.
type errForge struct{ err error }

func (f errForge) PR(int) (worktreePR, error) { return worktreePR{}, f.err }

func (j wjob) useForge(f worktreeForge) {
	j.ws.ForgeFactory = func(repo string, env []string) worktreeForge { return f }
}

type wjob struct {
	base, repo, scratch string
	// ws is this test's own seams; copies of the job share them.
	ws *worktreeSeams
}

func newWJob(t *testing.T) wjob {
	t.Helper()
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	j := wjob{ws: prodWorktreeSeams(), base: base, repo: filepath.Join(base, "repo"), scratch: filepath.Join(base, "scratch")}
	for _, d := range []string{j.repo, j.scratch} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	return j
}

func (j wjob) tool(t *testing.T, env []string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := j.ws.worktreeVerb(args, &out, &errb, env)
	return code, out.String(), errb.String()
}

func recordGUID(t *testing.T, scratch string, id int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(scratch, fmt.Sprintf("%d.pr", id)))
	require.NoError(t, err, "the record file is not there: %s", err)
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "guid="); ok {
			return v
		}
	}
	t.Fatalf("the record file has no guid= line: %q", raw)
	return ""
}

func okLine(scratch, guid, sha string) string {
	return "WORKTREE OK path=" + filepath.Join(scratch, guid) + " head=" + sha + "\n"
}

func createTree(t *testing.T, j wjob, sha, id string) string {
	t.Helper()
	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--pr", id)
	require.Equal(t, 0, code, "creating the tree for --pr %s: exit %d, stderr %q", id, code, errb)
	expect := "WORKTREE OK path=" + filepath.Join(j.scratch, recordGUID(t, j.scratch, atoi(t, id))) + " head=" + sha + "\n"
	require.Equal(t, expect, out, "creating the tree for --pr %s: stdout %q, want %q", id, out, expect)
	return filepath.Join(j.scratch, recordGUID(t, j.scratch, atoi(t, id)))
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	require.NoError(t, err, "%q is not a number", s)
	return n
}

// 1. A fake forge answering head <sha> makes the verb print exactly WORKTREE OK
// path=<tmp>/<guid> head=<sha>, and the tree and its .git file exist.
func TestWorktreeMaterialisesThePRHead(t *testing.T) {
	t.Parallel()

	j := newWJob(t)
	g := newFakeGit(j.repo)
	j.useFakeGit(g)
	sha := strings.Repeat("a", 40)
	j.useForge(&fakeForge{byID: map[int]worktreePR{7: {Head: sha, Base: "main", State: "open"}}})

	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--pr", "7")
	require.Equal(t, 0, code, "exit %d, want 0; stderr %q", code, errb)
	guid := recordGUID(t, j.scratch, 7)
	want := okLine(j.scratch, guid, sha)
	require.Equal(t, want, out, "stdout %q, want %q", out, want)
	_, err := os.Stat(filepath.Join(j.scratch, guid, ".git"))
	require.NoError(t, err, "the worktree's .git file is not there: %s", err)
}

// 2. A second call for the same --pr reuses the first path, prints the same
// line, and adds no second entry to the fake git worktree call log.
func TestWorktreeSecondCallReusesTheTree(t *testing.T) {
	t.Parallel()

	j := newWJob(t)
	g := newFakeGit(j.repo)
	j.useFakeGit(g)
	sha := strings.Repeat("b", 40)
	j.useForge(&fakeForge{byID: map[int]worktreePR{7: {Head: sha, Base: "main", State: "open"}}})

	code, first, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--pr", "7")
	require.Equal(t, 0, code, "exit %d, want 0; stderr %q", code, errb)
	g.log = nil
	code, second, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--pr", "7")
	require.Equal(t, 0, code, "second exit %d, want 0; stderr %q", code, errb)
	require.Equal(t, first, second, "the second call printed %q, want the first line %q", second, first)
	n := g.addCount()
	require.Equal(t, 0, n, "the reuse ran %d git worktree add calls, want 0", n)
}

// 3. A record whose tree the test deletes is rebuilt on the next call with
// exactly one git worktree add.
func TestWorktreeDeletedTreeIsRebuilt(t *testing.T) {
	t.Parallel()

	j := newWJob(t)
	g := newFakeGit(j.repo)
	j.useFakeGit(g)
	sha := strings.Repeat("c", 40)
	j.useForge(&fakeForge{byID: map[int]worktreePR{7: {Head: sha, Base: "main", State: "open"}}})

	path := createTree(t, j, sha, "7")
	require.NoError(t, os.RemoveAll(path))
	g.log = nil
	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--pr", "7")
	require.Equal(t, 0, code, "exit %d, want 0; stderr %q", code, errb)
	n := g.addCount()
	require.Equal(t, 1, n, "the rebuild ran %d git worktree add calls, want exactly 1", n)
	want := okLine(j.scratch, recordGUID(t, j.scratch, 7), sha)
	require.Equal(t, want, out, "stdout %q, want %q", out, want)
	_, err := os.Stat(filepath.Join(path, ".git"))
	require.NoError(t, err, "the rebuilt tree has no .git file: %s", err)
}

// 4. --prune with a fake forge reporting one PR merged and one closed prints
// one WORKTREE REMOVED reason=pr_merged and one reason=pr_closed, removes both
// trees, and leaves a third open PR's tree standing.
func TestWorktreePruneRemovesMergedAndClosedAndKeepsOpen(t *testing.T) {
	t.Parallel()

	j := newWJob(t)
	g := newFakeGit(j.repo)
	j.useFakeGit(g)
	sha := strings.Repeat("d", 40)
	ff := &fakeForge{byID: map[int]worktreePR{}}
	for _, id := range []int{1, 2, 3} {
		ff.byID[id] = worktreePR{Head: sha, Base: "main", State: "open"}
	}
	j.useForge(ff)

	paths := map[int]string{}
	for _, id := range []int{1, 2, 3} {
		paths[id] = createTree(t, j, sha, fmt.Sprintf("%d", id))
	}
	ff.byID[1] = worktreePR{Head: sha, Base: "main", State: "merged"}
	ff.byID[2] = worktreePR{Head: sha, Base: "main", State: "closed"}

	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--prune")
	require.Equal(t, 0, code, "exit %d, want 0; stderr %q", code, errb)
	require.Contains(t, out, "WORKTREE REMOVED path="+paths[1]+" reason=pr_merged\n", "no pr_merged line for %s in %q", paths[1], out)
	require.Contains(t, out, "WORKTREE REMOVED path="+paths[2]+" reason=pr_closed\n", "no pr_closed line for %s in %q", paths[2], out)
	require.True(t, strings.HasSuffix(strings.TrimSpace(out), "WORKTREE OK removed=2 kept=1"), "the prune summary is not removed=2 kept=1: %q", out)
	for _, id := range []int{1, 2} {
		_, err := os.Stat(paths[id])
		require.ErrorIs(t, err, fs.ErrNotExist, "the tree for --pr %d is still there", id)
	}
	_, err := os.Stat(paths[3])
	require.NoError(t, err, "the open PR's tree (%s) was removed: %s", paths[3], err)
}

// 5. --prune with a fake clock one day and one minute past a tree's guid mtime
// removes it as reason=stale while one minute under a day is kept, and a stale
// tree the fake process probe reports in use is kept with no line.
func TestWorktreePruneStaleUsesTheClockAndTheProbe(t *testing.T) {
	t.Parallel()

	j := newWJob(t)
	g := newFakeGit(j.repo)
	j.useFakeGit(g)
	sha := strings.Repeat("e", 40)
	ff := &fakeForge{byID: map[int]worktreePR{}}
	for _, id := range []int{1, 2, 3} {
		ff.byID[id] = worktreePR{Head: sha, Base: "main", State: "open"}
	}
	j.useForge(ff)

	paths := map[int]string{}
	for _, id := range []int{1, 2, 3} {
		paths[id] = createTree(t, j, sha, fmt.Sprintf("%d", id))
	}

	now := time.Now()
	j.ws.Now = func() time.Time { return now }

	inUse := map[string]bool{}
	j.ws.InUse = func(dir string) bool { return inUse[dir] }

	touch := func(path string, age time.Duration) {
		stamp := now.Add(-age)
		require.NoError(t, os.Chtimes(path, stamp, stamp))
	}
	touch(paths[1], 24*time.Hour+time.Minute) // stale, not in use -> removed
	touch(paths[2], 24*time.Hour-time.Minute) // under a day -> kept
	touch(paths[3], 24*time.Hour+time.Minute) // stale, in use -> kept with no line
	inUse[paths[3]] = true

	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--prune")
	require.Equal(t, 0, code, "exit %d, want 0; stderr %q", code, errb)
	require.Contains(t, out, "WORKTREE REMOVED path="+paths[1]+" reason=stale\n", "no stale removal for %s in %q", paths[1], out)
	require.NotContains(t, out, paths[2], "the under-a-day tree %s was named in %q", paths[2], out)
	require.NotContains(t, out, paths[3], "the in-use tree %s was named in %q", paths[3], out)
	require.True(t, strings.HasSuffix(strings.TrimSpace(out), "WORKTREE OK removed=1 kept=2"), "the prune summary is not removed=1 kept=2: %q", out)
}

// 6. --prune over a fake git worktree list holding a hand-made worktree no
// record names leaves it byte-identical and prints removed=0 kept=<n>.
func TestWorktreePruneLeavesTheHandMadeWorktree(t *testing.T) {
	t.Parallel()

	j := newWJob(t)
	g := newFakeGit(j.repo)
	hand := filepath.Join(j.scratch, "hand-made")
	require.NoError(t, os.MkdirAll(hand, 0o755))
	keep := filepath.Join(hand, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("hello\n"), 0o644))
	g.trees[hand] = strings.Repeat("f", 40)
	j.useFakeGit(g)
	j.useForge(&fakeForge{byID: map[int]worktreePR{}})

	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--prune")
	require.Equal(t, 0, code, "a prune that removed nothing is the verb doing its job and must exit 0, got %d; stderr %q", code, errb)
	require.Contains(t, out, "WORKTREE OK removed=0 kept=1\n", "stdout %q, want a removed=0 kept=1 summary", out)
	got, err := os.ReadFile(keep)
	require.NoError(t, err, "the hand-made worktree was touched: %q, %v", got, err)
	require.Equal(t, "hello\n", string(got), "the hand-made worktree was touched: %q, %v", got, err)
}

// 7. No --repo, no --scratch, --repo <tmp>/not-a-repo, --scratch <tmp>/absent,
// --pr 0, --pr abc and --pr --prune are each exit 2 with one remedy line, and
// the absent scratch dir still does not exist.
func TestWorktreeRefusals(t *testing.T) {
	t.Parallel()

	j := newWJob(t)
	g := newFakeGit(j.repo)
	j.useFakeGit(g)
	j.useForge(&fakeForge{byID: map[int]worktreePR{}})
	absent := filepath.Join(j.base, "absent")
	remedy := "run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id>"

	cases := []struct {
		name   string
		args   []string
		reason string
		says   string
	}{
		{"no repo", []string{"--scratch", j.scratch, "--pr", "1"}, "bad_repo", ""},
		{"no scratch", []string{"--repo", j.repo, "--pr", "1"}, "bad_scratch", ""},
		{"not a repo", []string{"--repo", filepath.Join(j.base, "not-a-repo"), "--scratch", j.scratch, "--pr", "1"}, "bad_repo", ""},
		{"relative repo", []string{"--repo", ".", "--scratch", j.scratch, "--pr", "1"}, "bad_repo", "--repo wants an existing repository named by an absolute path"},
		{"absent scratch", []string{"--repo", j.repo, "--scratch", absent, "--pr", "1"}, "bad_scratch", ""},
		{"pr zero", []string{"--repo", j.repo, "--scratch", j.scratch, "--pr", "0"}, "bad_pr", ""},
		{"pr abc", []string{"--repo", j.repo, "--scratch", j.scratch, "--pr", "abc"}, "bad_pr", ""},
		{"pr with prune", []string{"--repo", j.repo, "--scratch", j.scratch, "--pr", "1", "--prune"}, "bad_pr", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errb := j.tool(t, nil, c.args...)
			require.Equal(t, 2, code, "exit %d, want 2; stderr %q", code, errb)
			require.Empty(t, out, "a refusal wrote to stdout: %q", out)
			require.Contains(t, errb, "WORKTREE REFUSED reason="+c.reason+":", "stderr %q does not refuse reason=%s", errb, c.reason)
			if c.says != "" {
				require.Contains(t, errb, c.says, "stderr %q does not say %q", errb, c.says)
			}
			require.Contains(t, errb, remedy, "stderr %q carries no remedy line %q", errb, remedy)
		})
	}
	_, err := os.Stat(absent)
	require.ErrorIs(t, err, fs.ErrNotExist, "a refusal created the scratch dir %s", absent)
}

// 9. --prune over a scratch holding one open-PR worktree and nothing prunable
// is the verb doing its job: it prints WORKTREE OK removed=0 kept=1 and exits 0.
func TestWorktreePruneKeptOpenExitsZero(t *testing.T) {
	t.Parallel()

	j := newWJob(t)
	g := newFakeGit(j.repo)
	j.useFakeGit(g)
	sha := strings.Repeat("9", 40)
	j.useForge(&fakeForge{byID: map[int]worktreePR{7: {Head: sha, Base: "main", State: "open"}}})
	createTree(t, j, sha, "7")

	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--prune")
	require.Equal(t, 0, code, "a prune that kept an open PR must exit 0, got %d; stderr %q", code, errb)
	require.Contains(t, out, "WORKTREE OK removed=0 kept=1\n", "stdout %q, want a removed=0 kept=1 summary", out)
}

// 10. --prune over an empty scratch is the verb doing its job: it prints
// WORKTREE OK removed=0 kept=0 and exits 0.
func TestWorktreePruneEmptyScratchExitsZero(t *testing.T) {
	t.Parallel()

	j := newWJob(t)
	g := newFakeGit(j.repo)
	j.useFakeGit(g)
	j.useForge(&fakeForge{byID: map[int]worktreePR{}})

	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--prune")
	require.Equal(t, 0, code, "a prune of an empty scratch must exit 0, got %d; stderr %q", code, errb)
	require.Contains(t, out, "WORKTREE OK removed=0 kept=0\n", "stdout %q, want a removed=0 kept=0 summary", out)
}

// 8. A fake forge token in the environment appears on no line, scanned over
// every byte the verb wrote.
func TestWorktreeNeverPrintsTheToken(t *testing.T) {
	t.Parallel()

	j := newWJob(t)
	g := newFakeGit(j.repo)
	j.useFakeGit(g)
	sha := strings.Repeat("0", 40)
	j.useForge(&fakeForge{byID: map[int]worktreePR{5: {Head: sha, Base: "main", State: "open"}}})
	const token = "sekret-forge-token-do-not-print"

	code, out, errb := j.tool(t, []string{"GH_TOKEN=" + token}, "--repo", j.repo, "--scratch", j.scratch, "--pr", "5")
	require.Equal(t, 0, code, "exit %d, want 0; stderr %q", code, errb)
	require.NotContains(t, out+errb, token, "the token leaked into the verb's output: %q", out+errb)
}

// 9. The owner and name the forge client names come out of the origin remote's
// path in every shape git stores a remote in, an ssh Host alias standing where
// the forge's own name would included, and a remote whose path names no
// owner/name yields nothing at all.
func TestWorktreeParseOwnerRepo(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, url, want string }{
		{"https with .git", "https://example.com/o/n.git", "o/n"},
		{"https without .git", "https://example.com/o/n", "o/n"},
		{"scp-like", "git@example.com:o/n.git", "o/n"},
		{"ssh url", "ssh://git@example.com/o/n.git", "o/n"},
		{"ssh url with a port", "ssh://git@example.com:22/o/n.git", "o/n"},
		{"ssh host alias", "git@forge-alias:o/n.git", "o/n"},
		{"owner and name alone", "o/n", "o/n"},
		{"alias with no path", "git@forge-alias:", ""},
		{"host with no path", "https://example.com/", ""},
		{"one path word", "https://example.com/n.git", ""},
		{"nothing", "", ""},
		{"file url", "file:///tmp/x/o/n.git", ""},
		{"relative path up", "../o/n.git", ""},
		{"relative path here", "./o/n", ""},
		{"absolute path", "/abs/path/o/n.git", ""},
		{"drive letter", "C:/repos/o/n", ""},
		{"drive letter backslashes", `C:\repos\o\n`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseOwnerRepo(c.url)
			require.Equal(t, c.want, got, "parseOwnerRepo(%q) = %q, want %q", c.url, got, c.want)
		})
	}
}

// 10. An origin remote the forge client can read no owner/name out of, and a
// repository with no origin remote at all, are errBadOrigin and not a forge that
// could not be reached, and the error names the origin it read.
func TestGhForgeBadOriginIsNotAnOutage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		git  gitRunner
		says string
	}{
		{"origin names no owner/name", func(string, ...string) (string, error) {
			return "git@forge-alias:\n", nil
		}, "git@forge-alias:"},
		{"no origin remote", func(string, ...string) (string, error) {
			return "", fmt.Errorf("fatal: no such remote 'origin'")
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, err := ghForge{repo: t.TempDir(), git: c.git}.PR(7)
			require.ErrorIs(t, err, errBadOrigin, "PR error %v, want errBadOrigin", err)
			require.NotErrorIs(t, err, errNoForge, "PR error %v reads as a forge that could not be reached", err)
			if c.says != "" {
				require.Contains(t, err.Error(), c.says, "PR error %v does not name the origin it read (%q)", err, c.says)
			}
		})
	}
}

// 11. The forge seam's three failures each refuse in their own words: an origin
// with no owner/name says what it wants and what it read and carries the plain
// remedy, while the two that are the forge's own carry the retry line.
func TestWorktreeForgeRefusalsSayWhichFailureItWas(t *testing.T) {
	t.Parallel()

	j := newWJob(t)
	remedy := "run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id>"
	retry := "retry once the forge answers"

	cases := []struct {
		name   string
		err    error
		reason string
		says   []string
		wants  string
	}{
		{"unknown pr", errNoPR, "no_pr", []string{"the forge does not know this pull request"}, retry},
		{"forge unreachable", errNoForge, "no_forge", []string{"the forge could not be reached"}, retry},
		{"origin with no owner/name", badOrigin("git@forge-alias:"), "bad_origin",
			[]string{"--repo wants an origin remote", "git@forge-alias:"}, remedy},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newFakeGit(j.repo)
			j.useFakeGit(g)
			j.useForge(errForge{err: c.err})

			code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--pr", "9")
			require.Equal(t, 2, code, "exit %d, want 2; stderr %q", code, errb)
			require.Empty(t, out, "a refusal wrote to stdout: %q", out)
			require.Contains(t, errb, "WORKTREE REFUSED reason="+c.reason+":", "stderr %q does not refuse reason=%s", errb, c.reason)
			for _, says := range c.says {
				require.Contains(t, errb, says, "stderr %q does not say %q", errb, says)
			}
			require.Contains(t, errb, c.wants, "stderr %q carries no %q", errb, c.wants)
			if c.wants == remedy {
				require.NotContains(t, errb, retry, "stderr %q tells the reader to retry a bad origin", errb)
			}
		})
	}
}

// A fetch that hit its deadline is reported by name; any other fetch failure is tolerated
// (the head may already be here) and left to the add.
func TestFetchHeadReportsATimeoutAndToleratesTheRest(t *testing.T) {
	t.Parallel()

	timedOut := func(string, ...string) (string, error) {
		return "", &subproc.TimeoutError{What: "git fetch origin abc", Budget: 5 * time.Minute, Err: errors.New("signal: killed")}
	}
	err := fetchHead(timedOut, "/repo", "abc")
	var te *subproc.TimeoutError
	require.Error(t, err, "a timed-out fetch was reported as %v", err)
	require.ErrorAs(t, err, &te, "a timed-out fetch was reported as %v", err)
	require.Contains(t, err.Error(), "fetching abc from origin", "a timed-out fetch was reported as %v", err)
	refused := func(string, ...string) (string, error) { return "", errors.New("fatal: couldn't find remote ref") }
	err = fetchHead(refused, "/repo", "abc")
	require.NoError(t, err, "an ordinary fetch failure was reported: %v", err)
}

// A forge head that is not a whole commit id -- an option such as
// --upload-pack=..., a ref, a short or uppercase string -- is refused, with the
// shape named, before it can reach a git argv, and the fetch a good head takes
// puts -- before it so git reads it only as an operand.
func TestAForgeHeadThatIsNotAShaIsRefused(t *testing.T) {
	t.Parallel()

	bad := []string{
		"--upload-pack=touch /tmp/pwn",
		"-c",
		"HEAD",
		"abc",
		strings.Repeat("a", 39),
		strings.Repeat("a", 41),
		strings.Repeat("A", 40),
		strings.Repeat("g", 40),
		"",
	}
	for _, head := range bad {
		_, _, err := forgeHead(&fakeForge{byID: map[int]worktreePR{7: {Head: head, Base: "main", State: "open"}}}, 7)
		if assert.Error(t, err, "a head that is not a sha was accepted: %q", head) {
			assert.Contains(t, err.Error(), "forty lowercase hex characters", "head %q: error %v does not name the shape", head, err)
		}
	}

	sha := "0123456789abcdef0123456789abcdef01234567"
	if _, _, err := forgeHead(&fakeForge{byID: map[int]worktreePR{7: {Head: sha, Base: "main", State: "open"}}}, 7); err != nil {
		require.NoError(t, err, "a well-formed head %q was refused", sha)
	}

	var got []string
	spy := func(dir string, args ...string) (string, error) {
		got = append([]string{dir}, args...)
		return "", nil
	}
	require.NoError(t, fetchHead(spy, "/repo", sha))
	require.Equal(t, []string{"/repo", "fetch", "origin", "--", sha}, got, "the fetch argv does not put -- before the head")
}

package main

// The worktree verb's tests, written first from docs/SPEC-SANDBOX.md's numbered
// list: a fake forge, a fake `git` behind the tool's one seam, a fake clock and
// a fake process probe, so no test touches a network, a real forge or the clock.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
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

func useFakeGit(t *testing.T, g *fakeGit) {
	t.Helper()
	old := worktreeGit
	worktreeGit = g.run
	t.Cleanup(func() { worktreeGit = old })
}

// errForge is the forge seam for the failure tests: every call answers one
// error.
type errForge struct{ err error }

func (f errForge) PR(int) (worktreePR, error) { return worktreePR{}, f.err }

func useForge(t *testing.T, f worktreeForge) {
	t.Helper()
	old := worktreeForgeFactory
	worktreeForgeFactory = func(repo string, env []string) worktreeForge { return f }
	t.Cleanup(func() { worktreeForgeFactory = old })
}

type wjob struct{ base, repo, scratch string }

func newWJob(t *testing.T) wjob {
	t.Helper()
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	j := wjob{base: base, repo: filepath.Join(base, "repo"), scratch: filepath.Join(base, "scratch")}
	for _, d := range []string{j.repo, j.scratch} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return j
}

func (j wjob) tool(t *testing.T, env []string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"worktree"}, args...), nil, &out, &errb, env)
	return code, out.String(), errb.String()
}

func recordGUID(t *testing.T, scratch string, id int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(scratch, fmt.Sprintf("%d.pr", id)))
	if err != nil {
		t.Fatalf("the record file is not there: %s", err)
	}
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
	if code != 0 {
		t.Fatalf("creating the tree for --pr %s: exit %d, stderr %q", id, code, errb)
	}
	expect := "WORKTREE OK path=" + filepath.Join(j.scratch, recordGUID(t, j.scratch, atoi(t, id))) + " head=" + sha + "\n"
	if out != expect {
		t.Fatalf("creating the tree for --pr %s: stdout %q, want %q", id, out, expect)
	}
	return filepath.Join(j.scratch, recordGUID(t, j.scratch, atoi(t, id)))
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		t.Fatalf("%q is not a number", s)
	}
	return n
}

// 1. A fake forge answering head <sha> makes the verb print exactly WORKTREE OK
// path=<tmp>/<guid> head=<sha>, and the tree and its .git file exist.
func TestWorktreeMaterialisesThePRHead(t *testing.T) {
	j := newWJob(t)
	g := newFakeGit(j.repo)
	useFakeGit(t, g)
	sha := strings.Repeat("a", 40)
	useForge(t, &fakeForge{byID: map[int]worktreePR{7: {Head: sha, Base: "main", State: "open"}}})

	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--pr", "7")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errb)
	}
	guid := recordGUID(t, j.scratch, 7)
	if want := okLine(j.scratch, guid, sha); out != want {
		t.Fatalf("stdout %q, want %q", out, want)
	}
	if _, err := os.Stat(filepath.Join(j.scratch, guid, ".git")); err != nil {
		t.Fatalf("the worktree's .git file is not there: %s", err)
	}
}

// 2. A second call for the same --pr reuses the first path, prints the same
// line, and adds no second entry to the fake git worktree call log.
func TestWorktreeSecondCallReusesTheTree(t *testing.T) {
	j := newWJob(t)
	g := newFakeGit(j.repo)
	useFakeGit(t, g)
	sha := strings.Repeat("b", 40)
	useForge(t, &fakeForge{byID: map[int]worktreePR{7: {Head: sha, Base: "main", State: "open"}}})

	code, first, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--pr", "7")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errb)
	}
	g.log = nil
	code, second, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--pr", "7")
	if code != 0 {
		t.Fatalf("second exit %d, want 0; stderr %q", code, errb)
	}
	if second != first {
		t.Fatalf("the second call printed %q, want the first line %q", second, first)
	}
	if n := g.addCount(); n != 0 {
		t.Fatalf("the reuse ran %d git worktree add calls, want 0", n)
	}
}

// 3. A record whose tree the test deletes is rebuilt on the next call with
// exactly one git worktree add.
func TestWorktreeDeletedTreeIsRebuilt(t *testing.T) {
	j := newWJob(t)
	g := newFakeGit(j.repo)
	useFakeGit(t, g)
	sha := strings.Repeat("c", 40)
	useForge(t, &fakeForge{byID: map[int]worktreePR{7: {Head: sha, Base: "main", State: "open"}}})

	path := createTree(t, j, sha, "7")
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	g.log = nil
	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--pr", "7")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errb)
	}
	if n := g.addCount(); n != 1 {
		t.Fatalf("the rebuild ran %d git worktree add calls, want exactly 1", n)
	}
	if want := okLine(j.scratch, recordGUID(t, j.scratch, 7), sha); out != want {
		t.Fatalf("stdout %q, want %q", out, want)
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		t.Fatalf("the rebuilt tree has no .git file: %s", err)
	}
}

// 4. --prune with a fake forge reporting one PR merged and one closed prints
// one WORKTREE REMOVED reason=pr_merged and one reason=pr_closed, removes both
// trees, and leaves a third open PR's tree standing.
func TestWorktreePruneRemovesMergedAndClosedAndKeepsOpen(t *testing.T) {
	j := newWJob(t)
	g := newFakeGit(j.repo)
	useFakeGit(t, g)
	sha := strings.Repeat("d", 40)
	ff := &fakeForge{byID: map[int]worktreePR{}}
	for _, id := range []int{1, 2, 3} {
		ff.byID[id] = worktreePR{Head: sha, Base: "main", State: "open"}
	}
	useForge(t, ff)

	paths := map[int]string{}
	for _, id := range []int{1, 2, 3} {
		paths[id] = createTree(t, j, sha, fmt.Sprintf("%d", id))
	}
	ff.byID[1] = worktreePR{Head: sha, Base: "main", State: "merged"}
	ff.byID[2] = worktreePR{Head: sha, Base: "main", State: "closed"}

	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--prune")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errb)
	}
	if !strings.Contains(out, "WORKTREE REMOVED path="+paths[1]+" reason=pr_merged\n") {
		t.Fatalf("no pr_merged line for %s in %q", paths[1], out)
	}
	if !strings.Contains(out, "WORKTREE REMOVED path="+paths[2]+" reason=pr_closed\n") {
		t.Fatalf("no pr_closed line for %s in %q", paths[2], out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "WORKTREE OK removed=2 kept=1") {
		t.Fatalf("the prune summary is not removed=2 kept=1: %q", out)
	}
	for _, id := range []int{1, 2} {
		if _, err := os.Stat(paths[id]); !os.IsNotExist(err) {
			t.Fatalf("the tree for --pr %d is still there", id)
		}
	}
	if _, err := os.Stat(paths[3]); err != nil {
		t.Fatalf("the open PR's tree (%s) was removed: %s", paths[3], err)
	}
}

// 5. --prune with a fake clock one day and one minute past a tree's guid mtime
// removes it as reason=stale while one minute under a day is kept, and a stale
// tree the fake process probe reports in use is kept with no line.
func TestWorktreePruneStaleUsesTheClockAndTheProbe(t *testing.T) {
	j := newWJob(t)
	g := newFakeGit(j.repo)
	useFakeGit(t, g)
	sha := strings.Repeat("e", 40)
	ff := &fakeForge{byID: map[int]worktreePR{}}
	for _, id := range []int{1, 2, 3} {
		ff.byID[id] = worktreePR{Head: sha, Base: "main", State: "open"}
	}
	useForge(t, ff)

	paths := map[int]string{}
	for _, id := range []int{1, 2, 3} {
		paths[id] = createTree(t, j, sha, fmt.Sprintf("%d", id))
	}

	now := time.Now()
	old := worktreeNow
	worktreeNow = func() time.Time { return now }
	t.Cleanup(func() { worktreeNow = old })

	oldProbe := worktreeInUse
	inUse := map[string]bool{}
	worktreeInUse = func(dir string) bool { return inUse[dir] }
	t.Cleanup(func() { worktreeInUse = oldProbe })

	touch := func(path string, age time.Duration) {
		stamp := now.Add(-age)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	touch(paths[1], 24*time.Hour+time.Minute) // stale, not in use -> removed
	touch(paths[2], 24*time.Hour-time.Minute) // under a day -> kept
	touch(paths[3], 24*time.Hour+time.Minute) // stale, in use -> kept with no line
	inUse[paths[3]] = true

	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--prune")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errb)
	}
	if !strings.Contains(out, "WORKTREE REMOVED path="+paths[1]+" reason=stale\n") {
		t.Fatalf("no stale removal for %s in %q", paths[1], out)
	}
	if strings.Contains(out, paths[2]) {
		t.Fatalf("the under-a-day tree %s was named in %q", paths[2], out)
	}
	if strings.Contains(out, paths[3]) {
		t.Fatalf("the in-use tree %s was named in %q", paths[3], out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "WORKTREE OK removed=1 kept=2") {
		t.Fatalf("the prune summary is not removed=1 kept=2: %q", out)
	}
}

// 6. --prune over a fake git worktree list holding a hand-made worktree no
// record names leaves it byte-identical and prints removed=0 kept=<n>.
func TestWorktreePruneLeavesTheHandMadeWorktree(t *testing.T) {
	j := newWJob(t)
	g := newFakeGit(j.repo)
	hand := filepath.Join(j.scratch, "hand-made")
	if err := os.MkdirAll(hand, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(hand, "keep.txt")
	if err := os.WriteFile(keep, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g.trees[hand] = strings.Repeat("f", 40)
	useFakeGit(t, g)
	useForge(t, &fakeForge{byID: map[int]worktreePR{}})

	code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--prune")
	if code != 1 {
		t.Fatalf("a prune that removed nothing must exit 1, got %d; stderr %q", code, errb)
	}
	if !strings.Contains(out, "WORKTREE OK removed=0 kept=1\n") {
		t.Fatalf("stdout %q, want a removed=0 kept=1 summary", out)
	}
	got, err := os.ReadFile(keep)
	if err != nil || string(got) != "hello\n" {
		t.Fatalf("the hand-made worktree was touched: %q, %v", got, err)
	}
}

// 7. No --repo, no --scratch, --repo <tmp>/not-a-repo, --scratch <tmp>/absent,
// --pr 0, --pr abc and --pr --prune are each exit 2 with one remedy line, and
// the absent scratch dir still does not exist.
func TestWorktreeRefusals(t *testing.T) {
	j := newWJob(t)
	g := newFakeGit(j.repo)
	useFakeGit(t, g)
	useForge(t, &fakeForge{byID: map[int]worktreePR{}})
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
			if code != 2 {
				t.Fatalf("exit %d, want 2; stderr %q", code, errb)
			}
			if out != "" {
				t.Fatalf("a refusal wrote to stdout: %q", out)
			}
			if !strings.Contains(errb, "WORKTREE REFUSED reason="+c.reason+":") {
				t.Fatalf("stderr %q does not refuse reason=%s", errb, c.reason)
			}
			if c.says != "" && !strings.Contains(errb, c.says) {
				t.Fatalf("stderr %q does not say %q", errb, c.says)
			}
			if !strings.Contains(errb, remedy) {
				t.Fatalf("stderr %q carries no remedy line %q", errb, remedy)
			}
		})
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatalf("a refusal created the scratch dir %s", absent)
	}
}

// 8. A fake forge token in the environment appears on no line, scanned over
// every byte the verb wrote.
func TestWorktreeNeverPrintsTheToken(t *testing.T) {
	j := newWJob(t)
	g := newFakeGit(j.repo)
	useFakeGit(t, g)
	sha := strings.Repeat("0", 40)
	useForge(t, &fakeForge{byID: map[int]worktreePR{5: {Head: sha, Base: "main", State: "open"}}})
	const token = "sekret-forge-token-do-not-print"

	code, out, errb := j.tool(t, []string{"GH_TOKEN=" + token}, "--repo", j.repo, "--scratch", j.scratch, "--pr", "5")
	if code != 0 {
		t.Fatalf("exit %d, want 0; stderr %q", code, errb)
	}
	if strings.Contains(out+errb, token) {
		t.Fatalf("the token leaked into the verb's output: %q", out+errb)
	}
}

// 9. The owner and name the forge client names come out of the origin remote's
// path in every shape git stores a remote in, an ssh Host alias standing where
// the forge's own name would included, and a remote whose path names no
// owner/name yields nothing at all.
func TestWorktreeParseOwnerRepo(t *testing.T) {
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
			if got := parseOwnerRepo(c.url); got != c.want {
				t.Fatalf("parseOwnerRepo(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}
}

// 10. An origin remote the forge client can read no owner/name out of, and a
// repository with no origin remote at all, are errBadOrigin and not a forge that
// could not be reached, and the error names the origin it read.
func TestGhForgeBadOriginIsNotAnOutage(t *testing.T) {
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
			old := worktreeGit
			worktreeGit = c.git
			t.Cleanup(func() { worktreeGit = old })

			_, err := ghForge{repo: t.TempDir()}.PR(7)
			if !errors.Is(err, errBadOrigin) {
				t.Fatalf("PR error %v, want errBadOrigin", err)
			}
			if errors.Is(err, errNoForge) {
				t.Fatalf("PR error %v reads as a forge that could not be reached", err)
			}
			if c.says != "" && !strings.Contains(err.Error(), c.says) {
				t.Fatalf("PR error %v does not name the origin it read (%q)", err, c.says)
			}
		})
	}
}

// 11. The forge seam's three failures each refuse in their own words: an origin
// with no owner/name says what it wants and what it read and carries the plain
// remedy, while the two that are the forge's own carry the retry line.
func TestWorktreeForgeRefusalsSayWhichFailureItWas(t *testing.T) {
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
			useFakeGit(t, g)
			useForge(t, errForge{err: c.err})

			code, out, errb := j.tool(t, nil, "--repo", j.repo, "--scratch", j.scratch, "--pr", "9")
			if code != 2 {
				t.Fatalf("exit %d, want 2; stderr %q", code, errb)
			}
			if out != "" {
				t.Fatalf("a refusal wrote to stdout: %q", out)
			}
			if !strings.Contains(errb, "WORKTREE REFUSED reason="+c.reason+":") {
				t.Fatalf("stderr %q does not refuse reason=%s", errb, c.reason)
			}
			for _, says := range c.says {
				if !strings.Contains(errb, says) {
					t.Fatalf("stderr %q does not say %q", errb, says)
				}
			}
			if !strings.Contains(errb, c.wants) {
				t.Fatalf("stderr %q carries no %q", errb, c.wants)
			}
			if c.wants == remedy && strings.Contains(errb, retry) {
				t.Fatalf("stderr %q tells the reader to retry a bad origin", errb)
			}
		})
	}
}

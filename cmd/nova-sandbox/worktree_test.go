package main

// The worktree verb's tests, written first from docs/SPEC-SANDBOX.md's numbered
// list: a fake forge, a fake `git` behind the tool's one seam, a fake clock and
// a fake process probe, so no test touches a network, a real forge or the clock.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
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

func useFakeGit(t *testing.T, g *fakeGit) {
	t.Helper()
	swap(t, &worktreeGit, g.run)
}

// errForge is the forge seam for the failure tests: every call answers one error.
type errForge struct{ err error }

func (f errForge) PR(int) (worktreePR, error) { return worktreePR{}, f.err }

func useForge(t *testing.T, f worktreeForge) {
	t.Helper()
	swap(t, &worktreeForgeFactory, func(repo string, env []string) worktreeForge { return f })
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
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	return j
}

// runEnv is `nova-sandbox worktree <args>` under env; on is the same with no environment
// and against this job's repo and scratch, which is every call but the flag tests'.
func (j wjob) runEnv(t *testing.T, env []string, args ...string) testkit.Ran {
	t.Helper()
	return withEnv(run, env).Do(t, append([]string{"worktree"}, args...)...)
}

func (j wjob) on(t *testing.T, args ...string) testkit.Ran {
	t.Helper()
	return j.runEnv(t, nil, append([]string{"--repo", j.repo, "--scratch", j.scratch}, args...)...)
}

// wbench is a worktree job with the fake git and a fake forge in place, the forge
// answering every pr in ids as open at head sha.
type wbench struct {
	wjob
	g   *fakeGit
	ff  *fakeForge
	sha string
}

func newWBench(t *testing.T, sha string, ids ...int) wbench {
	t.Helper()
	b := wbench{wjob: newWJob(t), ff: &fakeForge{byID: map[int]worktreePR{}}, sha: sha}
	b.g = newFakeGit(b.repo)
	for _, id := range ids {
		b.ff.byID[id] = worktreePR{Head: sha, Base: "main", State: "open"}
	}
	useFakeGit(t, b.g)
	useForge(t, b.ff)
	return b
}

// create runs --pr id under env and requires exit 0 and exactly WORKTREE OK
// path=<scratch>/<guid> head=<sha>, the guid read off the record; it returns the run and
// the tree's path.
func (b wbench) create(t *testing.T, id int, env ...string) (testkit.Ran, string) {
	t.Helper()
	r := b.runEnv(t, env, "--repo", b.repo, "--scratch", b.scratch, "--pr", strconv.Itoa(id)).Exit(0)
	raw := testkit.ReadFile(t, filepath.Join(b.scratch, fmt.Sprintf("%d.pr", id)))
	for _, line := range strings.Split(raw, "\n") {
		if guid, ok := strings.CutPrefix(line, "guid="); ok {
			path := filepath.Join(b.scratch, guid)
			require.Equal(t, "WORKTREE OK path="+path+" head="+b.sha+"\n", r.Stdout, r)
			return r, path
		}
	}
	t.Fatalf("the record file has no guid= line: %q", raw)
	return r, ""
}

// The tree's life from docs/SPEC-SANDBOX.md's list, one step on the next: 1. a fake forge
// answering head <sha> makes the verb print exactly the WORKTREE OK line and the tree's
// .git file exists, with 8. a forge token in the environment on no byte the verb wrote;
// 2. a second call for the same --pr prints the same line with no git worktree add; 3. a
// tree deleted under its record is rebuilt with exactly one.
func TestWorktreeMaterialisesThePRHead(t *testing.T) {
	const token = "sekret-forge-token-do-not-print"
	b := newWBench(t, strings.Repeat("a", 40), 7)
	var first testkit.Ran
	var path string
	require.True(t, t.Run("materialises the PR head and never prints the token", func(t *testing.T) {
		first, path = b.create(t, 7, "GH_TOKEN="+token)
		require.NotContains(t, first.Stdout+first.Stderr, token, "the token leaked into the verb's output")
		require.NoError(t, statErr(filepath.Join(path, ".git")))
	}))
	require.True(t, t.Run("second call reuses the tree", func(t *testing.T) {
		b.g.log = nil
		r, _ := b.create(t, 7)
		require.Equal(t, first.Stdout, r.Stdout, r)
		require.Equal(t, 0, b.g.addCount(), "the reuse ran git worktree add")
	}))
	t.Run("deleted tree is rebuilt", func(t *testing.T) {
		require.NoError(t, os.RemoveAll(path))
		b.g.log = nil
		b.create(t, 7)
		require.Equal(t, 1, b.g.addCount(), "the rebuild's git worktree add calls")
		require.NoError(t, statErr(filepath.Join(path, ".git")))
	})
}

// Every --prune here is the verb doing its job, whatever it removed, and exits 0.
func TestWorktreePruneStaleUsesTheClockAndTheProbe(t *testing.T) {
	sha := strings.Repeat("d", 40)
	// 4. One PR merged and one closed are each removed with their reason; an open one stands.
	t.Run("merged and closed removed, open kept", func(t *testing.T) {
		b := newWBench(t, sha, 1, 2, 3)
		paths := map[int]string{}
		for _, id := range []int{1, 2, 3} {
			_, paths[id] = b.create(t, id)
		}
		b.ff.byID[1] = worktreePR{Head: sha, Base: "main", State: "merged"}
		b.ff.byID[2] = worktreePR{Head: sha, Base: "main", State: "closed"}
		r := b.on(t, "--prune").Exit(0).Out(
			"WORKTREE REMOVED path="+paths[1]+" reason=pr_merged\n",
			"WORKTREE REMOVED path="+paths[2]+" reason=pr_closed\n")
		require.True(t, strings.HasSuffix(strings.TrimSpace(r.Stdout), "WORKTREE OK removed=2 kept=1"), r)
		for _, id := range []int{1, 2} {
			require.ErrorIs(t, statErr(paths[id]), fs.ErrNotExist, "the tree for --pr %d is still there", id)
		}
		require.NoError(t, statErr(paths[3]), "the open PR's tree was removed")
	})
	// 6. A hand-made worktree in git's list that no record names is left byte-identical.
	t.Run("hand-made worktree left alone", func(t *testing.T) {
		b := newWBench(t, sha)
		keep := filepath.Join(b.scratch, "hand-made", "keep.txt")
		testkit.WriteFile(t, keep, "hello\n")
		b.g.trees[filepath.Dir(keep)] = strings.Repeat("f", 40)
		b.on(t, "--prune").Exit(0).Out("WORKTREE OK removed=0 kept=1\n")
		require.Equal(t, "hello\n", testkit.ReadFile(t, keep), "the hand-made worktree was touched")
	})
	// 9. One open PR's tree and nothing prunable.
	t.Run("kept open exits zero", func(t *testing.T) {
		b := newWBench(t, sha, 7)
		b.create(t, 7)
		b.on(t, "--prune").Exit(0).Out("WORKTREE OK removed=0 kept=1\n")
	})
	// 10. An empty scratch.
	t.Run("empty scratch exits zero", func(t *testing.T) {
		newWBench(t, sha).on(t, "--prune").Exit(0).Out("WORKTREE OK removed=0 kept=0\n")
	})
	// 5. A fake clock one day and one minute past a tree's guid mtime removes it as
	// reason=stale; one a minute under a day is kept, and so is a stale tree the fake process
	// probe reports in use, with no line for either.
	t.Run("stale uses the clock and the probe", func(t *testing.T) {
		b := newWBench(t, sha, 1, 2, 3)
		paths := map[int]string{}
		for _, id := range []int{1, 2, 3} {
			_, paths[id] = b.create(t, id)
		}
		now := time.Now()
		swap(t, &worktreeNow, func() time.Time { return now })
		swap(t, &worktreeInUse, func(dir string) bool { return dir == paths[3] })
		for id, age := range map[int]time.Duration{1: 24*time.Hour + time.Minute, 2: 24*time.Hour - time.Minute, 3: 24*time.Hour + time.Minute} {
			require.NoError(t, os.Chtimes(paths[id], now.Add(-age), now.Add(-age)))
		}
		r := b.on(t, "--prune").Exit(0).Out("WORKTREE REMOVED path="+paths[1]+" reason=stale\n").NotOut(paths[2], paths[3])
		require.True(t, strings.HasSuffix(strings.TrimSpace(r.Stdout), "WORKTREE OK removed=1 kept=2"), r)
	})
}

// 7. No --repo, no --scratch, --repo <tmp>/not-a-repo or relative, --scratch <tmp>/absent,
// --pr 0, --pr abc and --pr --prune, and 11. the forge seam's three failures: each is exit
// 2, nothing on stdout, a WORKTREE REFUSED reason=<r> line in its own words and one remedy.
// The forge's own two failures carry the retry line; an origin with no owner/name says
// what it wants and what it read and carries the plain remedy, never the retry. The absent
// scratch dir still does not exist afterwards.
func TestWorktreeRefusals(t *testing.T) {
	b := newWBench(t, "")
	absent := filepath.Join(b.base, "absent")
	remedy := "run: nova-sandbox worktree --repo <dir> --scratch <dir> --pr <id>"
	retry := "retry once the forge answers"
	at := func(args ...string) []string {
		return append([]string{"--repo", b.repo, "--scratch", b.scratch}, args...)
	}
	for _, c := range []struct {
		name, reason string
		args         []string
		forge        error // the forge seam's one answer, for the forge's rows
		says         []string
		remedy       string
	}{
		{"no repo", "bad_repo", []string{"--scratch", b.scratch, "--pr", "1"}, nil, nil, remedy},
		{"no scratch", "bad_scratch", []string{"--repo", b.repo, "--pr", "1"}, nil, nil, remedy},
		{"not a repo", "bad_repo", []string{"--repo", filepath.Join(b.base, "not-a-repo"), "--scratch", b.scratch, "--pr", "1"}, nil, nil, remedy},
		{"relative repo", "bad_repo", []string{"--repo", ".", "--scratch", b.scratch, "--pr", "1"}, nil,
			[]string{"--repo wants an existing repository named by an absolute path"}, remedy},
		{"absent scratch", "bad_scratch", []string{"--repo", b.repo, "--scratch", absent, "--pr", "1"}, nil, nil, remedy},
		{"pr zero", "bad_pr", at("--pr", "0"), nil, nil, remedy},
		{"pr abc", "bad_pr", at("--pr", "abc"), nil, nil, remedy},
		{"pr with prune", "bad_pr", at("--pr", "1", "--prune"), nil, nil, remedy},
		{"unknown pr", "no_pr", at("--pr", "9"), errNoPR, []string{"the forge does not know this pull request"}, retry},
		{"forge unreachable", "no_forge", at("--pr", "9"), errNoForge, []string{"the forge could not be reached"}, retry},
		{"origin with no owner/name", "bad_origin", at("--pr", "9"), badOrigin("git@forge-alias:"),
			[]string{"--repo wants an origin remote", "git@forge-alias:"}, remedy},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.forge != nil {
				useForge(t, errForge{err: c.forge})
			}
			r := b.runEnv(t, nil, c.args...).Exit(2).Err("WORKTREE REFUSED reason="+c.reason+":", c.remedy).Err(c.says...)
			require.Empty(t, r.Stdout, r)
			if c.remedy == remedy {
				r.NotErr(retry)
			}
		})
	}
	require.ErrorIs(t, statErr(absent), fs.ErrNotExist, "a refusal created the scratch dir")
}

// The owner and name the forge client names come out of the origin remote's path in every
// shape git stores a remote in, an ssh Host alias standing where the forge's own name would
// included, and a remote whose path names no owner/name yields nothing at all.
func TestWorktreeParseOwnerRepo(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"https://example.com/o/n.git": "o/n", "https://example.com/o/n": "o/n", "git@example.com:o/n.git": "o/n",
		"ssh://git@example.com/o/n.git": "o/n", "ssh://git@example.com:22/o/n.git": "o/n",
		"git@forge-alias:o/n.git": "o/n", "o/n": "o/n",
		"git@forge-alias:": "", "https://example.com/": "", "https://example.com/n.git": "", "": "",
		"file:///tmp/x/o/n.git": "", "../o/n.git": "", "./o/n": "", "/abs/path/o/n.git": "",
		"C:/repos/o/n": "", `C:\repos\o\n`: "",
	} {
		t.Run(url, func(t *testing.T) { require.Equal(t, want, parseOwnerRepo(url), url) })
	}
}

// An origin remote the forge client can read no owner/name out of, and a repository with
// no origin remote at all, are errBadOrigin and not a forge that could not be reached, and
// the error names the origin it read.
func TestGhForgeBadOriginIsNotAnOutage(t *testing.T) {
	for _, c := range []struct {
		name, origin string
		err          error
		says         string
	}{
		{"origin names no owner/name", "git@forge-alias:\n", nil, "git@forge-alias:"},
		{"no origin remote", "", fmt.Errorf("fatal: no such remote 'origin'"), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			swap(t, &worktreeGit, func(string, ...string) (string, error) { return c.origin, c.err })
			_, err := ghForge{repo: t.TempDir()}.PR(7)
			require.ErrorIs(t, err, errBadOrigin)
			require.NotErrorIs(t, err, errNoForge, "a bad origin reads as a forge that could not be reached")
			if c.says != "" {
				require.Contains(t, err.Error(), c.says, "the error does not name the origin it read")
			}
		})
	}
}

// A fetch that hit its deadline is reported by name; any other fetch failure is tolerated
// (the head may already be here) and left to the add.
func TestFetchHeadReportsATimeoutAndToleratesTheRest(t *testing.T) {
	t.Parallel()
	err := fetchHead(func(string, ...string) (string, error) {
		return "", &subproc.TimeoutError{What: "git fetch origin abc", Budget: 5 * time.Minute, Err: errors.New("signal: killed")}
	}, "/repo", "abc")
	var te *subproc.TimeoutError
	require.ErrorAs(t, err, &te)
	require.Contains(t, err.Error(), "fetching abc from origin")
	err = fetchHead(func(string, ...string) (string, error) { return "", errors.New("fatal: couldn't find remote ref") }, "/repo", "abc")
	require.NoError(t, err, "an ordinary fetch failure was reported")
}

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// gitMerger is the merger's hands (member.MergeGit; docs/SPEC-SWARM.md, `member
// --merger`): one working repository per card repository under <root>/merge/,
// with no hooks and the bench mirror as an alternate, fetched from origin every
// batch and pushed to origin with this machine's own credential. It merges
// (a merge commit per card, never a rebase) and pushes without force: each
// batch's branch is new, and the development branch only fast-forwards.
type gitMerger struct {
	root string
	// env is added to this process's environment for every git: none in
	// production, a test's git configuration.
	env []string
	git func(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error)
	mu  sync.Mutex
}

func newGitMerger(root string) *gitMerger {
	return &gitMerger{root: root, git: gitrun.Run}
}

// mergeSubject begins the subject of the merge commit of a card's head:
// `merger: merge <card> <sha>`, so a batch branch says what it holds.
const mergeSubject = "merger: merge "

// The refs a batch is fetched into, in the merger's own namespace.
const (
	refDev   = "refs/merger/dev"
	refCards = "refs/merger/cards/"
)

// run is one git in dir (none: the process's), with no terminal prompt.
func (g *gitMerger) run(dir string, args ...string) (gitrun.Result, error) {
	o := gitrun.Options{C: dir, Env: append(append(os.Environ(), g.env...), "GIT_TERMINAL_PROMPT=0")}
	return g.git(context.Background(), o, args...)
}

// repo is the working repository of a card repository, made once.
func (g *gitMerger) repo(url string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	sum := sha256.Sum256([]byte(url))
	name := strings.TrimSuffix(filepath.Base(strings.TrimSuffix(url, "/")), ".git")
	dir := filepath.Join(g.root, "merge", sanitize(name)+"-"+hex.EncodeToString(sum[:4]))
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return dir, nil
	}
	if res, err := g.run("", "init", "-q", "--", dir); err != nil {
		return "", fmt.Errorf("git init: %s", gitLine(res, err))
	}
	for _, kv := range [][2]string{{"gc.auto", "0"}, {"maintenance.auto", "false"}, {"core.hooksPath", os.DevNull}, {"merge.renames", "true"}} {
		if res, err := g.run(dir, "config", "--", kv[0], kv[1]); err != nil {
			return "", fmt.Errorf("git config %s: %s", kv[0], gitLine(res, err))
		}
	}
	if mirror := swarm.FindBenchMirror("", url); mirror != "" {
		objects := filepath.Join(mirror, "objects")
		if _, err := os.Stat(objects); err != nil {
			objects = filepath.Join(mirror, ".git", "objects")
		}
		if err := addAlternate(filepath.Join(dir, ".git"), objects); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// sanitize is a repository name as a path element: letters, digits, - _ . only.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, s)
}

// fetch brings origin's development branch and every card's branch into the
// merger's refs, and names the batch's branch: sprint/<stream>.e<epoch>.b<k>, k
// one past the highest of the stream's batch branches origin holds at the epoch
// (1 for the first). A card's head must be on its branch.
func (g *gitMerger) fetch(b member.Batch) (dir, branch string, err error) {
	if dir, err = g.repo(b.Repo); err != nil {
		return "", "", err
	}
	stem := member.StreamBranch(b.Stream, b.Epoch)
	res, err := g.run(dir, "ls-remote", "--heads", "--", b.Repo, "refs/heads/"+stem+".b*")
	if err != nil {
		return "", "", fmt.Errorf("ls-remote %s: %s", b.Repo, gitLine(res, err))
	}
	k := 0 // the highest batch branch of the stem origin holds
	for _, l := range strings.Split(string(res.Stdout), "\n") {
		if m := batchBranchRE.FindStringSubmatch(strings.TrimSpace(l)); m != nil && m[1] == "refs/heads/"+stem {
			if n, _ := strconv.Atoi(m[2]); n > k {
				k = n
			}
		}
	}
	specs := []string{"+refs/heads/" + b.Base + ":" + refDev}
	for _, c := range b.Cards {
		specs = append(specs, "+refs/heads/"+c.Branch+":"+refCards+c.ID)
	}
	fetch := append([]string{"fetch", "-q", "--no-tags", "--no-write-fetch-head", "--", b.Repo}, specs...)
	if res, err := g.run(dir, fetch...); err != nil {
		return "", "", fmt.Errorf("fetch from %s: %s", b.Repo, gitLine(res, err))
	}
	for _, c := range b.Cards {
		if !g.ancestor(dir, c.Head, refCards+c.ID) {
			return "", "", fmt.Errorf("card %s's head %s is not on origin's %s", c.ID, c.Head, c.Branch)
		}
	}
	return dir, stem + ".b" + strconv.Itoa(k+1), nil
}

// batchBranchRE is a batch branch's ls-remote line: its stem's ref and its k.
var batchBranchRE = regexp.MustCompile(`\s(refs/heads/\S+)\.b([0-9]+)$`)

// ancestor says a is an ancestor of (or is) b in dir.
func (g *gitMerger) ancestor(dir, a, b string) bool {
	_, err := g.run(dir, "merge-base", "--is-ancestor", "--end-of-options", a, b)
	return err == nil
}

// Landed says origin's development branch holds every card's head.
func (g *gitMerger) Landed(b member.Batch) (bool, error) {
	dir, _, err := g.fetch(b)
	if err != nil {
		return false, err
	}
	for _, c := range b.Cards {
		if !g.ancestor(dir, c.Head, refDev) {
			return false, nil
		}
	}
	return true, nil
}

// Build makes the batch's branch in the working repository, fresh from origin's
// development branch: each card's head merged in order, a merge commit each. On
// a conflict head is what was built before it, the batch's record. The branch is
// new every batch (fetch names it), so its push is never a rewrite and a batch
// that went red or conflicted needs nothing deleted: the next batch takes the
// next branch.
func (g *gitMerger) Build(b member.Batch) (branch, head, conflict string, err error) {
	dir, branch, err := g.fetch(b)
	if err != nil {
		return "", "", "", err
	}
	if res, err := g.run(dir, "checkout", "-q", "-f", "-B", "merger", refDev); err != nil {
		return "", "", "", fmt.Errorf("checkout %s: %s", refDev, gitLine(res, err))
	}
	if res, err := g.run(dir, "reset", "-q", "--hard", refDev); err != nil {
		return "", "", "", fmt.Errorf("reset to %s: %s", refDev, gitLine(res, err))
	}
	for _, c := range b.Cards {
		if g.ancestor(dir, c.Head, "HEAD") {
			continue
		}
		if !g.merge(dir, c.Head, mergeSubject+c.ID+" "+c.Head) {
			conflict = c.ID
			break
		}
	}
	res, err := g.run(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("rev-parse: %s", gitLine(res, err))
	}
	return branch, strings.TrimSpace(string(res.Stdout)), conflict, nil
}

// merge merges a commit into HEAD with a merge commit of the subject; false,
// the merge aborted, when it did not merge cleanly.
func (g *gitMerger) merge(dir, commit, subject string) bool {
	if _, err := g.run(dir, "merge", "-q", "--no-ff", "--no-edit", "-m", subject, "--end-of-options", commit); err != nil {
		// ignored: the abort and the reset clean the merger's own worktree; the next build checks out -f and resets again
		_, _ = g.run(dir, "merge", "--abort")
		// ignored: as above, the next build's checkout -f and reset start clean whatever this left
		_, _ = g.run(dir, "reset", "-q", "--hard")
		return false
	}
	return true
}

// Push puts the head on origin's stream branch: never forced, so the branch only grows.
func (g *gitMerger) Push(b member.Batch, head string) error {
	dir, err := g.repo(b.Repo)
	if err != nil {
		return err
	}
	if res, err := g.run(dir, "push", "-q", "--porcelain", "--no-verify", "--", b.Repo, head+":refs/heads/"+b.Branch); err != nil {
		return fmt.Errorf("%s", gitLine(res, err))
	}
	return nil
}

// Land pushes the head to origin's development branch, never forced: a push that
// is not a fast-forward is rejected, and rejected is git's line.
func (g *gitMerger) Land(b member.Batch, head string) (rejected string, err error) {
	dir, err := g.repo(b.Repo)
	if err != nil {
		return "", err
	}
	res, err := g.run(dir, "push", "-q", "--porcelain", "--no-verify", "--", b.Repo, head+":refs/heads/"+b.Base)
	if err == nil {
		return "", nil
	}
	line := gitLine(res, err)
	if strings.Contains(line, "non-fast-forward") || strings.Contains(line, "fetch first") || strings.Contains(line, "(rejected)") {
		return line, nil
	}
	return "", fmt.Errorf("%s", line)
}

// ghChecks is the proof of a pushed head (member.Checks): every GitHub check run
// on the commit, read with gh, as this machine.
type ghChecks struct {
	gh  string
	env []string
}

// checksBudget bounds one read of a commit's checks.
const checksBudget = 60 * time.Second

// checkRuns is the part of GitHub's check-runs answer the merger reads.
type checkRuns struct {
	Total int `json:"total_count"`
	Runs  []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"check_runs"`
}

// redConclusions are the conclusions of a check run that fail the batch.
var redConclusions = map[string]bool{"failure": true, "cancelled": true, "timed_out": true, "action_required": true, "startup_failure": true, "stale": true}

func (c *ghChecks) State(repo, head string) (state, note string, err error) {
	bin, err := exec.LookPath(c.gh)
	if err != nil {
		return "", "", fmt.Errorf("no %s on this machine's PATH: %s", c.gh, oneLineOf(err.Error()))
	}
	cmd, cancel := subproc.CommandFor(context.Background(), checksBudget, bin, "api", "repos/"+prRepo(repo)+"/commits/"+head+"/check-runs?per_page=100")
	defer cancel()
	cmd.Env = append(append(os.Environ(), c.env...), "GH_PROMPT_DISABLED=1")
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("gh api: %s", oneLineOf(errb.String()+" "+err.Error()))
	}
	var r checkRuns
	if err := json.Unmarshal(out, &r); err != nil {
		return "", "", fmt.Errorf("gh api: not the check runs: %s", oneLineOf(err.Error()))
	}
	if r.Total == 0 && len(r.Runs) == 0 {
		return member.CheckNone, "", nil
	}
	var red, waiting []string
	for _, x := range r.Runs {
		switch {
		case x.Status != "completed":
			waiting = append(waiting, x.Name)
		case redConclusions[x.Conclusion]:
			red = append(red, x.Name+"="+x.Conclusion)
		}
	}
	switch {
	case len(red) > 0:
		return member.CheckRed, "red: " + strings.Join(red, ","), nil
	case len(waiting) > 0:
		return member.CheckPending, "pending: " + strings.Join(waiting, ","), nil
	}
	return member.CheckGreen, fmt.Sprintf("%d check runs green", len(r.Runs)), nil
}

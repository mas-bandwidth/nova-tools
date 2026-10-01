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
// (a merge commit per card, never a rebase) and pushes without force: the
// stream branch only grows, and the development branch only fast-forwards.
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

// mergeSubject is the subject of the merge commit of a card's head, which a
// later build reads to know which cards origin's stream branch holds.
const mergeSubject = "merger: merge "

var mergeSubjectRE = regexp.MustCompile(`^merger: merge (\S+) ([0-9a-f]{40})$`)

// The refs a batch is fetched into, in the merger's own namespace.
const (
	refDev    = "refs/merger/dev"
	refStream = "refs/merger/stream"
	refCards  = "refs/merger/cards/"
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

// fetch brings origin's development branch, the stream branch when origin has
// it, and every card's branch into the merger's refs; streamHeld says origin
// has the stream branch. A card's head must be on its branch.
func (g *gitMerger) fetch(b member.Batch) (dir string, streamHeld bool, err error) {
	if dir, err = g.repo(b.Repo); err != nil {
		return "", false, err
	}
	res, err := g.run(dir, "ls-remote", "--heads", "--", b.Repo, "refs/heads/"+b.Branch)
	if err != nil {
		return "", false, fmt.Errorf("ls-remote %s: %s", b.Repo, gitLine(res, err))
	}
	streamHeld = strings.TrimSpace(string(res.Stdout)) != ""
	specs := []string{"+refs/heads/" + b.Base + ":" + refDev}
	if streamHeld {
		specs = append(specs, "+refs/heads/"+b.Branch+":"+refStream)
	}
	for _, c := range b.Cards {
		specs = append(specs, "+refs/heads/"+c.Branch+":"+refCards+c.ID)
	}
	fetch := append([]string{"fetch", "-q", "--no-tags", "--no-write-fetch-head", "--", b.Repo}, specs...)
	if res, err := g.run(dir, fetch...); err != nil {
		return "", false, fmt.Errorf("fetch from %s: %s", b.Repo, gitLine(res, err))
	}
	if !streamHeld {
		// ignored: the ref of a stream branch origin no longer holds; streamHeld false is what Build reads
		_, _ = g.run(dir, "update-ref", "-d", refStream)
	}
	for _, c := range b.Cards {
		if !g.ancestor(dir, c.Head, refCards+c.ID) {
			return "", false, fmt.Errorf("card %s's head %s is not on origin's %s", c.ID, c.Head, c.Branch)
		}
	}
	return dir, streamHeld, nil
}

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

// Build makes the stream branch for the batch in the working repository: from
// origin's development branch, or, when origin's stream branch is not on the
// development branch and holds only cards of this batch at their heads (a batch
// whose landing was rejected), from it with the development branch merged in;
// then each card's head merged in order, a merge commit each. A stream branch
// that holds a card this batch does not (a batch that went red) is never
// rewritten: the build is refused, naming the cards, for the coordinator.
func (g *gitMerger) Build(b member.Batch) (head, conflict string, err error) {
	dir, streamHeld, err := g.fetch(b)
	if err != nil {
		return "", "", err
	}
	start := refDev
	var held []string // the cards origin's stream branch holds, in the order merged
	if streamHeld && !g.ancestor(dir, refStream, refDev) {
		res, err := g.run(dir, "log", "--first-parent", "--format=%s", refDev+".."+refStream)
		if err != nil {
			return "", "", fmt.Errorf("reading %s: %s", b.Branch, gitLine(res, err))
		}
		heads := map[string]string{}
		for _, c := range b.Cards {
			heads[c.ID] = c.Head
		}
		var foreign []string
		for _, l := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n") {
			m := mergeSubjectRE.FindStringSubmatch(strings.TrimSpace(l))
			if m == nil {
				continue
			}
			if heads[m[1]] != m[2] {
				foreign = append(foreign, m[1]+"@"+m[2][:12])
				continue
			}
			held = append([]string{m[1]}, held...)
		}
		if len(foreign) > 0 {
			return "", "", fmt.Errorf("origin's %s holds %s, which is not this batch (%s), and is never rewritten: delete it (git push %s --delete %s) and the next pass builds it again",
				b.Branch, strings.Join(foreign, ","), strings.Join(b.IDs(), ","), b.Repo, b.Branch)
		}
		start = refStream
	}
	if res, err := g.run(dir, "checkout", "-q", "-f", "-B", "merger", start); err != nil {
		return "", "", fmt.Errorf("checkout %s: %s", start, gitLine(res, err))
	}
	if res, err := g.run(dir, "reset", "-q", "--hard", start); err != nil {
		return "", "", fmt.Errorf("reset to %s: %s", start, gitLine(res, err))
	}
	if start == refStream && !g.ancestor(dir, refDev, "HEAD") {
		dev, _ := g.run(dir, "rev-parse", refDev)
		if !g.merge(dir, refDev, "merger: bring "+b.Base+" "+strings.TrimSpace(string(dev.Stdout))) {
			// the development branch moved under the batch the branch holds: the
			// first card of it is the conflict the coordinator is told
			if len(held) == 0 {
				return "", b.Cards[0].ID, nil
			}
			return "", held[0], nil
		}
	}
	for _, c := range b.Cards {
		if g.ancestor(dir, c.Head, "HEAD") {
			continue
		}
		if !g.merge(dir, c.Head, mergeSubject+c.ID+" "+c.Head) {
			return "", c.ID, nil
		}
	}
	res, err := g.run(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("rev-parse: %s", gitLine(res, err))
	}
	return strings.TrimSpace(string(res.Stdout)), "", nil
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

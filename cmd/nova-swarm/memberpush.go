package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// gitPusher is the member's push at a work card's finish (member.Pusher): the
// commit the child's RESULT.md names, pushed from outside the wall to the
// branch the sprint named, at the repository the card names (the URL staging
// set the checkout's origin to), with this machine's own credential (its ssh
// key or its git credential helper) and never a value read from a seat.
//
// It never runs git with the checkout's own configuration: the child wrote that
// configuration, and its hooks, inside the wall, and a push run there would run
// a pre-push hook, a credential helper or an insteadOf of the child's choosing
// with this machine's credential. The member keeps its own bare repository,
// <root>/push.git, fetches the checkout's branches into it (upload-pack in the
// checkout runs no hook and honours no command of the checkout's
// configuration), and pushes the commit from there. Never forced: a branch
// that origin holds at another commit refuses the push, and the finish says so.
type gitPusher struct {
	root, slots, sprintBin string
	// git runs one git; gitrun.Run, or a test's fake.
	git func(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error)
	mu  sync.Mutex // the push repository's creation and its alternates
}

func newGitPusher(root, slots, sprintBin string) *gitPusher {
	return &gitPusher{root: root, slots: slots, sprintBin: sprintBin, git: gitrun.Run}
}

// pushHeadRE is a head a push names: a sha, as RESULT.md's `rev:` line gives it.
var pushHeadRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// pushBranchRE is a branch a push names: a ref name with no refspec character
// (no `:`, `+`, `^`, `~`, `*`, no blank), so the branch can only name itself.
var pushBranchRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)

// Push pushes the child's commit, or says why it did not (member.Push).
func (g *gitPusher) Push(p member.Packet, r member.Result) member.Push {
	head := strings.ToLower(strings.TrimSpace(r.Head))
	if !pushHeadRE.MatchString(head) {
		return member.Push{Refused: fmt.Sprintf("the result's head %q is not a sha", r.Head)}
	}
	if !pushBranchRE.MatchString(p.Branch) || strings.Contains(p.Branch, "..") || strings.HasSuffix(p.Branch, "/") || strings.HasSuffix(p.Branch, ".lock") {
		return member.Push{Refused: fmt.Sprintf("the packet's branch %q is not a branch name", p.Branch)}
	}
	// the repository staging cloned: read from the card the member wrote, never
	// from the checkout's configuration
	url := swarm.ReadCardBase([]byte(member.CardText(p, g.sprintBin))).Repo
	if url == "" {
		return member.Push{None: "the card names no repository"}
	}
	checkout := filepath.Join(g.slots, launchName(p), "jobs", p.Card, swarm.JobRepo)
	if fi, err := os.Stat(filepath.Join(checkout, ".git")); err != nil || !fi.IsDir() {
		return member.Push{Refused: "no checkout at " + checkout + " to push " + head + " from"}
	}
	repo, err := g.repo(url)
	if err != nil {
		return member.Push{Refused: "the member's push repository: " + oneLineOf(err.Error())}
	}
	ctx := context.Background()
	ns := "refs/member/" + launchName(p)
	defer g.drop(ctx, repo, ns)
	fetch := []string{"fetch", "-q", "--no-tags", "--no-write-fetch-head", "--", checkout, "+refs/heads/*:" + ns + "/heads/*", "+refs/remotes/origin/*:" + ns + "/origin/*"}
	if res, err := g.run(ctx, repo, nil, fetch...); err != nil {
		return member.Push{Refused: "fetch from the checkout: " + gitLine(res, err)}
	}
	verify := []string{"rev-parse", "--verify", "-q", "--end-of-options", head + "^{commit}"}
	res, err := g.run(ctx, repo, nil, verify...)
	full := strings.TrimSpace(string(res.Stdout))
	if err != nil || len(full) < 40 {
		return member.Push{Refused: "the result's head " + head + " is not a commit on the checkout's branches"}
	}
	// the child committed when its head has a commit no branch of origin's
	// (as the clone knew them) holds
	res, err = g.run(ctx, repo, nil, "for-each-ref", "--format=%(objectname)", ns+"/origin/")
	if err != nil {
		return member.Push{Refused: "reading origin's branches: " + gitLine(res, err)}
	}
	revs := full + "\n"
	for _, o := range strings.Fields(string(res.Stdout)) {
		revs += "^" + o + "\n"
	}
	res, err = g.run(ctx, repo, strings.NewReader(revs), "rev-list", "--count", "--stdin")
	if err != nil {
		return member.Push{Refused: "counting the child's commits: " + gitLine(res, err)}
	}
	if n, _ := strconv.Atoi(strings.TrimSpace(string(res.Stdout))); n == 0 {
		return member.Push{None: "the child committed nothing: head " + full + " is on origin's branches already"}
	}
	push := []string{"push", "-q", "--porcelain", "--no-verify", "--", url, full + ":refs/heads/" + p.Branch}
	if res, err := g.run(ctx, repo, nil, push...); err != nil {
		return member.Push{Refused: gitLine(res, err)}
	}
	return member.Push{Sha: full}
}

// repo is the member's push repository, made once: a bare repository with no
// hooks, no automatic gc (pushes run side by side), and the bench mirror of
// the card's repository as an alternate when there is one, so a fetch from a
// checkout copies only the child's own objects.
func (g *gitPusher) repo(url string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	repo := filepath.Join(g.root, "push.git")
	ctx := context.Background()
	if _, err := os.Stat(filepath.Join(repo, "HEAD")); err != nil {
		mk := []string{"init", "-q", "--bare", "--", repo}
		if res, err := g.run(ctx, "", nil, mk...); err != nil {
			return "", fmt.Errorf("git init: %s", gitLine(res, err))
		}
		for _, kv := range [][2]string{{"gc.auto", "0"}, {"maintenance.auto", "false"}, {"core.hooksPath", os.DevNull}} {
			set := []string{"config", "--", kv[0], kv[1]}
			if res, err := g.run(ctx, repo, nil, set...); err != nil {
				return "", fmt.Errorf("git config %s: %s", kv[0], gitLine(res, err))
			}
		}
	}
	if mirror := swarm.FindBenchMirror("", url); mirror != "" {
		objects := filepath.Join(mirror, "objects")
		if _, err := os.Stat(objects); err != nil {
			objects = filepath.Join(mirror, ".git", "objects")
		}
		if err := addAlternate(repo, objects); err != nil {
			return "", err
		}
	}
	return repo, nil
}

// addAlternate lists an object directory in the repository's alternates once
// (the file written whole, beside the lines it held).
func addAlternate(repo, objects string) error {
	if _, err := os.Stat(objects); err != nil {
		return nil // a mirror with no object directory lends nothing; the fetch copies what it needs
	}
	path := filepath.Join(repo, "objects", "info", "alternates")
	held, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(held), "\n") {
		if strings.TrimSpace(l) == objects {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if len(held) > 0 && !strings.HasSuffix(string(held), "\n") {
		held = append(held, '\n')
	}
	return atomicfile.Write(path, append(held, []byte(objects+"\n")...), 0o644)
}

// drop deletes a launch's refs from the push repository once its push is
// done; the objects stay for the next card's fetch.
func (g *gitPusher) drop(ctx context.Context, repo, ns string) {
	res, err := g.run(ctx, repo, nil, "for-each-ref", "--format=delete %(refname)", ns+"/")
	if err != nil || len(res.Stdout) == 0 {
		return
	}
	// ignored: a ref left behind names objects the next fetch reuses; it is never pushed
	_, _ = g.run(ctx, repo, strings.NewReader(string(res.Stdout)), "update-ref", "--stdin")
}

// run is one git in the push repository (C, or none), with no terminal prompt:
// a push the machine holds no credential for is refused, never left waiting.
func (g *gitPusher) run(ctx context.Context, c string, stdin *strings.Reader, args ...string) (gitrun.Result, error) {
	o := gitrun.Options{C: c, Env: append(os.Environ(), "GIT_TERMINAL_PROMPT=0")}
	if stdin != nil {
		o.Stdin = stdin
	}
	return g.git(ctx, o, args...)
}

// gitLine is git's own line for a failure: a push's rejected ref (porcelain
// `!` on stdout), else the first fatal:/error: line on stderr, else stderr's
// last line, else the error.
func gitLine(res gitrun.Result, err error) string {
	for _, l := range strings.Split(string(res.Stdout), "\n") {
		if strings.HasPrefix(l, "!") {
			return oneLineOf(l)
		}
	}
	var last string
	for _, l := range strings.Split(string(res.Stderr), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "error:") {
			return oneLineOf(l)
		}
		if l != "" {
			last = l
		}
	}
	if last != "" {
		return oneLineOf(last)
	}
	if err != nil {
		return oneLineOf(err.Error())
	}
	return "git failed and said nothing"
}

// oneLineOf is text as one line: each run of blanks (tabs, newlines) one blank.
func oneLineOf(s string) string { return strings.Join(strings.Fields(s), " ") }

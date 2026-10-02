package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
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
// The rule is docs/SPEC-SWARM.md's `member`.
type gitPusher struct {
	root, slots string
	// gh is the GitHub CLI the member opens a card's pull request with, as itself,
	// outside the wall, when the child's result asks for one (gh pr create inside it).
	gh string
	// env is added to this process's environment for every git and gh the pusher
	// runs: none in production, a test's git configuration
	env []string
	// git runs one git; gitrun.Run, or a test's fake.
	git func(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error)
	mu  sync.Mutex // the push repository's creation and its alternates
	// sleep waits between two tries of a push origin rejected on its own side
	// (pushWaits); nil is time.Sleep, a test gives its own.
	sleep func(time.Duration)
	// notes is where the pusher says what it pushed that the result did not name (the
	// member's own output); nil says nothing.
	notes io.Writer
}

// pushWaits is the waits before a push that origin rejected on its own side, git's
// `[remote rejected]`, is sent again: the remote's failure, never the commit's (three
// pushes were rejected so within thirty seconds of a fleet pass, 2026-10-01, and each
// failed its card). A push origin refuses for the commit (`[rejected]`: not a fast
// forward) is refused at once, as before, and never forced.
var pushWaits = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

func newGitPusher(root, slots string) *gitPusher {
	return &gitPusher{root: root, slots: slots, gh: "gh", git: gitrun.Run}
}

// pushBranchRE is a branch a push names: a ref name with no refspec character
// (no `:`, `+`, `^`, `~`, `*`, no blank), so the branch can only name itself.
var pushBranchRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)

// Push pushes the child's commit, or says why it did not (member.Push).
func (g *gitPusher) Push(p member.Packet, r member.Result) member.Push {
	head := strings.ToLower(strings.TrimSpace(r.Head))
	if !typedrec.IsSha(head) {
		return member.Push{Refused: fmt.Sprintf("the result's head %q is not a sha", r.Head)}
	}
	if !pushBranchRE.MatchString(p.Branch) || strings.Contains(p.Branch, "..") || strings.HasSuffix(p.Branch, "/") || strings.HasSuffix(p.Branch, ".lock") {
		return member.Push{Refused: fmt.Sprintf("the packet's branch %q is not a branch name", p.Branch)}
	}
	// the repository staging cloned: the launch's frame (the member wrote it, the
	// brief's header, docs/SPEC-CARD-CONTRACT.md layer 1), else the brief's header
	// for a launch from before frames; never the checkout's configuration
	url, ref := g.cardRepo(p)
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
	// a ref this launch's namespace kept from an interrupted push is dropped before the
	// fetch, so only this checkout's own branches can authorize the head
	g.drop(ctx, repo, ns)
	defer g.drop(ctx, repo, ns)
	// every branch and the HEAD of the checkout: the child's commit is on whichever branch
	// it made, in the checkout or in a clone the git shim linked to it (docs/SPEC-CARD-CONTRACT.md)
	fetch := []string{"fetch", "-q", "--no-tags", "--no-write-fetch-head", "--", checkout, "+HEAD:" + ns + "/HEAD", "+refs/heads/*:" + ns + "/heads/*"}
	if res, err := g.run(ctx, repo, nil, fetch...); err != nil {
		return member.Push{Refused: "fetch from the checkout: " + gitLine(res, err)}
	}
	verify := []string{"rev-parse", "--verify", "-q", "--end-of-options", head + "^{commit}"}
	res, err := g.run(ctx, repo, nil, verify...)
	full := strings.TrimSpace(string(res.Stdout))
	refused := ""
	if err != nil || len(full) < 40 {
		refused = "the result's head " + head + " is not a commit on the checkout's branches"
	} else if res, err := g.run(ctx, repo, nil, "for-each-ref", "--count=1", "--format=%(refname)", "--contains", full, ns+"/"); err != nil || strings.TrimSpace(string(res.Stdout)) == "" {
		// the head must be one this launch's checkout holds: the push repository keeps
		// every launch's objects, so a commit being there is no evidence it is this one's
		refused = "the result's head " + full + " is not on this checkout's branches or HEAD"
	}
	// the child committed when its head has a commit the staged commit does not:
	// counted from the commit native recorded in the slot, never from the
	// checkout's own refs, which a stale mirror or the child can move
	// (docs/SPEC-CARD-CONTRACT.md section 4; tla/CardContract.tla Push)
	staged, serr := os.ReadFile(filepath.Join(g.slots, launchName(p), cardcontract.StagedName))
	base := strings.TrimSpace(string(staged))
	if refused != "" {
		// a result whose head names no commit of the checkout (a model that wrote a sha's
		// first characters right and invented its tail) is the checkout's own head when the
		// child made exactly one line of work; else the refusal stands
		tip := ""
		if serr == nil && typedrec.IsFullSha(base) {
			tip = g.checkoutTip(ctx, repo, ns, base)
		}
		if tip == "" {
			return member.Push{Refused: refused}
		}
		if g.notes != nil {
			fmt.Fprintf(g.notes, "NOTE push %s head: the result named %s, which is no commit of the checkout; the checkout's own head %s was pushed\n", oneline.Field(p.Card), oneline.Field(head), oneline.Field(tip))
		}
		full = tip
	}
	if serr != nil || !typedrec.IsFullSha(base) {
		return member.Push{None: "no staged commit is recorded for this launch to count the child's commits from"}
	}
	if res, err := g.run(ctx, repo, nil, "merge-base", "--is-ancestor", "--end-of-options", base, full); err != nil {
		return member.Push{Refused: "the result's head " + full + " does not descend from the staged commit " + base + ": " + gitLine(res, err)}
	}
	res, err = g.run(ctx, repo, nil, "rev-list", "--count", "--end-of-options", full, "^"+base)
	if err != nil {
		return member.Push{Refused: "counting the child's commits from the staged " + base + ": " + gitLine(res, err)}
	}
	if n, _ := strconv.Atoi(strings.TrimSpace(string(res.Stdout))); n == 0 {
		return member.Push{None: "the child committed nothing: head " + full + " is the staged commit or behind it"}
	}
	push := []string{"push", "-q", "--porcelain", "--no-verify", "--", url, full + ":refs/heads/" + p.Branch}
	for try := 0; ; try++ {
		res, err := g.run(ctx, repo, nil, push...)
		if err == nil {
			break
		}
		line := gitLine(res, err)
		if try == len(pushWaits) || !strings.Contains(line, "[remote rejected]") {
			return member.Push{Refused: line}
		}
		wait := g.sleep
		if wait == nil {
			wait = time.Sleep
		}
		wait(pushWaits[try])
	}
	pu := member.Push{Sha: full}
	if strings.TrimSpace(r.Title) != "" {
		pu.PR, pu.PRNote = g.openPR(url, ref, p.Branch, r.Title, r.Body)
	}
	return pu
}

// checkoutTip is the one tip of the checkout's HEAD and branches (fetched under ns) that holds
// a commit the staged commit does not and descends from it: the child's one line of work. ""
// when there is none, or more than one. On the 1000-card load test of 2026-10-01 five of the
// first twelve failures were a result naming a sha whose first characters were right and whose
// tail was invented, from one route, with the commit itself on the checkout's branch.
func (g *gitPusher) checkoutTip(ctx context.Context, repo, ns, base string) string {
	res, err := g.run(ctx, repo, nil, "for-each-ref", "--format=%(objectname)", ns+"/")
	if err != nil {
		return ""
	}
	var tips []string
	seen := map[string]bool{base: true}
	for _, sha := range strings.Fields(string(res.Stdout)) {
		if seen[sha] {
			continue
		}
		seen[sha] = true
		if _, err := g.run(ctx, repo, nil, "merge-base", "--is-ancestor", "--end-of-options", base, sha); err == nil {
			tips = append(tips, sha)
		}
	}
	if len(tips) != 1 {
		return ""
	}
	return tips[0]
}

// cardRepo is the repository and base ref of a launch: its frame's, else the brief's header.
func (g *gitPusher) cardRepo(p member.Packet) (url, ref string) {
	if f, err := cardcontract.ReadFrame(filepath.Join(g.slots, launchName(p)+cardcontract.FrameName)); err == nil && f.Repo != "" {
		if url = swarm.CardRepoURL(f.Repo); url == "" {
			url = f.Repo
		}
		return url, f.BaseRef
	}
	cb := swarm.ReadCardBase([]byte(p.Brief))
	return cb.Repo, cb.Ref
}

// prBudget bounds the member's gh pr create: one call to the forge, and a stuck one is a
// tick that never ends.
const prBudget = 60 * time.Second

// openPR opens the card's pull request as the member, outside the wall, from the branch the
// push landed on into the card's base, with the title and body the child's gh pr create
// gave (docs/SPEC-CARD-CONTRACT.md section 4). It returns the address gh prints, or why
// none was opened: the finish stands either way, since the work is on origin.
func (g *gitPusher) openPR(url, base, branch, title, body string) (pr, note string) {
	bin, err := exec.LookPath(g.gh)
	if err != nil {
		return "", "no " + g.gh + " on this machine's PATH: " + oneLineOf(err.Error())
	}
	args := []string{"pr", "create", "--repo", prRepo(url), "--head", branch, "--title", title, "--body-file", "-"}
	if base != "" {
		args = append(args, "--base", base)
	}
	cmd, cancel := subproc.CommandFor(context.Background(), prBudget, bin, args...)
	defer cancel()
	cmd.Stdin = strings.NewReader(body)
	cmd.Env = append(append(os.Environ(), g.env...), "GH_PROMPT_DISABLED=1")
	var errb strings.Builder
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", "gh pr create: " + oneLineOf(errb.String()+" "+err.Error())
	}
	// gh prints the pull request's address, the last word on its stdout
	words := strings.Fields(string(out))
	if len(words) == 0 {
		return "", "gh pr create said nothing on stdout"
	}
	return words[len(words)-1], ""
}

// prRepo is the [host/]owner/name gh names a repository by: a GitHub URL's owner/name,
// another host's host/owner/name, a local path as it is.
func prRepo(url string) string {
	if strings.HasPrefix(url, "/") {
		return url
	}
	u := strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.Index(u, "@"); i >= 0 && !strings.Contains(u[:i], "/") {
		u = u[i+1:]
	}
	u = strings.Replace(u, ":", "/", 1)
	return strings.TrimPrefix(u, githubHost+"/")
}

// githubHost is the forge whose repositories gh names by owner/name alone.
const githubHost = "github.com"

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
	o := gitrun.Options{C: c, Env: append(append(os.Environ(), g.env...), "GIT_TERMINAL_PROMPT=0")}
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

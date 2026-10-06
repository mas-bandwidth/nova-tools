package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

func init() {
	// promote is the machine's schedule, like run and friend clean: it needs no
	// actor. The class test holds every verb to one class (coordinator.go).
	verbClasses["promote"] = classMachine
	// git and gh run where the verb is typed, as land does. The server runs
	// neither (serve.go, notServed).
	notServed = append(notServed, "promote")
}

// promoteArmed, when set, is the promote step a land round runs. Nil runs
// nothing: the verb is the schedule. run's own flags are not this file, so the
// server does not arm it by default and a land round never opens a pull request
// from the server's working directory.
var promoteArmed func(context.Context, io.Writer)

// promoteOnTick is the promote step on a land round (docs/SPEC-SPRINT.md
// section 11, promote). The land loop calls it every round.
func (a *app) promoteOnTick(ctx context.Context, stdout io.Writer) {
	if promoteArmed == nil {
		return
	}
	promoteArmed(ctx, stdout)
}

// promoteStep cuts a frozen promo/<date>-<n> from the sprint tip and opens its
// pull request to dev (docs/SPEC-SPRINT.md section 11, promote). The pull
// request head is that branch, never the live sprint branch: a queued pull
// request whose head is the live branch blocks the lander's pushes (GH006,
// found 2026-10-04). The tree gate runs on the frozen commit before the pull
// request. Admission to the merge queue is the enqueuePullRequest mutation,
// which carries no merge strategy (the queue refuses one); a query of
// mergeQueueEntry confirms the entry. gh pr merge, and a flag named auto, are
// the spelling TestNoGhPrMergeSpellingInTheToolsGo refuses, and that test names
// this mutation as the one admission. A failed merge-group run raises one
// judgment, decisions fix-and-recut and skip, with the failing check's log
// tail. A merge records `promoted --sha`.
//
// The queries are one literal each, so they are not an argument list of pr and
// merge, and neither is a strategy flag.
const (
	promoteEnqueueQuery = `mutation($id:ID!){enqueuePullRequest(input:{pullRequestId:$id}){mergeQueueEntry{id}}}`
	promoteQueueQuery   = `query($id:ID!){node(id:$id){... on PullRequest{mergeQueueEntry{id state}}}}`
)

// promoteDecisions are the one judgment a failed merge-group run raises.
var promoteDecisions = []string{"fix-and-recut", "skip"}

// landSubject is a land commit's subject: land <id> (sprint stream <s>).
var landSubject = regexp.MustCompile(`^land (\S+) \(sprint stream [^)]+\)$`)

// promoteEveryDefault is how often the verb looks when --every is left unset.
const promoteEveryDefault = time.Hour

// promoter is one promote schedule over one clone.
type promoter struct {
	dir, live, base, check string
	now                    time.Time
	dry                    bool
	env                    []string
	// gitRun and ghRun, when set, stand in for git and gh (a test). Nil runs
	// the real programs.
	gitRun func(ctx context.Context, dir string, args ...string) (string, error)
	ghRun  func(ctx context.Context, dir string, args ...string) (string, error)
	// gate, when set, is the tree gate (a test). Nil runs --check, or nothing
	// when --check is empty.
	gate func(ctx context.Context, dir, sha string) (string, error)
	// forge, when set, is the forge (a test). Nil runs gh.
	forge promoteForge
	// judged is the branch a judgment was already raised for, so a later pass
	// does not raise a second one.
	judged string
	// every and landings are the schedule. started is when the verb began, and
	// lastAt is when it last cut a branch. The step reads them in due.
	every    time.Duration
	landings int
	started  time.Time
	lastAt   time.Time
}

// promoteJudgment is the one judgment a failed merge-group run raises.
type promoteJudgment struct {
	What      string
	Tail      string
	Decisions []string
	Kind      string   // the line's word: merge-conflict, checks-failed, merge-group failed
	Files     []string // the conflicted files, on a merge conflict
}

// promoteMergeDecisions are the one judgment a conflicting target raises.
var promoteMergeDecisions = []string{"resolve-and-recut", "skip"}

// promoteOutcome is what one pass did.
type promoteOutcome struct {
	Branch   string // the frozen branch, promo/<date>-<n>
	Live     string // the live sprint branch, never the pull request head
	Tip      string // the sprint tip the branch was cut from
	Head     string // the pull request head ref
	Body     string
	Cards    []string
	Entry    string // the confirmed merge-queue entry, when there is one
	Promoted string // the merge sha, when it merged
	Judgment *promoteJudgment
	Dry      bool
	Nothing  bool
	Cut      bool   // a frozen branch was cut on this pass
	CutSHA   string // the cut commit: the tip with the target merged in
	Waiting  bool   // an open pull request waits on checks or the queue
}

// cmdPromote is `nova-sprint promote`. --dry-run is one pass and changes
// nothing. Without it the verb repeats every --every until it is interrupted.
func (a *app) cmdPromote(args []string, stdout, stderr io.Writer) int {
	fs, _ := a.verbSetup("promote")
	every := fs.Duration("every", promoteEveryDefault, "how often to look, a duration (default 1h); each pass promotes when that long has passed or --landings cards have landed")
	landings := fs.Int("landings", 0, "also promote once this many cards have landed since the last promotion (0: the clock only)")
	dry := fs.Bool("dry-run", false, "print the branch and the landed cards and change nothing")
	branch := fs.String("branch", "", "the live sprint branch the cut is taken from (default: the checkout's current branch)")
	repo := fs.String("repo-dir", "", "the clone the branch is cut in (default: the current directory)")
	base := fs.String("base", "dev", "the branch the pull request targets (default dev)")
	check := fs.String("check", "", "the tree gate, a command run on the frozen commit before the pull request (default: none)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "promote", argErr("takes no words ", err, pos...))
	}
	if *every <= 0 {
		return refuse(stderr, "promote", "--every must be above zero; run: nova-sprint promote --every 1h --dry-run")
	}
	if *landings < 0 {
		return refuse(stderr, "promote", "--landings must be zero or more; run: nova-sprint promote --landings 1 --dry-run")
	}
	now := time.Now()
	if a.now != nil {
		now = a.now()
	}
	p := &promoter{
		dir: *repo, live: *branch, base: *base, check: *check,
		now: now, dry: *dry, env: a.gitEnv,
		every: *every, landings: *landings, started: now,
	}
	ctx := context.Background()
	if !*dry && a.notify != nil {
		var cancel context.CancelFunc
		ctx, cancel = a.notify(ctx)
		defer cancel()
	}
	for {
		if ctx.Err() != nil {
			return 0
		}
		out, code := p.step(ctx, stdout, stderr)
		if code == 2 {
			return 2
		}
		if out.Cut {
			p.lastAt = p.now
		}
		if *dry {
			return code
		}
		wait := *every
		if (*landings > 0 || out.Waiting) && wait > time.Minute {
			wait = time.Minute
		}
		after := a.after
		if after == nil {
			after = time.After
		}
		select {
		case <-ctx.Done():
			return 0
		case <-after(wait):
			if a.now != nil {
				p.now = a.now()
			} else {
				p.now = time.Now()
			}
		}
	}
}

// step is one promote pass. Each step prints a PROMOTE line before it runs, so
// the verb never blocks on something it has not named.
func (p *promoter) step(ctx context.Context, stdout, stderr io.Writer) (promoteOutcome, int) {
	o := promoteOutcome{Live: p.live, Dry: p.dry}
	if p.base == "" {
		p.base = "dev"
	}
	if p.now.IsZero() {
		p.now = time.Now()
	}
	live, err := p.liveBranch(ctx)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	o.Live = live
	p.live = live
	// the cut is taken from the forge's branch, never from a local ref: a stale
	// local ref cut a promotion with nothing in it (2026-10-05)
	liveRef := "refs/remotes/origin/" + live
	baseRef := "refs/remotes/origin/" + p.base
	fmt.Fprintf(stdout, "PROMOTE FETCH origin %s %s\n", oneline.Field(live), oneline.Field(p.base))
	if _, err := p.git(ctx, "fetch", "--quiet", "origin",
		"+refs/heads/"+live+":"+liveRef, "+refs/heads/"+p.base+":"+baseRef); err != nil {
		return o, p.fail(stderr, err)
	}
	tip, err := p.rev(ctx, liveRef)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	o.Tip = tip
	if branch, pr, ok := p.pending(ctx, tip); ok {
		o.Branch, o.Head = branch, branch
		return p.watch(ctx, o, pr, stdout, stderr)
	}
	since, err := p.since(ctx, baseRef)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	log, err := p.git(ctx, "log", "--format=%s", "--end-of-options", since+".."+tip)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	o.Cards = landedCards(log)
	if len(o.Cards) == 0 {
		o.Nothing = true
		fmt.Fprintf(stdout, "PROMOTE NONE live=%s tip=%s\n", oneline.Field(live), tip)
		return o, 0
	}
	ahead, err := p.count(ctx, baseRef+".."+tip)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if ahead == 0 {
		return o, p.fail(stderr, fmt.Errorf("the cut is not ahead of %s: origin/%s has everything %s has", p.base, live, live))
	}
	day := p.now.Format("2006-01-02")
	listed, err := p.git(ctx, "branch", "--list", "promo/"+day+"-*")
	if err != nil {
		return o, p.fail(stderr, err)
	}
	branch := nextPromo(day, listed)
	o.Branch, o.Head = branch, branch
	o.Body = promoteBody(o.Cards)
	if branch == live || !strings.HasPrefix(branch, "promo/") {
		return o, p.fail(stderr, errors.New("the pull request head would be the live sprint branch "+live))
	}
	if p.dry {
		fmt.Fprintf(stdout, "PROMOTE DRY-RUN branch=%s live=%s tip=%s cards=%s\n", oneline.Field(branch), oneline.Field(live), tip, oneline.Field(strings.Join(o.Cards, ",")))
		fmt.Fprintln(stdout, "dry-run: nothing was cut")
		return o, 0
	}
	if !p.due(len(o.Cards)) {
		fmt.Fprintf(stdout, "PROMOTE WAIT live=%s tip=%s cards=%d landings=%d\n", oneline.Field(live), tip, len(o.Cards), p.landings)
		return o, 0
	}
	cut, files, err := p.mergeTarget(ctx, tip, baseRef, branch, stdout)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if len(files) > 0 {
		o.Judgment = &promoteJudgment{
			What:      fmt.Sprintf("merging origin/%s into the cut of %s conflicts in %s", p.base, live, strings.Join(files, ", ")),
			Files:     files,
			Decisions: append([]string(nil), promoteMergeDecisions...),
		}
		key := "merge:" + tip
		if p.judged == key {
			o.Judgment = nil
			fmt.Fprintf(stdout, "PROMOTE WAIT live=%s judgment=already\n", oneline.Field(live))
			return o, 1
		}
		p.judged = key
		fmt.Fprintf(stdout, "JUDGMENT merge-conflict live=%s base=%s files=%s decisions=%s\n", oneline.Field(live), oneline.Field(p.base), oneline.Field(strings.Join(files, ",")), strings.Join(o.Judgment.Decisions, ","))
		return o, 1
	}
	o.CutSHA = cut
	if _, err := p.git(ctx, "branch", "--no-track", branch, cut); err != nil {
		return o, p.fail(stderr, err)
	}
	fmt.Fprintf(stdout, "PROMOTE GATE branch=%s sha=%s\n", oneline.Field(branch), cut)
	if _, err := p.runGate(ctx, cut); err != nil {
		return o, p.fail(stderr, err)
	}
	spec := "refs/heads/" + branch + ":refs/heads/" + branch
	if strings.Contains(spec, "refs/heads/"+live+":") {
		return o, p.fail(stderr, errors.New("the push spec names the live sprint branch "+live))
	}
	fmt.Fprintf(stdout, "PROMOTE PUSH branch=%s\n", oneline.Field(branch))
	if _, err := p.git(ctx, "push", "origin", spec); err != nil {
		return o, p.fail(stderr, err)
	}
	fmt.Fprintf(stdout, "PROMOTE OPEN branch=%s base=%s\n", oneline.Field(branch), oneline.Field(p.base))
	number, err := p.forgeOf().Open(ctx, p.base, branch, "promote "+branch, o.Body)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	for _, kv := range [][2]string{{"promote.branch", branch}, {"promote.pr", number}, {"promote.tip", tip}} {
		if _, err := p.git(ctx, "config", "--local", kv[0], kv[1]); err != nil {
			return o, p.fail(stderr, err)
		}
	}
	o.Cut = true
	fmt.Fprintf(stdout, "PROMOTE CUT branch=%s live=%s tip=%s pr=%s cards=%s\n", oneline.Field(branch), oneline.Field(live), tip, number, oneline.Field(strings.Join(o.Cards, ",")))
	return p.watch(ctx, o, number, stdout, stderr)
}

// mergeTarget is the cut commit: the sprint tip with the target merged into it.
// It is built in the object store (git merge-tree, git commit-tree), so no
// checkout is touched. A target already in the tip is no merge and the cut is
// the tip. A conflict returns the conflicted files and nothing else: the tool
// resolves nothing.
func (p *promoter) mergeTarget(ctx context.Context, tip, baseRef, branch string, stdout io.Writer) (cut string, files []string, err error) {
	behind, err := p.count(ctx, tip+".."+baseRef)
	if err != nil {
		return "", nil, err
	}
	if behind == 0 {
		return tip, nil, nil
	}
	fmt.Fprintf(stdout, "PROMOTE MERGE origin/%s into %s (%d commits behind)\n", p.base, oneline.Field(branch), behind)
	out, err := p.git(ctx, "merge-tree", "--write-tree", "--name-only", "--no-messages", tip, baseRef)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err != nil {
		for _, l := range lines[1:] {
			if l = strings.TrimSpace(l); l != "" {
				files = append(files, l)
			}
		}
		if len(files) == 0 {
			return "", nil, err
		}
		return "", files, nil
	}
	tree := strings.TrimSpace(lines[0])
	if tree == "" {
		return "", nil, errors.New("git merge-tree wrote no tree")
	}
	cut, err = p.git(ctx, "commit-tree", tree, "-p", tip, "-p", baseRef, "-m", "merge origin/"+p.base+" into "+branch)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(cut), nil, nil
}

// count is the number of commits in a range.
func (p *promoter) count(ctx context.Context, rng string) (int, error) {
	out, err := p.git(ctx, "rev-list", "--count", "--end-of-options", rng)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// forgeOf is the forge the step talks to: the one a test set, else gh.
func (p *promoter) forgeOf() promoteForge {
	if p.forge != nil {
		return p.forge
	}
	return ghForge{run: func(ctx context.Context, args ...string) (string, error) { return p.gh(ctx, args...) }}
}

// due says this pass cuts a branch. With --landings 0 the verb's loop is the
// clock and every pass is due. With --landings above zero a pass is due when
// that many cards have landed, or when --every has passed since the last cut
// (since the verb started, when none has).
func (p *promoter) due(n int) bool {
	if p.landings <= 0 {
		return true
	}
	if n >= p.landings {
		return true
	}
	anchor := p.started
	if !p.lastAt.IsZero() {
		anchor = p.lastAt
	}
	if anchor.IsZero() || p.every <= 0 {
		return true
	}
	return !p.now.Before(anchor.Add(p.every))
}

// raise sets the one judgment of a cause and prints it; a cause already raised
// prints a WAIT line and sets none.
func (p *promoter) raise(o *promoteOutcome, key string, j promoteJudgment, stdout io.Writer) {
	if p.judged == key {
		fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(o.Branch))
		return
	}
	p.judged = key
	o.Judgment = &j
	fmt.Fprintf(stdout, "JUDGMENT %s branch=%s decisions=%s\n%s\n", j.Kind, oneline.Field(o.Branch), strings.Join(j.Decisions, ","), j.What)
	if j.Tail != "" {
		fmt.Fprintln(stdout, j.Tail)
	}
}

// watch carries an open pull request to the queue and to a recorded merge. A
// pass that cannot go further says what it waits on: the checks (by name), the
// queue. A failed check raises one judgment naming it; the queue is entered
// only once the checks pass.
func (p *promoter) watch(ctx context.Context, o promoteOutcome, number string, stdout, stderr io.Writer) (promoteOutcome, int) {
	f := p.forgeOf()
	view, err := f.View(ctx, number)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if sha := mergedSHA(view); sha != "" {
		return p.record(ctx, o, sha, stdout, stderr)
	}
	entry, err := f.Entry(ctx, view.ID)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if entry == "" {
		fmt.Fprintf(stdout, "PROMOTE CHECKS branch=%s pr=%s\n", oneline.Field(o.Branch), number)
		checks, err := f.Checks(ctx, number)
		if err != nil {
			return o, p.fail(stderr, err)
		}
		switch checks.State {
		case promoteChecksFail:
			p.raise(&o, "checks:"+o.Branch, promoteJudgment{
				Kind:      "checks-failed",
				What:      "a check of the pull request failed: " + strings.Join(checks.Failing, ", "),
				Decisions: append([]string(nil), promoteDecisions...),
			}, stdout)
			return o, 1
		case promoteChecksPending:
			o.Waiting = true
			fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s pr=%s on=checks pending=%s\n", oneline.Field(o.Branch), number, oneline.Field(strings.Join(checks.Pending, ",")))
			return o, 0
		}
		fmt.Fprintf(stdout, "PROMOTE ENQUEUE branch=%s pr=%s\n", oneline.Field(o.Branch), number)
		if _, err := f.Enqueue(ctx, view.ID); err != nil {
			return o, p.fail(stderr, err)
		}
		entry, err = f.Entry(ctx, view.ID)
		if err != nil {
			return o, p.fail(stderr, err)
		}
		if entry == "" {
			return o, p.fail(stderr, errors.New("the merge queue has no entry for pull request "+number))
		}
	}
	o.Entry = entry
	fmt.Fprintf(stdout, "PROMOTE QUEUE branch=%s entry=%s\n", oneline.Field(o.Branch), oneline.Field(o.Entry))
	failure, err := f.GroupFailure(ctx, o.Branch)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if failure.Name != "" || failure.Log != "" {
		p.raise(&o, o.Branch, promoteJudgment{
			Kind:      "merge-group failed",
			What:      "a merge-group run failed: " + failure.Name,
			Tail:      logTail(failure.Log),
			Decisions: append([]string(nil), promoteDecisions...),
		}, stdout)
		return o, 1
	}
	// the queue may have merged it between the view and the run list
	view, err = f.View(ctx, number)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if sha := mergedSHA(view); sha != "" {
		return p.record(ctx, o, sha, stdout, stderr)
	}
	o.Waiting = true
	fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s pr=%s on=queue\n", oneline.Field(o.Branch), number)
	return o, 0
}

// record prints `promoted --sha` and remembers the sha so the next pass's
// landed list starts after it.
func (p *promoter) record(ctx context.Context, o promoteOutcome, sha string, stdout, stderr io.Writer) (promoteOutcome, int) {
	// the merge commit is the forge's, not yet in this clone
	fmt.Fprintf(stdout, "PROMOTE FETCH origin %s for the merge %s\n", oneline.Field(p.base), sha)
	if _, err := p.git(ctx, "fetch", "--quiet", "origin", "+refs/heads/"+p.base+":refs/remotes/origin/"+p.base); err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "update-ref", "refs/promoted/last", sha); err != nil {
		return o, p.fail(stderr, err)
	}
	for _, k := range []string{"promote.branch", "promote.pr", "promote.tip"} {
		if _, err := p.git(ctx, "config", "--local", "--unset", k); err != nil {
			return o, p.fail(stderr, err)
		}
	}
	o.Promoted = sha
	fmt.Fprintf(stdout, "promoted --sha %s\n", sha)
	return o, 0
}

func (p *promoter) fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "nova-sprint promote: %s; run: nova-sprint promote --dry-run\n", oneline.Err(err))
	return 1
}

func (p *promoter) liveBranch(ctx context.Context) (string, error) {
	if p.live != "" {
		return p.live, nil
	}
	name, err := p.git(ctx, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	if name == "" || name == "HEAD" {
		return "", errors.New("the checkout is not on a branch; pass --branch <name>, the live sprint branch")
	}
	return name, nil
}

// since is the revision the landed list starts after: the last recorded
// promotion, else the base.
func (p *promoter) since(ctx context.Context, baseRef string) (string, error) {
	if sha, err := p.rev(ctx, "refs/promoted/last"); err == nil && sha != "" {
		return sha, nil
	}
	return p.rev(ctx, baseRef)
}

// pending is an open promotion of this same tip: its branch and pull request
// number. ok is false when there is none or the tip has moved.
func (p *promoter) pending(ctx context.Context, tip string) (branch, pr string, ok bool) {
	branch, err := p.git(ctx, "config", "--local", "--get", "promote.branch")
	if err != nil || branch == "" || branch == p.live || !strings.HasPrefix(branch, "promo/") {
		return "", "", false
	}
	if cut, err := p.git(ctx, "config", "--local", "--get", "promote.tip"); err != nil || cut != tip {
		return "", "", false
	}
	pr, _ = p.git(ctx, "config", "--local", "--get", "promote.pr")
	if pr == "" {
		return "", "", false
	}
	return branch, pr, true
}

func (p *promoter) rev(ctx context.Context, rev string) (string, error) {
	out, err := p.git(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", rev)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", errors.New("no revision " + rev)
	}
	return out, nil
}

func (p *promoter) runGate(ctx context.Context, sha string) (string, error) {
	if p.gate != nil {
		return p.gate(ctx, p.dir, sha)
	}
	if p.check == "" {
		return "", nil
	}
	b := subproc.Prepare(ctx, landCheckBudget, "sh", "-c", p.check)
	defer b.Cancel()
	b.Cmd.Dir = p.dir
	if p.env != nil {
		b.Cmd.Env = p.env
	}
	raw, err := b.Cmd.CombinedOutput()
	if err = b.Wrap("check "+p.check, err); err != nil {
		return string(raw), errors.New("the tree gate failed: " + oneline.Err(err) + checkTail(string(raw)))
	}
	return string(raw), nil
}

func (p *promoter) git(ctx context.Context, args ...string) (string, error) {
	if p.gitRun != nil {
		return p.gitRun(ctx, p.dir, args...)
	}
	res, err := gitrun.Run(ctx, gitrun.Options{C: p.dir, Env: p.env, OwnRepo: p.dir != ""}, args...)
	if err != nil {
		words := strings.TrimSpace(string(res.Stderr) + "\n" + string(res.Stdout))
		return strings.TrimSpace(string(res.Stdout)), fmt.Errorf("git %s: %s", args[0], oneLine(words))
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

func (p *promoter) gh(ctx context.Context, args ...string) (string, error) {
	for _, a := range args {
		if promoteStrategy(a) {
			return "", errors.New("the queue refuses a strategy flag (" + a + ")")
		}
	}
	if p.ghRun != nil {
		return p.ghRun(ctx, p.dir, args...)
	}
	b := subproc.Prepare(ctx, subproc.GHBudget, "gh", args...)
	defer b.Cancel()
	if p.dir != "" {
		b.Cmd.Dir = p.dir
	}
	if p.env != nil {
		b.Cmd.Env = p.env
	}
	var stdout, stderr bytes.Buffer
	b.Cmd.Stdout, b.Cmd.Stderr = &stdout, &stderr
	err := b.Cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err = b.Wrap("gh", err); err != nil {
		return out, fmt.Errorf("gh %s: %s", args[0], oneLine(strings.TrimSpace(stderr.String()+"\n"+out)))
	}
	return out, nil
}

// promoteStrategy says an argument is a merge strategy flag. A flag whose name
// carries "auto" is one too: the queue refuses a strategy, and auto-merge is
// not how a pull request is admitted.
func promoteStrategy(arg string) bool {
	switch arg {
	case "--squash", "--rebase", "--merge":
		return true
	default:
		return strings.HasPrefix(arg, "--") && strings.Contains(arg, "auto")
	}
}

// prJSON is the view the pass reads.
type prJSON struct {
	ID    string `json:"id"`
	State string `json:"state"`
	Merge *struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
}

func mergedSHA(v prJSON) string {
	if !strings.EqualFold(v.State, "MERGED") || v.Merge == nil {
		return ""
	}
	return v.Merge.OID
}

func prNumber(url string) (string, error) {
	url = strings.TrimSpace(url)
	url = strings.TrimSuffix(url, "/")
	i := strings.LastIndex(url, "/")
	if i < 0 || i == len(url)-1 {
		return "", errors.New("the pull request url carried no number: " + oneLine(url))
	}
	n := url[i+1:]
	if _, err := strconv.Atoi(n); err != nil {
		return "", errors.New("the pull request url carried no number: " + oneLine(url))
	}
	return n, nil
}

// nextPromo is promo/<day>-<n>, n one past the highest listed branch of that day.
func nextPromo(day, listed string) string {
	prefix := "promo/" + day + "-"
	n := 0
	for _, line := range strings.Split(listed, "\n") {
		name := strings.TrimSpace(line)
		name = strings.TrimPrefix(name, "*")
		name = strings.TrimSpace(name)
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		k, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
		if err == nil && k > n {
			n = k
		}
	}
	return prefix + strconv.Itoa(n+1)
}

// landedCards is the card ids in landing order, oldest first.
func landedCards(log string) []string {
	lines := strings.Split(log, "\n")
	var ids []string
	for i := len(lines) - 1; i >= 0; i-- {
		m := landSubject.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if m != nil {
			ids = append(ids, m[1])
		}
	}
	return ids
}

func promoteBody(ids []string) string {
	var b strings.Builder
	b.WriteString("Landed since the last promotion:\n")
	for _, id := range ids {
		b.WriteString(id)
		b.WriteByte('\n')
	}
	return b.String()
}

// logTail is the failing check's log, the last lines, capped so a judgment
// stays one screen.
func logTail(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	lines := strings.Split(out, "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	s := strings.Join(lines, "\n")
	if len(s) > 2000 {
		s = s[len(s)-2000:]
	}
	return s
}

func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " | "))
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

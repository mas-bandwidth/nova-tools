package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
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

// promoteConflictDecisions are the one judgment a conflicting merge of the target
// into the cut raises. The tool resolves nothing: a person does, on the sprint
// branch, and the verb cuts again.
var promoteConflictDecisions = []string{"resolve-and-recut", "skip"}

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
	// forge, when set, is every call to the forge (a test's fake). Nil is the gh
	// CLI through ghRun or internal/subproc.
	forge promoteForge
	// recorder writes a merge to the sprint's store, the promoted step. Nil
	// writes no store (a test of the cut alone); the verb always sets it.
	recorder promoteRecorder
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
	Files     []string // the conflicted files, when the merge of the target conflicted
	Tail      string
	Decisions []string
}

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
	Cut      bool // a frozen branch was cut on this pass
}

// cmdPromote is `nova-sprint promote`. --dry-run is one pass and changes
// nothing. Without it the verb repeats every --every until it is interrupted.
func (a *app) cmdPromote(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("promote")
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
		recorder: appRecorder{a: a, c: *c},
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
		if *landings > 0 && wait > time.Minute {
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

// step is one promote pass.
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
	// the cut is taken from origin's tip of the branch and origin's tip of the
	// target, never a local ref (a stale local ref cut a branch 0 ahead of dev,
	// 2026-10-05)
	fmt.Fprintf(stdout, "PROMOTE FETCH origin %s %s\n", oneline.Field(live), oneline.Field(p.base))
	if _, err := p.git(ctx, "fetch", "--quiet", "origin",
		"+refs/heads/"+live+":"+remoteRef(live), "+refs/heads/"+p.base+":"+remoteRef(p.base)); err != nil {
		return o, p.fail(stderr, err)
	}
	tip, err := p.rev(ctx, remoteRef(live))
	if err != nil {
		return o, p.fail(stderr, err)
	}
	target, err := p.rev(ctx, remoteRef(p.base))
	if err != nil {
		return o, p.fail(stderr, err)
	}
	o.Tip = tip
	if branch, pr, ok := p.pending(ctx, tip); ok {
		o.Branch, o.Head = branch, branch
		if p.dry {
			fmt.Fprintf(stdout, "PROMOTE DRY-RUN branch=%s pr=%s pending; nothing was queued or recorded\n", oneline.Field(branch), pr)
			return o, 0
		}
		return p.watch(ctx, o, pr, stdout, stderr)
	}
	since, err := p.since(ctx, target)
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
	ahead, err := p.git(ctx, "rev-list", "--count", "--end-of-options", target+".."+tip)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if strings.TrimSpace(ahead) == "0" {
		return o, p.fail(stderr, fmt.Errorf("origin/%s (%s) is not ahead of origin/%s (%s): the cut would be no commits ahead of %s; nothing is cut", live, short(tip), p.base, short(target), p.base))
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
	cut, files, err := p.cutCommit(ctx, live, tip, target, stdout)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if len(files) > 0 {
		key := "conflict:" + tip + ":" + target
		if p.judged == key {
			fmt.Fprintf(stdout, "PROMOTE WAIT live=%s judgment=already\n", oneline.Field(live))
			return o, 1
		}
		p.judged = key
		o.Judgment = &promoteJudgment{
			What:      "merging " + p.base + " into the cut conflicts",
			Files:     files,
			Tail:      strings.Join(files, "\n"),
			Decisions: append([]string(nil), promoteConflictDecisions...),
		}
		fmt.Fprintf(stdout, "JUDGMENT merge conflict base=%s live=%s files=%s decisions=%s\n%s\n", oneline.Field(p.base), oneline.Field(live), oneline.Field(strings.Join(files, ",")), strings.Join(o.Judgment.Decisions, ","), o.Judgment.Tail)
		return o, 1
	}
	fmt.Fprintf(stdout, "PROMOTE GATE branch=%s cut=%s\n", oneline.Field(branch), short(cut))
	if _, err := p.runGate(ctx, cut); err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "branch", "--no-track", branch, cut); err != nil {
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
	title := "promote " + branch
	number, err := p.forged().OpenPR(ctx, p.base, branch, title, o.Body)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "config", "--local", "promote.branch", branch); err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "config", "--local", "promote.pr", number); err != nil {
		return o, p.fail(stderr, err)
	}
	o.Cut = true
	fmt.Fprintf(stdout, "PROMOTE CUT branch=%s live=%s tip=%s pr=%s cards=%s\n", oneline.Field(branch), oneline.Field(live), tip, number, oneline.Field(strings.Join(o.Cards, ",")))
	return p.watch(ctx, o, number, stdout, stderr)
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

// watch carries an open pull request on: its checks must pass, then it is
// queued, then the queue's outcome is read. Each pass prints where it stands. A
// pass that is waiting on the forge returns 0 and the loop looks again.
func (p *promoter) watch(ctx context.Context, o promoteOutcome, number string, stdout, stderr io.Writer) (promoteOutcome, int) {
	f := p.forged()
	view, err := f.View(ctx, number)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if view.Merged != "" {
		return p.record(ctx, o, view.Merged, stdout, stderr)
	}
	if view.ID == "" {
		return o, p.fail(stderr, errors.New("the pull request "+number+" has no id"))
	}
	entry, err := f.Confirm(ctx, view.ID)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if entry == "" {
		checks, err := f.Checks(ctx, number)
		if err != nil {
			return o, p.fail(stderr, err)
		}
		switch checks.State {
		case checksFail:
			return p.judge(o, stdout, "a pull request check failed: "+checks.Name, checks.Log)
		case checksPending:
			fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s pr=%s checks=pending %s\n", oneline.Field(o.Branch), number, oneline.Field(checks.Name))
			return o, 0
		}
		fmt.Fprintf(stdout, "PROMOTE CHECKS branch=%s pr=%s passed\n", oneline.Field(o.Branch), number)
		if _, err := f.Enqueue(ctx, view.ID); err != nil {
			return o, p.fail(stderr, err)
		}
		entry, err = f.Confirm(ctx, view.ID)
		if err != nil {
			return o, p.fail(stderr, err)
		}
		if entry == "" {
			return o, p.fail(stderr, errors.New("the merge queue has no entry for pull request "+number+"; run: nova-sprint promote --dry-run"))
		}
		fmt.Fprintf(stdout, "PROMOTE QUEUE branch=%s entry=%s\n", oneline.Field(o.Branch), oneline.Field(entry))
	}
	o.Entry = entry
	failed, err := f.FailedGroup(ctx, o.Branch)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if failed.Name != "" {
		return p.judge(o, stdout, "a merge-group run failed: "+failed.Name, failed.Log)
	}
	// the queue may have merged it between the view and the run list
	view, err = f.View(ctx, number)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if view.Merged != "" {
		return p.record(ctx, o, view.Merged, stdout, stderr)
	}
	fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s pr=%s queue=%s\n", oneline.Field(o.Branch), number, oneline.Field(entry))
	return o, 0
}

// judge raises the one judgment of a failed check, once per branch.
func (p *promoter) judge(o promoteOutcome, stdout io.Writer, what, log string) (promoteOutcome, int) {
	if p.judged == o.Branch {
		fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(o.Branch))
		return o, 1
	}
	p.judged = o.Branch
	o.Judgment = &promoteJudgment{What: what, Tail: logTail(log), Decisions: append([]string(nil), promoteDecisions...)}
	fmt.Fprintf(stdout, "JUDGMENT %s branch=%s decisions=%s\n%s\n", oneline.Field(what), oneline.Field(o.Branch), strings.Join(o.Judgment.Decisions, ","), o.Judgment.Tail)
	return o, 1
}

// cutCommit is the commit the frozen branch points at: the tip itself when it
// already holds the target, else a merge of the target into the tip made with
// merge-tree and commit-tree, so no working tree is touched. A conflict returns
// the conflicted files and no commit; nothing is resolved here.
func (p *promoter) cutCommit(ctx context.Context, live, tip, target string, stdout io.Writer) (cut string, files []string, err error) {
	if _, aerr := p.git(ctx, "merge-base", "--is-ancestor", target, tip); aerr == nil {
		fmt.Fprintf(stdout, "PROMOTE MERGE %s already in %s\n", oneline.Field(p.base), oneline.Field(live))
		return tip, nil, nil
	}
	fmt.Fprintf(stdout, "PROMOTE MERGE %s into %s\n", oneline.Field(p.base), oneline.Field(live))
	out, merr := p.git(ctx, "merge-tree", "--write-tree", "--name-only", "--no-messages", tip, target)
	lines := strings.Split(out, "\n")
	tree := strings.TrimSpace(lines[0])
	if merr != nil {
		for _, l := range lines[1:] {
			l = strings.TrimSpace(l)
			if l == "" {
				break
			}
			files = append(files, l)
		}
		if len(files) == 0 || tree == "" {
			return "", nil, merr
		}
		return "", files, nil
	}
	cut, err = p.git(ctx, "commit-tree", tree, "-p", tip, "-p", target, "-m", "promote: merge "+p.base+" into "+live)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(cut), nil, nil
}

// remoteRef is the remote-tracking ref the verb fetches a branch into.
func remoteRef(branch string) string { return "refs/remotes/origin/" + branch }

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// record writes the merge to the sprint's store, the promoted step that
// `nova-sprint promoted --sha` runs, then remembers the sha in the clone so the
// next pass's landed list starts after it and the pull request is no longer
// pending. The store is written first: a pass that dies before the clone's
// record finds the pull request pending again and comes back here, and the
// store's step changes nothing for a sha it holds already (PromotedOnce), so a
// merge is recorded once (tla/PromoteRecord.tla).
func (p *promoter) record(ctx context.Context, o promoteOutcome, sha string, stdout, stderr io.Writer) (promoteOutcome, int) {
	if p.recorder != nil {
		said, err := p.recorder.Record(ctx, sha)
		if err != nil {
			return o, p.fail(stderr, fmt.Errorf("the merge %s is not recorded in the sprint's store: %w", short(sha), err))
		}
		fmt.Fprintf(stdout, "PROMOTE RECORD sha=%s %s\n", sha, oneline.Escape(said))
	}
	if _, err := p.git(ctx, "update-ref", "refs/promoted/last", sha); err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "config", "--local", "--unset", "promote.branch"); err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "config", "--local", "--unset", "promote.pr"); err != nil {
		return o, p.fail(stderr, err)
	}
	o.Promoted = sha
	fmt.Fprintf(stdout, "promoted --sha %s\n", sha)
	return o, 0
}

// promoteRecorder writes a merge to the sprint's store. said is what the store
// answered: the promotion recorded, or recorded already.
type promoteRecorder interface {
	Record(ctx context.Context, sha string) (said string, err error)
}

// appRecorder is the promoted step through the app's store, the store the
// verb's --redis names. With no --actor the step acts as the sprint's
// coordinator seat, whose word a promotion is (sprint.Promoted).
type appRecorder struct {
	a *app
	c common
}

func (r appRecorder) Record(ctx context.Context, sha string) (string, error) {
	c, why := r.a.friendSyncActor(ctx, r.c)
	if why != "" {
		return "", errors.New(why)
	}
	c.verb = "promoted"
	st, err := r.a.storeCtx(ctx, c)
	if err != nil {
		return "", err
	}
	return recordPromotion(ctx, st, c.actor, sha)
}

// recordPromotion runs the promoted step once for the merge sha (sprint.PromotedOnce).
func recordPromotion(ctx context.Context, st *store.Store, who, sha string) (string, error) {
	req := sprint.PromotedReq{Sha: sha, Who: who}
	res, err := st.Run(ctx, store.Step{Args: store.ArgsOf(req), Verb: "promoted", Load: []string{sprint.Work},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.PromotedOnce(s, req) }})
	if err != nil {
		return "", err
	}
	if len(res.Refused) > 0 {
		var why []string
		for _, f := range res.Refused {
			why = append(why, f.Why)
		}
		return "", errors.New(strings.Join(why, "; "))
	}
	if len(res.Moved) == 0 {
		return strings.Join(res.Said, "; "), nil
	}
	return "recorded: " + strings.Join(res.Moved, "; "), nil
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
func (p *promoter) since(ctx context.Context, target string) (string, error) {
	if sha, err := p.rev(ctx, "refs/promoted/last"); err == nil && sha != "" {
		return sha, nil
	}
	return target, nil
}

// pending is an open promotion of this same tip: its branch and pull request
// number. ok is false when there is none or the tip has moved.
func (p *promoter) pending(ctx context.Context, tip string) (branch, pr string, ok bool) {
	branch, err := p.git(ctx, "config", "--local", "--get", "promote.branch")
	if err != nil || branch == "" || branch == p.live || !strings.HasPrefix(branch, "promo/") {
		return "", "", false
	}
	if _, err := p.rev(ctx, branch); err != nil {
		return "", "", false
	}
	// the cut is the tip with the target merged in, so the tip is its ancestor
	if _, err := p.git(ctx, "merge-base", "--is-ancestor", tip, branch); err != nil {
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

// promoteForge is every call the verb makes to the forge. ghForge is the gh CLI;
// a test supplies a fake.
type promoteForge interface {
	// OpenPR opens the pull request and returns its number.
	OpenPR(ctx context.Context, base, head, title, body string) (string, error)
	// View reads the pull request's node id and, when merged, its merge commit.
	View(ctx context.Context, number string) (prView, error)
	// Checks reads the pull request's checks.
	Checks(ctx context.Context, number string) (prChecks, error)
	// Enqueue admits the pull request to the merge queue.
	Enqueue(ctx context.Context, id string) (string, error)
	// Confirm returns the pull request's merge queue entry, "" when it has none.
	Confirm(ctx context.Context, id string) (string, error)
	// FailedGroup returns the failed merge-group run of the branch, a zero value
	// when none has failed.
	FailedGroup(ctx context.Context, branch string) (prRun, error)
}

// prView is what the pass reads of a pull request.
type prView struct {
	ID     string
	Merged string // the merge commit, "" until it merges
}

const (
	checksPass    = "pass"
	checksPending = "pending"
	checksFail    = "fail"
)

// prChecks is the state of a pull request's checks; Name is the failing (or a
// waiting) check and Log the failing run's log.
type prChecks struct {
	State, Name, Log string
}

// prRun is a failed run: its check name and log.
type prRun struct {
	Name, Log string
}

func (p *promoter) forged() promoteForge {
	if p.forge != nil {
		return p.forge
	}
	return ghForge{p}
}

// ghForge is the gh CLI behind promoteForge.
type ghForge struct{ p *promoter }

func (g ghForge) OpenPR(ctx context.Context, base, head, title, body string) (string, error) {
	url, err := g.p.gh(ctx, "pr", "create", "--base", base, "--head", head, "--title", title, "--body", body)
	if err != nil {
		return "", err
	}
	return prNumber(url)
}

func (g ghForge) View(ctx context.Context, number string) (prView, error) {
	raw, err := g.p.gh(ctx, "pr", "view", number, "--json", "id,state,mergeCommit")
	if err != nil {
		return prView{}, err
	}
	var v prJSON
	if jerr := json.Unmarshal([]byte(raw), &v); jerr != nil {
		return prView{}, fmt.Errorf("pull request %s view: %s", number, oneLine(raw))
	}
	return prView{ID: v.ID, Merged: mergedSHA(v)}, nil
}

func (g ghForge) Checks(ctx context.Context, number string) (prChecks, error) {
	// gh exits non-zero for a failing or pending check and still prints the list
	raw, err := g.p.gh(ctx, "pr", "checks", number, "--json", "name,bucket")
	var list []struct {
		Name   string `json:"name"`
		Bucket string `json:"bucket"`
	}
	if jerr := json.Unmarshal([]byte(raw), &list); jerr != nil {
		if err != nil {
			return prChecks{}, err
		}
		return prChecks{}, fmt.Errorf("pull request %s checks: %s", number, oneLine(raw))
	}
	res := prChecks{State: checksPass}
	if len(list) == 0 {
		return prChecks{State: checksPending, Name: "no checks reported yet"}, nil
	}
	for _, c := range list {
		switch c.Bucket {
		case "fail", "cancel":
			return prChecks{State: checksFail, Name: c.Name}, nil
		case "pending":
			if res.State == checksPass {
				res = prChecks{State: checksPending, Name: c.Name}
			}
		}
	}
	return res, nil
}

func (g ghForge) Enqueue(ctx context.Context, id string) (string, error) {
	raw, err := g.p.gh(ctx, "api", "graphql", "-f", "query="+promoteEnqueueQuery, "-f", "id="+id)
	if err != nil {
		return "", err
	}
	var resp struct {
		Data struct {
			EnqueuePullRequest struct {
				MergeQueueEntry *struct {
					ID string `json:"id"`
				} `json:"mergeQueueEntry"`
			} `json:"enqueuePullRequest"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if jerr := json.Unmarshal([]byte(raw), &resp); jerr != nil {
		return "", fmt.Errorf("enqueue: %s", oneLine(raw))
	}
	if len(resp.Errors) > 0 {
		return "", errors.New("enqueue: " + resp.Errors[0].Message)
	}
	if resp.Data.EnqueuePullRequest.MergeQueueEntry == nil || resp.Data.EnqueuePullRequest.MergeQueueEntry.ID == "" {
		return "", errors.New("enqueue returned no merge queue entry")
	}
	return resp.Data.EnqueuePullRequest.MergeQueueEntry.ID, nil
}

func (g ghForge) Confirm(ctx context.Context, id string) (string, error) {
	raw, err := g.p.gh(ctx, "api", "graphql", "-f", "query="+promoteQueueQuery, "-f", "id="+id)
	if err != nil {
		return "", err
	}
	var resp struct {
		Data struct {
			Node struct {
				MergeQueueEntry *struct {
					ID string `json:"id"`
				} `json:"mergeQueueEntry"`
			} `json:"node"`
		} `json:"data"`
	}
	if jerr := json.Unmarshal([]byte(raw), &resp); jerr != nil {
		return "", fmt.Errorf("queue query: %s", oneLine(raw))
	}
	if resp.Data.Node.MergeQueueEntry == nil {
		return "", nil
	}
	return resp.Data.Node.MergeQueueEntry.ID, nil
}

func (g ghForge) FailedGroup(ctx context.Context, branch string) (prRun, error) {
	raw, err := g.p.gh(ctx, "run", "list", "--branch", branch, "--event", "merge_group", "--json", "databaseId,conclusion,status,name", "--limit", "5")
	if err != nil {
		return prRun{}, err
	}
	var runs []struct {
		DatabaseID int    `json:"databaseId"`
		Conclusion string `json:"conclusion"`
		Status     string `json:"status"`
		Name       string `json:"name"`
	}
	if jerr := json.Unmarshal([]byte(raw), &runs); jerr != nil {
		return prRun{}, fmt.Errorf("merge-group runs: %s", oneLine(raw))
	}
	for _, r := range runs {
		if r.Conclusion != "failure" {
			continue
		}
		logText, _ := g.p.gh(ctx, "run", "view", strconv.Itoa(r.DatabaseID), "--log-failed")
		name := r.Name
		if name == "" {
			name = "merge-group run " + strconv.Itoa(r.DatabaseID)
		}
		return prRun{Name: name, Log: logText}, nil
	}
	return prRun{}, nil
}

func (p *promoter) git(ctx context.Context, args ...string) (string, error) {
	if p.gitRun != nil {
		return p.gitRun(ctx, p.dir, args...)
	}
	res, err := gitrun.Run(ctx, gitrun.Options{C: p.dir, Env: p.env, OwnRepo: p.dir != ""}, args...)
	if err != nil {
		words := strings.TrimSpace(string(res.Stderr) + "\n" + string(res.Stdout))
		// stdout comes back with the error: merge-tree lists the conflicted files there
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

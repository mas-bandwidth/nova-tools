package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
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

// promoteStep carries a promotion from the cut to the recorded merge
// (docs/SPEC-SPRINT.md section 11, promote), the hand sequence of 2026-10-05 as
// one verb. It fetches origin and cuts a frozen promo/<date>-<n> from
// origin/<sprint branch>, never a local ref (a stale local ref, 0 ahead of dev,
// was cut twice that day), and refuses a cut that is not ahead of the target.
// It merges origin/<target> into the cut without a checkout (git merge-tree,
// commit-tree); a conflict stops the promotion with one judgment naming the
// files, and the tool resolves nothing. The pull request head is the frozen
// branch, never the live sprint branch: a queued pull request whose head is the
// live branch blocks the lander's pushes (GH006, found 2026-10-04). The tree
// gate runs on the cut before the push. Once the pull request's checks pass it
// is admitted to the merge queue by the enqueuePullRequest mutation, which
// carries no merge strategy (the queue refuses one); a query of mergeQueueEntry
// confirms the entry. gh pr merge, and a flag named auto, are the spelling
// TestNoGhPrMergeSpellingInTheToolsGo refuses, and that test names this
// mutation as the one admission. A failed check, or a failed merge-group run,
// raises one judgment naming the check, decisions fix-and-recut and skip, with
// the failing run's log tail, and cuts one fix card per failing test (promote_red.go);
// a failed run on the base at the last promotion's merge does the same. A merge
// records `promoted --sha` in the store. Every forge call goes through promoteForge
// (promote_forge.go).

// promoteDecisions are the one judgment a failed merge-group run raises.
var promoteDecisions = []string{"fix-and-recut", "skip"}

// promoteConflictDecisions are the one judgment a conflicted merge of the
// target into the cut raises: the tool resolves nothing.
var promoteConflictDecisions = []string{"merge-by-hand-and-recut", "skip"}

// promoteClosedDecisions are the one judgment a pull request closed without a
// merge raises: the promotion in flight is cleared, so recut is the next pass.
var promoteClosedDecisions = []string{"recut", "skip"}

// promoteHex is a full object name.
var promoteHex = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// landSubject is a land commit's subject: land <id> (sprint stream <s>).
var landSubject = regexp.MustCompile(`^land (\S+) \(sprint stream [^)]+\)$`)

// promoteEveryDefault is how often the verb looks when --every is left unset.
const promoteEveryDefault = time.Hour

// promotePollDefault is how often the verb looks while a promotion is in
// flight, when --poll is left unset.
const promotePollDefault = time.Minute

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
	// forge, when set, is the forge (a test). Nil is gh, through ghRun when
	// that is set.
	forge promoteForge
	// recordFn, when set, records a merge in the sprint's store (the verb sets
	// it). Nil prints `promoted --sha` for the coordinator to run.
	recordFn func(ctx context.Context, sha string) error
	// gate, when set, is the tree gate (a test). Nil runs --check, or nothing
	// when --check is empty.
	gate func(ctx context.Context, dir, sha string) (string, error)
	// judged is the branch (or the conflicted tip and target) a judgment was
	// already raised for, so a later pass does not raise a second one.
	judged string
	// red, when set, cuts one fix card per failing test of a red run into the
	// promote-red stream (promote_red.go) and returns the cards cut and the
	// tests an open card already names. Nil cuts none.
	red func(ctx context.Context, reds []sprint.RedTest, sp sprint.FixSpec) (cut, open []string, err error)
	// redRuns is each red run of the base already judged, so a later pass
	// does not cut its cards again.
	redRuns map[int]bool
	// every and landings are the schedule. started is when the verb began, and
	// lastAt is when it last cut a branch. The step reads them in due.
	every    time.Duration
	landings int
	started  time.Time
	lastAt   time.Time
	// tell, when set (the verb sets it), writes a note to the coordinator in the store, so
	// the seat's push delivers it: a judgment of the promotion and an ejection from the merge
	// queue are never stdout alone (the owner, 2026-10-10: no silent stops). Nil tells none.
	tell func(ctx context.Context, typ, what, hint string)
	// queued is the pull request this promoter saw confirmed in the merge queue, and ejected
	// how many times it found that pull request out of the queue, neither merged nor closed.
	queued  string
	ejected int
}

// The notes a promotion tells the coordinator (promoter.tell).
const (
	// NPromoteJudgment is a promotion's judgment (a failed check, a failed merge-group run,
	// a red pull request, a pull request closed): the decisions are on its hint.
	NPromoteJudgment = "a promotion needs the coordinator"
	// NPromoteEjected is a promotion's pull request found out of the merge queue, neither
	// merged nor closed: the merge queue ejected it, and the promoter queues it again.
	NPromoteEjected = "a promotion was ejected from the merge queue"
)

// told tells the coordinator of the promotion's judgment, when the verb tells.
func (p *promoter) told(ctx context.Context, o promoteOutcome) {
	if p.tell == nil || o.Judgment == nil {
		return
	}
	what := fmt.Sprintf("promotion %s: %s", o.Branch, o.Judgment.What)
	if o.Judgment.Tail != "" {
		what += "; " + oneline.Escape(o.Judgment.Tail)
	}
	p.tell(ctx, NPromoteJudgment, what, "decide: "+strings.Join(o.Judgment.Decisions, ", ")+"; nova-sprint promote --once shows it")
}

// promoteJudgment is the one judgment a promotion raises: a conflicted merge
// of the target (Files), or a failed check or merge-group run (Check, Tail),
// with the fix cards cut and tests an open card already named (Cards, Open).
type promoteJudgment struct {
	What      string
	Files     []string
	Check     string
	Tail      string
	Decisions []string
	Cards     []string
	Open      []string
}

// promoteOutcome is what one pass did.
type promoteOutcome struct {
	Branch      string // the frozen branch, promo/<date>-<n>
	Live        string // the live sprint branch, never the pull request head
	Tip         string // the sprint tip the branch was cut from
	Head        string // the pull request head ref
	Body        string
	Cards       []string
	Entry       string // the confirmed merge-queue entry, when there is one
	Promoted    string // the merge sha, when it merged
	Judgment    *promoteJudgment
	DevJudgment *promoteJudgment // the judgment of a red run on the base at the last promotion's merge
	Dry         bool
	Nothing     bool
	Cut         bool     // a frozen branch was cut on this pass
	Files       []string // the conflicted files, when the merge of the target conflicted
	Pending     bool     // the promotion is in flight on the forge: its checks or its queue
	Waiting     string   // what it waits on, when Pending
}

// cmdPromote is `nova-sprint promote`. --dry-run is one pass and changes
// nothing. --once carries one promotion to its end (recorded, a judgment,
// nothing to promote, or a refusal) and exits. Without either the verb repeats
// until it is interrupted: every --every, and every --poll while a promotion is
// in flight.
func (a *app) cmdPromote(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("promote")
	every := fs.Duration("every", promoteEveryDefault, "how often to look, a duration (default 1h); each pass promotes when that long has passed or --landings cards have landed")
	landings := fs.Int("landings", 0, "also promote once this many cards have landed since the last promotion (0: the clock only)")
	dry := fs.Bool("dry-run", false, "print the branch and the landed cards, or the promotion in flight, and write, enqueue and record nothing: no ref, no config, no push, no pull request, no store write (it reads origin's tips with ls-remote and fetches their objects)")
	once := fs.Bool("once", false, "carry one promotion from the cut to the recorded merge (or to the judgment or refusal that stops it) and exit")
	poll := fs.Duration("poll", promotePollDefault, "how often to look while a promotion is in flight, its checks or its merge queue (default 1m)")
	branch := fs.String("branch", "", "the live sprint branch; the cut is taken from origin/<branch> after a fetch, never a local ref (default: the checkout's current branch)")
	repo := fs.String("repo-dir", "", "the clone the branch is cut in (default: the current directory)")
	base := fs.String("base", "dev", "the branch the pull request targets, fetched and merged into the cut first (default dev)")
	check := fs.String("check", "", "the tree gate, a command run on the frozen commit before the pull request (default: none)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "promote", argErr("takes no words ", err, pos...))
	}
	if *every <= 0 {
		return refuse(stderr, "promote", "--every must be above zero; run: nova-sprint promote --every 1h --dry-run")
	}
	if *poll <= 0 {
		return refuse(stderr, "promote", "--poll must be above zero; run: nova-sprint promote --once --poll 1m")
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
	if f, ok := promoteForges.Load(*repo); ok {
		p.forge = f.(promoteForge)
	}
	if !*dry {
		p.red = a.redCutter(*c)
		p.tell = func(ctx context.Context, typ, what, hint string) {
			st, err := a.store(*c)
			if err == nil {
				err = st.Tell(ctx, "promote", typ, what, hint)
			}
			if err != nil {
				fmt.Fprintf(stderr, "NOTE promote: the coordinator was not told (%s): %s\n", oneline.Escape(what), oneline.Err(err))
			}
		}
		p.recordFn = func(ctx context.Context, sha string) error {
			st, err := a.store(*c)
			if err != nil {
				return err
			}
			if code := a.runStep("promoted", *c, st, store.PromotedStep(sprint.PromotedReq{Sha: sha, Who: c.actor}), stdout, stderr); code != 0 {
				return fmt.Errorf("the store did not record it (exit %d)", code)
			}
			return nil
		}
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
		if *dry || *once && !out.Pending {
			return code
		}
		wait := *every
		if *landings > 0 && wait > time.Minute {
			wait = time.Minute
		}
		if (out.Pending || *once) && wait > *poll {
			wait = *poll
		}
		if out.Pending {
			fmt.Fprintf(stdout, "PROMOTE NEXT branch=%s in=%s: %s\n", oneline.Field(out.Branch), wait, out.Waiting)
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

// step is one promote pass: fetch, cut from origin's sprint tip, merge the
// target into the cut, gate, push, open the pull request, then watch it. Each
// step prints a line as it goes, so the verb never waits without saying on what.
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
	if !p.dry {
		o.DevJudgment = p.devRed(ctx, stdout, stderr)
	}
	// the cut is origin's tip, never the clone's local ref: on 2026-10-05 a
	// stale local ref, 0 ahead of dev, was cut and pushed twice
	fmt.Fprintf(stdout, "PROMOTE FETCH origin %s %s\n", oneline.Field(live), oneline.Field(p.base))
	tip, target, err := p.fetch(ctx, live)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	o.Tip = tip
	if branch, pr, ok := p.pending(ctx, tip); ok {
		o.Branch, o.Head = branch, branch
		if p.dry {
			fmt.Fprintf(stdout, "PROMOTE DRY-RUN pending branch=%s pr=%s tip=%s\n", oneline.Field(branch), pr, tip)
			fmt.Fprintln(stdout, "dry-run: promotion is in flight; nothing was checked or watched")
			return o, 0
		}
		if p.judged == branch {
			// a judged promotion still ends when its pull request does: closed
			// clears it, merged records it; otherwise it holds while the tip stands
			view, err := p.forger().PR(ctx, pr)
			if err != nil {
				return o, p.fail(stderr, err)
			}
			if sha := mergedSHA(view); sha != "" {
				return p.record(ctx, o, pr, sha, stdout, stderr)
			}
			if strings.EqualFold(view.State, "CLOSED") {
				return p.closed(ctx, o, pr, stdout, stderr)
			}
			fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s pr=%s judgment=already; the next cut waits for origin/%s to move past %s\n", oneline.Field(branch), pr, oneline.Field(live), tip)
			return o, 1
		}
		fmt.Fprintf(stdout, "PROMOTE PENDING branch=%s pr=%s tip=%s\n", oneline.Field(branch), pr, tip)
		return p.watch(ctx, o, pr, stdout, stderr)
	}
	ahead, err := p.git(ctx, "rev-list", "--count", "--end-of-options", target+".."+tip)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if strings.TrimSpace(ahead) == "0" {
		o.Nothing = true
		fmt.Fprintf(stdout, "PROMOTE NONE live=%s tip=%s ahead=0\n", oneline.Field(live), tip)
		return o, p.fail(stderr, fmt.Errorf("the cut is not ahead of origin/%s: origin/%s at %s is 0 commits ahead of it; nothing was cut", p.base, live, tip))
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
	cut, files, err := p.mergeTarget(ctx, stdout, branch, tip, target)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if len(files) > 0 {
		o.Files = files
		key := "conflict " + tip + " " + target
		if p.judged == key {
			fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(branch))
			return o, 1
		}
		p.judged = key
		o.Judgment = &promoteJudgment{
			What:      fmt.Sprintf("merging origin/%s into the cut of origin/%s conflicts", p.base, live),
			Files:     files,
			Decisions: append([]string(nil), promoteConflictDecisions...),
		}
		fmt.Fprintf(stdout, "JUDGMENT promote conflict branch=%s live=%s tip=%s base=%s files=%s decisions=%s\nnothing was cut or pushed; the tool resolves nothing: merge origin/%s into %s by hand, then run the promotion again\n",
			oneline.Field(branch), oneline.Field(live), tip, oneline.Field(p.base), oneline.Field(strings.Join(files, ",")), strings.Join(o.Judgment.Decisions, ","), p.base, live)
		return o, 1
	}
	if _, err := p.git(ctx, "branch", "--no-track", branch, cut); err != nil {
		return o, p.fail(stderr, err)
	}
	if p.check != "" || p.gate != nil {
		fmt.Fprintf(stdout, "PROMOTE GATE branch=%s sha=%s\n", oneline.Field(branch), cut)
	}
	if _, err := p.runGate(ctx, cut); err != nil {
		return o, p.fail(stderr, err)
	}
	spec := "refs/heads/" + branch + ":refs/heads/" + branch
	if strings.Contains(spec, "refs/heads/"+live+":") {
		return o, p.fail(stderr, errors.New("the push spec names the live sprint branch "+live))
	}
	fmt.Fprintf(stdout, "PROMOTE PUSH branch=%s sha=%s\n", oneline.Field(branch), cut)
	if _, err := p.git(ctx, "push", "origin", spec); err != nil {
		return o, p.fail(stderr, err)
	}
	number, err := p.forger().OpenPR(ctx, p.base, branch, "promote "+branch, o.Body)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	for _, kv := range [][2]string{{"promote.branch", branch}, {"promote.pr", number}, {"promote.tip", tip}} {
		if _, err := p.git(ctx, "config", "--local", kv[0], kv[1]); err != nil {
			return o, p.fail(stderr, err)
		}
	}
	o.Cut = true
	fmt.Fprintf(stdout, "PROMOTE CUT branch=%s live=%s tip=%s sha=%s pr=%s cards=%s\n", oneline.Field(branch), oneline.Field(live), tip, cut, number, oneline.Field(strings.Join(o.Cards, ",")))
	return p.watch(ctx, o, number, stdout, stderr)
}

// fetch is origin's sprint tip and target. A real pass fetches them into
// origin's remote-tracking refs and reads those. A dry run moves no ref: it
// reads both tips with ls-remote, then fetches their objects with no refmap and
// no FETCH_HEAD, so the clone's refs and config are as they were.
func (p *promoter) fetch(ctx context.Context, live string) (tip, target string, err error) {
	heads := []string{"refs/heads/" + live, "refs/heads/" + p.base}
	if !p.dry {
		if _, err := p.git(ctx, "fetch", "--quiet", "origin",
			"+"+heads[0]+":refs/remotes/origin/"+live,
			"+"+heads[1]+":refs/remotes/origin/"+p.base); err != nil {
			return "", "", err
		}
		if tip, err = p.rev(ctx, "refs/remotes/origin/"+live); err != nil {
			return "", "", err
		}
		target, err = p.rev(ctx, "refs/remotes/origin/"+p.base)
		return tip, target, err
	}
	out, err := p.git(ctx, "ls-remote", "--end-of-options", "origin", heads[0], heads[1])
	if err != nil {
		return "", "", err
	}
	at := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t"); ok {
			at[ref] = sha
		}
	}
	for _, h := range heads {
		if !promoteHex.MatchString(at[h]) {
			return "", "", errors.New("origin has no " + h)
		}
	}
	if _, err := p.git(ctx, "fetch", "--quiet", "--no-write-fetch-head", "--refmap=", "origin", heads[0], heads[1]); err != nil {
		return "", "", err
	}
	return at[heads[0]], at[heads[1]], nil
}

// mergeTarget merges the target into the sprint tip without a checkout: the
// cut is the tip itself when the target is already in it, else a merge commit
// of the tip and the target (git merge-tree, then commit-tree). files is the
// conflicted paths when the merge conflicts, and then nothing is written.
func (p *promoter) mergeTarget(ctx context.Context, stdout io.Writer, branch, tip, target string) (cut string, files []string, err error) {
	if _, err := p.git(ctx, "merge-base", "--is-ancestor", target, tip); err == nil {
		fmt.Fprintf(stdout, "PROMOTE MERGE origin/%s into %s: already in the tip, the cut is %s\n", oneline.Field(p.base), oneline.Field(branch), tip)
		return tip, nil, nil
	}
	fmt.Fprintf(stdout, "PROMOTE MERGE origin/%s at %s into %s\n", oneline.Field(p.base), target, oneline.Field(branch))
	out, err := p.git(ctx, "merge-tree", "--write-tree", "--name-only", "--no-messages", tip, target)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	tree := strings.TrimSpace(lines[0])
	if !promoteHex.MatchString(tree) {
		if err == nil {
			err = errors.New("git merge-tree printed no tree: " + oneLine(out))
		}
		return "", nil, err
	}
	if err != nil {
		seen := map[string]bool{}
		for _, l := range lines[1:] {
			l = strings.TrimSpace(l)
			if l != "" && !seen[l] {
				seen[l] = true
				files = append(files, l)
			}
		}
		if len(files) == 0 {
			return "", nil, err
		}
		return "", files, nil
	}
	msg := fmt.Sprintf("merge origin/%s into %s\n\nThe promotion's cut: origin/%s at %s, with origin/%s at %s merged in.", p.base, branch, p.live, tip, p.base, target)
	cut, err = p.git(ctx, "commit-tree", tree, "-p", tip, "-p", target, "-m", msg)
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(cut), nil, nil
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

// watch carries an open pull request one look further: a merge is recorded; a
// failed merge-group run, or a failed check before it is queued, raises the one
// judgment naming the check; checks still pending are a wait that names them;
// checks all passed queue it (the enqueuePullRequest mutation, confirmed by a
// query of mergeQueueEntry); a queued one is a wait. A wait is Pending, and
// the verb looks again after --poll.
func (p *promoter) watch(ctx context.Context, o promoteOutcome, number string, stdout, stderr io.Writer) (promoteOutcome, int) {
	f := p.forger()
	view, err := f.PR(ctx, number)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if sha := mergedSHA(view); sha != "" {
		return p.record(ctx, o, number, sha, stdout, stderr)
	}
	if strings.EqualFold(view.State, "CLOSED") {
		return p.closed(ctx, o, number, stdout, stderr)
	}
	if view.ID == "" {
		return o, p.fail(stderr, errors.New("the pull request "+number+" has no id"))
	}
	if p.red != nil {
		rRuns, err := f.FailedRuns(ctx, o.Branch, "")
		if err != nil {
			fmt.Fprintf(stderr, "NOTE promote: the runs of %s cannot be read: %s; run: gh run list --branch %s\n", oneline.Field(o.Branch), oneline.Err(err), oneline.Field(o.Branch))
		}
		if len(rRuns) > 0 {
			if p.judged == o.Branch {
				fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(o.Branch))
				return o, 1
			}
			p.markJudged(ctx, o.Branch)
			o.Judgment = p.redJudgment(ctx, rRuns, "a check of the pull request failed", "the pull request of "+o.Branch, promoteDecisions, stderr)
			fmt.Fprintf(stdout, "JUDGMENT promotion red branch=%s decisions=%s cards=%s open=%s\n%s\n", oneline.Field(o.Branch), strings.Join(o.Judgment.Decisions, ","),
				oneline.Field(dashed(strings.Join(o.Judgment.Cards, ","))), oneline.Field(dashed(strings.Join(o.Judgment.Open, ","))), o.Judgment.Tail)
			p.told(ctx, o)
			return o, 1
		}
	}
	runs, err := f.QueueRuns(ctx, p.base, number)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	for _, r := range runs {
		if r.Conclusion != "failure" {
			continue
		}
		logText, err := f.RunLog(ctx, r.ID)
		if err != nil {
			logText = "the failing run's log could not be read: " + oneline.Err(err)
		}
		return p.judge(ctx, o, "merge-group failed", r.Name, logText, stdout)
	}
	entry, err := f.QueueEntry(ctx, view.ID)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if entry == "" && p.queued == number {
		// it was in the queue, and is out of it neither merged nor closed: the queue ejected it
		p.queued = ""
		p.ejected++
		fmt.Fprintf(stdout, "PROMOTE EJECTED branch=%s pr=%s times=%d: out of the merge queue, neither merged nor closed; queued again once its checks pass\n", oneline.Field(o.Branch), number, p.ejected)
		if p.tell != nil {
			p.tell(ctx, NPromoteEjected, fmt.Sprintf("pull request %s of %s was ejected from the merge queue (%d times this run): neither merged nor closed; the promoter queues it again once its checks pass", number, o.Branch, p.ejected),
				"look at its merge-group run: gh pr checks "+number+"; gh run list --branch "+o.Branch)
		}
	}
	if entry == "" {
		checks, err := f.Checks(ctx, number)
		if err != nil {
			return o, p.fail(stderr, err)
		}
		var pending []string
		for _, c := range checks {
			switch c.Bucket {
			case "fail", "cancel":
				return p.judge(ctx, o, "check failed", c.Name, "", stdout)
			case "pass", "skipping":
			default:
				pending = append(pending, c.Name)
			}
		}
		if len(checks) == 0 {
			return p.wait(o, number, "no checks reported yet", stdout), 0
		}
		if len(pending) > 0 {
			return p.wait(o, number, "checks pending: "+strings.Join(pending, ","), stdout), 0
		}
		if p.dry {
			return p.wait(o, number, "checks passed; dry-run does not enqueue", stdout), 0
		}
		if _, err := f.Enqueue(ctx, view.ID); err != nil {
			return o, p.fail(stderr, err)
		}
		entry, err = f.QueueEntry(ctx, view.ID)
		if err != nil {
			return o, p.fail(stderr, err)
		}
		if entry == "" {
			return o, p.fail(stderr, errors.New("the merge queue has no entry for pull request "+number+" after the enqueue"))
		}
		o.Entry = entry
		p.queued = number
		fmt.Fprintf(stdout, "PROMOTE QUEUE branch=%s pr=%s entry=%s\n", oneline.Field(o.Branch), number, oneline.Field(entry))
		// the queue may have merged it already
		view, err = f.PR(ctx, number)
		if err != nil {
			return o, p.fail(stderr, err)
		}
		if sha := mergedSHA(view); sha != "" {
			return p.record(ctx, o, number, sha, stdout, stderr)
		}
	}
	o.Entry = entry
	p.queued = number
	return p.wait(o, number, "in the merge queue, entry "+entry, stdout), 0
}

// closed ends a promotion whose pull request was closed without a merge: the
// clone forgets the promotion in flight, so the next pass cuts afresh, and one
// judgment names the pull request. Nothing is recorded. The claim that the
// promotion cleared is made only after every in-flight key is gone: a cleanup
// that fails is a refusal naming the keys that remain, never a false "cuts
// afresh".
func (p *promoter) closed(ctx context.Context, o promoteOutcome, number string, stdout, stderr io.Writer) (promoteOutcome, int) {
	if err := p.forget(ctx); err != nil {
		return o, p.fail(stderr, fmt.Errorf("pull request %s of %s was closed without a merge, but the promotion in flight was not cleared: %s; the keys %s remain, so the next pass would handle it again", number, o.Branch, oneline.Err(err), strings.Join(promoteKeys, ",")))
	}
	fmt.Fprintf(stdout, "PROMOTE CLOSED branch=%s pr=%s; the next pass cuts afresh\n", oneline.Field(o.Branch), number)
	o.Judgment = &promoteJudgment{
		What:      fmt.Sprintf("pull request %s of %s was closed without a merge", number, o.Branch),
		Check:     "pr-" + number,
		Decisions: append([]string(nil), promoteClosedDecisions...),
	}
	fmt.Fprintf(stdout, "JUDGMENT closed-pr branch=%s pr=%s decisions=%s\n", oneline.Field(o.Branch), number, strings.Join(o.Judgment.Decisions, ","))
	p.told(ctx, o)
	return o, 1
}

// wait is a pass that stopped on the forge: it says on what, and the verb looks
// again.
func (p *promoter) wait(o promoteOutcome, number, what string, stdout io.Writer) promoteOutcome {
	o.Pending = true
	o.Waiting = "pull request " + number + " " + what
	fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s pr=%s %s\n", oneline.Field(o.Branch), number, what)
	return o
}

// judge raises the one judgment of a failed check or merge-group run, naming
// the check, once per branch. The clone remembers it (promote.judged), so a
// later run does not raise it again; the promotion is cut afresh once the sprint
// tip moves (the fix landed), the decision fix-and-recut.
func (p *promoter) judge(ctx context.Context, o promoteOutcome, kind, check, logText string, stdout io.Writer) (promoteOutcome, int) {
	if p.judged == o.Branch {
		fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(o.Branch))
		return o, 1
	}
	p.markJudged(ctx, o.Branch)
	o.Judgment = &promoteJudgment{
		What:      "a " + kind + ": " + check,
		Check:     check,
		Tail:      logTail(logText),
		Decisions: append([]string(nil), promoteDecisions...),
	}
	fmt.Fprintf(stdout, "JUDGMENT %s branch=%s check=%s decisions=%s\n", kind, oneline.Field(o.Branch), oneline.Field(check), strings.Join(o.Judgment.Decisions, ","))
	if o.Judgment.Tail != "" {
		fmt.Fprintln(stdout, o.Judgment.Tail)
	}
	p.told(ctx, o)
	return o, 1
}

// markJudged records the branch a judgment was raised for in the clone's local
// config (promote.judged), so a later run does not raise it again while the
// sprint tip stands; once the tip moves (the fix landed) pending forgets the
// promotion and the next pass cuts afresh (docs/SPEC-SPRINT.md, promote). A
// failed config write is ignored: the judgment is raised either way, and
// without the mark a later run raises it once more.
func (p *promoter) markJudged(ctx context.Context, branch string) {
	p.judged = branch
	// ignored: the judgment is raised either way; without the mark a later run raises it once more
	_, _ = p.git(ctx, "config", "--local", "promote.judged", branch)
}

// record records the merge: refs/promoted/last moves to it, so the next pass's
// landed list starts after it, and the store records `promoted --sha`.
func (p *promoter) record(ctx context.Context, o promoteOutcome, number, sha string, stdout, stderr io.Writer) (promoteOutcome, int) {
	if p.dry {
		fmt.Fprintf(stdout, "PROMOTE DRY-RUN branch=%s pr=%s merged sha=%s\n", oneline.Field(o.Branch), number, sha)
		return o, 0
	}
	fmt.Fprintf(stdout, "PROMOTE MERGED branch=%s pr=%s sha=%s\n", oneline.Field(o.Branch), number, sha)
	if _, err := p.git(ctx, "update-ref", "refs/promoted/last", sha); err != nil {
		return o, p.fail(stderr, err)
	}
	o.Promoted = sha
	if p.recordFn == nil {
		fmt.Fprintf(stdout, "promoted --sha %s\n", sha)
	} else if err := p.recordFn(ctx, sha); err != nil {
		return o, p.fail(stderr, fmt.Errorf("the merge %s was not recorded: %s; record it: nova-sprint promoted --sha %s", sha, oneline.Err(err), sha))
	} else {
		fmt.Fprintf(stdout, "PROMOTE RECORDED sha=%s\n", sha)
	}
	if err := p.forget(ctx); err != nil {
		return o, p.fail(stderr, fmt.Errorf("the merge %s is recorded, but the promotion in flight was not cleared: %s; the keys %s remain, so the next pass would handle pull request %s again", sha, oneline.Err(err), strings.Join(promoteKeys, ","), number))
	}
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
// promotion, else the target's tip.
func (p *promoter) since(ctx context.Context, target string) (string, error) {
	if sha, err := p.rev(ctx, "refs/promoted/last"); err == nil && sha != "" {
		return sha, nil
	}
	return target, nil
}

// promoteKeys are the clone's record of the promotion in flight.
var promoteKeys = []string{"promote.branch", "promote.pr", "promote.tip", "promote.judged"}

// pending is the promotion in flight: its branch and pull request number. A
// promotion in flight is carried to its end before another is cut, though the
// sprint tip moves on meanwhile. One whose judgment was raised holds while the
// tip stands, and is forgotten once the tip moves (the fix landed), so the next
// pass cuts afresh; a dry run forgets nothing and plans that fresh cut.
func (p *promoter) pending(ctx context.Context, tip string) (branch, pr string, ok bool) {
	branch, err := p.git(ctx, "config", "--local", "--get", "promote.branch")
	if err != nil || branch == "" || branch == p.live || !strings.HasPrefix(branch, "promo/") {
		return "", "", false
	}
	pr, _ = p.git(ctx, "config", "--local", "--get", "promote.pr")
	if pr == "" {
		return "", "", false
	}
	if judged, _ := p.git(ctx, "config", "--local", "--get", "promote.judged"); judged == branch {
		if was, _ := p.git(ctx, "config", "--local", "--get", "promote.tip"); was != tip {
			if !p.dry {
				// ignored: a stale record blocks nothing: the fix landed, so this pass cuts afresh
				_ = p.forget(ctx)
			}
			return "", "", false
		}
		p.judged = branch
	}
	return branch, pr, true
}

// forget drops the clone's record of the promotion in flight. A key already
// gone is no fault: an older verb set fewer, and git config --unset answers exit
// 5 for a key that is not set. Any other failure is returned: a key left behind
// means the promotion is still in flight, and a caller that claimed it cleared
// would wedge the verb.
func (p *promoter) forget(ctx context.Context) error {
	var errs []error
	for _, key := range promoteKeys {
		if _, err := p.git(ctx, "config", "--local", "--unset", key); err != nil && !promoteKeyAbsent(err) {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
		}
	}
	return errors.Join(errs...)
}

// promoteKeyAbsent reports git config --unset's answer for a key that is not set
// (exit 5): the state wanted, never a fault.
func promoteKeyAbsent(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == 5
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
		// stdout stays the answer: a conflicted merge-tree prints its tree and files and exits 1.
		// The cause is wrapped, so a caller can tell git config --unset's absent-key answer (5)
		// from a real failure.
		words := oneLine(strings.TrimSpace(string(res.Stderr) + "\n" + string(res.Stdout)))
		if words == "" {
			return strings.TrimSpace(string(res.Stdout)), fmt.Errorf("git %s: %w", args[0], err)
		}
		return strings.TrimSpace(string(res.Stdout)), fmt.Errorf("git %s: %s: %w", args[0], words, err)
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

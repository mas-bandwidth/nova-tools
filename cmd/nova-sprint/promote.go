package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
// from the server's working directory. The promote verb does not set this; it
// calls (*promoter).step itself.
var promoteArmed func(context.Context, io.Writer)

// promoteForge, when non-nil, is the forge the promote verb's pass uses. Nil is
// gh (ghForge). A test sets it; production leaves it nil and the verb still runs
// the step.
var promoteForge Forge

// promoteOnTick is the promote step on a land round (docs/SPEC-SPRINT.md
// section 11, promote). The land loop calls it every round.
func (a *app) promoteOnTick(ctx context.Context, stdout io.Writer) {
	if promoteArmed == nil {
		return
	}
	promoteArmed(ctx, stdout)
}

// promoteStep cuts a frozen promo/<date>-<n> from the sprint tip, merges the
// target branch (dev) into it, and opens its pull request to dev
// (docs/SPEC-SPRINT.md section 11, promote). The pull request head is that
// branch, never the live sprint branch: a queued pull request whose head is the
// live branch blocks the lander's pushes (GH006, found 2026-10-04). The tree gate
// runs on the frozen commit before the pull request. All forge calls go through
// the Forge interface. Admission to the merge queue is the enqueuePullRequest
// mutation, which carries no merge strategy (the queue refuses one); a query of
// mergeQueueEntry confirms the entry. gh pr merge, and a flag named auto, are
// the spelling TestNoGhPrMergeSpellingInTheToolsGo refuses, and that test names
// this mutation as the one admission. A failed merge-group run raises one
// judgment, decisions fix-and-recut and skip, naming the failing check with its
// log tail. A conflict when merging the target branch stops the promotion with
// one judgment naming the files. A merge records `promoted --sha`.
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

// promoteWatchEvery is how often the verb looks at an open pull request.
const promoteWatchEvery = time.Minute

// PRView is the facts read about a pull request.
type PRView struct {
	ID       string
	State    string
	MergeSHA string
}

// Forge is what the promote verb needs from a forge (GitHub). All forge calls
// go through this interface.
type Forge interface {
	// CreatePR opens a pull request targeting base with the given head, title, and body.
	CreatePR(ctx context.Context, base, head, title, body string) (number string, err error)

	// PRView returns the status of a pull request.
	PRView(ctx context.Context, number string) (PRView, error)

	// PRChecks returns whether checks have passed, or if any check has failed (and its name).
	PRChecks(ctx context.Context, number string) (passed bool, failedCheck string, err error)

	// Enqueue puts the pull request into the merge queue.
	Enqueue(ctx context.Context, prID string) (entryID string, err error)

	// ConfirmEntry returns the merge queue entry ID for the pull request, or empty if none.
	ConfirmEntry(ctx context.Context, prID string) (entryID string, err error)

	// MergeGroupStatus reports whether a merge-group run has failed, the failing check name, and its log.
	MergeGroupStatus(ctx context.Context, branch string) (failed bool, checkName string, logText string, err error)
}

// ghForge is the real forge calling gh.
type ghForge struct {
	p *promoter
}

func (g *ghForge) CreatePR(ctx context.Context, base, head, title, body string) (string, error) {
	url, err := g.p.gh(ctx, "pr", "create", "--base", base, "--head", head, "--title", title, "--body", body)
	if err != nil {
		return "", err
	}
	return prNumber(url)
}

func (g *ghForge) PRView(ctx context.Context, number string) (PRView, error) {
	view, _, err := g.p.prView(ctx, number)
	if err != nil {
		return PRView{}, err
	}
	return PRView{
		ID:       view.ID,
		State:    view.State,
		MergeSHA: mergedSHA(view),
	}, nil
}

func (g *ghForge) PRChecks(ctx context.Context, number string) (bool, string, error) {
	raw, err := g.p.gh(ctx, "pr", "checks", number, "--json", "bucket,name,state")
	// gh exits non-zero while a check is pending or has failed, with the JSON
	// on stdout; the JSON is the answer when there is some.
	if err != nil && strings.HasPrefix(strings.TrimSpace(raw), "[") {
		// ignored: the exit says pending or failed, and the JSON read below says which
		err = nil
	}
	if err != nil {
		words := err.Error()
		if strings.Contains(words, "no checks reported") || strings.Contains(words, "no commit found") {
			return true, "", nil
		}
		if strings.Contains(words, "pending") {
			return false, "", nil
		}
		return false, "", err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return true, "", nil
	}
	var checks []struct {
		Bucket string `json:"bucket"`
		Name   string `json:"name"`
		State  string `json:"state"`
	}
	if jerr := json.Unmarshal([]byte(raw), &checks); jerr != nil {
		return false, "", fmt.Errorf("pr checks %s: %s", number, oneLine(raw))
	}
	if len(checks) == 0 {
		return true, "", nil
	}
	for _, c := range checks {
		switch strings.ToLower(c.Bucket) {
		case "fail", "cancel":
			return false, c.Name, nil
		case "pending":
			return false, "", nil
		}
	}
	return true, "", nil
}

func (g *ghForge) Enqueue(ctx context.Context, prID string) (string, error) {
	return g.p.enqueue(ctx, prID)
}

func (g *ghForge) ConfirmEntry(ctx context.Context, prID string) (string, error) {
	return g.p.confirm(ctx, prID)
}

func (g *ghForge) MergeGroupStatus(ctx context.Context, branch string) (bool, string, string, error) {
	return g.p.mergeGroup(ctx, branch)
}

// promoter is one promote schedule over one clone.
type promoter struct {
	dir, live, base, check string
	now                    time.Time
	dry                    bool
	env                    []string
	forge                  Forge
	// gitRun and ghRun, when set, stand in for git and gh (a test). Nil runs
	// the real programs.
	gitRun func(ctx context.Context, dir string, args ...string) (string, error)
	ghRun  func(ctx context.Context, dir string, args ...string) (string, error)
	// gate, when set, is the tree gate (a test). Nil runs --check, or nothing
	// when --check is empty.
	gate func(ctx context.Context, dir, sha string) (string, error)
	// promoted records a merged promotion in the sprint's store, the step the
	// promoted verb runs (sprint.Promoted). The verb always sets it; nil, a test
	// of the pass alone, moves refs/promoted/last only.
	promoted func(ctx context.Context, sha string) error
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

func (p *promoter) getForge() Forge {
	if p.forge != nil {
		return p.forge
	}
	return &ghForge{p: p}
}

// promoteJudgment is the one judgment a failed merge-group run or conflict raises.
type promoteJudgment struct {
	What      string
	Check     string
	Files     []string
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
	// WaitOn is what the pass is waiting on when it ends without a promotion
	// or a judgment: "checks" or "queue" for an open pull request, "landings"
	// for the --landings count. Empty is the --every clock.
	WaitOn string
}

// cmdPromote is `nova-sprint promote`. --dry-run is one pass and changes
// nothing. Without it the verb repeats every --every until it is interrupted.
func (a *app) cmdPromote(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("promote")
	every := fs.Duration("every", promoteEveryDefault, "how often to look, a duration (default 1h); each pass promotes when that long has passed or --landings cards have landed")
	landings := fs.Int("landings", 0, "also promote once this many cards have landed since the last promotion (0: the clock only)")
	dry := fs.Bool("dry-run", false, "one pass: fetch origin's copies, print the branch, the landed cards and how far the cut is ahead of the target, and change no branch")
	branch := fs.String("branch", "", "the live sprint branch; the cut is taken from origin's copy, fetched each pass, never the local ref (default: the checkout's current branch name)")
	repo := fs.String("repo-dir", "", "the clone the branch is cut in (default: the current directory)")
	base := fs.String("base", "dev", "the branch the pull request targets, fetched each pass and merged into the cut first (default dev)")
	check := fs.String("check", "", "the tree gate, a command run in a worktree of the frozen commit before the push (default: none)")
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
		now: now, dry: *dry, env: a.gitEnv, forge: promoteForge,
		every: *every, landings: *landings, started: now,
	}
	// A merge is recorded as `nova-sprint promoted --sha <merge>` is, by the
	// coordinator the verb runs as (--actor or NOVA_SPRINT_ACTOR), on the store
	// --redis names (docs/SPEC-SPRINT.md, promotion).
	p.promoted = func(ctx context.Context, sha string) error {
		pc := *c
		pc.verb = "promoted"
		st, err := a.storeCtx(ctx, pc)
		if err != nil {
			return fmt.Errorf("record promoted --sha %s: %w", sha, err)
		}
		if code := a.runStep("promoted", pc, st, store.PromotedStep(sprint.PromotedReq{Sha: sha, Who: pc.actor}), stdout, stderr); code != 0 {
			return fmt.Errorf("record promoted --sha %s: the store refused it (exit %d)", sha, code)
		}
		return nil
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
		// An open pull request is looked at again in a minute; so is a
		// --landings count. The line says what the wait is on before it starts.
		wait := *every
		on := out.WaitOn
		if on == "" {
			on = "every"
		}
		if (*landings > 0 || out.WaitOn == "checks" || out.WaitOn == "queue") && wait > promoteWatchEvery {
			wait = promoteWatchEvery
		}
		fmt.Fprintf(stdout, "PROMOTE SLEEP for=%s until=%s on=%s\n", wait, p.now.Add(wait).UTC().Format(time.RFC3339), on)
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

// step is one promote pass. cmdPromote calls it; promoteArmed is not this pass.
// The live branch and the target are fetched first and the pass reads only
// origin's copies: a clone's local ref of either may be stale (2026-10-05, a
// local merge commit 0 ahead of dev was cut and pushed). Each step is said on a
// PROMOTE STEP line before it runs.
func (p *promoter) step(ctx context.Context, stdout, stderr io.Writer) (o promoteOutcome, code int) {
	o = promoteOutcome{Live: p.live, Dry: p.dry}
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
	liveRef, targetRef := "refs/remotes/origin/"+live, "refs/remotes/origin/"+p.base
	p.say(stdout, "fetch", "origin="+oneline.Field(live)+","+oneline.Field(p.base))
	if _, err := p.git(ctx, "fetch", "-q", "--no-tags", "origin",
		"+refs/heads/"+live+":"+liveRef, "+refs/heads/"+p.base+":"+targetRef); err != nil {
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
	behind, ahead, err := p.aheadBehind(ctx, targetRef, tip)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if ahead == 0 {
		fmt.Fprintf(stdout, "PROMOTE REFUSED live=%s tip=%s target=origin/%s ahead=0 behind=%d: the cut would not be ahead of the target, nothing to promote; run: nova-sprint promote --dry-run once a card has landed on %s\n", oneline.Field(live), tip, oneline.Field(p.base), behind, oneline.Field(live))
		return o, 1
	}
	since, err := p.since(ctx, targetRef)
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
	// origin's promotions of the day count too: a fresh clone holds none of them
	remote, err := p.git(ctx, "ls-remote", "--heads", "origin", "refs/heads/promo/"+day+"-*")
	if err != nil {
		return o, p.fail(stderr, err)
	}
	branch := nextPromo(day, listed+"\n"+remoteHeads(remote))
	o.Branch, o.Head = branch, branch
	o.Body = promoteBody(o.Cards)
	if branch == live || !strings.HasPrefix(branch, "promo/") {
		return o, p.fail(stderr, errors.New("the pull request head would be the live sprint branch "+live))
	}
	if p.dry {
		fmt.Fprintf(stdout, "PROMOTE DRY-RUN branch=%s live=%s tip=%s ahead=%d behind=%d cards=%s\n", oneline.Field(branch), oneline.Field(live), tip, ahead, behind, oneline.Field(strings.Join(o.Cards, ",")))
		fmt.Fprintln(stdout, "dry-run: nothing was cut")
		return o, 0
	}
	if !p.due(len(o.Cards)) {
		o.WaitOn = "landings"
		fmt.Fprintf(stdout, "PROMOTE WAIT live=%s tip=%s cards=%d landings=%d\n", oneline.Field(live), tip, len(o.Cards), p.landings)
		return o, 0
	}
	already := p.judged == branch
	p.say(stdout, "merge", "target=origin/"+oneline.Field(p.base)+" into="+oneline.Field(branch)+" tip="+tip+" behind="+strconv.Itoa(behind))
	cutSha, conflictFiles, err := p.mergeTarget(ctx, branch, tip, targetRef, behind)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if len(conflictFiles) > 0 {
		if already {
			fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(branch))
			return o, 1
		}
		p.judged = branch
		o.Judgment = &promoteJudgment{
			What:      "target merge conflict: " + strings.Join(conflictFiles, ", "),
			Files:     conflictFiles,
			Decisions: append([]string(nil), promoteDecisions...),
		}
		fmt.Fprintf(stdout, "JUDGMENT conflict branch=%s target=origin/%s files=%s decisions=%s\n", oneline.Field(branch), oneline.Field(p.base), oneline.Field(strings.Join(conflictFiles, ",")), strings.Join(o.Judgment.Decisions, ","))
		return o, 1
	}
	p.judged = ""
	p.say(stdout, "gate", "sha="+cutSha)
	if _, err := p.runGate(ctx, cutSha); err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "branch", "--no-track", branch, cutSha); err != nil {
		return o, p.fail(stderr, err)
	}
	spec := "refs/heads/" + branch + ":refs/heads/" + branch
	if strings.Contains(spec, "refs/heads/"+live+":") {
		return o, p.fail(stderr, errors.New("the push spec names the live sprint branch "+live))
	}
	p.say(stdout, "push", "ref="+oneline.Field(spec))
	if _, err := p.git(ctx, "push", "origin", spec); err != nil {
		return o, p.fail(stderr, err)
	}
	title := "promote " + branch
	f := p.getForge()
	p.say(stdout, "pr-create", "base="+oneline.Field(p.base)+" head="+oneline.Field(branch))
	number, err := f.CreatePR(ctx, p.base, branch, title, o.Body)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "config", "--local", "promote.branch", branch); err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "config", "--local", "promote.pr", number); err != nil {
		return o, p.fail(stderr, err)
	}
	// pending() reads this tip to know the open promotion is still this cut.
	if _, err := p.git(ctx, "config", "--local", "promote.tip", tip); err != nil {
		return o, p.fail(stderr, err)
	}
	o.Cut = true
	fmt.Fprintf(stdout, "PROMOTE CUT branch=%s live=%s tip=%s cut=%s pr=%s cards=%s\n", oneline.Field(branch), oneline.Field(live), tip, cutSha, number, oneline.Field(strings.Join(o.Cards, ",")))
	return p.watch(ctx, o, number, stdout, stderr)
}

// say is the line a step prints before it runs, so a pass never works or
// waits without saying on what.
func (p *promoter) say(stdout io.Writer, step, detail string) {
	fmt.Fprintf(stdout, "PROMOTE STEP %s %s\n", step, detail)
}

// aheadBehind counts the commits the target has that tip lacks (behind) and
// the commits tip has that the target lacks (ahead).
func (p *promoter) aheadBehind(ctx context.Context, target, tip string) (behind, ahead int, err error) {
	out, err := p.git(ctx, "rev-list", "--left-right", "--count", "--end-of-options", target+"..."+tip)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return 0, 0, errors.New("git rev-list --left-right --count: " + oneLine(out))
	}
	if behind, err = strconv.Atoi(f[0]); err == nil {
		ahead, err = strconv.Atoi(f[1])
	}
	if err != nil {
		return 0, 0, errors.New("git rev-list --left-right --count: " + oneLine(out))
	}
	return behind, ahead, nil
}

// remoteHeads is ls-remote --heads output as branch names, one a line.
func remoteHeads(out string) string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			names = append(names, strings.TrimPrefix(f[1], "refs/heads/"))
		}
	}
	return strings.Join(names, "\n")
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

// watch enqueues a pull request that is not merged, confirms the queue entry,
// and either records the merge or raises the one judgment.
func (p *promoter) watch(ctx context.Context, o promoteOutcome, number string, stdout, stderr io.Writer) (promoteOutcome, int) {
	f := p.getForge()
	p.say(stdout, "view", "pr="+number)
	view, err := f.PRView(ctx, number)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if view.MergeSHA != "" {
		return p.record(ctx, o, view.MergeSHA, stdout, stderr)
	}
	if view.ID == "" {
		return o, p.fail(stderr, errors.New("the pull request "+number+" has no id"))
	}
	entry, err := f.ConfirmEntry(ctx, view.ID)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if entry == "" {
		p.say(stdout, "checks", "pr="+number)
		passed, failedCheck, err := f.PRChecks(ctx, number)
		if err != nil {
			return o, p.fail(stderr, err)
		}
		if failedCheck != "" {
			if p.judged == o.Branch {
				fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(o.Branch))
				return o, 1
			}
			p.judged = o.Branch
			o.Judgment = &promoteJudgment{
				What:      "a pull request check failed: " + failedCheck,
				Check:     failedCheck,
				Decisions: append([]string(nil), promoteDecisions...),
			}
			fmt.Fprintf(stdout, "JUDGMENT check failed branch=%s check=%s decisions=%s\n", oneline.Field(o.Branch), oneline.Field(failedCheck), strings.Join(o.Judgment.Decisions, ","))
			return o, 1
		}
		if !passed {
			o.WaitOn = "checks"
			fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s pr=%s checks=pending\n", oneline.Field(o.Branch), number)
			return o, 0
		}
		p.say(stdout, "enqueue", "pr="+number)
		enqueued, err := f.Enqueue(ctx, view.ID)
		if err != nil {
			return o, p.fail(stderr, err)
		}
		confirmed, err := f.ConfirmEntry(ctx, view.ID)
		if err != nil {
			return o, p.fail(stderr, err)
		}
		if confirmed != "" {
			entry = confirmed
		} else if enqueued != "" {
			entry = enqueued
		}
	}
	if entry == "" {
		return o, p.fail(stderr, errors.New("the merge queue has no entry for pull request "+number+"; run: nova-sprint promote --dry-run"))
	}
	o.Entry = entry
	fmt.Fprintf(stdout, "PROMOTE QUEUE branch=%s entry=%s\n", oneline.Field(o.Branch), oneline.Field(o.Entry))
	p.say(stdout, "queue", "branch="+oneline.Field(o.Branch))
	failed, checkName, logText, err := f.MergeGroupStatus(ctx, o.Branch)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if failed {
		if p.judged == o.Branch {
			fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(o.Branch))
			return o, 1
		}
		p.judged = o.Branch
		what := "a merge-group run failed"
		if checkName != "" {
			what = "a merge-group run failed: " + checkName
		}
		o.Judgment = &promoteJudgment{
			What:      what,
			Check:     checkName,
			Tail:      logTail(logText),
			Decisions: append([]string(nil), promoteDecisions...),
		}
		if checkName != "" {
			fmt.Fprintf(stdout, "JUDGMENT merge-group failed branch=%s check=%s decisions=%s\n%s\n", oneline.Field(o.Branch), oneline.Field(checkName), strings.Join(o.Judgment.Decisions, ","), o.Judgment.Tail)
		} else {
			fmt.Fprintf(stdout, "JUDGMENT merge-group failed branch=%s decisions=%s\n%s\n", oneline.Field(o.Branch), strings.Join(o.Judgment.Decisions, ","), o.Judgment.Tail)
		}
		return o, 1
	}
	// the queue may have merged it between the view and the run list
	view, err = f.PRView(ctx, number)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if view.MergeSHA != "" {
		return p.record(ctx, o, view.MergeSHA, stdout, stderr)
	}
	o.WaitOn = "queue"
	fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s pr=%s queue=%s\n", oneline.Field(o.Branch), number, oneline.Field(o.Entry))
	return o, 0
}

// record writes the promotion to the sprint's store, prints `promoted --sha`
// and remembers the sha so the next pass's landed list starts after it. A store
// that does not record it leaves the open promotion in place, so the next pass
// records the same merge.
func (p *promoter) record(ctx context.Context, o promoteOutcome, sha string, stdout, stderr io.Writer) (promoteOutcome, int) {
	p.say(stdout, "record", "sha="+sha)
	if p.promoted != nil {
		if err := p.promoted(ctx, sha); err != nil {
			fmt.Fprintf(stderr, "nova-sprint promote: %s; run: nova-sprint promoted --sha %s\n", oneline.Err(err), sha)
			return o, 1
		}
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
	if _, err := p.git(ctx, "config", "--local", "--unset", "promote.tip"); err != nil {
		return o, p.fail(stderr, err)
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

// mergeTarget is the cut commit: tip with the target merged in. The merge is
// git merge-tree, so the checkout, the index and every branch stay where they
// are, and nothing is resolved: a conflict returns the conflicted paths and no
// commit. A tip already holding the whole target is the cut itself.
func (p *promoter) mergeTarget(ctx context.Context, branch, tip, target string, behind int) (sha string, conflictFiles []string, err error) {
	if behind == 0 {
		return tip, nil, nil
	}
	out, mergeErr := p.gitOut(ctx, "merge-tree", "--write-tree", "--name-only", "--no-messages", tip, target)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	tree := strings.TrimSpace(lines[0])
	if !isHexID(tree) {
		if mergeErr == nil {
			mergeErr = errors.New("no tree: " + oneLine(out))
		}
		return "", nil, fmt.Errorf("git merge-tree %s into %s: %w", target, branch, mergeErr)
	}
	if mergeErr != nil {
		for _, l := range lines[1:] {
			if l = strings.TrimSpace(l); l != "" {
				conflictFiles = append(conflictFiles, l)
			}
		}
		if len(conflictFiles) == 0 {
			return "", nil, fmt.Errorf("git merge-tree %s into %s: %w", target, branch, mergeErr)
		}
		return "", conflictFiles, nil
	}
	sha, err = p.git(ctx, "commit-tree", tree, "-p", tip, "-p", target, "-m", "merge "+p.base+" into "+branch)
	if err != nil {
		return "", nil, err
	}
	return sha, nil, nil
}

// isHexID says s is a full object id.
func isHexID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// since is the revision the landed list starts after: the last recorded
// promotion, else origin's copy of the target.
func (p *promoter) since(ctx context.Context, target string) (string, error) {
	if sha, err := p.rev(ctx, "refs/promoted/last"); err == nil && sha != "" {
		return sha, nil
	}
	return p.rev(ctx, target)
}

// pending is an open promotion of this same tip: its branch and pull request
// number. ok is false when there is none or the tip has moved.
func (p *promoter) pending(ctx context.Context, tip string) (branch, pr string, ok bool) {
	branch, err := p.git(ctx, "config", "--local", "--get", "promote.branch")
	if err != nil || branch == "" || branch == p.live || !strings.HasPrefix(branch, "promo/") {
		return "", "", false
	}
	storedTip, err := p.git(ctx, "config", "--local", "--get", "promote.tip")
	if err == nil && storedTip != "" {
		if storedTip != tip {
			return "", "", false
		}
	} else {
		sha, err := p.rev(ctx, branch)
		if err != nil || sha != tip {
			return "", "", false
		}
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
	// The cut is never checked out in the clone, so the gate runs in a
	// worktree of the cut commit, removed after.
	wt, err := os.MkdirTemp("", "nova-promote-gate-")
	if err != nil {
		return "", err
	}
	if _, err := p.git(ctx, "worktree", "add", "-q", "--detach", wt, sha); err != nil {
		// ignored: the directory is the empty one MkdirTemp made; the add's error is the one returned
		_ = os.Remove(wt)
		return "", err
	}
	defer func() {
		// The directory goes with the worktree; one left behind is listed by git
		// worktree list and cleared by git worktree prune.
		// ignored: the gate's verdict stands whether or not the worktree is removed
		_, _ = p.git(context.WithoutCancel(ctx), "worktree", "remove", "--force", wt)
	}()
	b := subproc.Prepare(ctx, landCheckBudget, "sh", "-c", p.check)
	defer b.Cancel()
	b.Cmd.Dir = wt
	if p.env != nil {
		b.Cmd.Env = p.env
	}
	raw, err := b.Cmd.CombinedOutput()
	if err = b.Wrap("check "+p.check, err); err != nil {
		return string(raw), errors.New("the tree gate failed: " + oneline.Err(err) + checkTail(string(raw)))
	}
	return string(raw), nil
}

func (p *promoter) prView(ctx context.Context, number string) (prJSON, string, error) {
	raw, err := p.gh(ctx, "pr", "view", number, "--json", "id,state,mergeCommit")
	if err != nil {
		return prJSON{}, raw, err
	}
	var v prJSON
	if jerr := json.Unmarshal([]byte(raw), &v); jerr != nil {
		return prJSON{}, raw, fmt.Errorf("pull request %s view: %s", number, oneLine(raw))
	}
	return v, raw, nil
}

func (p *promoter) enqueue(ctx context.Context, id string) (string, error) {
	raw, err := p.gh(ctx, "api", "graphql", "-f", "query="+promoteEnqueueQuery, "-f", "id="+id)
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

func (p *promoter) confirm(ctx context.Context, id string) (string, error) {
	raw, err := p.gh(ctx, "api", "graphql", "-f", "query="+promoteQueueQuery, "-f", "id="+id)
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

// mergeGroup reports a failed merge-group run for the branch and its log.
func (p *promoter) mergeGroup(ctx context.Context, branch string) (failed bool, checkName string, logText string, err error) {
	raw, err := p.gh(ctx, "run", "list", "--branch", branch, "--event", "merge_group", "--json", "databaseId,conclusion,status,name", "--limit", "5")
	if err != nil {
		return false, "", "", err
	}
	var runs []struct {
		DatabaseID int    `json:"databaseId"`
		Conclusion string `json:"conclusion"`
		Status     string `json:"status"`
		Name       string `json:"name"`
	}
	if jerr := json.Unmarshal([]byte(raw), &runs); jerr != nil {
		return false, "", "", fmt.Errorf("merge-group runs: %s", oneLine(raw))
	}
	for _, r := range runs {
		if r.Conclusion != "failure" {
			continue
		}
		logText, err = p.gh(ctx, "run", "view", strconv.Itoa(r.DatabaseID), "--log-failed")
		if err != nil {
			return true, r.Name, logText, nil
		}
		return true, r.Name, logText, nil
	}
	return false, "", "", nil
}

func (p *promoter) git(ctx context.Context, args ...string) (string, error) {
	if p.gitRun != nil {
		return p.gitRun(ctx, p.dir, args...)
	}
	res, err := gitrun.Run(ctx, gitrun.Options{C: p.dir, Env: p.env, OwnRepo: p.dir != ""}, args...)
	if err != nil {
		words := strings.TrimSpace(string(res.Stderr) + "\n" + string(res.Stdout))
		return "", fmt.Errorf("git %s: %s", args[0], oneLine(words))
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// gitOut is git's stdout even when git exits non-zero (merge-tree prints the
// conflicted paths and exits 1).
func (p *promoter) gitOut(ctx context.Context, args ...string) (string, error) {
	if p.gitRun != nil {
		return p.gitRun(ctx, p.dir, args...)
	}
	res, err := gitrun.Run(ctx, gitrun.Options{C: p.dir, Env: p.env, OwnRepo: p.dir != ""}, args...)
	if err != nil {
		return string(res.Stdout), fmt.Errorf("git %s: %s", args[0], oneLine(strings.TrimSpace(string(res.Stderr))))
	}
	return string(res.Stdout), nil
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

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

// promoteStep cuts a frozen promote/<date>-<n> (the legacy promo/ spelling
// too) from the sprint tip and opens its pull request to dev
// (docs/SPEC-SPRINT.md section 11, promote). The pull
// request head is that branch, never the live sprint branch: a queued pull
// request whose head is the live branch blocks the lander's pushes (GH006,
// found 2026-10-04). The tree gate runs on the base tip before the pull
// request. Admission to the merge queue is the enqueuePullRequest mutation,
// which carries no merge strategy (the queue refuses one); a query of
// mergeQueueEntry confirms the entry. gh pr merge, and a flag named auto, are
// the spelling TestNoGhPrMergeSpellingInTheToolsGo refuses, and that test names
// this mutation as the one admission. A failed run of the pull request's branch
// raises one judgment, decisions fix-and-recut and skip, with the failing
// check's log tail, and cuts one fix card per failing test (promote_red.go); a
// failed run on the base at the last promotion's merge does the same. A merge
// records `promoted --sha`.
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

// promoteCheckDefault is the whole-tree gate the verb runs when --check is
// left unset, from the module root: the build, the vet and every package's
// tests (the tree's own checks, internal/docs and internal/ci, among them).
// It is a refusal, never a skip, when it cannot run: a promoter with no gate
// at all does not promote.
const promoteCheckDefault = "go build ./... && go vet ./... && go test ./..."

// promoter is one promote schedule over one clone.
type promoter struct {
	dir, live, base, check string
	now                    time.Time
	dry                    bool
	env                    []string
	forge                  promoteForge
	recorder               promoteRecorder
	// gitRun and ghRun, when set, stand in for git and gh (a test). Nil runs
	// the real programs.
	gitRun func(ctx context.Context, dir string, args ...string) (string, error)
	ghRun  func(ctx context.Context, dir string, args ...string) (string, error)
	// gate, when set, is the tree gate (a test). Nil runs --check, the
	// whole-tree gate by default; an empty --check refuses, it never skips.
	gate func(ctx context.Context, dir, sha string) (string, error)
	// judged is the branch a judgment was already raised for, so a later pass
	// does not raise a second one.
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
	st, err := r.a.store(c)
	if err != nil {
		return "", err
	}
	req := sprint.PromotedReq{Sha: sha, Who: c.actor}
	res, err := st.Run(ctx, store.Step{
		Args: store.ArgsOf(req),
		Verb: "promoted",
		Load: []string{sprint.Work},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.PromotedOnce(s, req) },
	})
	if err != nil {
		return "", err
	}
	return strings.Join(res.Said, "\n"), nil
}

// promoteJudgment is the one judgment a red run raises: the fix cards it cut,
// and the failing tests an open card already named.
type promoteJudgment struct {
	What      string
	Tail      string
	Decisions []string
	Cards     []string
	Open      []string
}

// promoteOutcome is what one pass did.
type promoteOutcome struct {
	Branch   string // the frozen branch, promote/<date>-<n>
	Live     string // the live sprint branch, never the pull request head
	Tip      string // the sprint tip the branch was cut from
	Head     string // the pull request head ref
	Body     string
	Cards    []string
	Entry    string // the confirmed merge-queue entry, when there is one
	Promoted string // the merge sha, when it merged
	Judgment *promoteJudgment
	// DevJudgment is the judgment of a red run on the base at the last
	// promotion's merge, when this pass raised one.
	DevJudgment *promoteJudgment
	Dry         bool
	Nothing     bool
	Cut         bool // a frozen branch was cut on this pass
	Pending     bool // the promotion is in flight on the forge
	Waiting     string
}

// promoteCheckFlag registers promote's --check with the whole-tree gate
// default. cmdPromote and the test that pins the default read it here, so the
// two cannot drift.
func promoteCheckFlag(fs flagSet) *string {
	return fs.String("check", promoteCheckDefault, "the tree gate, a command run on the base tip before the pull request (default: go build ./... && go vet ./... && go test ./...)")
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
	dry := fs.Bool("dry-run", false, "print the branch and the landed cards and change nothing")
	once := fs.Bool("once", false, "carry one promotion from the cut to the recorded merge (or to the judgment or refusal that stops it) and exit")
	poll := fs.Duration("poll", time.Minute, "how often to look while a promotion is in flight, its checks or its merge queue (default 1m)")
	branch := fs.String("branch", "", "the live sprint branch the cut is taken from (default: the checkout's current branch)")
	repo := fs.String("repo-dir", "", "the clone the branch is cut in (default: the current directory)")
	base := fs.String("base", "dev", "the branch the pull request targets (default dev)")
	check := promoteCheckFlag(fs)
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
	if !*dry {
		p.red = a.redCutter(*c)
		p.recorder = appRecorder{a: a, c: *c}
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
		if *dry || (*once && !out.Pending) {
			return code
		}
		wait := *every
		if *landings > 0 && wait > time.Minute {
			wait = time.Minute
		}
		if (out.Pending || *once) && wait > *poll {
			wait = *poll
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
	if !p.dry {
		o.DevJudgment = p.devRed(ctx, stdout, stderr)
	}
	tip, err := p.rev(ctx, live)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	o.Tip = tip
	if branch, pr, ok := p.pending(ctx, tip); ok {
		o.Branch, o.Head = branch, branch
		return p.watch(ctx, o, pr, stdout, stderr)
	}
	since, err := p.since(ctx)
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
	listed, err := p.git(ctx, "branch", "--list", "promote/"+day+"-*", "promo/"+day+"-*")
	if err != nil {
		return o, p.fail(stderr, err)
	}
	branch := nextPromo(day, listed)
	o.Branch, o.Head = branch, branch
	o.Body = promoteBody(o.Cards)
	if branch == live || (!strings.HasPrefix(branch, "promote/") && !strings.HasPrefix(branch, "promo/")) {
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
	if _, err := p.runGate(ctx, tip); err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "branch", "--no-track", branch, tip); err != nil {
		return o, p.fail(stderr, err)
	}
	spec := "refs/heads/" + branch + ":refs/heads/" + branch
	if strings.Contains(spec, "refs/heads/"+live+":") {
		return o, p.fail(stderr, errors.New("the push spec names the live sprint branch "+live))
	}
	if _, err := p.git(ctx, "push", "origin", spec); err != nil {
		return o, p.fail(stderr, err)
	}
	f := p.forged()
	title := "promote " + branch
	number, err := f.OpenPR(ctx, p.base, branch, title, o.Body)
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

// watch enqueues a pull request that is not merged, confirms the queue entry,
// and either records the merge or raises the one judgment.
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
	confirmed, err := f.Confirm(ctx, view.ID)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if confirmed == "" {
		checks, err := f.Checks(ctx, number)
		if err != nil {
			return o, p.fail(stderr, err)
		}
		switch checks.State {
		case checksFail:
			runs, _ := f.FailedRuns(ctx, "--branch", o.Branch)
			if p.judged == o.Branch {
				fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(o.Branch))
				return o, 1
			}
			p.judged = o.Branch
			o.Judgment = p.redJudgment(ctx, runs, "a check of the pull request failed: "+checks.Name, "the pull request of "+o.Branch, promoteDecisions, stderr)
			fmt.Fprintf(stdout, "JUDGMENT promotion red branch=%s decisions=%s cards=%s open=%s\n%s\n", oneline.Field(o.Branch), strings.Join(o.Judgment.Decisions, ","),
				oneline.Field(dashed(strings.Join(o.Judgment.Cards, ","))), oneline.Field(dashed(strings.Join(o.Judgment.Open, ","))), o.Judgment.Tail)
			return o, 1
		case checksPending:
			o.Pending = true
			o.Waiting = "checks pending: " + checks.Name
			fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s pr=%s waiting=%s\n", oneline.Field(o.Branch), number, oneline.Field(o.Waiting))
			return o, 0
		case checksPass:
			entry, err := f.Enqueue(ctx, view.ID)
			if err != nil {
				return o, p.fail(stderr, err)
			}
			confirmed, err = f.Confirm(ctx, view.ID)
			if err != nil {
				return o, p.fail(stderr, err)
			}
			if confirmed == "" && entry != "" {
				confirmed = entry
			}
			if confirmed == "" {
				return o, p.fail(stderr, errors.New("the merge queue has no entry for pull request "+number+"; run: nova-sprint promote --dry-run"))
			}
		}
	}
	o.Entry = confirmed
	fmt.Fprintf(stdout, "PROMOTE QUEUE branch=%s entry=%s\n", oneline.Field(o.Branch), oneline.Field(o.Entry))
	runs, err := f.FailedRuns(ctx, "--branch", o.Branch)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if len(runs) > 0 {
		if p.judged == o.Branch {
			fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(o.Branch))
			return o, 1
		}
		p.judged = o.Branch
		o.Judgment = p.redJudgment(ctx, runs, "a check of the pull request failed", "the pull request of "+o.Branch, promoteDecisions, stderr)
		fmt.Fprintf(stdout, "JUDGMENT promotion red branch=%s decisions=%s cards=%s open=%s\n%s\n", oneline.Field(o.Branch), strings.Join(o.Judgment.Decisions, ","),
			oneline.Field(dashed(strings.Join(o.Judgment.Cards, ","))), oneline.Field(dashed(strings.Join(o.Judgment.Open, ","))), o.Judgment.Tail)
		return o, 1
	}
	// the queue may have merged it between the view and the run list
	view, err = f.View(ctx, number)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if view.Merged != "" {
		return p.record(ctx, o, view.Merged, stdout, stderr)
	}
	o.Pending = true
	o.Waiting = "in merge queue"
	fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s pr=%s entry=%s\n", oneline.Field(o.Branch), number, oneline.Field(o.Entry))
	return o, 0
}

// record prints `promoted --sha` and remembers the sha so the next pass's
// landed list starts after it.
func (p *promoter) record(ctx context.Context, o promoteOutcome, sha string, stdout, stderr io.Writer) (promoteOutcome, int) {
	if p.recorder != nil {
		if _, err := p.recorder.Record(ctx, sha); err != nil {
			return o, p.fail(stderr, err)
		}
	}
	if _, err := p.git(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", sha+"^{commit}"); err != nil {
		if _, ferr := p.git(ctx, "fetch", "origin", p.base); ferr != nil {
			// ignored: sha may already have been fetched by base
			_, _ = p.git(ctx, "fetch", "origin", sha)
		}
	}
	if _, err := p.git(ctx, "update-ref", "refs/promoted/last", sha); err != nil {
		return o, p.fail(stderr, err)
	}
	// ignored: config key may not be set
	_, _ = p.git(ctx, "config", "--local", "--unset", "promote.branch")
	// ignored: config key may not be set
	_, _ = p.git(ctx, "config", "--local", "--unset", "promote.pr")
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
func (p *promoter) since(ctx context.Context) (string, error) {
	if sha, err := p.rev(ctx, "refs/promoted/last"); err == nil && sha != "" {
		return sha, nil
	}
	return p.rev(ctx, p.base)
}

// pending is an open promotion of this same tip: its branch and pull request
// number. ok is false when there is none or the tip has moved.
func (p *promoter) pending(ctx context.Context, tip string) (branch, pr string, ok bool) {
	branch, err := p.git(ctx, "config", "--local", "--get", "promote.branch")
	if err != nil || branch == "" || branch == p.live || (!strings.HasPrefix(branch, "promote/") && !strings.HasPrefix(branch, "promo/")) {
		return "", "", false
	}
	sha, err := p.rev(ctx, branch)
	if err != nil || sha != tip {
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
		return "", errors.New("no tree gate: --check is empty, and the promotion is refused rather than pushed ungated; run: nova-sprint promote --check " + strconv.Quote(promoteCheckDefault))
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

// ghRunRow is one run gh run list prints.
type ghRunRow struct {
	DatabaseID int    `json:"databaseId"`
	Conclusion string `json:"conclusion"`
	Status     string `json:"status"`
	Name       string `json:"name"`
}

func (p *promoter) failedRuns(ctx context.Context, filter ...string) ([]ghRunRow, error) {
	return p.forged().FailedRuns(ctx, filter...)
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

// nextPromo is promote/<day>-<n>, n one past the highest listed branch of that day.
func nextPromo(day, listed string) string {
	n := 0
	for _, line := range strings.Split(listed, "\n") {
		name := strings.TrimSpace(line)
		name = strings.TrimPrefix(name, "*")
		name = strings.TrimSpace(name)
		var rest string
		if strings.HasPrefix(name, "promote/"+day+"-") {
			rest = strings.TrimPrefix(name, "promote/"+day+"-")
		} else if strings.HasPrefix(name, "promo/"+day+"-") {
			rest = strings.TrimPrefix(name, "promo/"+day+"-")
		} else {
			continue
		}
		k, err := strconv.Atoi(rest)
		if err == nil && k > n {
			n = k
		}
	}
	return "promote/" + day + "-" + strconv.Itoa(n+1)
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

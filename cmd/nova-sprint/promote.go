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
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
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

// promoteDevDecisions are the one judgment a red development branch after a promotion
// raises: the fix cards the machine cut, or a revert of the promotion.
var promoteDevDecisions = []string{"fix", "revert"}

// promoteRedStream is the stream a red check's fix cards go into: promote-red-<YYYY-MM-DD>.
const promoteRedStream = "promote-red-"

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
	// judged is the branch a judgment was already raised for, so a later pass
	// does not raise a second one.
	judged string
	// cut, when set, admits the fix cards of a red check (promoteCutter): it returns the
	// cards it cut and what it said of the rest (a test an open card is already on). Nil
	// cuts none, and the judgment says so.
	cut func(ctx context.Context, r sprint.CIFixReq) (cut, said []string, err error)
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
	// Cards is the fix cards the red check cut, one per failing test no open card names.
	Cards []string
	// Said is what the cut said beside its cards: a test an open card is on, a log that
	// names no failing test, or what kept the cut from the store.
	Said []string
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
	}
	if !*dry {
		p.cut = a.promoteCutter(*c)
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
	tip, err := p.rev(ctx, live)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	o.Tip = tip
	if j, err := p.devCheck(ctx, tip, stdout); err != nil {
		return o, p.fail(stderr, err)
	} else if j != nil {
		o.Judgment = j
		return o, 1
	}
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
	if _, err := p.git(ctx, "branch", "--no-track", branch, tip); err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.runGate(ctx, tip); err != nil {
		return o, p.fail(stderr, err)
	}
	spec := "refs/heads/" + branch + ":refs/heads/" + branch
	if strings.Contains(spec, "refs/heads/"+live+":") {
		return o, p.fail(stderr, errors.New("the push spec names the live sprint branch "+live))
	}
	if _, err := p.git(ctx, "push", "origin", spec); err != nil {
		return o, p.fail(stderr, err)
	}
	title := "promote " + branch
	url, err := p.gh(ctx, "pr", "create", "--base", p.base, "--head", branch, "--title", title, "--body", o.Body)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	number, err := prNumber(url)
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
	view, raw, err := p.prView(ctx, number)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if sha := mergedSHA(view); sha != "" {
		return p.record(ctx, o, sha, stdout, stderr)
	}
	if view.ID == "" {
		return o, p.fail(stderr, errors.New("the pull request "+number+" has no id ("+oneLine(raw)+")"))
	}
	entry, err := p.enqueue(ctx, view.ID)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	confirmed, err := p.confirm(ctx, view.ID)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if confirmed == "" {
		return o, p.fail(stderr, errors.New("the merge queue has no entry for pull request "+number+"; run: nova-sprint promote --dry-run"))
	}
	o.Entry = confirmed
	if entry != "" && entry != confirmed {
		o.Entry = confirmed
	}
	fmt.Fprintf(stdout, "PROMOTE QUEUE branch=%s entry=%s\n", oneline.Field(o.Branch), oneline.Field(o.Entry))
	failed, logText, err := p.mergeGroup(ctx, o.Branch)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if failed {
		if p.judged == o.Branch {
			fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(o.Branch))
			return o, 1
		}
		p.judged = o.Branch
		o.Judgment = &promoteJudgment{
			What:      "a merge-group run failed",
			Tail:      logTail(logText),
			Decisions: append([]string(nil), promoteDecisions...),
		}
		o.Judgment.Cards, o.Judgment.Said = p.cutFixes(ctx, o.Tip, logText, "the pull request #"+number+" of "+o.Branch)
		fmt.Fprintf(stdout, "JUDGMENT merge-group failed branch=%s decisions=%s cards=%s\n", oneline.Field(o.Branch), strings.Join(o.Judgment.Decisions, ","), oneline.Field(strings.Join(o.Judgment.Cards, ",")))
		printSaid(stdout, o.Judgment.Said)
		fmt.Fprintln(stdout, o.Judgment.Tail)
		return o, 1
	}
	// the queue may have merged it between the view and the run list
	view, _, err = p.prView(ctx, number)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if sha := mergedSHA(view); sha != "" {
		return p.record(ctx, o, sha, stdout, stderr)
	}
	fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s pr=%s\n", oneline.Field(o.Branch), number)
	return o, 0
}

// record prints `promoted --sha` and remembers the sha so the next pass's
// landed list starts after it.
func (p *promoter) record(ctx context.Context, o promoteOutcome, sha string, stdout, stderr io.Writer) (promoteOutcome, int) {
	if _, err := p.git(ctx, "update-ref", "refs/promoted/last", sha); err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "config", "--local", "--unset", "promote.branch"); err != nil {
		return o, p.fail(stderr, err)
	}
	if _, err := p.git(ctx, "config", "--local", "--unset", "promote.pr"); err != nil {
		return o, p.fail(stderr, err)
	}
	// the development branch's own checks run on the merge: a later pass reads them
	// (devCheck), and a red one cuts its fix cards
	if _, err := p.git(ctx, "config", "--local", "promote.dev", sha); err != nil {
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
	if err != nil || branch == "" || branch == p.live || !strings.HasPrefix(branch, "promo/") {
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

// ghRunRow is one row of gh run list --json databaseId,conclusion,status,name.
type ghRunRow struct {
	DatabaseID int    `json:"databaseId"`
	Conclusion string `json:"conclusion"`
	Status     string `json:"status"`
	Name       string `json:"name"`
}

// runs is gh run list with the words given, read.
func (p *promoter) runs(ctx context.Context, what string, args ...string) ([]ghRunRow, error) {
	raw, err := p.gh(ctx, append(append([]string{"run", "list"}, args...), "--json", "databaseId,conclusion,status,name")...)
	if err != nil {
		return nil, err
	}
	var runs []ghRunRow
	if jerr := json.Unmarshal([]byte(raw), &runs); jerr != nil {
		return nil, fmt.Errorf("%s runs: %s", what, oneLine(raw))
	}
	return runs, nil
}

// failedLogs is the failed log of every failed run, one after another.
func (p *promoter) failedLogs(ctx context.Context, runs []ghRunRow) (failed bool, logText string) {
	var b strings.Builder
	for _, r := range runs {
		if r.Conclusion != "failure" {
			continue
		}
		failed = true
		out, err := p.gh(ctx, "run", "view", strconv.Itoa(r.DatabaseID), "--log-failed")
		if err != nil { // the run failed all the same: its tail says why the log is missing
			out = "the failed log of run " + strconv.Itoa(r.DatabaseID) + " could not be read: " + oneline.Err(err)
		}
		b.WriteString(out)
		b.WriteByte('\n')
	}
	return failed, b.String()
}

// mergeGroup reports a failed check of the promotion's pull request, a pull_request run or
// a merge-group run of the branch, and the failed log of each.
func (p *promoter) mergeGroup(ctx context.Context, branch string) (failed bool, logText string, err error) {
	var all []ghRunRow
	for _, event := range []string{"pull_request", "merge_group"} {
		runs, err := p.runs(ctx, strings.ReplaceAll(event, "_", "-"), "--branch", branch, "--event", event, "--limit", "5")
		if err != nil {
			return false, "", err
		}
		all = append(all, runs...)
	}
	failed, logText = p.failedLogs(ctx, all)
	return failed, logText, nil
}

// devCheck reads the development branch's checks on the last promotion's merge
// (promote.dev, set when it merged). While one runs the pass goes on; when one failed it
// cuts the fix cards and raises the one judgment, once; when every one passed it lets
// the merge go.
func (p *promoter) devCheck(ctx context.Context, tip string, stdout io.Writer) (*promoteJudgment, error) {
	sha, err := p.git(ctx, "config", "--local", "--get", "promote.dev")
	if err != nil || sha == "" {
		return nil, nil
	}
	runs, err := p.runs(ctx, "dev", "--branch", p.base, "--commit", sha, "--limit", "20")
	if err != nil {
		return nil, err
	}
	for _, r := range runs {
		if r.Status != "completed" {
			fmt.Fprintf(stdout, "PROMOTE DEV WAIT base=%s sha=%s run=%s\n", oneline.Field(p.base), sha, oneline.Field(r.Name))
			return nil, nil
		}
	}
	if len(runs) == 0 {
		fmt.Fprintf(stdout, "PROMOTE DEV WAIT base=%s sha=%s run=none\n", oneline.Field(p.base), sha)
		return nil, nil
	}
	failed, logText := p.failedLogs(ctx, runs)
	if _, err := p.git(ctx, "config", "--local", "--unset", "promote.dev"); err != nil {
		return nil, err
	}
	if !failed {
		fmt.Fprintf(stdout, "PROMOTE DEV GREEN base=%s sha=%s\n", oneline.Field(p.base), sha)
		return nil, nil
	}
	j := &promoteJudgment{
		What:      "the development branch went red after a promotion",
		Tail:      logTail(logText),
		Decisions: append([]string(nil), promoteDevDecisions...),
	}
	j.Cards, j.Said = p.cutFixes(ctx, tip, logText, p.base+" at "+sha)
	fmt.Fprintf(stdout, "JUDGMENT dev red base=%s sha=%s decisions=%s cards=%s\n", oneline.Field(p.base), sha, strings.Join(j.Decisions, ","), oneline.Field(strings.Join(j.Cards, ",")))
	printSaid(stdout, j.Said)
	fmt.Fprintln(stdout, j.Tail)
	return j, nil
}

// cutFixes cuts one fix card per failing test of a red check's failed log into the day's
// promote-red stream (sprint.CIFix), on the live sprint branch. What kept a card from the
// store is said, never an error of the pass: the judgment still names the red check.
func (p *promoter) cutFixes(ctx context.Context, tip, logText, source string) (cards, said []string) {
	module := p.module(ctx, tip)
	fails := sprint.ParseFailedLog(logText, module)
	if len(fails) == 0 {
		return nil, []string{"the failed log names no failing test (--- FAIL:): no fix card cut; read the tail"}
	}
	if p.cut == nil {
		return nil, []string{"no store to cut the fix cards into (a dry run): no fix card cut"}
	}
	r := sprint.CIFixReq{
		Stream: promoteRedStream + p.now.Format("2006-01-02"),
		Repo:   repoOf(module), Base: p.live, Source: source, Failures: fails,
	}
	cards, said, err := p.cut(ctx, r)
	if err != nil {
		return nil, append(said, "the fix cards were not cut: "+oneline.Err(err))
	}
	return cards, said
}

// module is the module path of the tip's go.mod, "" when it has none.
func (p *promoter) module(ctx context.Context, tip string) string {
	out, err := p.git(ctx, "show", tip+":go.mod")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(out, "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(l), "module "); ok {
			return strings.Trim(strings.TrimSpace(m), `"`)
		}
	}
	return ""
}

// repoOf is owner/name of a github.com module path, else the module path.
func repoOf(module string) string {
	if rest, ok := strings.CutPrefix(module, "github.com/"); ok {
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) >= 2 {
			return parts[0] + "/" + parts[1]
		}
	}
	return module
}

func printSaid(stdout io.Writer, said []string) {
	for _, s := range said {
		fmt.Fprintf(stdout, "NOTE %s\n", oneline.Escape(s))
	}
}

// promoteCutter is the step that admits a red check's fix cards into the store, as the
// machine: each brief held to the sprint's card lint as add holds one, then one write that
// cuts a card per failing test no open card names (sprint.CIFix), so the coordinator
// writes no fix card by hand.
func (a *app) promoteCutter(c common) func(ctx context.Context, r sprint.CIFixReq) ([]string, []string, error) {
	return func(ctx context.Context, r sprint.CIFixReq) ([]string, []string, error) {
		var st *store.Store
		var words bytes.Buffer
		rs, code := a.briefRules("promote", "", &c, &st, &words)
		if code != 0 {
			return nil, nil, errors.New(oneLine(words.String()))
		}
		if st == nil {
			return nil, nil, errors.New("the sprint's rules are the server's, and promote cuts its fix cards on the store itself; run it with --redis")
		}
		r.Rules = swarm.RulesParagraph(rs.rules)
		r.Who = sprint.MachineActor
		s, err := st.Load(ctx, []string{sprint.Work}, nil)
		if err != nil {
			return nil, nil, err
		}
		if cards := sprint.CIFixCards(s, r); len(cards) > 0 {
			r.Held = cardRules(cards[0].Brief, rs).held
			if code := lintBriefFiles("promote", sprint.CIFixCards(s, r), rs, 0, &words); code != 0 {
				return nil, nil, errors.New(oneLine(words.String()))
			}
		}
		var cut []string
		step := store.Step{Verb: "add", Named: true, Actor: sprint.MachineActor, Args: store.ArgsOf(r), Mirrors: true,
			Load: []string{sprint.Work, sprint.Merge, sprint.Fleet},
			Extras: func(*sprint.Snapshot) map[string][]string {
				ids := make([]string, 0, len(r.Failures))
				for _, f := range r.Failures {
					ids = append(ids, sprint.CIFixID(f.Test))
				}
				return map[string][]string{sprint.Work: ids, sprint.Merge: {sprint.CtlID(r.Stream)}}
			},
			Plan: func(s *sprint.Snapshot) sprint.Plan {
				cut = cut[:0]
				for _, cd := range sprint.CIFixCards(s, r) {
					cut = append(cut, cd.ID)
				}
				return sprint.CIFix(s, r)
			}}
		res, err := st.Run(ctx, step)
		if err != nil {
			return nil, res.Said, err
		}
		if len(res.Refused) > 0 {
			var why []string
			for _, f := range res.Refused {
				why = append(why, fmt.Sprintf("%v", f))
			}
			return nil, res.Said, errors.New("the store refused the fix cards: " + strings.Join(why, "; "))
		}
		return append([]string(nil), cut...), res.Said, nil
	}
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

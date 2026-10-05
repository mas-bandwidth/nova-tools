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

// promoteStep carries one promotion from a cut to a recorded sha with no hand
// steps (docs/SPEC-SPRINT.md section 11, promote). It fetches origin and cuts
// promo/<date>-<n> from origin/<branch>, never from a local ref. A cut that is
// not ahead of origin/<base> is refused. The target is merged into the cut; a
// conflict stops with one judgment naming the files and resolves nothing. The
// tree gate runs on that merged commit, the pull request is opened, and
// admission is the enqueuePullRequest mutation, which carries no merge strategy
// (the queue refuses one). The pull request head is the frozen branch, never
// the live sprint branch: a queued pull request whose head is the live branch
// blocks the lander's pushes (GH006, found 2026-10-04). A query of
// mergeQueueEntry confirms the entry. A failed merge-group run raises one
// judgment naming the failing check, decisions fix-and-recut and skip, with
// the check's log tail. When the queue merges, the pass records `promoted
// --sha`. Each step is printed before the call that can block.
//
// The queries are one literal each, so they are not an argument list of pr and
// merge, and neither is a strategy flag.
//
// promoteForge is the one seam those forge calls go through. A test passes a
// fake. The real forge runs gh through internal/subproc.
const (
	promoteEnqueueQuery = `mutation($id:ID!){enqueuePullRequest(input:{pullRequestId:$id}){mergeQueueEntry{id}}}`
	promoteQueueQuery   = `query($id:ID!){node(id:$id){... on PullRequest{mergeQueueEntry{id state}}}}`
)

// promoteForge is every forge call the promote pass makes. Call is one gh
// invocation. Tests pass a fake; nothing in a test runs gh.
type promoteForge interface {
	Call(ctx context.Context, args ...string) (string, error)
}

// promoteForgeFunc adapts a function to promoteForge.
type promoteForgeFunc func(ctx context.Context, args ...string) (string, error)

func (f promoteForgeFunc) Call(ctx context.Context, args ...string) (string, error) {
	return f(ctx, args...)
}

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
	// forge, when set, is the forge (a test). Nil uses ghRun when that is set,
	// else gh through internal/subproc. Every forge call goes through
	// promoteForge, including a ghRun wrapped as one.
	forge promoteForge
	// gitRun and ghRun, when set, stand in for git and gh (a test). Nil runs
	// the real programs. ghRun is wrapped as a promoteForge.
	gitRun func(ctx context.Context, dir string, args ...string) (string, error)
	ghRun  func(ctx context.Context, dir string, args ...string) (string, error)
	// gate, when set, is the tree gate (a test). Nil runs --check, or nothing
	// when --check is empty.
	gate func(ctx context.Context, dir, sha string) (string, error)
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
	fs, _ := a.verbSetup("promote")
	every := fs.Duration("every", promoteEveryDefault, "how often to look, a duration (default 1h); each pass fetches origin and promotes when that long has passed or --landings cards have landed")
	landings := fs.Int("landings", 0, "also promote once this many cards have landed since the last promotion (0: the clock only)")
	dry := fs.Bool("dry-run", false, "fetch origin, print the branch and the landed cards, and change nothing")
	branch := fs.String("branch", "", "the live sprint branch to cut from origin (default: the checkout's current branch); the cut is origin/<branch>, never the local ref")
	repo := fs.String("repo-dir", "", "the clone the branch is cut in (default: the current directory)")
	base := fs.String("base", "dev", "the target branch (default dev); origin/<base> is fetched and merged into the cut, which is refused when it is not ahead of that target")
	check := fs.String("check", "", "the tree gate, a command run on the merged cut before the pull request (default: none)")
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
		if *landings > 0 && wait > time.Minute {
			wait = time.Minute
		}
		fmt.Fprintf(stdout, "PROMOTE WAITING %s before the next pass\n", wait)
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

// step is one promote pass. It fetches before it reads a tip, cuts from
// origin/<branch>, and prints a line before every call that can block.
func (p *promoter) step(ctx context.Context, stdout, stderr io.Writer) (promoteOutcome, int) {
	o := promoteOutcome{Live: p.live, Dry: p.dry}
	if p.base == "" {
		p.base = "dev"
	}
	if p.now.IsZero() {
		p.now = time.Now()
	}
	if p.live == "" {
		fmt.Fprintf(stdout, "PROMOTE BRANCH reading the checkout\n")
	}
	live, err := p.liveBranch(ctx)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	o.Live = live
	p.live = live
	fmt.Fprintf(stdout, "PROMOTE FETCH origin branch=%s base=%s\n", oneline.Field(live), oneline.Field(p.base))
	if _, err := p.git(ctx, "fetch", "origin", p.fetchSpec(live), p.fetchSpec(p.base)); err != nil {
		return o, p.fail(stderr, err)
	}
	tip, err := p.rev(ctx, p.originRef(live))
	if err != nil {
		return o, p.fail(stderr, err)
	}
	o.Tip = tip
	if branch, pr, ok := p.pending(ctx, tip); ok {
		o.Branch, o.Head = branch, branch
		return p.watch(ctx, o, pr, stdout, stderr)
	}
	fmt.Fprintf(stdout, "PROMOTE AHEAD cut=origin/%s target=origin/%s\n", oneline.Field(live), oneline.Field(p.base))
	n, err := p.ahead(ctx, p.originRef(live), p.originRef(p.base))
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if n == 0 {
		return o, p.fail(stderr, fmt.Errorf("the cut origin/%s is not ahead of the target origin/%s", live, p.base))
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
	fmt.Fprintf(stdout, "PROMOTE CUT-FROM ref=origin/%s sha=%s\n", oneline.Field(live), tip)
	if _, err := p.git(ctx, "branch", "--no-track", branch, tip); err != nil {
		return o, p.fail(stderr, err)
	}
	names, err := p.mergeIntoCut(ctx, stdout, branch, live, p.originRef(p.base))
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if len(names) > 0 {
		files := strings.Join(names, ",")
		o.Judgment = &promoteJudgment{
			What:      fmt.Sprintf("merge of origin/%s into %s conflicted: %s", p.base, branch, files),
			Decisions: append([]string(nil), promoteDecisions...),
		}
		fmt.Fprintf(stdout, "JUDGMENT merge conflict branch=%s files=%s decisions=%s\n", oneline.Field(branch), oneline.Field(files), strings.Join(o.Judgment.Decisions, ","))
		return o, 1
	}
	merged, err := p.rev(ctx, branch)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	fmt.Fprintf(stdout, "PROMOTE GATE sha=%s\n", merged)
	if _, err := p.runGate(ctx, merged); err != nil {
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
	fmt.Fprintf(stdout, "PROMOTE OPEN head=%s base=%s\n", oneline.Field(branch), oneline.Field(p.base))
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
	if _, err := p.git(ctx, "config", "--local", "promote.tip", tip); err != nil {
		return o, p.fail(stderr, err)
	}
	o.Cut = true
	fmt.Fprintf(stdout, "PROMOTE CUT branch=%s live=%s tip=%s pr=%s cards=%s\n", oneline.Field(branch), oneline.Field(live), tip, number, oneline.Field(strings.Join(o.Cards, ",")))
	return p.watch(ctx, o, number, stdout, stderr)
}

// fetchSpec maps a branch name to the refspec that updates its remote-tracking
// ref and nothing else.
func (p *promoter) fetchSpec(name string) string {
	return "refs/heads/" + name + ":refs/remotes/origin/" + name
}

// originRef is the remote-tracking ref of a branch. The cut is this ref, never
// the local one.
func (p *promoter) originRef(name string) string {
	return "refs/remotes/origin/" + name
}

// ahead is how many commits cutRef has that targetRef does not.
func (p *promoter) ahead(ctx context.Context, cutRef, targetRef string) (int, error) {
	out, err := p.git(ctx, "rev-list", "--count", targetRef+".."+cutRef)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("ahead count %q", strings.TrimSpace(out))
	}
	return n, nil
}

// mergeIntoCut checks out the cut, merges the target, and returns to live.
// Conflicted paths are returned and the merge is aborted; nothing is resolved.
// A clean merge, including one already contained, returns no names.
func (p *promoter) mergeIntoCut(ctx context.Context, stdout io.Writer, branch, live, targetRef string) ([]string, error) {
	fmt.Fprintf(stdout, "PROMOTE MERGE target=%s into=%s\n", oneline.Field(targetRef), oneline.Field(branch))
	if _, err := p.git(ctx, "checkout", "-q", branch); err != nil {
		return nil, err
	}
	_, merr := p.git(ctx, "merge", "--no-edit", "-m", "merge "+targetRef+" into "+branch, targetRef)
	if merr != nil {
		names, lerr := p.conflictFiles(ctx)
		if aerr := p.abortMerge(ctx, live); aerr != nil {
			return nil, aerr
		}
		if lerr != nil {
			return nil, merr
		}
		if len(names) > 0 {
			return names, nil
		}
		return nil, merr
	}
	if _, err := p.git(ctx, "checkout", "-q", live); err != nil {
		return nil, err
	}
	return nil, nil
}

// conflictFiles is the paths git still has unmerged. Empty means the failure
// was not a content conflict.
func (p *promoter) conflictFiles(ctx context.Context) ([]string, error) {
	out, err := p.git(ctx, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// abortMerge leaves the cut where it was and the checkout on live. A merge
// that never started is only the checkout. An error means the worktree may
// still be mid-merge, so the caller must not report a clean stop.
func (p *promoter) abortMerge(ctx context.Context, live string) error {
	if _, err := p.git(ctx, "rev-parse", "-q", "--verify", "MERGE_HEAD"); err == nil {
		if _, err := p.git(ctx, "merge", "--abort"); err != nil {
			return err
		}
	}
	_, err := p.git(ctx, "checkout", "-q", live)
	return err
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
	fmt.Fprintf(stdout, "PROMOTE VIEW pr=%s\n", number)
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
	fmt.Fprintf(stdout, "PROMOTE ENQUEUE pr=%s\n", number)
	entry, err := p.enqueue(ctx, view.ID)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	fmt.Fprintf(stdout, "PROMOTE CONFIRM pr=%s\n", number)
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
	fmt.Fprintf(stdout, "PROMOTE WAITING on merge queue pr=%s branch=%s\n", number, oneline.Field(o.Branch))
	failed, check, logText, err := p.mergeGroup(ctx, o.Branch)
	if err != nil {
		return o, p.fail(stderr, err)
	}
	if failed {
		if p.judged == o.Branch {
			fmt.Fprintf(stdout, "PROMOTE WAIT branch=%s judgment=already\n", oneline.Field(o.Branch))
			return o, 1
		}
		p.judged = o.Branch
		if check == "" {
			check = "unknown"
		}
		o.Judgment = &promoteJudgment{
			What:      "a merge-group run failed: " + check,
			Tail:      logTail(logText),
			Decisions: append([]string(nil), promoteDecisions...),
		}
		fmt.Fprintf(stdout, "JUDGMENT merge-group failed branch=%s check=%s decisions=%s\n%s\n", oneline.Field(o.Branch), oneline.Field(check), strings.Join(o.Judgment.Decisions, ","), o.Judgment.Tail)
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
	if _, err := p.rev(ctx, sha); err != nil {
		fmt.Fprintf(stdout, "PROMOTE FETCH merge sha=%s from origin/%s\n", sha, oneline.Field(p.base))
		if _, ferr := p.git(ctx, "fetch", "origin", p.base); ferr != nil {
			return o, p.fail(stderr, ferr)
		}
		if _, err := p.rev(ctx, sha); err != nil {
			return o, p.fail(stderr, fmt.Errorf("the merge %s is not in the clone after fetching origin/%s", sha, p.base))
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
	// ignored: the tip key is absent when an older pass recorded the promotion; the sha is already written
	_, _ = p.git(ctx, "config", "--local", "--unset", "promote.tip")
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
// promotion, else origin's target. The local base ref is not read.
func (p *promoter) since(ctx context.Context) (string, error) {
	if sha, err := p.rev(ctx, "refs/promoted/last"); err == nil && sha != "" {
		return sha, nil
	}
	return p.rev(ctx, p.originRef(p.base))
}

// pending is an open promotion of this same origin tip: its branch and pull
// request number. The recorded tip is the origin commit the branch was cut
// from, which stays that commit after the target is merged in. ok is false
// when there is none or that tip has moved.
func (p *promoter) pending(ctx context.Context, tip string) (branch, pr string, ok bool) {
	branch, err := p.git(ctx, "config", "--local", "--get", "promote.branch")
	if err != nil || branch == "" || branch == p.live || !strings.HasPrefix(branch, "promo/") {
		return "", "", false
	}
	pr, _ = p.git(ctx, "config", "--local", "--get", "promote.pr")
	if pr == "" {
		return "", "", false
	}
	recorded, err := p.git(ctx, "config", "--local", "--get", "promote.tip")
	if err == nil && strings.TrimSpace(recorded) != "" {
		return branch, pr, strings.TrimSpace(recorded) == tip
	}
	sha, err := p.rev(ctx, branch)
	if err != nil || sha != tip {
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

// mergeGroup reports a failed merge-group run for the branch, the failing
// check's name, and its log. Several failures are one result, names joined.
func (p *promoter) mergeGroup(ctx context.Context, branch string) (failed bool, check, logText string, err error) {
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
	var names []string
	for _, r := range runs {
		if r.Conclusion != "failure" {
			continue
		}
		name := r.Name
		if name == "" {
			name = "unknown"
		}
		names = append(names, name)
		if logText == "" {
			logText, _ = p.gh(ctx, "run", "view", strconv.Itoa(r.DatabaseID), "--log-failed")
		}
	}
	if len(names) == 0 {
		return false, "", "", nil
	}
	return true, strings.Join(names, ","), logText, nil
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
	return p.forgeCall().Call(ctx, args...)
}

// forgeCall is the one forge. A test's forge or ghRun is wrapped so every
// call, including the real gh, goes through promoteForge.
func (p *promoter) forgeCall() promoteForge {
	if p.forge != nil {
		return p.forge
	}
	if p.ghRun != nil {
		run := p.ghRun
		dir := p.dir
		return promoteForgeFunc(func(ctx context.Context, args ...string) (string, error) {
			return run(ctx, dir, args...)
		})
	}
	return ghForge{dir: p.dir, env: p.env}
}

// ghForge runs gh through internal/subproc. It is the real promoteForge.
type ghForge struct {
	dir string
	env []string
}

func (g ghForge) Call(ctx context.Context, args ...string) (string, error) {
	b := subproc.Prepare(ctx, subproc.GHBudget, "gh", args...)
	defer b.Cancel()
	if g.dir != "" {
		b.Cmd.Dir = g.dir
	}
	if g.env != nil {
		b.Cmd.Env = g.env
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

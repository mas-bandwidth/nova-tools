// Package harvest is nova-sprint `card harvest --bench` (#2932, spec #2756
// 3.2, 4.3, 5.4): one worker per bench, every bench in parallel, so a down
// bench never blocks another. Each worker holds lease:harvest:<b>, pushes each
// ended(DONE) card's branch nova/<S>/<label>-a<attempt> from its bench
// (idempotent to the same sha), finds or opens the PR under the idempotency
// key pr:<repo>:<branch>, writes the PR record pr:<repo>:<n> from the head a
// REST response verified, reads that record back and only then moves the
// card to harvested in one Redis Function call. The reconciler runs this as
// its harvest duty (cmd/nova-sprint/consume.go): one goroutine per bench,
// each fenced by its own lease.
//
// The PR step reuses the #2611 rule (harvest_commit.go): the harvest never
// commits to a trunk and never pushes outside its own branch prefix; here the
// branch is fixed by the card's identity, so the worker never infers one.
//
// Harvest steps and restart rule (#2932 rev 2). A card on its way to
// harvested passes durable steps in its hash field harvest_step, written only
// by the lease holder: pushed (ls-remote on the bench shows the branch at
// pushed_sha), intent (right before the REST create), published (the
// verified PR reserved under pr:<repo>:<branch>, same call) and harvested
// (the receipt). Every pass starts from the top and is idempotent at each
// step, whatever harvest_step says:
//
//	a. push, skipped when the remote is already at pushed_sha; another sha
//	   there is err=branch-moved and never a force push;
//	b. an idem key naming PR n: the PR record pr:<repo>:<n> answers with its
//	   head (no GitHub); a key with no record yet is a REST read of n; head
//	   == pushed_sha (and head.ref == branch), else err=idem-mismatch (no
//	   create, no receipt);
//	c. look up before creating (state=all): one PR is verified and recorded,
//	   two are err=pr-duplicate, a closed unmerged one is err=pr-closed;
//	d. none: intent, then the create; a 201 is verified and recorded, an
//	   ambiguous reply (timeout, reset, 5xx, 422 exists) is read back by the
//	   lookup at most len(Readback) times, else err=create-ambiguous with the
//	   card left at intent and no second POST in this pass;
//	e. the receipt (ns_card_harvested), after the PR record is read back
//	   from Redis with head == pushed_sha.
//
// A card is harvested only through (e). GitHub is asked only for what only it
// has: the PR number (the create or the lookup). The head is verified from
// that same response and then lives in the record; no pass reads it back
// from GitHub, and the lander reads the record, never GitHub.
package harvest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Redis Functions registered by internal/nsprint/fn/lua/harvest.lua.
const (
	FunctionLease     = "ns_harvest_lease"
	FunctionPass      = "ns_harvest_pass"
	FunctionDue       = "ns_harvest_due"
	FunctionPR        = "ns_harvest_pr"
	FunctionStep      = "ns_harvest_step"
	FunctionHarvested = "ns_card_harvested"
	FunctionRefuse    = "ns_harvest_refuse"
	FunctionFail      = "ns_harvest_fail"
)

// The durable harvest steps (card hash field harvest_step).
const (
	StepPushed    = "pushed"
	StepIntent    = "intent"
	StepPublished = "published"
	StepHarvested = "harvested"
)

// The fault points Options.Fault is called at: a non-nil return stops the
// pass there, as if the process had died (no pass line, no lease release).
const (
	FaultAfterPush   = "after-push"
	FaultAfterIntent = "after-intent"
	FaultAfterCreate = "after-create"
	FaultAfterRecord = "after-record"
)

// The err= codes a failed card carries (HARVEST-FAILED ... err=<code>).
const (
	CodeBranchMoved     = "branch-moved"
	CodeIdemMismatch    = "idem-mismatch"
	CodePRDuplicate     = "pr-duplicate"
	CodePRClosed        = "pr-closed"
	CodePRMismatch      = "pr-mismatch"
	CodeCreateAmbiguous = "create-ambiguous"
	CodeCreateFailed    = "create-failed"
	CodeResultsRelative = "results-relative"
	CodeNoCommit        = "no-commit"
	CodeFailed          = "failed"
)

// ErrAmbiguous marks a forge reply after which the request may have been
// applied (timeout, connection reset, 5xx, 422 "already exists").
var ErrAmbiguous = errors.New("ambiguous forge reply")

// DefaultReadback is the bounded readback after an ambiguous create: the
// lookup repeated at most three times, at 1 s, 2 s and 4 s.
var DefaultReadback = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// Defaults from the spec: the lease renews every 2 s with a 6 s TTL (2.2);
// the verified-PR clock is 5 min from card end (section 1).
const (
	DefaultClock    = 5 * time.Minute
	DefaultLeaseTTL = 6 * time.Second
	DefaultRenew    = 2 * time.Second
	DefaultLimit    = 256
	// DefaultFailCap is how many failed passes an ended(DONE) card gets
	// before ns_harvest_fail moves it to done/fail (#3712).
	DefaultFailCap = 3
)

// ErrLeaseHeld: another worker holds lease:harvest:<b>; this one does nothing.
var ErrLeaseHeld = errors.New("harvest lease held elsewhere")

// ErrFenced: the lease was lost mid-pass; the worker stops writing.
var ErrFenced = errors.New("harvest lease lost (fenced)")

// Card is one ended(DONE) card due for harvest, as ns_harvest_due returns it.
type Card struct {
	Label, Repo, Base, Attempt, PushedSHA, Identity, Results, Branch string
	// IdemPR is the PR number already recorded under pr:<repo>:<branch>, or "".
	IdemPR string
	// Step is the card's harvest_step when the pass read it, or "".
	Step string
	// BaseSHA, Stream and DoneWhen are the card hash's base_sha, stream and
	// done_when (the card's base-sha, STREAM and DONE-WHEN lines), carried
	// into the PR body and the PR record.
	BaseSHA, Stream, DoneWhen string
	// RecHead is the head in the PR record pr:<name>:<IdemPR>, or "" when
	// there is no idem PR or no record yet.
	RecHead string
}

// RecordKey is the PR record pr:<name>:<n> (prkey.Key: the bare repository
// name, whether repo is owner/name or name), the hash ns_harvest_pr writes once
// (head, base, base_sha, stream, label, sprint, branch, state, at) for the
// lander and for every later pass: the head lives here, not on GitHub. The
// same call writes pr and head on the card record (the card model of
// rowan-new specs/ws-index.md), so the card and its PR point at each other.
func RecordKey(repo string, n int) string { return prkey.Key(repo, n) }

// BenchInfo names the bench the worker pushes from; host and user come from
// the bench's own beat (bench:<b>:beat), never assumed.
type BenchInfo struct{ Name, Host, User string }

// PR is a pull request as GitHub REST reports it. Ref is head.ref; State is
// open or closed, and Merged says a closed PR was merged.
type PR struct {
	Number int
	Head   string
	Ref    string
	URL    string
	State  string
	Merged bool
}

// Lister is the lookup before a create (step c): every PR in any state whose
// head is the branch. A Forge without it is looked up by FindOpenPR.
type Lister interface {
	ListPRs(ctx context.Context, repo, branch string) ([]PR, error)
}

// Forge is GitHub by REST.
type Forge interface {
	FindOpenPR(ctx context.Context, repo, branch string) (PR, bool, error)
	OpenPR(ctx context.Context, repo, branch, base, title, body string) (PR, error)
	ReadPR(ctx context.Context, repo string, number int) (PR, error)
}

// Pusher pushes card.PushedSHA to card.Branch from the bench. It must be
// idempotent: a branch already at that sha is success; a branch at another
// sha is an error, never overwritten.
type Pusher interface {
	Push(ctx context.Context, bench BenchInfo, card Card) error
}

// Options configures one harvest pass.
type Options struct {
	Sprint   string
	Benches  []string
	Labels   map[string]bool // optional: only these labels
	Clock    time.Duration   // each bench's own clock
	Instance string          // this process; the lease holder name
	Actor    string
	LeaseTTL time.Duration
	Renew    time.Duration
	Limit    int
	Forge    Forge
	Pusher   Pusher
	// Fault is the crash seam: called at each FaultAfter* point; a non-nil
	// return stops the pass there as if the process had died.
	Fault func(step string) error
	// Readback is the waits before each lookup after an ambiguous create;
	// nil means DefaultReadback. Sleep waits (nil: a timer on ctx).
	Readback []time.Duration
	Sleep    func(ctx context.Context, d time.Duration) error
	// FailCap is the failed passes a card gets before it moves to done/fail;
	// 0 means DefaultFailCap.
	FailCap int
}

// CardResult is one harvested card. Via is record (the idem key and its PR
// record, no GitHub), idem (the idem key, PR read by REST), rest, readback or
// opened: how the PR was found.
type CardResult struct {
	Label, Branch string
	PR            int
	Head          string
	URL           string
	Via           string
}

// CardFailure is one card this pass could not harvest; it stays ended and the
// next pass retries it under the same idempotency key.
type CardFailure struct {
	Label string
	Code  string // an err= code above
	Err   error
	// Fails is the card's failed passes so far (harvest_fails), and Moved
	// says this one reached FailCap and moved the card to done/fail.
	Fails int
	Moved bool
}

// BenchResult is one bench's pass.
type BenchResult struct {
	Bench  string
	Cards  []CardResult
	Failed []CardFailure
	Took   time.Duration
	Done   time.Time
	Err    error // the bench's own clock, the lease, or Redis
}

// Run harvests every bench in parallel, each under its own clock, and returns
// one result per bench in the order given.
func Run(ctx context.Context, st *store.Store, opt Options) []BenchResult {
	if opt.Clock <= 0 {
		opt.Clock = DefaultClock
	}
	if opt.LeaseTTL <= 0 {
		opt.LeaseTTL = DefaultLeaseTTL
	}
	if opt.Renew <= 0 {
		opt.Renew = DefaultRenew
	}
	if opt.Limit <= 0 {
		opt.Limit = DefaultLimit
	}
	if opt.Actor == "" {
		opt.Actor = "card-harvest"
	}
	if opt.FailCap <= 0 {
		opt.FailCap = DefaultFailCap
	}
	out := make([]BenchResult, len(opt.Benches))
	var wg sync.WaitGroup
	for i, b := range opt.Benches {
		wg.Add(1)
		go func(i int, b string) {
			defer wg.Done()
			out[i] = runBench(ctx, st, opt, b)
		}(i, b)
	}
	wg.Wait()
	return out
}

func newToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

type lease struct {
	bench, instance, token string
}

func runBench(parent context.Context, st *store.Store, opt Options, bench string) (res BenchResult) {
	start := time.Now()
	res.Bench = bench
	defer func() {
		res.Took = time.Since(start)
		res.Done = time.Now()
	}()
	if st == nil || opt.Forge == nil || opt.Pusher == nil || opt.Sprint == "" || opt.Instance == "" {
		res.Err = errors.New("harvest: store, forge, pusher, sprint and instance are required")
		return res
	}
	ctx, cancel := context.WithTimeout(parent, opt.Clock)
	defer cancel()

	l := lease{bench: bench, instance: opt.Instance, token: newToken()}
	status, err := st.Client().FCall(ctx, FunctionLease, nil, bench, l.instance, l.token, opt.LeaseTTL.Milliseconds()).Text()
	if err != nil {
		res.Err = fmt.Errorf("lease %s: %w", bench, err)
		return res
	}
	if status != "TAKEN" && status != "RENEWED" {
		res.Err = fmt.Errorf("%w: %s %s", ErrLeaseHeld, bench, status)
		return res
	}

	// Renew the lease every Renew until the pass ends; a lost lease cancels
	// the pass so this worker writes nothing more.
	lost := make(chan struct{})
	var lostOnce sync.Once
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		tick := time.NewTicker(opt.Renew)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				s, err := st.Client().FCall(ctx, FunctionLease, nil, bench, l.instance, l.token, opt.LeaseTTL.Milliseconds()).Text()
				if err == nil && s != "RENEWED" {
					lostOnce.Do(func() { close(lost) })
					cancel()
					return
				}
			}
		}
	}()

	res.Cards, res.Failed, res.Err = harvestBench(ctx, st, opt, l)
	var died *faultStop
	if errors.As(res.Err, &died) {
		// The fault seam: the process "died" at died.step. It writes no pass
		// line and releases nothing; its lease expires on its own TTL.
		cancel()
		<-renewDone
		return res
	}
	select {
	case <-lost:
		res.Err = fmt.Errorf("%w: %s", ErrFenced, bench)
	default:
	}
	if res.Err == nil && ctx.Err() != nil {
		res.Err = fmt.Errorf("bench %s clock %v: %w", bench, opt.Clock, ctx.Err())
	}
	cancel()
	<-renewDone

	// The pass line and the release, on a short context of their own so a
	// bench that spent its whole clock still says why.
	pctx, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pcancel()
	errText := ""
	if res.Err != nil {
		errText = oneLine(res.Err.Error())
	} else if len(res.Failed) > 0 {
		errText = fmt.Sprintf("%d card(s) failed: %s", len(res.Failed), oneLine(res.Failed[0].Err.Error()))
	}
	_ = st.Client().FCall(pctx, FunctionPass, nil, bench, l.instance, l.token,
		time.Since(start).Milliseconds(), len(res.Cards), errText).Err()
	return res
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// harvestBench is the pass: due cards in label order, each pushed, its PR
// found or opened, its head read back, then the harvested transition. A card
// that fails stays ended; the bench goes on to the next card until its clock.
func harvestBench(ctx context.Context, st *store.Store, opt Options, l lease) ([]CardResult, []CardFailure, error) {
	if labels, err := st.Client().SInter(ctx, "s:"+opt.Sprint+":bench:"+l.bench+":ended", "s:"+opt.Sprint+":idx:card:ended").Result(); err == nil {
		sort.Strings(labels)
		for _, label := range labels {
			if len(opt.Labels) > 0 && !opt.Labels[label] {
				continue
			}
			attempt, err := st.Client().HGet(ctx, "s:"+opt.Sprint+":card:"+label, "attempt").Result()
			if err != nil || attempt == "" {
				continue
			}
			resKey := "s:" + opt.Sprint + ":card:" + label + ":result:a" + attempt
			resFields, err := st.Client().HMGet(ctx, resKey, "valid", "field", "defect").Result()
			if err != nil || len(resFields) < 3 {
				continue
			}
			valid, _ := resFields[0].(string)
			field, _ := resFields[1].(string)
			defect, _ := resFields[2].(string)
			if valid == "0" {
				fmt.Printf("HARVEST-REFUSED %s %s field=%s defect=%s\n", opt.Sprint, label, field, defect)
				reply, err := st.Client().FCall(ctx, FunctionRefuse, nil, opt.Sprint, label, l.bench, l.instance, l.token, field, defect).Text()
				if err != nil {
					continue
				}
				if reply == "FENCED" {
					return nil, nil, ErrFenced
				}
			}
		}
	}

	info, cards, err := due(ctx, st, opt.Sprint, l.bench, opt.Limit)
	if err != nil {
		return nil, nil, err
	}
	var done []CardResult
	var failed []CardFailure
	for _, c := range cards {
		if len(opt.Labels) > 0 && !opt.Labels[c.Label] {
			continue
		}
		if ctx.Err() != nil {
			return done, failed, nil
		}
		r, err := harvestCard(ctx, st, opt, l, info, c)
		var died *faultStop
		if errors.Is(err, ErrFenced) || errors.As(err, &died) {
			return done, failed, err
		}
		if err != nil {
			if ctx.Err() != nil {
				return done, failed, nil
			}
			code := CodeFailed
			var ce *cardError
			if errors.As(err, &ce) {
				code = ce.code
			}
			f := CardFailure{Label: c.Label, Code: code, Err: err}
			if ferr := countFail(ctx, st, opt, l, &f); ferr != nil {
				return done, append(failed, f), ferr
			}
			failed = append(failed, f)
			continue
		}
		done = append(done, r)
	}
	return done, failed, nil
}

// cardError is a card failure with its err= code.
type cardError struct {
	code string
	err  error
}

func (e *cardError) Error() string { return e.code + ": " + e.err.Error() }
func (e *cardError) Unwrap() error { return e.err }

func fail(code, label, format string, args ...any) error {
	return &cardError{code: code, err: fmt.Errorf(label+": "+format, args...)}
}

// faultStop is the pass stopped by the fault seam at step.
type faultStop struct {
	step string
	err  error
}

func (f *faultStop) Error() string { return "fault at " + f.step + ": " + f.err.Error() }
func (f *faultStop) Unwrap() error { return f.err }

func fault(opt Options, step string) error {
	if opt.Fault == nil {
		return nil
	}
	if err := opt.Fault(step); err != nil {
		return &faultStop{step: step, err: err}
	}
	return nil
}

func sleep(ctx context.Context, opt Options, d time.Duration) error {
	if opt.Sleep != nil {
		return opt.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// verified is the rule of steps b and c: the PR's head.sha is the card's
// pushed_sha and its head.ref is the card's branch. (A forge that reports no
// ref is checked on the sha alone; GitHub always reports it.)
func verified(pr PR, c Card) bool {
	return pr.Number > 0 && pr.Head == c.PushedSHA && (pr.Ref == "" || pr.Ref == c.Branch)
}

func lookup(ctx context.Context, f Forge, repo, branch string) ([]PR, error) {
	if l, ok := f.(Lister); ok {
		return l.ListPRs(ctx, repo, branch)
	}
	pr, found, err := f.FindOpenPR(ctx, repo, branch)
	if err != nil || !found {
		return nil, err
	}
	return []PR{pr}, nil
}

func harvestCard(ctx context.Context, st *store.Store, opt Options, l lease, info BenchInfo, c Card) (CardResult, error) {
	want := "nova/" + opt.Sprint + "/" + c.Label + "-a" + c.Attempt
	if c.Branch != want || c.Attempt == "" {
		return CardResult{}, fmt.Errorf("%s: branch %q is not the identity's %q", c.Label, c.Branch, want)
	}
	if c.PushedSHA == "" || c.PushedSHA == "-" {
		return CardResult{}, fail(CodeNoCommit, c.Label, "pushed_sha %q: a card that committed nothing is not harvested", c.PushedSHA)
	}

	// The card record: the body is written from it alone (#3712).
	rec, err := ReadRecord(ctx, st, opt.Sprint, c.Label, c.Attempt)
	if err != nil {
		return CardResult{}, err
	}

	// a. Push (idempotent to the same sha), then the durable pushed step. A
	// RangePusher also reads the paths base_sha..pushed_sha changed.
	var rangePaths []string
	if rp, ok := opt.Pusher.(RangePusher); ok {
		rangePaths, err = rp.PushRange(ctx, info, c, rec.Card["base_sha"])
	} else {
		err = opt.Pusher.Push(ctx, info, c)
	}
	if err != nil {
		if errors.Is(err, ErrBranchMoved) {
			return CardResult{}, fail(CodeBranchMoved, c.Label, "push %s: %w", c.Branch, err)
		}
		if errors.Is(err, ErrResultsRelative) {
			// The pusher refused before it ran any ssh (SSHPusher.repoDir).
			return CardResult{}, fail(CodeResultsRelative, c.Label, "push %s: %w", c.Branch, err)
		}
		return CardResult{}, fmt.Errorf("%s: push %s: %w", c.Label, c.Branch, err)
	}
	if c.Step == "" {
		if err := writeStep(ctx, st, opt.Sprint, l, c, StepPushed); err != nil {
			return CardResult{}, err
		}
	}
	if err := fault(opt, FaultAfterPush); err != nil {
		return CardResult{}, err
	}
	res := CardResult{Label: c.Label, Branch: c.Branch}

	// b. The idem key names the PR: its record answers with the head (no
	// GitHub); a key from before the record existed is read by REST once.
	if c.IdemPR != "" {
		n, err := strconv.Atoi(c.IdemPR)
		if err != nil {
			return res, fail(CodeIdemMismatch, c.Label, "idem PR %q: %w", c.IdemPR, err)
		}
		if c.RecHead != "" {
			if c.RecHead != c.PushedSHA {
				return res, fail(CodeIdemMismatch, c.Label, "idem pr:%s:%s names PR %d whose record head is %s, not pushed_sha %s",
					c.Repo, c.Branch, n, short(c.RecHead), short(c.PushedSHA))
			}
			res.Via = "record"
			return receipt(ctx, st, opt, l, c, res, PR{Number: n, Head: c.RecHead, Ref: c.Branch}, "")
		}
		pr, err := opt.Forge.ReadPR(ctx, c.Repo, n)
		if err != nil {
			return res, fmt.Errorf("%s: read PR %d head: %w", c.Label, n, err)
		}
		if !verified(pr, c) {
			return res, fail(CodeIdemMismatch, c.Label, "idem pr:%s:%s names PR %d at %s %s, not %s %s",
				c.Repo, c.Branch, n, pr.Ref, short(pr.Head), c.Branch, short(c.PushedSHA))
		}
		res.Via = "idem"
		return receipt(ctx, st, opt, l, c, res, pr, "")
	}

	// c. Look up before creating.
	prs, err := lookup(ctx, opt.Forge, c.Repo, c.Branch)
	if err != nil {
		return res, fmt.Errorf("%s: find PR: %w", c.Label, err)
	}
	if len(prs) > 0 {
		res.Via = "rest"
		return found(ctx, st, opt, l, c, res, prs, "")
	}

	// d. None: intent, then one create.
	if err := writeStep(ctx, st, opt.Sprint, l, c, StepIntent); err != nil {
		return res, err
	}
	if err := fault(opt, FaultAfterIntent); err != nil {
		return res, err
	}
	body := Body(opt.Sprint, l.bench, c, rec, rangePaths)
	pr, cerr := opt.Forge.OpenPR(ctx, c.Repo, c.Branch, c.Base, Title(opt.Sprint, c, rec), body)
	if err := fault(opt, FaultAfterCreate); err != nil {
		return res, err
	}
	if cerr == nil {
		res.Via = "opened"
		return publish(ctx, st, opt, l, c, res, pr, body)
	}
	if !errors.Is(cerr, ErrAmbiguous) {
		return res, fail(CodeCreateFailed, c.Label, "open PR: %w", cerr)
	}
	waits := opt.Readback
	if waits == nil {
		waits = DefaultReadback
	}
	for _, d := range waits {
		if err := sleep(ctx, opt, d); err != nil {
			return res, fmt.Errorf("%s: readback: %w", c.Label, err)
		}
		prs, err := lookup(ctx, opt.Forge, c.Repo, c.Branch)
		if err != nil || len(prs) == 0 {
			continue
		}
		res.Via = "readback"
		return found(ctx, st, opt, l, c, res, prs, body)
	}
	return res, fail(CodeCreateAmbiguous, c.Label, "create reply %v and %d readbacks found no PR; the card stays at intent, the next pass looks up first", cerr, len(waits))
}

// found is step c on a lookup that returned PRs. body is the body this pass
// POSTed ("" when it opened nothing).
func found(ctx context.Context, st *store.Store, opt Options, l lease, c Card, res CardResult, prs []PR, body string) (CardResult, error) {
	if len(prs) > 1 {
		nums := make([]string, len(prs))
		for i, p := range prs {
			nums[i] = strconv.Itoa(p.Number)
		}
		return res, fail(CodePRDuplicate, c.Label, "%d PRs on %s (%s); nothing written", len(prs), c.Branch, strings.Join(nums, ","))
	}
	pr := prs[0]
	if pr.State == "closed" && !pr.Merged {
		return res, fail(CodePRClosed, c.Label, "PR %d on %s is closed and not merged; no reopen, no second PR", pr.Number, c.Branch)
	}
	return publish(ctx, st, opt, l, c, res, pr, body)
}

// publish verifies a PR GitHub returned, reserves it (published) with its
// record, reads the record back from Redis and writes the receipt. body is
// the body this pass POSTed ("" when it opened nothing).
func publish(ctx context.Context, st *store.Store, opt Options, l lease, c Card, res CardResult, pr PR, body string) (CardResult, error) {
	if !verified(pr, c) {
		return res, fail(CodePRMismatch, c.Label, "PR %d head %s %s is not %s %s", pr.Number, pr.Ref, short(pr.Head), c.Branch, short(c.PushedSHA))
	}
	recorded, err := recordPR(ctx, st, opt.Sprint, l, c, pr.Number, pr.Head)
	if err != nil {
		return res, err
	}
	if recorded != pr.Number {
		return res, fail(CodeIdemMismatch, c.Label, "idem pr:%s:%s names PR %d, GitHub gave %d", c.Repo, c.Branch, recorded, pr.Number)
	}
	if err := fault(opt, FaultAfterRecord); err != nil {
		return res, err
	}
	back, err := st.Client().HMGet(ctx, RecordKey(c.Repo, pr.Number), "head", "branch").Result()
	if err != nil || len(back) < 2 {
		return res, fmt.Errorf("%s: read record %s: %w", c.Label, RecordKey(c.Repo, pr.Number), err)
	}
	head, _ := back[0].(string)
	branch, _ := back[1].(string)
	if head != c.PushedSHA || branch != c.Branch {
		return res, fail(CodePRMismatch, c.Label, "record %s head %s %s is not %s %s", RecordKey(c.Repo, pr.Number), branch, short(head), c.Branch, short(c.PushedSHA))
	}
	return receipt(ctx, st, opt, l, c, res, PR{Number: pr.Number, Head: head, Ref: branch, URL: pr.URL}, body)
}

// receipt is step e: ns_card_harvested with the head as the PR record holds
// it (or, for an idem key from before the record existed, as REST read it),
// and the body this pass opened the PR with (stored as pr_body, #3712; ""
// stores none: a PR found open was opened by an earlier pass).
func receipt(ctx context.Context, st *store.Store, opt Options, l lease, c Card, res CardResult, pr PR, body string) (CardResult, error) {
	reply, err := st.Client().FCall(ctx, FunctionHarvested, nil, opt.Sprint, c.Label, l.bench,
		l.instance, l.token, strconv.Itoa(pr.Number), pr.Head, opt.Actor, body).Text()
	if err != nil {
		return res, fmt.Errorf("%s: harvested: %w", c.Label, err)
	}
	status, _, _ := strings.Cut(reply, "|")
	switch status {
	case "OK":
	case "FENCED":
		return res, ErrFenced
	default:
		return res, fmt.Errorf("%s: harvested refused %s", c.Label, status)
	}
	res.PR, res.Head, res.URL = pr.Number, pr.Head, pr.URL
	return res, nil
}

// writeStep is ns_harvest_step: forward only, lease-fenced.
func writeStep(ctx context.Context, st *store.Store, sprint string, l lease, c Card, step string) error {
	reply, err := st.Client().FCall(ctx, FunctionStep, nil, sprint, l.bench, l.instance, l.token, c.Label, step).Text()
	if err != nil {
		return fmt.Errorf("%s: step %s: %w", c.Label, step, err)
	}
	status, _, _ := strings.Cut(reply, "|")
	switch status {
	case "OK":
		return nil
	case "FENCED":
		return ErrFenced
	default:
		return fmt.Errorf("%s: step %s refused %s", c.Label, step, reply)
	}
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// countFail is ns_harvest_fail for one failed card: it counts the pass and,
// at FailCap, moves the card to done/fail (reason harvest, the err line as
// why). Only a fenced lease is returned as an error; any other refusal
// leaves f as it is (the pass line already names the failure).
func countFail(ctx context.Context, st *store.Store, opt Options, l lease, f *CardFailure) error {
	reply, err := st.Client().FCall(ctx, FunctionFail, nil, opt.Sprint, f.Label, l.bench, l.instance, l.token,
		f.Code, oneLine(f.Err.Error()), opt.FailCap).Text()
	if err != nil {
		return nil
	}
	status, value, _ := strings.Cut(reply, "|")
	switch status {
	case "COUNT":
		f.Fails, _ = strconv.Atoi(value)
	case "FAILED":
		f.Fails, _ = strconv.Atoi(value)
		f.Moved = true
	case "FENCED":
		return ErrFenced
	}
	return nil
}

// dueCols is ns_harvest_due's row: label repo base attempt pushed_sha
// identity results branch pr_idem harvest_step base_sha stream done_when
// rec_head.
const dueCols = 14

func due(ctx context.Context, st *store.Store, sprint, bench string, limit int) (BenchInfo, []Card, error) {
	raw, err := st.Client().FCallRO(ctx, FunctionDue, nil, sprint, bench, limit).StringSlice()
	if err != nil {
		return BenchInfo{}, nil, fmt.Errorf("due %s: %w", bench, err)
	}
	if len(raw) < 3 || raw[0] != "OK" || (len(raw)-3)%dueCols != 0 {
		return BenchInfo{}, nil, fmt.Errorf("due %s: unexpected reply %q", bench, raw)
	}
	info := BenchInfo{Name: bench, Host: raw[1], User: raw[2]}
	var cards []Card
	for i := 3; i < len(raw); i += dueCols {
		r := raw[i : i+dueCols]
		cards = append(cards, Card{Label: r[0], Repo: r[1], Base: r[2], Attempt: r[3], PushedSHA: r[4],
			Identity: r[5], Results: r[6], Branch: r[7], IdemPR: r[8], Step: r[9],
			BaseSHA: r[10], Stream: r[11], DoneWhen: r[12], RecHead: r[13]})
	}
	return info, cards, nil
}

// recordPR is ns_harvest_pr: the reservation and the PR record, from the head
// the REST response verified.
func recordPR(ctx context.Context, st *store.Store, sprint string, l lease, c Card, n int, head string) (int, error) {
	reply, err := st.Client().FCall(ctx, FunctionPR, nil, sprint, l.bench, l.instance, l.token,
		c.Label, c.Repo, c.Branch, strconv.Itoa(n), head).Text()
	if err != nil {
		return 0, fmt.Errorf("%s: record PR: %w", c.Label, err)
	}
	status, value, _ := strings.Cut(reply, "|")
	switch status {
	case "PR":
		v, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("%s: record PR reply %q", c.Label, reply)
		}
		return v, nil
	case "FENCED":
		return 0, ErrFenced
	default:
		return 0, fmt.Errorf("%s: record PR refused %s", c.Label, reply)
	}
}

// Plan is what one `card harvest` pass covers: the sprints, the benches that
// beat (each gets a worker), and the benches with no bench:<b>:beat, which
// are skipped (HARVEST SKIP bench=<b> reason=no-beat) while the others finish.
type Plan struct {
	Sprints []string
	Benches []string
	NoBeat  []string
}

// NewPlan resolves --sprint and --bench. "all" (or empty) reads SMEMBERS
// sprints and SMEMBERS benches; that read and every named bench's beat host
// are one pipeline each way, never a read per bench.
func NewPlan(ctx context.Context, st *store.Store, sprint string, benches []string) (Plan, error) {
	allSprints := sprint == "" || sprint == "all"
	allBenches := len(benches) == 0 || (len(benches) == 1 && benches[0] == "all")
	var p Plan
	c := st.Client()
	if allSprints || allBenches {
		pipe := c.Pipeline()
		sp := pipe.SMembers(ctx, "sprints")
		bp := pipe.SMembers(ctx, "benches")
		if _, err := pipe.Exec(ctx); err != nil {
			return p, fmt.Errorf("plan: %w", err)
		}
		if allSprints {
			p.Sprints = sp.Val()
		}
		if allBenches {
			benches = bp.Val()
		}
	}
	if !allSprints {
		p.Sprints = []string{sprint}
	}
	sort.Strings(p.Sprints)
	benches = append([]string(nil), benches...)
	sort.Strings(benches)
	if len(benches) == 0 {
		return p, nil
	}
	pipe := c.Pipeline()
	hosts := make([]*redis.StringCmd, len(benches))
	for i, b := range benches {
		hosts[i] = pipe.HGet(ctx, "bench:"+b+":beat", "host")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return p, fmt.Errorf("plan beats: %w", err)
	}
	for i, b := range benches {
		if hosts[i].Val() == "" {
			p.NoBeat = append(p.NoBeat, b)
			continue
		}
		p.Benches = append(p.Benches, b)
	}
	return p, nil
}

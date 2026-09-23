// Package harvest is nova-sprint `card harvest --bench` (#2932, spec #2756
// 3.2, 4.3, 5.4): one worker per bench, every bench in parallel, so a down
// bench never blocks another. Each worker holds lease:harvest:<b>, pushes each
// ended(DONE) card's branch nova/<S>/<label>-a<attempt> from its bench
// (idempotent to the same sha), finds or opens the PR under the idempotency
// key pr:<repo>:<branch>, reads the PR head back by REST and only then moves
// the card to harvested in one Redis Function call.
//
// The PR step reuses the #2611 rule (harvest_commit.go): the harvest never
// commits to a trunk and never pushes outside its own branch prefix; here the
// branch is fixed by the card's identity, so the worker never infers one.
package harvest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// Redis Functions registered by internal/nsprint/fn/lua/harvest.lua.
const (
	FunctionLease     = "ns_harvest_lease"
	FunctionPass      = "ns_harvest_pass"
	FunctionDue       = "ns_harvest_due"
	FunctionPR        = "ns_harvest_pr"
	FunctionHarvested = "ns_card_harvested"
)

// Defaults from the spec: the lease renews every 2 s with a 6 s TTL (2.2);
// the verified-PR clock is 5 min from card end (section 1).
const (
	DefaultClock    = 5 * time.Minute
	DefaultLeaseTTL = 6 * time.Second
	DefaultRenew    = 2 * time.Second
	DefaultLimit    = 256
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
}

// BenchInfo names the bench the worker pushes from; host and user come from
// the bench's own beat (bench:<b>:beat), never assumed.
type BenchInfo struct{ Name, Host, User string }

// PR is a pull request as GitHub REST reports it.
type PR struct {
	Number int
	Head   string
	URL    string
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
}

// CardResult is one harvested card. Via is idem, rest or opened: how the PR
// was found.
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
	Err   error
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
		if errors.Is(err, ErrFenced) {
			return done, failed, err
		}
		if err != nil {
			if ctx.Err() != nil {
				return done, failed, nil
			}
			failed = append(failed, CardFailure{Label: c.Label, Err: err})
			continue
		}
		done = append(done, r)
	}
	return done, failed, nil
}

func harvestCard(ctx context.Context, st *store.Store, opt Options, l lease, info BenchInfo, c Card) (CardResult, error) {
	want := "nova/" + opt.Sprint + "/" + c.Label + "-a" + c.Attempt
	if c.Branch != want || c.Attempt == "" {
		return CardResult{}, fmt.Errorf("%s: branch %q is not the identity's %q", c.Label, c.Branch, want)
	}
	if c.PushedSHA == "" {
		return CardResult{}, fmt.Errorf("%s: no pushed_sha", c.Label)
	}
	if err := opt.Pusher.Push(ctx, info, c); err != nil {
		return CardResult{}, fmt.Errorf("%s: push %s: %w", c.Label, c.Branch, err)
	}

	res := CardResult{Label: c.Label, Branch: c.Branch}
	var n int
	switch {
	case c.IdemPR != "":
		v, err := strconv.Atoi(c.IdemPR)
		if err != nil {
			return res, fmt.Errorf("%s: idem PR %q: %w", c.Label, c.IdemPR, err)
		}
		n, res.Via = v, "idem"
	default:
		pr, found, err := opt.Forge.FindOpenPR(ctx, c.Repo, c.Branch)
		if err != nil {
			return res, fmt.Errorf("%s: find PR: %w", c.Label, err)
		}
		if found {
			res.Via = "rest"
		} else {
			pr, err = opt.Forge.OpenPR(ctx, c.Repo, c.Branch, c.Base, prTitle(opt.Sprint, c), prBody(opt.Sprint, c))
			if err != nil {
				return res, fmt.Errorf("%s: open PR: %w", c.Label, err)
			}
			res.Via = "opened"
		}
		recorded, err := recordPR(ctx, st, opt.Sprint, l, c, pr.Number)
		if err != nil {
			return res, err
		}
		if recorded != pr.Number {
			return res, fmt.Errorf("%s: CONFLICT idem pr:%s:%s names PR %d, GitHub gave %d", c.Label, c.Repo, c.Branch, recorded, pr.Number)
		}
		n = pr.Number
	}

	pr, err := opt.Forge.ReadPR(ctx, c.Repo, n)
	if err != nil {
		return res, fmt.Errorf("%s: read PR %d head: %w", c.Label, n, err)
	}
	if pr.Head != c.PushedSHA {
		return res, fmt.Errorf("%s: PR %d head %s is not pushed_sha %s", c.Label, n, short(pr.Head), short(c.PushedSHA))
	}
	reply, err := st.Client().FCall(ctx, FunctionHarvested, nil, opt.Sprint, c.Label, l.bench,
		l.instance, l.token, strconv.Itoa(n), pr.Head, opt.Actor).Text()
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
	res.PR, res.Head, res.URL = n, pr.Head, pr.URL
	return res, nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func prTitle(sprint string, c Card) string {
	return fmt.Sprintf("%s: nova-sprint %s card %s attempt %s", c.Label, sprint, c.Label, c.Attempt)
}

func prBody(sprint string, c Card) string {
	return fmt.Sprintf("nova-sprint harvest (#2932)\n\nsprint: %s\ncard: %s\nidentity: %s\npushed_sha: %s\nresults: %s\n",
		sprint, c.Label, c.Identity, c.PushedSHA, c.Results)
}

func due(ctx context.Context, st *store.Store, sprint, bench string, limit int) (BenchInfo, []Card, error) {
	raw, err := st.Client().FCallRO(ctx, FunctionDue, nil, sprint, bench, limit).StringSlice()
	if err != nil {
		return BenchInfo{}, nil, fmt.Errorf("due %s: %w", bench, err)
	}
	if len(raw) < 3 || raw[0] != "OK" || (len(raw)-3)%9 != 0 {
		return BenchInfo{}, nil, fmt.Errorf("due %s: unexpected reply %q", bench, raw)
	}
	info := BenchInfo{Name: bench, Host: raw[1], User: raw[2]}
	var cards []Card
	for i := 3; i < len(raw); i += 9 {
		r := raw[i : i+9]
		cards = append(cards, Card{Label: r[0], Repo: r[1], Base: r[2], Attempt: r[3], PushedSHA: r[4],
			Identity: r[5], Results: r[6], Branch: r[7], IdemPR: r[8]})
	}
	return info, cards, nil
}

func recordPR(ctx context.Context, st *store.Store, sprint string, l lease, c Card, n int) (int, error) {
	reply, err := st.Client().FCall(ctx, FunctionPR, nil, sprint, l.bench, l.instance, l.token,
		c.Repo, c.Branch, strconv.Itoa(n)).Text()
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

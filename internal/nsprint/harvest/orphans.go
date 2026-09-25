// `card harvest --orphans` (#3042, spec #2756 3.2 row "orphan-effect", 4.3,
// controls 25 and 26): the bench harvest worker's sweep over its
// orphan-effect cards.
//
// An orphan-effect card touched the world (a branch, a PR, a live process)
// with no end record for its attempt. Every sweep re-reads the attempt's own
// results directory <root>/<S>/<label>/<base sha8>/<bench>/<attempt> through
// card.Resolve: only an end record of that fixed identity and its token_sha
// ends the card, with the record's outcome (a DONE record then goes on to
// harvest with the branch it names). A record for another attempt, another
// cut sha or another bench resolves nothing. Nothing else ends an orphan:
// no age, no branch, no PR.
//
// When a later attempt of the same label ends DONE, the orphan is superseded:
// its PR is closed with a comment naming the identity, its branch
// nova/<S>/<label>-a<n> is renamed orphan/<S>/<label>-a<n>, and only then is
// the unresolved item settled, under this bench's harvest lease. Each step
// is idempotent, so a pass cut short is finished by the next one.
package harvest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Redis Functions registered by internal/nsprint/fn/lua/harvest_orphans.lua.
const (
	FunctionOrphanDue    = "ns_orphan_due"
	FunctionOrphanSettle = "ns_orphan_settle"
)

// OrphanForge is the GitHub the supersede step needs, by REST: two writes,
// the close-out of a superseded card's PR and branch. It never reads: the PR
// is the one the card's record names (nova-tools#3967).
type OrphanForge interface {
	// ClosePR comments on the PR, then closes it.
	ClosePR(ctx context.Context, repo string, number int, comment string) error
	// RenameBranch is idempotent: from absent and to present is success.
	RenameBranch(ctx context.Context, repo, from, to string) error
}

// OrphanOptions configures one --orphans pass.
type OrphanOptions struct {
	Sprint  string
	Benches []string
	// ResultsRoot is where end records land: <root>/<identity>/end.record.
	ResultsRoot string
	// Grace is policy orphan_grace: an orphan older than this (Redis TIME
	// since orphan_at) is reported RecutDue. It is still re-read every pass;
	// the recut itself is 4.6, not this sweep. Zero reports none.
	Grace    time.Duration
	Clock    time.Duration
	Instance string
	Actor    string
	LeaseTTL time.Duration
	Renew    time.Duration
	Forge    OrphanForge
}

// OrphanEnded is an orphan its own end record ended this pass.
type OrphanEnded struct {
	Label, Outcome, Reason string
	Receipt                string // the end receipt (ns_card_end)
	Settled                string // the receipt that closed the unresolved item
}

// OrphanSuperseded is an orphan a later DONE attempt superseded this pass.
type OrphanSuperseded struct {
	Label, Identity, From, To string
	Closed                    []int
	Receipt                   string
}

// OrphanResult is one bench's --orphans pass.
type OrphanResult struct {
	Bench      string
	Ended      []OrphanEnded
	Superseded []OrphanSuperseded
	Waiting    []string // still orphan-effect: no record of their identity
	RecutDue   []string // waiting past orphan_grace
	Failed     []CardFailure
	Took       time.Duration
	Err        error
}

type orphanRow struct {
	kind, label, attempt, identity, repo string
	age                                  time.Duration
}

// OrphanBranch is the name a superseded orphan's branch is renamed to.
func OrphanBranch(sprint, label, attempt string) string {
	return "orphan/" + sprint + "/" + label + "-a" + attempt
}

// RunOrphans sweeps every bench in parallel, each under its own clock and
// its own harvest lease, and returns one result per bench in the order given.
func RunOrphans(ctx context.Context, st *store.Store, opt OrphanOptions) []OrphanResult {
	if opt.Clock <= 0 {
		opt.Clock = DefaultClock
	}
	if opt.LeaseTTL <= 0 {
		opt.LeaseTTL = DefaultLeaseTTL
	}
	if opt.Renew <= 0 {
		opt.Renew = DefaultRenew
	}
	if opt.Actor == "" {
		opt.Actor = "card-harvest-orphans"
	}
	out := make([]OrphanResult, len(opt.Benches))
	var wg sync.WaitGroup
	for i, b := range opt.Benches {
		wg.Add(1)
		go func(i int, b string) {
			defer wg.Done()
			out[i] = orphanBench(ctx, st, opt, b)
		}(i, b)
	}
	wg.Wait()
	return out
}

func orphanBench(parent context.Context, st *store.Store, opt OrphanOptions, bench string) (res OrphanResult) {
	start := time.Now()
	res.Bench = bench
	defer func() { res.Took = time.Since(start) }()
	if st == nil || opt.Forge == nil || opt.Sprint == "" || opt.Instance == "" || strings.TrimSpace(opt.ResultsRoot) == "" {
		res.Err = errors.New("harvest --orphans: store, forge, sprint, instance and results root are required")
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
	lost := make(chan struct{})
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
					close(lost)
					cancel()
					return
				}
			}
		}
	}()

	res.Err = sweepOrphanBench(ctx, st, opt, l, &res)
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

	pctx, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pcancel()
	errText := ""
	if res.Err != nil {
		errText = oneLine(res.Err.Error())
	} else if len(res.Failed) > 0 {
		errText = fmt.Sprintf("%d orphan(s) failed: %s", len(res.Failed), oneLine(res.Failed[0].Err.Error()))
	}
	_ = st.Client().FCall(pctx, FunctionPass, nil, bench, l.instance, l.token,
		time.Since(start).Milliseconds(), len(res.Ended)+len(res.Superseded), errText).Err()
	return res
}

func sweepOrphanBench(ctx context.Context, st *store.Store, opt OrphanOptions, l lease, res *OrphanResult) error {
	rows, err := orphansDue(ctx, st, opt.Sprint, l.bench)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return nil
		}
		var err error
		switch r.kind {
		case "ORPHAN":
			err = rereadOrphan(ctx, st, opt, l, r, res)
		case "ENDED":
			var e OrphanEnded
			e, err = settleEnded(ctx, st, opt, l, r)
			if err == nil {
				res.Ended = append(res.Ended, e)
			}
		case "SUPERSEDE":
			var s OrphanSuperseded
			s, err = supersede(ctx, st, opt, l, r)
			if err == nil {
				res.Superseded = append(res.Superseded, s)
			}
		}
		if errors.Is(err, ErrFenced) {
			return err
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			res.Failed = append(res.Failed, CardFailure{Label: r.label, Err: err})
		}
	}
	return nil
}

// rereadOrphan is the only way an orphan ends: its own record, read from its
// own directory by card.Resolve (ns_card_end record mode).
func rereadOrphan(ctx context.Context, st *store.Store, opt OrphanOptions, l lease, r orphanRow, res *OrphanResult) error {
	id, err := card.ParseIdentity(r.identity)
	if err != nil {
		return fmt.Errorf("%s: %w", r.label, err)
	}
	dir := filepath.Join(opt.ResultsRoot, filepath.FromSlash(id.String()))
	got, err := card.Resolve(ctx, st, card.ResolveRequest{Sprint: opt.Sprint, Label: r.label, ResultsDir: dir})
	if err != nil {
		return fmt.Errorf("%s: resolve: %w", r.label, err)
	}
	if !got.Resolved {
		if got.Code != 0 {
			return fmt.Errorf("%s: resolve refused %s (exit %d)", r.label, got.Reason, got.Code)
		}
		res.Waiting = append(res.Waiting, r.label)
		if opt.Grace > 0 && r.age >= opt.Grace {
			res.RecutDue = append(res.RecutDue, r.label)
		}
		return nil
	}
	e, err := settleEnded(ctx, st, opt, l, r)
	if err != nil {
		return err
	}
	res.Ended = append(res.Ended, e)
	return nil
}

func settleEnded(ctx context.Context, st *store.Store, opt OrphanOptions, l lease, r orphanRow) (OrphanEnded, error) {
	vals, err := st.Client().HMGet(ctx, card.CardKey(opt.Sprint, r.label), "outcome", "reason", "end_receipt").Result()
	if err != nil {
		return OrphanEnded{}, fmt.Errorf("%s: read end: %w", r.label, err)
	}
	str := func(v any) string { s, _ := v.(string); return s }
	e := OrphanEnded{Label: r.label, Outcome: str(vals[0]), Reason: str(vals[1]), Receipt: str(vals[2])}
	e.Settled, err = settle(ctx, st, opt, l, r, "ended", "")
	return e, err
}

func supersede(ctx context.Context, st *store.Store, opt OrphanOptions, l lease, r orphanRow) (OrphanSuperseded, error) {
	from := "nova/" + opt.Sprint + "/" + r.label + "-a" + r.attempt
	s := OrphanSuperseded{Label: r.label, Identity: r.identity, From: from, To: OrphanBranch(opt.Sprint, r.label, r.attempt)}
	if r.repo == "" {
		return s, fmt.Errorf("%s: no repo on the card", r.label)
	}
	// The PR first: the one the attempt's idem key names (pr:<repo>:<branch>
	// in s:<S>:idem, written when harvest recorded it), never a GitHub lookup.
	prField, err := st.Client().HGet(ctx, "s:"+opt.Sprint+":idem", "pr:"+r.repo+":"+from).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return s, fmt.Errorf("%s: read the idem PR of %s: %w", r.label, from, err)
	}
	if n, _ := strconv.Atoi(strings.TrimSpace(prField)); n > 0 {
		if err := opt.Forge.ClosePR(ctx, r.repo, n, supersedeComment(r, from, s.To)); err != nil {
			return s, fmt.Errorf("%s: close PR %d: %w", r.label, n, err)
		}
		s.Closed = append(s.Closed, n)
	}
	if err := opt.Forge.RenameBranch(ctx, r.repo, from, s.To); err != nil {
		return s, fmt.Errorf("%s: rename %s to %s: %w", r.label, from, s.To, err)
	}
	prs := make([]string, len(s.Closed))
	for i, n := range s.Closed {
		prs[i] = strconv.Itoa(n)
	}
	evidence := "branch=" + from + "->" + s.To
	if len(prs) > 0 {
		evidence += ",closed=" + strings.Join(prs, ",")
	}
	s.Receipt, err = settle(ctx, st, opt, l, r, "superseded", evidence)
	return s, err
}

func supersedeComment(r orphanRow, from, to string) string {
	return fmt.Sprintf("nova-sprint card harvest --orphans (#3042): superseded orphan `%s`.\n\n"+
		"A later attempt of `%s` ended DONE, so this attempt is not harvested: this PR is closed and its branch `%s` is renamed `%s`.\n",
		r.identity, r.label, from, to)
}

func settle(ctx context.Context, st *store.Store, opt OrphanOptions, l lease, r orphanRow, mode, evidence string) (string, error) {
	reply, err := st.Client().FCall(ctx, FunctionOrphanSettle, nil, opt.Sprint, l.bench, l.instance, l.token,
		r.label, r.attempt, mode, evidence, opt.Actor).Text()
	if err != nil {
		return "", fmt.Errorf("%s: settle %s: %w", r.label, mode, err)
	}
	status, receipt, _ := strings.Cut(reply, "|")
	switch status {
	case "OK", "GONE":
		return receipt, nil
	case "FENCED":
		return "", ErrFenced
	default:
		return "", fmt.Errorf("%s: settle %s refused %s", r.label, mode, status)
	}
}

func orphansDue(ctx context.Context, st *store.Store, sprint, bench string) ([]orphanRow, error) {
	raw, err := st.Client().FCallRO(ctx, FunctionOrphanDue, nil, sprint, bench).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("orphans due %s: %w", bench, err)
	}
	if len(raw) < 1 || raw[0] != "OK" || (len(raw)-1)%6 != 0 {
		return nil, fmt.Errorf("orphans due %s: unexpected reply %q", bench, raw)
	}
	var rows []orphanRow
	for i := 1; i < len(raw); i += 6 {
		ms, _ := strconv.ParseInt(raw[i+5], 10, 64)
		rows = append(rows, orphanRow{kind: raw[i], label: raw[i+1], attempt: raw[i+2], identity: raw[i+3],
			repo: raw[i+4], age: time.Duration(ms) * time.Millisecond})
	}
	// ORPHAN rows first (they may end this pass), then settles, in label order.
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].kind == "ORPHAN" && rows[b].kind != "ORPHAN" })
	return rows, nil
}

// ClosePR comments on the PR (the identity goes there), then closes it.
func (g GitHub) ClosePR(ctx context.Context, repo string, number int, comment string) error {
	full, _ := g.full(repo)
	n := strconv.Itoa(number)
	if _, err := g.api(ctx, "-X", "POST", "-f", "body="+comment, "repos/"+full+"/issues/"+n+"/comments"); err != nil {
		return err
	}
	_, err := g.api(ctx, "-X", "PATCH", "-f", "state=closed", "repos/"+full+"/pulls/"+n)
	return err
}

// RenameBranch renames from to to by REST. A rename that already happened
// (from gone, to present) is success; anything else is the error.
func (g GitHub) RenameBranch(ctx context.Context, repo, from, to string) error {
	full, _ := g.full(repo)
	_, err := g.api(ctx, "-X", "POST", "-f", "new_name="+to, "repos/"+full+"/branches/"+from+"/rename")
	if err == nil {
		return nil
	}
	if _, gerr := g.api(ctx, "repos/"+full+"/branches/"+to); gerr == nil {
		if _, ferr := g.api(ctx, "repos/"+full+"/branches/"+from); ferr != nil {
			return nil
		}
	}
	return err
}

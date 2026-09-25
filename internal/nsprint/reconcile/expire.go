package reconcile

// This file: the expire duty (#2930 rev 5), which replaces rowan-tools
// bin/sprint-requeue. It runs every reconciler pass after the refill and
// gates itself to one sweep per sprint per s:<S>:policy expire_every_ms,
// against proc:reconciler expire_at:<S> (Redis TIME ms, written only by the
// fenced ns_expire_stamp). A sweep is at most three Redis round trips for
// every due sprint together:
//
//  1. one pipeline of reads: each sprint's policy, its dealt, launched,
//     running, reconcile-required and ended cards with the fields the sweep
//     needs (FCALL_RO ns_expire_read, one per index: SORT is @dangerous and the
//     coordinator ACL refuses it, #3620), its pending idem index, and every
//     bench's beat;
//  2. one pipeline of transitions: ns_card_reclaim, ns_card_retry and
//     ns_idem_ambiguous for each candidate, and one ns_expire_stamp per sprint;
//  3. evidence for the reconcile-required cards read in step 1: one Prober
//     call per bench, never a forge read (an unreachable bench gives no
//     evidence, so its cards stay reconcile-required);
//  4. one pipeline of resolutions: ns_card_required for each card with an
//     effect or with proven absence, and ns_card_required_timeout for each
//     card no evidence resolved that has sat in reconcile-required past
//     cfg:reconcile max_required_s (default 3600): it ends done/fail, reason
//     reconcile-timeout, never requeued (nova-tools #3803).
//
// Steps 3 and 4 run off the pass path (#3802: one bench's ssh held a pass
// 10.7 s): the sweep starts one bounded worker per bench (ExpireDeadline) and
// returns; a bench whose worker is still in flight gets no second one. The
// worker resolves its bench's cards and writes proc:expire:<bench> in one
// pipeline when it ends; its errors reach the next Run.
//
// Every function checks the reconciler fence before it writes; a FENCED
// answer anywhere stops the duty with ErrFenced.
//
// Once per LeaseReapEvery, whether a sprint is due or not, the pass runs one
// fenced ns_lease_reap (#3925, card_ghost.lua): a card in a bench's one
// lease ledger, bench:<b>:cards:working (#3998), whose sprint is closed is
// retired through the move and the slot is free. The count is
// Counts.Reaped. A card with no beat for LeaseStale is the sweep's beat-lost
// reclaim; a member no record accounts for is the fsck duty's repair.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
)

// Suspect is one reconcile-required card whose evidence a Prober gathers.
type Suspect struct {
	Sprint   string
	Label    string
	Bench    string
	Attempt  string
	Identity string // the attempt identity, matched against the bench's process table
	JobDir   string
	Branch   string // the attempt branch, read with git ls-remote
	Repo     string // owner/name
	Since    string // required_at: Redis TIME ms the card entered reconcile-required
}

// Key is the Prober's answer key for this card: <sprint>/<label>.
func (s Suspect) Key() string { return s.Sprint + "/" + s.Label }

// Prober gathers the evidence of one bench's reconcile-required cards in one
// batched session (job dir present, live pid by command identity, the attempt
// branch by git ls-remote). It never reads the forge's API. An error means
// the bench gave no evidence: its cards stay reconcile-required (no evidence
// is not negative evidence). A card missing from the map has none either.
type Prober interface {
	Probe(ctx context.Context, b deal.Bench, cards []Suspect) (map[string]Evidence, error)
}

// ConfigKey is cfg:reconcile, the reconciler's fleet-wide config hash. Its
// max_required_s field is the reconcile-required window: past it a card no
// evidence resolved ends done/fail (reason reconcile-timeout). An absent,
// non-numeric or non-positive value reads as DefaultMaxRequired.
const ConfigKey = "cfg:reconcile"

// DefaultMaxRequired is max_required_s's default.
const DefaultMaxRequired = time.Hour

// MaxRequired parses a max_required_s value (seconds).
func MaxRequired(v string) time.Duration {
	if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return DefaultMaxRequired
}

// ExpireStampField is the proc:reconciler field of sprint S's last sweep.
func ExpireStampField(sprint string) string { return "expire_at:" + sprint }

// Expire is the expire duty. Client is the sprint store's Redis; Prober is
// the bench evidence seam (nil gathers none, so no card leaves
// reconcile-required through this duty).
type Expire struct {
	Client *redis.Client
	Prober Prober

	mu       sync.Mutex
	sprints  []string // the sprint index as the last gate read it
	loaded   bool
	reapedAt time.Time // the last lease reap (LeaseReapEvery)

	wmu     sync.Mutex
	busy    map[string]bool // bench -> its evidence worker is in flight
	errs    []string        // worker errors no Run has returned yet
	fenced  bool            // a worker's resolution was FENCED
	expired int             // cards a worker ended by the window (#3803), for the next Run
	wctx    context.Context // every worker's context; Stop cancels it
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// ProbeProcKey is the row an evidence worker writes when it ends: at (ms),
// took_ms, cards, resolved and err (- when clean).
func ProbeProcKey(bench string) string { return "proc:expire:" + bench }

// Wait blocks until no evidence worker is in flight or ctx ends; it reports
// whether they all ended.
func (e *Expire) Wait(ctx context.Context) bool {
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// Stop is the way out: it waits for the evidence workers until ctx ends
// (each records its row), then cancels any still in flight. It holds no
// bench lease, so it releases none.
func (e *Expire) Stop(ctx context.Context) []string {
	e.Wait(ctx)
	e.wmu.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.wmu.Unlock()
	return nil
}

// drain returns the cards the workers ended by the window and their errors
// since the last Run, ErrFenced first.
func (e *Expire) drain() (int, error) {
	e.wmu.Lock()
	defer e.wmu.Unlock()
	errs, fenced, expired := e.errs, e.fenced, e.expired
	e.errs, e.fenced, e.expired = nil, false, 0
	if fenced {
		return expired, fmt.Errorf("expire required: %w", ErrFenced)
	}
	if len(errs) > 0 {
		return expired, errors.New("expire: " + strings.Join(errs, "; "))
	}
	return expired, nil
}

// gateRow is one sprint's self-gate read.
type gateRow struct {
	every, at *redis.StringCmd
}

// Run is one pass of the duty: the gate for every sprint in one round trip,
// then one sweep over the sprints that are due.
func (e *Expire) Run(ctx context.Context, l *Lease) (Counts, error) {
	if e == nil || e.Client == nil || l == nil {
		return Counts{}, errors.New("expire: no store or no lease")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c, err := e.run(ctx, l)
	expired, werr := e.drain()
	c.Expired += expired
	if werr != nil {
		switch {
		case errors.Is(werr, ErrFenced):
			return Counts{}, werr
		case err == nil:
			err = werr
		case !errors.Is(err, ErrFenced):
			err = fmt.Errorf("%w; %v", err, werr)
		}
	}
	return c, err
}

func (e *Expire) run(ctx context.Context, l *Lease) (Counts, error) {
	// The reap is due once per LeaseReapEvery; once the library is known
	// loaded it rides the gate's round trip, so a pass between reaps and
	// sweeps is still the gate read alone.
	reapDue := time.Since(e.reapedAt) >= LeaseReapEvery
	var reap *redis.Cmd
	due, benches, now, err := e.gate(ctx, func(pipe redis.Pipeliner) {
		if reapDue && e.loaded {
			reap = pipe.FCall(ctx, LeaseReapFunction, nil, l.Token(), LeaseStale.Milliseconds())
		}
	})
	if err != nil {
		return Counts{}, err
	}
	if !e.loaded {
		if err := fn.LoadMissing(ctx, e.Client); err != nil {
			return Counts{}, err
		}
		e.loaded = true
	}
	var c Counts
	switch {
	case reap != nil:
		c.Reaped, err = reapReply(reap)
	case reapDue:
		c.Reaped, err = ReapLeases(ctx, e.Client, l.Token(), LeaseStale)
	}
	if reapDue && err == nil {
		e.reapedAt = time.Now()
	}
	if err != nil {
		return c, err
	}
	if len(due) == 0 {
		return c, nil
	}
	sc, err := e.sweep(ctx, l, due, benches, now)
	sc.Reaped = c.Reaped
	return sc, err
}

// LeaseStale is how long a lease's card may go without a beat before the
// expire duty reaps the lease (#3925: 90 s; the card beats every
// card.DefaultBeatEvery, and its bench beat stamps it every second when
// the beat names it live).
const LeaseStale = 90 * time.Second

// LeaseReapEvery is the lease reap's cadence inside the expire duty: a
// ghost lease is gone within LeaseStale + LeaseReapEvery of its last beat.
const LeaseReapEvery = 10 * time.Second

// LeaseReapFunction is the fenced reaper (card_ghost.lua).
const LeaseReapFunction = "ns_lease_reap"

// ReapLeases is one ns_lease_reap over every registered bench: the number
// of lease entries dropped. A fenced token is ErrFenced.
func ReapLeases(ctx context.Context, c *redis.Client, token string, stale time.Duration) (int, error) {
	return reapReply(c.FCall(ctx, LeaseReapFunction, nil, token, stale.Milliseconds()))
}

func reapReply(cmd *redis.Cmd) (int, error) {
	reply, err := cmd.StringSlice()
	if err != nil {
		return 0, fmt.Errorf("lease reap: %w", err)
	}
	if len(reply) > 0 && reply[0] == "FENCED" {
		return 0, fmt.Errorf("lease reap: %w", ErrFenced)
	}
	if len(reply) < 2 || reply[0] != "REAPED" {
		return 0, fmt.Errorf("lease reap: reply %q", reply)
	}
	n, err := strconv.Atoi(reply[1])
	if err != nil {
		return 0, fmt.Errorf("lease reap: count %q", reply[1])
	}
	return n, nil
}

// gate answers the sprints due for a sweep, the bench index and Redis TIME
// ms. It is one pipeline (also's commands first, then the sprint and bench
// indexes, TIME, and each known sprint's expire_every_ms and expire_at); a
// sprint new to the index since the last gate costs one more round trip,
// once.
func (e *Expire) gate(ctx context.Context, also func(redis.Pipeliner)) ([]string, []string, int64, error) {
	pipe := e.Client.Pipeline()
	also(pipe)
	members := pipe.SMembers(ctx, "sprints")
	benches := pipe.SMembers(ctx, "benches")
	clock := pipe.Time(ctx)
	rows := e.gateRows(ctx, pipe, e.sprints)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, 0, fmt.Errorf("expire gate: %w", err)
	}
	t, err := clock.Result()
	if err != nil {
		return nil, nil, 0, fmt.Errorf("expire gate: TIME: %w", err)
	}
	now := t.UnixMilli()
	current := members.Val()
	slices.Sort(current)
	var fresh []string
	for _, s := range current {
		if _, ok := rows[s]; !ok {
			fresh = append(fresh, s)
		}
	}
	if len(fresh) > 0 {
		pipe := e.Client.Pipeline()
		more := e.gateRows(ctx, pipe, fresh)
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, nil, 0, fmt.Errorf("expire gate: %w", err)
		}
		for s, r := range more {
			rows[s] = r
		}
	}
	e.sprints = current
	var due []string
	for _, s := range current {
		r := rows[s]
		every := DefaultPolicy.ExpireEvery.Milliseconds()
		if v, err := strconv.ParseInt(r.every.Val(), 10, 64); err == nil && v > 0 {
			every = v
		}
		at, err := strconv.ParseInt(r.at.Val(), 10, 64)
		if err == nil && now-at < every {
			continue
		}
		due = append(due, s)
	}
	return due, benches.Val(), now, nil
}

func (e *Expire) gateRows(ctx context.Context, pipe redis.Pipeliner, sprints []string) map[string]gateRow {
	rows := make(map[string]gateRow, len(sprints))
	for _, s := range sprints {
		rows[s] = gateRow{
			every: pipe.HGet(ctx, PolicyKey(s), "expire_every_ms"),
			at:    pipe.HGet(ctx, ProcKey, ExpireStampField(s)),
		}
	}
	return rows
}

// The fields step 1 reads per card index, after the label.
var (
	dealtFields    = []string{"dealt_at"}
	liveFields     = []string{"beat_at", "launched_at"}
	requiredFields = []string{"bench", "attempt", "identity", "jobdir", "branch", "repo", "required_at"}
	endedFields    = []string{"outcome", "reason", "exit", "pushed_sha", "retries"}
)

type sprintRead struct {
	policy                               *redis.MapStringStringCmd
	dealt, launched, running, req, ended *redis.Cmd
	pending                              *redis.ZSliceCmd
}

// ExpireReadFunction is the library function that reads one card index with
// the named hash fields of each member (reconcile_required.lua).
const ExpireReadFunction = "ns_expire_read"

// indexRead reads every member of a card index with the named hash fields in
// one read-only call: FCALL_RO ns_expire_read 0 <S> <state> <field>... It
// replaces SORT <idx> BY nosort GET # GET s:<S>:card:*-><field> (#3620): the
// verb needs no @dangerous command, and the reply keeps SORT GET's shape.
func indexRead(ctx context.Context, pipe redis.Pipeliner, sprint, state string, fields []string) *redis.Cmd {
	args := make([]any, 0, len(fields)+2)
	args = append(args, sprint, state)
	for _, f := range fields {
		args = append(args, f)
	}
	return pipe.FCallRo(ctx, ExpireReadFunction, nil, args...)
}

// rowsOf splits an ns_expire_read reply into label -> field map.
func rowsOf(cmd *redis.Cmd, fields []string) []map[string]string {
	vals, _ := cmd.StringSlice()
	width := len(fields) + 1
	out := make([]map[string]string, 0, len(vals)/width)
	for i := 0; i+width <= len(vals); i += width {
		h := map[string]string{"label": vals[i]}
		for j, f := range fields {
			h[f] = vals[i+1+j]
		}
		out = append(out, h)
	}
	return out
}

type transition struct {
	kind string // reclaim, retry, ambiguous, stamp
	cmd  *redis.Cmd
}

// sweep runs steps 1-4 over the due sprints.
func (e *Expire) sweep(ctx context.Context, l *Lease, due, benches []string, now int64) (Counts, error) {
	token := l.Token()
	// Step 1: one pipeline of reads.
	pipe := e.Client.Pipeline()
	reads := make([]sprintRead, len(due))
	for i, s := range due {
		reads[i] = sprintRead{
			policy:   pipe.HGetAll(ctx, PolicyKey(s)),
			dealt:    indexRead(ctx, pipe, s, "dealt", dealtFields),
			launched: indexRead(ctx, pipe, s, "launched", liveFields),
			running:  indexRead(ctx, pipe, s, "running", liveFields),
			req:      indexRead(ctx, pipe, s, "reconcile-required", requiredFields),
			ended:    indexRead(ctx, pipe, s, "ended", endedFields),
			pending:  pipe.ZRangeWithScores(ctx, PendingIndexKey(s), 0, -1),
		}
	}
	cfg := pipe.HMGet(ctx, ConfigKey, "max_required_s")
	beats := make(map[string]*redis.SliceCmd, len(benches))
	for _, b := range benches {
		beats[b] = pipe.HMGet(ctx, "bench:"+b+":beat", "host", "user")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Counts{}, fmt.Errorf("expire read: %w", err)
	}

	// Step 2: one pipeline of transitions.
	pipe = e.Client.Pipeline()
	var ts []transition
	add := func(kind string, args ...any) {
		ts = append(ts, transition{kind: kind, cmd: pipe.FCall(ctx, fnName(kind), nil, args...)})
	}
	var suspects []Suspect
	for i, s := range due {
		r := reads[i]
		p := ParsePolicy(r.policy.Val())
		start, beat := p.Start.Milliseconds(), p.Beat.Milliseconds()
		for _, h := range rowsOf(r.dealt, dealtFields) {
			if at, err := strconv.ParseInt(h["dealt_at"], 10, 64); err != nil || now-at >= start {
				add("reclaim", s, h["label"], token, start, beat)
			}
		}
		for _, cmd := range []*redis.Cmd{r.launched, r.running} {
			for _, h := range rowsOf(cmd, liveFields) {
				last, err := strconv.ParseInt(h["beat_at"], 10, 64)
				if err != nil {
					last, err = strconv.ParseInt(h["launched_at"], 10, 64)
				}
				if err != nil || now-last >= beat {
					add("reclaim", s, h["label"], token, start, beat)
				}
			}
		}
		for _, h := range rowsOf(r.ended, endedFields) {
			if Retryable(h, p.RetryMax) {
				add("retry", s, h["label"], token, p.RetryMax)
			}
		}
		before := now - p.Open.Milliseconds()
		for _, z := range r.pending.Val() {
			if int64(z.Score) <= before {
				add("ambiguous", s, fmt.Sprint(z.Member), token, "", before)
			}
		}
		add("stamp", token, s)
		for _, h := range rowsOf(r.req, requiredFields) {
			suspects = append(suspects, Suspect{Sprint: s, Label: h["label"], Bench: h["bench"], Attempt: h["attempt"],
				Identity: h["identity"], JobDir: h["jobdir"], Branch: h["branch"], Repo: h["repo"], Since: h["required_at"]})
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !anyReply(ts) {
		return Counts{}, fmt.Errorf("expire transitions: %w", err)
	}
	var c Counts
	var errs []string
	for _, t := range ts {
		code, status, err := reply(t.cmd)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", t.kind, err))
			continue
		}
		if code == 3 {
			return Counts{}, fmt.Errorf("expire %s: %w", t.kind, ErrFenced)
		}
		switch {
		case t.kind == "reclaim" && (status == "QUEUED" || status == "REQUIRED"):
			c.Expired++
		case t.kind == "retry" && status == "QUEUED":
			c.Retried++
		case t.kind == "ambiguous" && code == 0 && status == "AMBIGUOUS":
			c.Ambiguous++
		}
	}

	// Step 4's window: max_required_s, read in step 1.
	maxReq := DefaultMaxRequired
	if v := cfg.Val(); len(v) > 0 {
		if s, ok := v[0].(string); ok {
			maxReq = MaxRequired(s)
		}
	}

	// Steps 3 and 4: one evidence worker per bench, off the pass path; each
	// resolves its bench's cards by evidence, then by the window (#3803).
	// A card no worker covers (no Prober, or a bench outside the registry)
	// can get no evidence: its window is checked here, in one pipeline.
	if rest := e.probe(ctx, l, beats, suspects, now, maxReq); len(rest) > 0 {
		pipe = e.Client.Pipeline()
		var rs []transition
		for _, s := range rest {
			if timeoutDue(s, now, maxReq) {
				rs = append(rs, transition{kind: "timeout", cmd: pipe.FCall(ctx, "ns_card_required_timeout", nil, s.Sprint, s.Label, token, maxReq.Milliseconds())})
			}
		}
		if len(rs) > 0 {
			if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !anyReply(rs) {
				return c, fmt.Errorf("expire resolutions: %w", err)
			}
			for _, t := range rs {
				code, status, err := reply(t.cmd)
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", t.kind, err))
					continue
				}
				if code == 3 {
					return c, fmt.Errorf("expire %s: %w", t.kind, ErrFenced)
				}
				if status == "TIMEOUT" {
					c.Expired++
				}
			}
		}
	}
	if len(errs) > 0 {
		return c, errors.New("expire: " + strings.Join(errs, "; "))
	}
	return c, nil
}

func fnName(kind string) string {
	switch kind {
	case "reclaim":
		return "ns_card_reclaim"
	case "retry":
		return "ns_card_retry"
	case "ambiguous":
		return "ns_idem_ambiguous"
	case "stamp":
		return "ns_expire_stamp"
	}
	return ""
}

// anyReply reports whether a pipeline's error was per command (some command
// answered), not a lost connection: per-command errors are read one by one.
func anyReply(ts []transition) bool {
	for _, t := range ts {
		if t.cmd.Err() == nil {
			return true
		}
	}
	return false
}

// reply parses a code|status|attempt|receipt answer.
func reply(cmd *redis.Cmd) (int, string, error) {
	raw, err := cmd.Text()
	if err != nil {
		return 0, "", err
	}
	parts := strings.SplitN(raw, "|", 4)
	if len(parts) != 4 {
		return 0, "", fmt.Errorf("reply %q", raw)
	}
	code, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", fmt.Errorf("reply %q", raw)
	}
	return code, parts[1], nil
}

// probe starts one evidence worker per bench that has suspects and none in
// flight, and returns at once with the suspects no worker covers. beats are
// the registered benches' beat hashes (host, user) from step 1; a card on a
// bench outside the registry has nothing to dial and gets no evidence, nor
// does any card when there is no Prober. A bench whose worker is in flight
// is covered: that worker, or the next pass's, checks its cards' window.
// now (Redis TIME ms) and maxReq are the sweep's, for the workers' window.
func (e *Expire) probe(ctx context.Context, l *Lease, beats map[string]*redis.SliceCmd, suspects []Suspect, now int64, maxReq time.Duration) []Suspect {
	if e.Prober == nil || len(suspects) == 0 {
		return suspects
	}
	var rest []Suspect
	byBench := map[string][]Suspect{}
	for _, s := range suspects {
		if _, ok := beats[s.Bench]; ok {
			byBench[s.Bench] = append(byBench[s.Bench], s)
		} else {
			rest = append(rest, s)
		}
	}
	for b, cards := range byBench {
		v := beats[b].Val()
		bench := deal.Bench{Name: b, Host: str(v, 0), User: str(v, 1), Up: len(v) > 0 && v[0] != nil}
		e.start(ctx, l, bench, cards, now, maxReq)
	}
	return rest
}

// timeoutDue reports whether a card no evidence resolved goes to
// ns_card_required_timeout at now (Redis TIME ms): past maxReq since its
// required_at, or with no required_at (the function stamps one). The
// function checks the window again against Redis TIME; this only saves the
// call for a card well inside it.
func timeoutDue(s Suspect, now int64, maxReq time.Duration) bool {
	at, err := strconv.ParseInt(s.Since, 10, 64)
	return err != nil || now-at >= maxReq.Milliseconds()
}

// start runs one bench's evidence worker unless one is in flight: the
// Prober call bounded by ExpireDeadline, then the resolutions and the row.
func (e *Expire) start(ctx context.Context, l *Lease, bench deal.Bench, cards []Suspect, now int64, maxReq time.Duration) {
	e.wmu.Lock()
	if e.busy == nil {
		e.busy = map[string]bool{}
	}
	if e.wctx == nil {
		// The workers outlive the pass that started them; Stop ends them.
		e.wctx, e.cancel = context.WithCancel(context.WithoutCancel(ctx))
	}
	if e.busy[bench.Name] || e.wctx.Err() != nil {
		e.wmu.Unlock()
		return
	}
	e.busy[bench.Name] = true
	wctx := e.wctx
	e.wg.Add(1)
	e.wmu.Unlock()
	go func() {
		defer e.wg.Done()
		defer func() {
			e.wmu.Lock()
			delete(e.busy, bench.Name)
			e.wmu.Unlock()
		}()
		begin := time.Now()
		pctx, cancel := context.WithTimeout(wctx, ExpireDeadline)
		ev, perr := e.Prober.Probe(pctx, bench, cards)
		cancel()
		// The window is judged at the sweep's Redis TIME plus the session's
		// wall time; the function checks it again against Redis TIME.
		at := now + time.Since(begin).Milliseconds()
		errs, fenced, expired := e.resolve(wctx, l, bench.Name, cards, ev, perr, begin, at, maxReq)
		e.wmu.Lock()
		defer e.wmu.Unlock()
		e.errs = append(e.errs, errs...)
		e.fenced = e.fenced || fenced
		e.expired += expired
	}()
}

// resolve is step 4 for one bench, in one pipeline: ns_card_required for
// each card with an effect or proven absence, ns_card_required_timeout for
// each card no evidence resolved whose window is due at now (#3803: an
// unreachable bench or a failed probe gives none, so its cards age out), and
// the worker's row (at, took_ms, cards, resolved = the evidence resolutions
// sent, timeouts = the window calls sent, err = the probe's error or -). A
// probe error is no evidence: it is the row's err, not a duty error. A
// resolution that errs or is FENCED rewrites err in one more write. It
// returns the errors, whether any answer was FENCED, and how many cards the
// window ended.
func (e *Expire) resolve(ctx context.Context, l *Lease, bench string, cards []Suspect, evidence map[string]Evidence, perr error, begin time.Time, now int64, maxReq time.Duration) ([]string, bool, int) {
	token := l.Token()
	// The row is written even when Stop cancelled the worker.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	pipe := e.Client.Pipeline()
	var errs []string
	var rs []transition
	resolved, timeouts := 0, 0
	for _, s := range cards {
		ev, ok := evidence[s.Key()]
		ok = ok && perr == nil
		switch {
		case ok && ev.Effect() && ev.Absent:
			errs = append(errs, fmt.Sprintf("required %s: %v", s.Key(), ErrEvidence))
			continue
		case ok && ev.Effect():
			rs = append(rs, transition{kind: "required", cmd: pipe.FCall(ctx, "ns_card_required", nil, s.Sprint, s.Label, token, "effect", ev.String())})
			resolved++
			continue
		case ok && ev.Absent:
			rs = append(rs, transition{kind: "required", cmd: pipe.FCall(ctx, "ns_card_required", nil, s.Sprint, s.Label, token, "absent", "")})
			resolved++
			continue
		}
		if timeoutDue(s, now, maxReq) {
			rs = append(rs, transition{kind: "timeout", cmd: pipe.FCall(ctx, "ns_card_required_timeout", nil, s.Sprint, s.Label, token, maxReq.Milliseconds())})
			timeouts++
		}
	}
	status := "-"
	if perr != nil {
		status = oneLine(perr.Error())
	}
	row := pipe.HSet(ctx, ProbeProcKey(bench), "at", time.Now().UnixMilli(), "took_ms", time.Since(begin).Milliseconds(),
		"cards", len(cards), "resolved", resolved, "timeouts", timeouts, "err", status)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !anyReply(rs) && row.Err() != nil {
		return append(errs, fmt.Sprintf("resolutions %s: %v", bench, err)), false, 0
	}
	fenced, expired := false, 0
	for _, t := range rs {
		code, st, err := reply(t.cmd)
		switch {
		case err != nil:
			errs = append(errs, fmt.Sprintf("%s: %v", t.kind, err))
		case code == 3:
			fenced = true
		case t.kind == "timeout" && st == "TIMEOUT":
			expired++
		}
	}
	if fenced || len(errs) > 0 {
		status = "FENCED"
		if !fenced {
			status = oneLine(strings.Join(errs, "; "))
		}
		if err := e.Client.HSet(ctx, ProbeProcKey(bench), "err", status).Err(); err != nil {
			errs = append(errs, fmt.Sprintf("row %s: %v", bench, err))
		}
	}
	return errs, fenced, expired
}

// oneLine keeps a row's err on one line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func str(v []any, i int) string {
	if i < len(v) {
		if s, ok := v[i].(string); ok {
			return s
		}
	}
	return ""
}

// ExpireDeadline bounds one bench's evidence session.
const ExpireDeadline = 30 * time.Second

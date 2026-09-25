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
//     effect or with proven absence.
//
// Every function checks the reconciler fence before it writes; a FENCED
// answer anywhere stops the duty with ErrFenced.

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

// ExpireStampField is the proc:reconciler field of sprint S's last sweep.
func ExpireStampField(sprint string) string { return "expire_at:" + sprint }

// Expire is the expire duty. Client is the sprint store's Redis; Prober is
// the bench evidence seam (nil gathers none, so no card leaves
// reconcile-required through this duty).
type Expire struct {
	Client *redis.Client
	Prober Prober

	mu      sync.Mutex
	sprints []string // the sprint index as the last gate read it
	loaded  bool
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
	due, benches, now, err := e.gate(ctx)
	if err != nil || len(due) == 0 {
		return Counts{}, err
	}
	if !e.loaded {
		if err := fn.LoadMissing(ctx, e.Client); err != nil {
			return Counts{}, err
		}
		e.loaded = true
	}
	return e.sweep(ctx, l, due, benches, now)
}

// gate answers the sprints due for a sweep, the bench index and Redis TIME
// ms. It is one pipeline (the sprint and bench indexes, TIME, and each known
// sprint's expire_every_ms and expire_at); a sprint new to the index since
// the last gate costs one more round trip, once.
func (e *Expire) gate(ctx context.Context) ([]string, []string, int64, error) {
	pipe := e.Client.Pipeline()
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
	requiredFields = []string{"bench", "attempt", "identity", "jobdir", "branch", "repo"}
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
				Identity: h["identity"], JobDir: h["jobdir"], Branch: h["branch"], Repo: h["repo"]})
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

	// Step 3: evidence, one Prober call per bench, in parallel.
	evidence := e.probe(ctx, beats, suspects)

	// Step 4: one pipeline of resolutions.
	if len(evidence) > 0 {
		pipe = e.Client.Pipeline()
		var rs []transition
		for _, s := range suspects {
			ev, ok := evidence[s.Key()]
			if !ok {
				continue
			}
			switch {
			case ev.Effect() && ev.Absent:
				errs = append(errs, fmt.Sprintf("required %s: %v", s.Key(), ErrEvidence))
			case ev.Effect():
				rs = append(rs, transition{kind: "required", cmd: pipe.FCall(ctx, "ns_card_required", nil, s.Sprint, s.Label, token, "effect", ev.String())})
			case ev.Absent:
				rs = append(rs, transition{kind: "required", cmd: pipe.FCall(ctx, "ns_card_required", nil, s.Sprint, s.Label, token, "absent", "")})
			}
		}
		if len(rs) > 0 {
			if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !anyReply(rs) {
				return c, fmt.Errorf("expire resolutions: %w", err)
			}
			for _, t := range rs {
				code, _, err := reply(t.cmd)
				if err != nil {
					errs = append(errs, fmt.Sprintf("required: %v", err))
					continue
				}
				if code == 3 {
					return c, fmt.Errorf("expire required: %w", ErrFenced)
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

// probe runs one Prober call per bench that has suspects, in parallel, each
// bounded by ExpireDeadline. beats are the registered benches' beat hashes
// (host, user) from step 1; a card on a bench outside the registry has
// nothing to dial and gets no evidence.
func (e *Expire) probe(ctx context.Context, beats map[string]*redis.SliceCmd, suspects []Suspect) map[string]Evidence {
	out := map[string]Evidence{}
	if e.Prober == nil || len(suspects) == 0 {
		return out
	}
	byBench := map[string][]Suspect{}
	for _, s := range suspects {
		if _, ok := beats[s.Bench]; ok {
			byBench[s.Bench] = append(byBench[s.Bench], s)
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for b, cards := range byBench {
		v := beats[b].Val()
		bench := deal.Bench{Name: b, Host: str(v, 0), User: str(v, 1), Up: len(v) > 0 && v[0] != nil}
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, ExpireDeadline)
			defer cancel()
			ev, err := e.Prober.Probe(pctx, bench, cards)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, s := range cards {
				if x, ok := ev[s.Key()]; ok {
					out[s.Key()] = x
				}
			}
		}()
	}
	wg.Wait()
	return out
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

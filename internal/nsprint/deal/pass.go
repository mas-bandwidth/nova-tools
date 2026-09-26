// Package deal is the reconciler's deal pass (#2756 section 5.3, #2743).
//
// THE HURT (2026-09-22 ~7:55 PM). Every launcher opened one ssh session per
// card and held it for the card's whole run. At max width the Studio's
// launchd-spawned sshd closed every new connection, localhost included, with
// 883 launcher clients pending and 2 cards running; the Studio sat at 2
// working for 40 minutes until Glenn restarted sshd by hand.
//
// THE RULE. For each UP, unpaused bench with free > 0 (desired minus leased,
// over every open sprint), take the highest-priority eligible cards from the
// open sprints' pools by sprint share and priority, filtered by leg against
// the bench profile and by backpressure; reserve them in ONE call per bench
// (queued to dealt, attempt, token, bench:<b>:cards:working), fenced by the
// reconciler's lease token; then open EXACTLY ONE ssh session per bench that
// carries the bench's whole batch to `card launch --stdin`, which starts every
// card detached and returns. Uptake is one pass, never a ramp.
//
// A bench whose sshd refuses the session gets `ssh: refused` on its row in the
// same pass (well inside 10 s: the connect timeout is 5 s), its reservations
// are returned to the pool (nothing ran, so nothing can have started), and the
// same pass deals those cards to the other benches. A launcher that tries to
// open a second session to one bench in one pass is refused by the pass itself
// before the second child starts.
//
// THE WIDTH (#3706, sprint quack-0925b, 2026-09-25 03:59Z). Twelve cards on
// six benches: batman, vision and one hetzner card launched, and hulk, space,
// superman and hetzner's second card read `WEDGED: no session opened: 1.726s
// of lease left after the 1s write margin`. The lease was renewed only when
// the reconciler pass began; the duties before the deal and the reserve calls,
// one bench after another (two round trips each), spent it, so the benches
// planned last had no lease left to open a session in, and their cards took
// 2-3 more passes (10-40 s) to launch. So every bench is one worker, all at
// once, bounded by cfg:deal max_sessions (default 8): the worker reserves its
// bench's batch, renews the lease (Renewer; the reconciler lease coalesces
// the renewals, so one serves every worker that starts just after it, #3737) so its session's lease-derived deadline
// is the full window, opens the one session, and writes its bench's row when
// its own session ends. A pass over N benches takes the slowest bench, not
// the sum.
//
// THE SECOND HURT (#3322, the 2026-09-23 live smoke). A wedged sshd held its
// session past the reconciler lease, the pass wrote no row, and 200
// reservations sat dealt on benches that ran nothing. So every session
// is bounded (the lease bound in the reconciler, the hard deadline in ssh.go,
// which kills the ssh process group), each bench's row is written the moment
// its session ends and not after the slowest bench, and a refused or timed-out
// bench's row and the return of its batch are ONE call (Row.Fail, the
// ns_card_deal companion ns_card_deal_fail), which also counts the bench's
// consecutive timeouts on its row; at cfg:fleet ssh_fail_after (default 3) of
// them the fleet duty marks the bench PROBING at its next step and holds it.
//
// THE SEAMS. The pass holds no state. It reads through Source, presents the
// reconciler's fencing token (Fence, #2726) on every write, writes only
// through Reserver and Row, and reaches benches only through ssh.go. The
// writes are Redis Function calls owned by the nova_sprint library (#2756 2.1
// rule 2); this package names the calls it needs and never issues a bare HSET.
package deal

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/metrics"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchrole"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
)

// SSH states on the bench row (#2756 2.2 `bench:<b>:beat` ssh: ok, refused,
// timeout), plus error for a session that failed after it may have run.
const (
	SSHOK      = "ok"
	SSHRefused = "refused"
	SSHTimeout = "timeout"
	SSHError   = "error"
)

// TierPriority is the only tier that flows while a sprint's backpressure is ON
// (#2756 5.3).
const TierPriority = "priority"

// ReasonSSHRefused is the receipt reason on a reservation returned to the pool
// because its bench's sshd refused the batch session before anything ran;
// ReasonSSHTimeout when the session was cut before anything ran. Row.Fail
// writes `ssh-<state>` itself; these name the two values it can write.
const (
	ReasonSSHRefused = "ssh-refused"
	ReasonSSHTimeout = "ssh-timeout"
)

// DefaultRefusedHold is how long a bench whose sshd refused is skipped before
// the pass tries it again. One sweep (#2756 5.2: a full sweep every 10 s).
const DefaultRefusedHold = 10 * time.Second

// DefaultMaxSessions is how many bench sessions one pass holds open at once
// when cfg:deal max_sessions is unset (#3706): one worker per bench, at most
// this many at a time; a bench past it waits for a slot, then reserves.
const DefaultMaxSessions = 8

// MaxSessionsKey is the hash whose max_sessions field bounds the pass's
// concurrent bench sessions (#3706).
const MaxSessionsKey = "cfg:deal"

// ErrFenced is what a Reserver or Row returns when the token presented is not
// the current lease:reconciler token (#2756 2.1 rule 8: exit 3 FENCED). The
// pass stops at once and deals nothing more.
var ErrFenced = errors.New("FENCED: the reconciler lease is held by another instance")

// Card is one queued card in a sprint's pool.
type Card struct {
	Sprint   string
	Label    string
	Priority float64  // the record's priority field; lower deals first, front items are negative
	Age      float64  // the pool score: the card's created_at ms (#3692); older deals first at equal priority
	Leg      string   // empty: any bench
	Tier     string   // TierPriority or anything else (bulk)
	Bench    string   // the card hash's bench pin, from the card's BENCH: line at card push (#3650); empty: any bench
	Avoid    []string // benches already failed by classification
	// DependsOn is the card's DEPENDS-ON entries (card ids in its sprint or
	// <owner>/<repo>#<n>); `-`, `none` or empty waits for nothing (#3066).
	DependsOn []string
	Repo      string // the card's REPO: a dependency card's PR with no repo is read here
	Base      string // the card's BASE: a dependency is landed only when merged into it
	WaitWhy   string // why the card is in s:<S>:waiting, as the last gate wrote it
}

// Bench is one registered bench as the pass sees it.
type Bench struct {
	Name   string
	Host   string // ssh target host, from the bench beat
	User   string // per bench (a registry field); empty: the ssh default
	Up     bool   // bench:<b>:beat present
	Paused bool
	Legs   []string // the bench profile; empty runs every leg
	Slots  int      // bench:<b>:desired slots
	Leased int      // ZCARD bench:<b>:cards:working, the one lease ledger (#3998)
	SSH    string   // the pass's last ssh state for this bench
	SSHAt  time.Time
	// Role is the registry role column (#3634): benchrole.Friends is dealt
	// no swarm card; empty or benchrole.Fleet is the fleet.
	Role string
	// Enrolled is bench:<b> in consumers (#3998): its harness takes
	// consumer copies itself (card work --fill at each session start), so
	// this pass deals it no sprint card and its sets keep one writer.
	Enrolled bool
}

// Free is desired minus leased, never negative.
func (b Bench) Free() int {
	if f := b.Slots - b.Leased; f > 0 {
		return f
	}
	return 0
}

// Target is the ssh destination.
func (b Bench) Target() string {
	host := b.Host
	if host == "" {
		host = b.Name
	}
	if b.User == "" {
		return host
	}
	return b.User + "@" + host
}

func (b Bench) runs(leg string) bool {
	if leg == "" || len(b.Legs) == 0 {
		return true
	}
	for _, l := range b.Legs {
		if l == leg {
			return true
		}
	}
	return false
}

// Sprint is one open sprint's share of the fleet and its eligible pool.
type Sprint struct {
	Name         string
	Share        int  // weight against the other open sprints; 0 reads as 1
	Backpressure bool // s:<S>:backpressure ON (or missing under a closed policy)
	// Pitstop is s:<S>:pitstop present (#3371): the sprint deals nothing
	// until `nova-sprint pitstop clear` lifts it.
	Pitstop bool
	Pool    []Card
	// Waiting is the queued cards in s:<S>:waiting: an unmet DEPENDS-ON when
	// the last gate ran (#3066). Ready re-tests them every pass.
	Waiting []Card
}

// Input is one consistent read of what the pass needs.
type Input struct {
	Now     time.Time // Redis server time
	Benches []Bench
	Sprints []Sprint
	// Deps is every card a pooled or waiting card names in DEPENDS-ON, keyed
	// <S>/<label>; a card id the sprint does not have is absent (#3066).
	Deps map[string]DepCard
	// MaxSessions is cfg:deal max_sessions; zero when unset or unreadable,
	// and the pass uses its own MaxSessions or DefaultMaxSessions (#3706).
	MaxSessions int
}

// Batch is the cards planned for one bench in one pass.
type Batch struct {
	Bench Bench
	Cards []Card
}

// Plan is the pure deal: for each eligible bench, min(free, eligible) cards
// split across the open sprints by share, highest priority first within each
// sprint. A card is planned to at most one bench; a pit-stopped sprint
// (#3371) plans nothing. hold is how long a refused bench is skipped. Plan
// never writes.
func Plan(in Input, hold time.Duration) []Batch {
	taken := map[string]bool{}
	benches := append([]Bench(nil), in.Benches...)
	sort.Slice(benches, func(i, j int) bool { return benches[i].Name < benches[j].Name })
	pools := make([][]Card, len(in.Sprints))
	for i, s := range in.Sprints {
		pools[i] = append([]Card(nil), s.Pool...)
		sort.SliceStable(pools[i], func(a, b int) bool {
			if pools[i][a].Priority != pools[i][b].Priority {
				return pools[i][a].Priority < pools[i][b].Priority
			}
			if pools[i][a].Age != pools[i][b].Age {
				return pools[i][a].Age < pools[i][b].Age
			}
			return pools[i][a].Label < pools[i][b].Label
		})
	}
	var out []Batch
	for _, b := range benches {
		if !eligible(b, in.Now, hold) {
			continue
		}
		free := b.Free()
		// The candidates of each sprint for this bench, in deal order.
		cands := make([][]Card, len(in.Sprints))
		total := 0
		for i, s := range in.Sprints {
			if s.Pitstop {
				continue
			}
			for _, c := range pools[i] {
				if taken[key(c)] || (c.Bench != "" && c.Bench != b.Name) || contains(c.Avoid, b.Name) || !b.runs(c.Leg) {
					continue
				}
				if s.Backpressure && c.Tier != TierPriority {
					continue
				}
				cands[i] = append(cands[i], c)
			}
			total += len(cands[i])
		}
		n := free
		if total < n {
			n = total
		}
		if n == 0 {
			continue
		}
		picked := share(in.Sprints, cands, n)
		for _, c := range picked {
			taken[key(c)] = true
		}
		out = append(out, Batch{Bench: b, Cards: picked})
	}
	return out
}

func key(c Card) string { return c.Sprint + "/" + c.Label }

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func eligible(b Bench, now time.Time, hold time.Duration) bool {
	if !b.Up || b.Paused || b.Enrolled || b.Role == benchrole.Friends || b.Free() == 0 {
		return false
	}
	if b.SSH == SSHRefused || b.SSH == SSHTimeout {
		// A refusal with no time is never permanent: it is retried.
		if !b.SSHAt.IsZero() && now.Sub(b.SSHAt) < hold {
			return false
		}
	}
	return true
}

// share splits n slots across sprints by weight (largest remainder), caps each
// sprint at its candidates, and gives the slots a short sprint leaves over to
// the remaining candidates in priority order across sprints.
func share(sprints []Sprint, cands [][]Card, n int) []Card {
	weights := make([]int, len(sprints))
	sum := 0
	for i, s := range sprints {
		w := s.Share
		if w <= 0 {
			w = 1
		}
		if len(cands[i]) == 0 {
			w = 0
		}
		weights[i] = w
		sum += w
	}
	quota := make([]int, len(sprints))
	if sum > 0 {
		type rem struct{ i, r int }
		var rems []rem
		given := 0
		for i, w := range weights {
			quota[i] = n * w / sum
			given += quota[i]
			rems = append(rems, rem{i, n * w % sum})
		}
		sort.SliceStable(rems, func(a, b int) bool { return rems[a].r > rems[b].r })
		for k := 0; given < n && k < len(rems); k++ {
			if weights[rems[k].i] > 0 {
				quota[rems[k].i]++
				given++
			}
		}
	}
	var picked, rest []Card
	for i := range sprints {
		q := quota[i]
		if q > len(cands[i]) {
			q = len(cands[i])
		}
		picked = append(picked, cands[i][:q]...)
		rest = append(rest, cands[i][q:]...)
	}
	if len(picked) < n {
		sort.SliceStable(rest, func(a, b int) bool { return rest[a].Priority < rest[b].Priority })
		picked = append(picked, rest[:n-len(picked)]...)
	}
	return picked
}

// Reservation is one card moved queued -> dealt on a bench: its attempt and
// the attempt's fencing token, which the card wrapper presents on every call.
type Reservation struct {
	Card    Card
	Bench   string
	Attempt int
	Token   string
}

// Fence is what the deal pass needs from the reconciler lease of #2726: the
// token of the lease:reconciler this instance holds. It returns an error when
// this instance does not hold the lease; a stale instance may still return its
// old token, and the Reserver's atomic check refuses it (ErrFenced).
type Fence interface {
	Token(ctx context.Context) (string, error)
}

// Renewer is a Fence whose lease the pass extends to its full TTL before it
// opens a bench session (#3706), so a session bounded by the lease gets the
// whole window however long the reads and reserves before it took. It
// returns ErrFenced (or an error wrapping it) when this instance no longer
// holds the lease. The Renewer coalesces (the reconciler lease serves every
// renewal inside 500 ms of the last with it, #3737); the pass asks before
// every session. A Fence that is not a Renewer is never renewed.
type Renewer interface {
	Renew(ctx context.Context) error
}

// Reserver is the per-bench reservation, one Redis Function call per bench
// (#2756 3.2 queued -> dealt, 5.3). Both calls check the fence token against
// lease:reconciler inside the function and return ErrFenced on a mismatch.
type Reserver interface {
	// Reserve moves the cards queued -> dealt on bench in one call: attempt+1,
	// a new token, bench:<b>:cards:working, one receipt each. A card no longer
	// queued is skipped, so the result may be shorter than cards.
	Reserve(ctx context.Context, fence, bench string, cards []Card) ([]Reservation, error)
	// Unreserve returns dealt reservations that were never sent to the bench
	// to queued (fenced, retries+1, reason), freeing their slots.
	Unreserve(ctx context.Context, fence, bench string, res []Reservation, reason string) error
}

// Row records the outcome of the pass's ssh session on the bench row: ok,
// refused, timeout or error, with why. The pass is the only writer.
type Row interface {
	// SSH writes the row for a session that ran (ok, or error: the batch
	// stays dealt).
	SSH(ctx context.Context, fence, bench, state, why string) error
	// Fail writes the row for a session that failed before anything ran
	// (refused or timeout) AND returns its reservations to the pool, in ONE
	// fenced call (#3322): the row can never be written without the return,
	// nor the return without the row. It reports the bench's consecutive
	// timeouts and whether they reached cfg:fleet ssh_fail_after, at which
	// the fleet duty holds the bench (PROBING) at its next step.
	Fail(ctx context.Context, fence, bench, state, why string, res []Reservation) (Failed, error)
}

// Failed is what Row.Fail did.
type Failed struct {
	Returned int    // reservations returned to the pool
	Timeouts int    // the bench's consecutive ssh timeouts after this call
	State    string // the bench's fleet state as the call read it (UP, PROBING, ...)
	Hold     bool   // the timeouts reached ssh_fail_after: the fleet duty holds the bench next
}

// CardWhy writes a refusal line on each named card (#3700): why[i] is
// res[i]'s line, empty for a card with none. The card record's why is the
// launcher's REFUSED line for that card, so `card show` names the refusal
// without a bench. Fenced like Row.
type CardWhy interface {
	CardWhy(ctx context.Context, fence string, res []Reservation, why []string) error
}

// Source reads the pass's Input in one consistent round.
type Source interface {
	Read(ctx context.Context) (Input, error)
}

// Pass is one reconciler deal pass.
type Pass struct {
	Source   Source
	Fence    Fence
	Reserver Reserver
	Row      Row
	Launcher Launcher // nil: BatchLauncher
	Dialer   Dialer   // how a session to a bench is opened
	Hold     time.Duration
	// PRs answers a DEPENDS-ON entry's PR by REST (#3066); nil leaves every
	// PR unknown, so a card that waits on one is never dealt.
	PRs PRs
	// Gate writes the pool <-> waiting moves; nil moves nothing in Redis, and
	// a card that is not ready is still never planned.
	Gate Gate
	// Metrics receives the pool left, the leases held and one session
	// latency per bench after every pass (nx-g61); nil exports nothing.
	Metrics *metrics.Set
	// MaxSessions bounds the bench sessions open at once when cfg:deal
	// max_sessions is unset; zero is DefaultMaxSessions (#3706).
	MaxSessions int
	// Why writes each refused card's refusal line (#3700); nil writes none.
	Why CardWhy
}

// BenchResult is what happened on one bench in one pass.
type BenchResult struct {
	Bench    string
	Planned  int
	Dealt    []Reservation
	Sessions int // ssh sessions opened; never more than 1
	SSH      string
	Why      string
	Returned int // reservations returned to the pool after a refusal or timeout
	Timeouts int // the bench's consecutive ssh timeouts after this pass
	// Held is true when this pass's failure brought the bench's consecutive
	// timeouts to cfg:fleet ssh_fail_after: the fleet duty marks it PROBING
	// at its next step, and this pass plans it nothing more.
	Held bool
	// Took is the bench's worker, reserve to row, on the wall clock (#3706).
	Took time.Duration
	// CardWhy is each Dealt reservation's refusal line (same index), empty
	// for a card that launched: its own REFUSED line from `card launch
	// --stdin`, or Why for every card when the batch never ran (#3700).
	CardWhy []string
}

// Result is one pass.
type Result struct {
	Benches []BenchResult
	Rounds  int
	// Waiting is every card the DEPENDS-ON gate held this pass, with why: an
	// entry that can no longer land is named on every pass (#3066).
	Waiting []Blocked
	// Released counts waiting cards whose dependencies landed this pass.
	Released int
}

// Launched counts reservations sent over a session that succeeded.
func (r Result) Launched() int {
	n := 0
	for _, b := range r.Benches {
		if b.SSH == SSHOK {
			n += len(b.Dealt)
		}
	}
	return n
}

// Run is one deal pass. It deals at most two rounds: the plan, and, only when
// a bench refused its session, the refused cards re-planned onto the benches
// that remain. Each round runs one worker per planned bench, all at once and
// at most maxSessions at a time (#3706); it stops at the first ErrFenced.
func (p *Pass) Run(ctx context.Context) (Result, error) {
	if p.Source == nil || p.Fence == nil || p.Reserver == nil || p.Row == nil || p.Dialer == nil {
		return Result{}, fmt.Errorf("deal: source, fence, reserver, row and dialer are required")
	}
	hold := p.Hold
	if hold <= 0 {
		hold = DefaultRefusedHold
	}
	launcher := p.Launcher
	if launcher == nil {
		launcher = BatchLauncher{}
	}
	token, err := p.Fence.Token(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("deal: %w", err)
	}
	in, err := p.Source.Read(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("deal: read: %w", err)
	}
	var res Result
	// The DEPENDS-ON gate (#3066): only a card whose every dependency is
	// landed on its base stays in the pool the plan reads.
	gated, moves, blocked := Ready(ctx, in, p.PRs)
	res.Waiting = blocked
	for _, s := range in.Sprints {
		m := moves[s.Name]
		for _, mv := range m {
			if mv.Verb == GateRelease {
				res.Released++
			}
		}
		if p.Gate == nil || len(m) == 0 {
			continue
		}
		if err := p.Gate.Gate(ctx, token, s.Name, m); err != nil {
			return res, fmt.Errorf("deal: gate %s: %w", s.Name, err)
		}
	}
	in = gated
	renew := &renewal{fence: p.Fence}
	for round := 0; round < 2; round++ {
		batches := Plan(in, hold)
		if len(batches) == 0 {
			break
		}
		res.Rounds++
		results, err := p.round(ctx, token, launcher, renew, batches, p.maxSessions(in))
		if err != nil {
			return res, err
		}
		refused := false
		for _, br := range results {
			if br.SSH == SSHRefused || br.SSH == SSHTimeout {
				// Nothing ran on the bench: its reservations are back in the
				// pool and this pass deals them elsewhere.
				refused = true
			}
			in = apply(in, br)
		}
		res.Benches = append(res.Benches, results...)
		if !refused {
			break
		}
	}
	p.export(in)
	return res, nil
}

// round runs one worker per batch, at most slots at a time (#3706). Each
// worker reserves its bench's batch (one call, fenced), renews the lease
// before it opens the bench's one session, and writes the bench's row the
// moment that session ends: a slow or wedged bench never delays another's
// launch or row, and the round takes the slowest bench, not the sum. After a
// fence no worker that has not started reserves; the round returns the
// first error by batch order, a fence before any other.
func (p *Pass) round(ctx context.Context, token string, l Launcher, renew *renewal, batches []Batch, slots int) ([]BenchResult, error) {
	results := make([]BenchResult, len(batches))
	errs := make([]error, len(batches))
	sem := make(chan struct{}, slots)
	var fenced atomic.Bool
	var wg sync.WaitGroup
	for i := range batches {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if fenced.Load() {
				errs[i] = ErrFenced
				return
			}
			results[i], errs[i] = p.bench(ctx, token, l, renew, batches[i])
			if errors.Is(errs[i], ErrFenced) {
				fenced.Store(true)
			}
		}(i)
	}
	wg.Wait()
	var first error
	for _, err := range errs {
		if errors.Is(err, ErrFenced) {
			return results, err
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return results, first
}

// bench is one bench's worker: reserve, renew, launch, record.
func (p *Pass) bench(ctx context.Context, token string, l Launcher, renew *renewal, b Batch) (BenchResult, error) {
	start := time.Now()
	reserved, err := p.Reserver.Reserve(ctx, token, b.Bench.Name, b.Cards)
	if err != nil {
		return BenchResult{Bench: b.Bench.Name, Planned: len(b.Cards)}, fmt.Errorf("deal: reserve %s: %w", b.Bench.Name, err)
	}
	if len(reserved) > 0 {
		if err := renew.renew(ctx); err != nil {
			// Fenced: nothing is sent; this instance's writes would be
			// refused, and the next holder's expiry returns the batch.
			return BenchResult{Bench: b.Bench.Name, Planned: len(b.Cards), Dealt: reserved}, fmt.Errorf("deal: renew before %s: %w", b.Bench.Name, err)
		}
	}
	br, err := p.record(ctx, token, p.launch(ctx, l, b, reserved))
	br.Took = time.Since(start)
	return br, err
}

// record writes one bench's row when its own session ends, then each
// refused card's refusal line (#3700). A refused or timed-out session
// (nothing ran on the bench) is Row.Fail: its row and the return of its batch
// are ONE call (#3322); anything else is Row.SSH.
func (p *Pass) record(ctx context.Context, token string, br BenchResult) (BenchResult, error) {
	if br.SSH != SSHRefused && br.SSH != SSHTimeout {
		if err := p.Row.SSH(ctx, token, br.Bench, br.SSH, br.Why); err != nil {
			return br, fmt.Errorf("deal: row %s: %w", br.Bench, err)
		}
	} else {
		f, err := p.Row.Fail(ctx, token, br.Bench, br.SSH, br.Why, br.Dealt)
		if err != nil {
			return br, fmt.Errorf("deal: row %s: %w", br.Bench, err)
		}
		br.Returned, br.Timeouts, br.Held = f.Returned, f.Timeouts, f.Hold
	}
	if p.Why != nil && anyWhy(br.CardWhy) {
		if err := p.Why.CardWhy(ctx, token, br.Dealt, br.CardWhy); err != nil {
			return br, fmt.Errorf("deal: why %s: %w", br.Bench, err)
		}
	}
	return br, nil
}

func (p *Pass) maxSessions(in Input) int {
	switch {
	case in.MaxSessions > 0:
		return in.MaxSessions
	case p.MaxSessions > 0:
		return p.MaxSessions
	}
	return DefaultMaxSessions
}

// renewal is a worker's lease renewal before it opens its session. The
// coalescing lives in the Renewer (the reconciler lease's one Renew, which
// its heartbeat shares, #3737), so there is one renewal mechanism, not two. A
// Fence that is not a Renewer is a no-op.
type renewal struct {
	fence Fence
}

func (r *renewal) renew(ctx context.Context) error {
	rn, ok := r.fence.(Renewer)
	if !ok {
		return nil
	}
	if err := rn.Renew(ctx); err != nil {
		if errors.Is(err, ErrFenced) {
			return err
		}
		// A renewal that failed on the wire is not a fence: the session is
		// still bounded by the lease as it stands.
		return nil
	}
	return nil
}

// export sets the dealer's gauges from the input as the pass left it: the
// cards still pooled over every sprint and the slots leased over every bench.
func (p *Pass) export(in Input) {
	pooled, leased := 0, 0
	for _, s := range in.Sprints {
		pooled += len(s.Pool)
	}
	for _, b := range in.Benches {
		leased += b.Leased
	}
	p.Metrics.QueueDepth(metrics.Dealer, pooled)
	p.Metrics.LeasesHeld(metrics.Dealer, leased)
}

// launch opens the bench's one session through the launcher and classifies it.
func (p *Pass) launch(ctx context.Context, l Launcher, b Batch, res []Reservation) BenchResult {
	br := BenchResult{Bench: b.Bench.Name, Planned: len(b.Cards), Dealt: res}
	if len(res) == 0 {
		br.SSH, br.Why = SSHOK, "nothing reserved"
		return br
	}
	open := &oneSession{dialer: p.Dialer, bench: b.Bench}
	start := time.Now()
	err := l.Launch(ctx, open.Open, b.Bench, res)
	p.Metrics.ProviderLatency(metrics.Dealer, b.Bench.Name, time.Since(start))
	br.Sessions = open.count()
	switch {
	case err == nil:
		br.SSH, br.Why = SSHOK, fmt.Sprintf("%d cards over 1 session", len(res))
	case errors.Is(err, ErrSessionPerCard):
		// The launcher is refused; its first session may have run, so the
		// reservations stay for the reconciler's start-ack rule (3.2).
		br.SSH, br.Why = SSHError, err.Error()
	default:
		var se *SessionError
		if errors.As(err, &se) {
			br.SSH, br.Why = se.State, se.Error()
			br.Why, br.CardWhy = refusalWhys(se, br.Why, len(res))
		} else {
			br.SSH, br.Why = SSHError, err.Error()
		}
	}
	return br
}

// refusalWhys reads a failed session's stdout (#3700): the row's why is the
// first REFUSED line `card launch --stdin` printed, verbatim, else the
// session's own why; each card's why is its own REFUSED line, or, when the
// verb printed no per-line answer at all (the batch never ran: ssh refused,
// the launcher could not start), the row's why.
func refusalWhys(se *SessionError, why string, n int) (string, []string) {
	refused, ran := Refusals(se.Stdout)
	first := 0
	for line := range refused {
		if first == 0 || line < first {
			first = line
		}
	}
	if first > 0 {
		why = refused[first]
	}
	cards := make([]string, n)
	for i := range cards {
		if l, ok := refused[i+1]; ok {
			cards[i] = l
		} else if !ran {
			cards[i] = why
		}
	}
	return why, cards
}

func anyWhy(why []string) bool {
	for _, w := range why {
		if w != "" {
			return true
		}
	}
	return false
}

// apply folds one bench's outcome into the input for the next round: dealt
// cards leave the pools, the bench's lease grows, a refusal marks the bench,
// and a bench whose timeouts reached the hold is no longer up.
func apply(in Input, br BenchResult) Input {
	gone := map[string]bool{}
	returned := br.SSH == SSHRefused || br.SSH == SSHTimeout
	if !returned {
		for _, r := range br.Dealt {
			gone[key(r.Card)] = true
		}
	}
	for i := range in.Benches {
		if in.Benches[i].Name != br.Bench {
			continue
		}
		if returned {
			in.Benches[i].SSH, in.Benches[i].SSHAt = br.SSH, in.Now
			if br.Held {
				in.Benches[i].Up = false
			}
		} else {
			in.Benches[i].Leased += len(br.Dealt)
		}
	}
	for s := range in.Sprints {
		pool := in.Sprints[s].Pool[:0:0]
		for _, c := range in.Sprints[s].Pool {
			if !gone[key(c)] {
				pool = append(pool, c)
			}
		}
		in.Sprints[s].Pool = pool
	}
	return in
}

// RedisSource reads the Input from the fleet Redis in pipelined rounds
// (#2756 2.2, 2.3): the registries, then every bench and sprint hash, then the
// pools, then the pooled cards. Nothing is read with SCAN or KEYS.
type RedisSource struct {
	Client *redis.Client
	// PoolLimit is retired (#3692): the pool is scored by age, so the whole
	// pool is read and each card's priority comes from its record. Kept so
	// callers that set it still build; it bounds nothing.
	PoolLimit int
}

// Read implements Source.
func (r RedisSource) Read(ctx context.Context) (Input, error) {
	c := r.Client
	if c == nil {
		return Input{}, fmt.Errorf("deal: nil redis client")
	}
	pipe := c.Pipeline()
	clock := pipe.Time(ctx)
	benchNames := pipe.SMembers(ctx, "benches")
	order := pipe.ZRange(ctx, "sprint:order", 0, -1)
	open := pipe.SMembers(ctx, "sprints")
	// cfg:deal max_sessions rides the first round (#3706). It is optional:
	// unset, unreadable (an ACL without cfg:*) or not a positive number, the
	// pass uses its default. Its own error is the only one the round
	// forgives; any other command's error (a lost connection, a NOPERM on the
	// registries) fails the read as before.
	maxSessions := pipe.HGet(ctx, MaxSessionsKey, "max_sessions")
	// the sprint epoch (nova-tools#4238) rides this round: the benches'
	// working sets below are keyed by it
	epochCmd := pipe.HGet(ctx, ws.EpochKey, ws.EpochField)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		if err := roundErr(maxSessions, clock, benchNames, order, open); err != nil {
			return Input{}, err
		}
	}
	epoch, err := ws.ParseEpoch(epochCmd.Val())
	if err != nil {
		return Input{}, err
	}
	in := Input{Now: clock.Val()}
	if n, err := strconv.Atoi(maxSessions.Val()); err == nil && n > 0 && maxSessions.Err() == nil {
		in.MaxSessions = n
	}
	openSet := map[string]bool{}
	for _, s := range open.Val() {
		openSet[s] = true
	}
	var sprintNames []string
	for _, s := range order.Val() {
		if openSet[s] {
			sprintNames = append(sprintNames, s)
		}
	}
	names := benchNames.Val()
	sort.Strings(names)

	pipe = c.Pipeline()

	type benchCmds struct {
		desired, beat, ssh *redis.MapStringStringCmd
		state              *redis.StringCmd
		working            *redis.IntCmd
	}
	bc := make([]benchCmds, len(names))
	for i, b := range names {
		bc[i] = benchCmds{
			desired: pipe.HGetAll(ctx, "bench:"+b+":desired"),
			beat:    pipe.HGetAll(ctx, "bench:"+b+":beat"),
			state:   pipe.HGet(ctx, "bench:"+b+":state", "state"),
			ssh:     pipe.HGetAll(ctx, RowKey(b)),
			working: pipe.ZCard(ctx, ws.ConsumerKeyAt(epoch, "bench:"+b, "working")),
		}
	}

	type sprintCmds struct {
		meta, policy, bp *redis.MapStringStringCmd
		stop             *redis.IntCmd
	}
	sc := make([]sprintCmds, len(sprintNames))
	for i, s := range sprintNames {
		sc[i] = sprintCmds{
			meta:   pipe.HGetAll(ctx, "s:"+s),
			policy: pipe.HGetAll(ctx, "s:"+s+":policy"),
			bp:     pipe.HGetAll(ctx, "s:"+s+":backpressure"),
			stop:   pipe.Exists(ctx, pitstop.Key(s)),
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Input{}, err
	}
	for i, name := range names {
		d, beat, ssh := bc[i].desired.Val(), bc[i].beat.Val(), bc[i].ssh.Val()
		slots, _ := strconv.Atoi(d["slots"])
		b := Bench{
			Name:   name,
			Host:   beat["host"],
			User:   beat["user"],
			Up:     bc[i].state.Val() == "UP",
			Paused: d["paused"] == "1" || d["paused"] == "true",
			Legs:   splitList(d["legs"]),
			Slots:  slots,
			Leased: int(bc[i].working.Val()),
			SSH:    ssh["state"],
			Role:   d[benchrole.Field],
		}
		if ms, err := strconv.ParseInt(ssh["at"], 10, 64); err == nil {
			b.SSHAt = time.UnixMilli(ms)
		}
		in.Benches = append(in.Benches, b)
	}
	// Enrollment (#3998) in its own pipeline: a seat whose ACL cannot read
	// consumers yet deals as before (not enrolled), never fails the pass.
	if len(names) > 0 {
		pipe = c.Pipeline()
		enrolled := make([]*redis.BoolCmd, len(names))
		for i, b := range names {
			enrolled[i] = pipe.SIsMember(ctx, "consumers", "bench:"+b)
		}
		if _, err := pipe.Exec(ctx); err == nil {
			for i := range in.Benches {
				in.Benches[i].Enrolled = enrolled[i].Val()
			}
		}
	}
	pipe = c.Pipeline()
	pools := make([]*redis.ZSliceCmd, len(sprintNames))
	waits := make([]*redis.StringSliceCmd, len(sprintNames))
	var live []int
	for i, s := range sprintNames {
		if sc[i].meta.Val()["status"] != "open" {
			continue
		}
		live = append(live, i)
		// The pool is scored by age (created_at, #3692), not priority, so
		// the whole pool is read (O(n)) and the priority comes from each
		// record.
		// the dealer's lists under the current epoch (nova-tools#4238): a
		// card pushed after a clear is in the epoch's pool, an older one is not
		pools[i] = pipe.ZRangeWithScores(ctx, ws.SprintListAt(epoch, s, "pool"), 0, -1)
		waits[i] = pipe.SMembers(ctx, ws.SprintListAt(epoch, s, "waiting"))

	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Input{}, err
	}
	pipe = c.Pipeline()
	type cardCmd struct {
		sprint  int
		label   string
		score   float64
		waiting bool
		cmd     *redis.SliceCmd
	}
	var cards []cardCmd
	fields := []string{"state", "leg", "tier", "bench", "depends_on", "repo", "base", "wait_why", "priority", "avoid"}
	for _, i := range live {
		for _, z := range pools[i].Val() {
			label, _ := z.Member.(string)
			cards = append(cards, cardCmd{i, label, z.Score, false, pipe.HMGet(ctx, "s:"+sprintNames[i]+":card:"+label, fields...)})
		}
		labels := waits[i].Val()
		sort.Strings(labels)
		for _, label := range labels {
			cards = append(cards, cardCmd{i, label, 0, true, pipe.HMGet(ctx, "s:"+sprintNames[i]+":card:"+label, fields...)})
		}
	}
	if len(cards) > 0 {
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return Input{}, err
		}
	}
	bySprint, waitBySprint := map[int][]Card{}, map[int][]Card{}
	for _, cc := range cards {
		v := cc.cmd.Val()
		if str(v, 0) != "queued" {
			continue
		}
		card := Card{
			Sprint: sprintNames[cc.sprint], Label: cc.label, Age: cc.score,
			Leg: str(v, 1), Tier: str(v, 2), Bench: str(v, 3),
			DependsOn: splitDeps(str(v, 4)), Repo: str(v, 5), Base: str(v, 6), WaitWhy: str(v, 7),
			Avoid: strings.Fields(str(v, 9)),
		}
		card.Priority, _ = strconv.ParseFloat(str(v, 8), 64)
		if cc.waiting {
			waitBySprint[cc.sprint] = append(waitBySprint[cc.sprint], card)
			continue
		}
		bySprint[cc.sprint] = append(bySprint[cc.sprint], card)
	}
	// The cards named in DEPENDS-ON, one pipelined round (#3066).
	type depCmd struct {
		key string
		cmd *redis.SliceCmd
	}
	var depCmds []depCmd
	seenDep := map[string]bool{}
	for _, group := range []map[int][]Card{bySprint, waitBySprint} {
		for _, cs := range group {
			for _, cd := range cs {
				for _, e := range cd.DependsOn {
					if e == "" || e == "-" || e == "none" {
						continue
					}
					if _, _, ok := parseRef(e); ok {
						continue
					}
					k := cd.Sprint + "/" + e
					if seenDep[k] {
						continue
					}
					seenDep[k] = true
					depCmds = append(depCmds, depCmd{k, nil})
				}
			}
		}
	}
	if len(depCmds) > 0 {
		sort.Slice(depCmds, func(a, b int) bool { return depCmds[a].key < depCmds[b].key })
		pipe = c.Pipeline()
		for i := range depCmds {
			slash := strings.IndexByte(depCmds[i].key, '/')
			S, label := depCmds[i].key[:slash], depCmds[i].key[slash+1:]
			depCmds[i].cmd = pipe.HMGet(ctx, "s:"+S+":card:"+label, "state", "outcome", "repo", "base", "pr", "pushed_sha")
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return Input{}, err
		}
		in.Deps = map[string]DepCard{}
		for _, d := range depCmds {
			v := d.cmd.Val()
			if len(v) == 0 || v[0] == nil {
				continue
			}
			pr, _ := strconv.Atoi(str(v, 4))
			in.Deps[d.key] = DepCard{Found: true, State: str(v, 0), Outcome: str(v, 1), Repo: str(v, 2), Base: str(v, 3), PR: pr, PushedSHA: str(v, 5)}
		}
	}
	for _, i := range live {
		policy, bp := sc[i].policy.Val(), sc[i].bp.Val()
		shareN, _ := strconv.Atoi(policy["share"])
		on := bp["state"] == "ON" || bp["read_bound"] == "1"
		if len(bp) == 0 {
			// #2756 5.3: a missing hash applies the declared policy; "open"
			// (fail-open) is OFF, "closed" is ON.
			on = policy["backpressure_missing"] == "closed"
		}
		in.Sprints = append(in.Sprints, Sprint{Name: sprintNames[i], Share: shareN, Backpressure: on,
			Pitstop: sc[i].stop.Val() > 0, Pool: bySprint[i], Waiting: waitBySprint[i]})
	}
	return in, nil
}

// roundErr is the first error, other than redis.Nil, of the round's
// commands, skipping forgiven: the one optional read whose own error leaves
// its default. A round whose Exec failed but whose every other command
// succeeded failed only on forgiven.
func roundErr(forgiven redis.Cmder, cmds ...redis.Cmder) error {
	for _, cmd := range cmds {
		if cmd == forgiven {
			continue
		}
		if err := cmd.Err(); err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
	}
	return nil
}

// RowKey is the hash (bench:<b>:ssh) the deal pass's Row writes the last session outcome to:
// state, why, at (Redis TIME, ms). The deal pass is its only writer; the bench
// beat keeps its own row (#2756 2.1 rule 4).
func RowKey(bench string) string { return "bench:" + bench + ":ssh" }

func splitList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		out = append(out, f)
	}
	return out
}

// splitDeps reads a card's depends_on field: entries separated by commas
// (SPEC-CARD clause 10), each trimmed.
func splitDeps(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func str(v []any, i int) string {
	if i >= len(v) || v[i] == nil {
		return ""
	}
	s, _ := v[i].(string)
	return s
}

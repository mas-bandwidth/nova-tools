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
// (queued to dealt, attempt, token, bench:<b>:starting), fenced by the
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
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/metrics"
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
// because its bench's sshd refused the batch session before anything ran.
const ReasonSSHRefused = "ssh-refused"

// DefaultRefusedHold is how long a bench whose sshd refused is skipped before
// the pass tries it again. One sweep (#2756 5.2: a full sweep every 10 s).
const DefaultRefusedHold = 10 * time.Second

// ErrFenced is what a Reserver or Row returns when the token presented is not
// the current lease:reconciler token (#2756 2.1 rule 8: exit 3 FENCED). The
// pass stops at once and deals nothing more.
var ErrFenced = errors.New("FENCED: the reconciler lease is held by another instance")

// Card is one queued card in a sprint's pool.
type Card struct {
	Sprint   string
	Label    string
	Priority float64  // the pool score; lower deals first, front items are negative
	Leg      string   // empty: any bench
	Tier     string   // TierPriority or anything else (bulk)
	Bench    string   // pinned by `card push --bench`; empty: any bench
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
	Leased int      // ZCARD starting + ZCARD living, over every sprint
	SSH    string   // the pass's last ssh state for this bench
	SSHAt  time.Time
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
	Pool         []Card
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
}

// Batch is the cards planned for one bench in one pass.
type Batch struct {
	Bench Bench
	Cards []Card
}

// Plan is the pure deal: for each eligible bench, min(free, eligible) cards
// split across the open sprints by share, highest priority first within each
// sprint. A card is planned to at most one bench. hold is how long a refused
// bench is skipped. Plan never writes.
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
	if !b.Up || b.Paused || b.Free() == 0 {
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

// Reserver is the per-bench reservation, one Redis Function call per bench
// (#2756 3.2 queued -> dealt, 5.3). Both calls check the fence token against
// lease:reconciler inside the function and return ErrFenced on a mismatch.
type Reserver interface {
	// Reserve moves the cards queued -> dealt on bench in one call: attempt+1,
	// a new token, bench:<b>:starting, one receipt each. A card no longer
	// queued is skipped, so the result may be shorter than cards.
	Reserve(ctx context.Context, fence, bench string, cards []Card) ([]Reservation, error)
	// Unreserve returns dealt reservations that were never sent to the bench
	// to queued (fenced, retries+1, reason), freeing their slots.
	Unreserve(ctx context.Context, fence, bench string, res []Reservation, reason string) error
}

// Row records the outcome of the pass's ssh session on the bench row: ok,
// refused, timeout or error, with why. The pass is the only writer.
type Row interface {
	SSH(ctx context.Context, fence, bench, state, why string) error
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
}

// BenchResult is what happened on one bench in one pass.
type BenchResult struct {
	Bench    string
	Planned  int
	Dealt    []Reservation
	Sessions int // ssh sessions opened; never more than 1
	SSH      string
	Why      string
	Returned int // reservations returned to the pool after a refusal
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
// that remain. It stops at the first ErrFenced.
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
	for round := 0; round < 2; round++ {
		batches := Plan(in, hold)
		if len(batches) == 0 {
			break
		}
		res.Rounds++
		// Reserve every bench first, one call each, so a fenced token deals
		// nothing further; then one session per bench, all benches at once.
		reserved := make([][]Reservation, len(batches))
		for i, b := range batches {
			r, err := p.Reserver.Reserve(ctx, token, b.Bench.Name, b.Cards)
			if err != nil {
				return res, fmt.Errorf("deal: reserve %s: %w", b.Bench.Name, err)
			}
			reserved[i] = r
		}
		results := make([]BenchResult, len(batches))
		var wg sync.WaitGroup
		for i := range batches {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i] = p.launch(ctx, launcher, batches[i], reserved[i])
			}(i)
		}
		wg.Wait()
		refused := false
		for i, br := range results {
			b := batches[i].Bench
			if err := p.Row.SSH(ctx, token, b.Name, br.SSH, br.Why); err != nil {
				return res, fmt.Errorf("deal: row %s: %w", b.Name, err)
			}
			if br.SSH == SSHRefused || br.SSH == SSHTimeout {
				// Nothing ran on the bench: the reservations go back to the
				// pool and this pass deals them elsewhere.
				if len(br.Dealt) > 0 {
					if err := p.Reserver.Unreserve(ctx, token, b.Name, br.Dealt, ReasonSSHRefused); err != nil {
						return res, fmt.Errorf("deal: unreserve %s: %w", b.Name, err)
					}
				}
				br.Returned = len(br.Dealt)
				results[i] = br
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
		} else {
			br.SSH, br.Why = SSHError, err.Error()
		}
	}
	return br
}

// apply folds one bench's outcome into the input for the next round: dealt
// cards leave the pools, the bench's lease grows, a refusal marks the bench.
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
	// PoolLimit bounds the pool entries read per sprint; 0 reads four times
	// the fleet's free slots (at least 64), enough for leg and pin filters.
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
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Input{}, err
	}
	in := Input{Now: clock.Val()}
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
		starting, living   *redis.IntCmd
	}
	bc := make([]benchCmds, len(names))
	for i, b := range names {
		bc[i] = benchCmds{
			desired:  pipe.HGetAll(ctx, "bench:"+b+":desired"),
			beat:     pipe.HGetAll(ctx, "bench:"+b+":beat"),
			state:    pipe.HGet(ctx, "bench:"+b+":state", "state"),
			ssh:      pipe.HGetAll(ctx, RowKey(b)),
			starting: pipe.ZCard(ctx, "bench:"+b+":starting"),
			living:   pipe.ZCard(ctx, "bench:"+b+":living"),
		}
	}
	type sprintCmds struct{ meta, policy, bp *redis.MapStringStringCmd }
	sc := make([]sprintCmds, len(sprintNames))
	for i, s := range sprintNames {
		sc[i] = sprintCmds{
			meta:   pipe.HGetAll(ctx, "s:"+s),
			policy: pipe.HGetAll(ctx, "s:"+s+":policy"),
			bp:     pipe.HGetAll(ctx, "s:"+s+":backpressure"),
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return Input{}, err
	}
	free := 0
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
			Leased: int(bc[i].starting.Val() + bc[i].living.Val()),
			SSH:    ssh["state"],
		}
		if ms, err := strconv.ParseInt(ssh["at"], 10, 64); err == nil {
			b.SSHAt = time.UnixMilli(ms)
		}
		in.Benches = append(in.Benches, b)
		if b.Up && !b.Paused {
			free += b.Free()
		}
	}
	limit := r.PoolLimit
	if limit <= 0 {
		limit = 4 * free
		if limit < 64 {
			limit = 64
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
		pools[i] = pipe.ZRangeWithScores(ctx, "s:"+s+":pool", 0, int64(limit-1))
		waits[i] = pipe.SMembers(ctx, "s:"+s+":waiting")
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
			Sprint: sprintNames[cc.sprint], Label: cc.label, Priority: cc.score,
			Leg: str(v, 1), Tier: str(v, 2), Bench: str(v, 3),
			DependsOn: splitDeps(str(v, 4)), Repo: str(v, 5), Base: str(v, 6), WaitWhy: str(v, 7),
			Avoid: strings.Fields(str(v, 9)),
		}
		if cc.waiting {
			card.Priority, _ = strconv.ParseFloat(str(v, 8), 64)
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
		on := bp["state"] == "ON"
		if len(bp) == 0 {
			// #2756 5.3: a missing hash applies the declared policy; "open"
			// (fail-open) is OFF, "closed" is ON.
			on = policy["backpressure_missing"] == "closed"
		}
		in.Sprints = append(in.Sprints, Sprint{Name: sprintNames[i], Share: shareN, Backpressure: on, Pool: bySprint[i], Waiting: waitBySprint[i]})
	}
	return in, nil
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

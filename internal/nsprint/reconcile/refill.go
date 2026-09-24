package reconcile

// The refill duty (nova-tools #2935; #2756 5.1.2, 5.1.3, 5.2).
//
// THE HURT (2026-09-22). A friend or bench whose child finished sat at
// working below its slots until something polled it: the bash refill ran on a
// timer, dealt by hand-written HSET and XADD in separate calls (control 9),
// and Glenn visited sessions by hand to refill them. The rule since 6:45 PM
// that night: on every child completion, working = min(slots, working +
// open), in the same pass.
//
// THE RULE. Refill runs inside the reconciler pass, after the pass has
// renewed lease:reconciler, and presents the lease token on every write, so
// the dealer is never a separate process (5.1.3) and a stale instance deals
// nothing. It is woken by stream events: each pass it reads the consumer
// group `reconciler` on cap:log and on every open sprint's s:<S>:log, and an
// event that frees a bench slot, raises capacity, returns a bench, or changes
// a card's eligibility runs the deal pass (#2743, #3063, #3066) in that same
// reconciler pass. With no event, the deal pass still runs once per sweep
// (10 s), and always on the first pass of a new instance (restart, 5.4),
// which first claims every entry a dead instance left pending. A bench whose
// beat appears after it was absent (bench return, 5.2) is a wake too; the
// beat has a TTL and writes no event, so the duty reads the registered beats'
// presence in one pipelined round each pass.
//
// Capacity is global: the deal reads free as desired minus leased over every
// open sprint, and ns_card_deal re-checks it inside the function, so two
// sprints sharing one bench, or two passes racing, never lease above desired
// (control 5).
//
// An event the reconciler itself wrote (actor = the duty's actor: its own
// `card deal`, `card wait`, `slot-freed` from an ssh-refused undeal) never
// wakes it, and a beat receipt never does (at 64 cards the beats alone would
// run a deal, and its forge reads, every pass). Wake events are acknowledged
// only after the deal pass they woke succeeds: a crash in between leaves them
// pending for the next instance, whose restart sweep supersedes them.
//
// Task and friend events (a friend slot freed, a friend's hello, a task
// push) are route wakes, handed to Route; routing ready tasks to friends is
// the route duty's, not this file's. Route wakes are acknowledged only after
// Route succeeds, apart from the deal's: a failed Route leaves them pending
// and the next pass hands them to Route again (RouteReplay) without waking
// the deal, so a failed route is replayed and never lost, and the deal it
// rode in with runs once.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
)

const (
	// Group is the reconciler's consumer group on cap:log and every
	// s:<S>:log (spec 2.3).
	Group = "reconciler"
	// CapLog is the capacity and presence stream (spec 2.2).
	CapLog = "cap:log"
	// DefaultSweep is the full-sweep floor (5.1.2: a full sweep every 10 s).
	DefaultSweep = 10 * time.Second
	// DefaultActor is the actor the reconciler's functions write on receipts.
	DefaultActor = "reconciler"
	// DefaultEventCount bounds the events read per stream per pass.
	DefaultEventCount = 1000
)

// Wake is what one pass saw that asks for work.
type Wake struct {
	Restart bool // first pass of this instance (5.4)
	Sweep   bool // the sweep floor elapsed
	Retry   bool // the previous deal pass failed
	Deal    int  // events that free a bench slot or change eligibility
	Route   int  // events for the route duty (friend and task)
	// RouteReplay counts route events a failed Route left pending (or a dead
	// instance left pending, claimed at restart), handed to Route again.
	RouteReplay int
	// Benches names each bench whose beat is present after it was absent.
	Benches []string
	Claimed int // entries a dead instance left pending, claimed at restart
}

// Dealing is true when this pass must run the deal pass.
func (w Wake) Dealing() bool {
	return w.Restart || w.Sweep || w.Retry || w.Deal > 0 || len(w.Benches) > 0
}

// Why is the wake in one line, for the pass record and the log.
func (w Wake) Why() string {
	var p []string
	if w.Restart {
		p = append(p, fmt.Sprintf("restart claimed=%d", w.Claimed))
	}
	if w.Sweep {
		p = append(p, "sweep")
	}
	if w.Retry {
		p = append(p, "retry")
	}
	if w.Deal > 0 {
		p = append(p, fmt.Sprintf("events=%d", w.Deal))
	}
	if len(w.Benches) > 0 {
		p = append(p, "bench-return="+strings.Join(w.Benches, ","))
	}
	if w.Route > 0 {
		p = append(p, fmt.Sprintf("route=%d", w.Route))
	}
	if w.RouteReplay > 0 {
		p = append(p, fmt.Sprintf("route-replay=%d", w.RouteReplay))
	}
	return strings.Join(p, " ")
}

// Refill is the reconciler's refill duty. Its Run is a Duty.
type Refill struct {
	Client *redis.Client
	// Deal is the deal pass. Fence is set from the lease each pass; a nil
	// Source, Reserver, Row or Gate is filled with the Redis ones.
	Deal *deal.Pass
	// Route, when set, runs on a pass that saw route events or has route
	// events a failed Route left pending; it returns the tasks it routed.
	// Its events are acknowledged only when it returns nil. Nil: route
	// events are acknowledged and counted only.
	Route func(ctx context.Context, l *Lease, w Wake) (int, error)
	// Sweep is the deal floor; DefaultSweep when zero.
	Sweep time.Duration
	// Block, when above zero, lets a pass that has nothing to do wait up to
	// Block on the streams for its first event, so the event is dealt in the
	// pass it arrives in. Keep it below the loop interval. Zero never blocks.
	Block time.Duration
	// Consumer is the group consumer name; the lease instance when empty.
	Consumer string
	// Actor is written on receipts and filtered from wakes; DefaultActor
	// when empty.
	Actor string
	// Now is the sweep clock; time.Now when nil.
	Now func() time.Time
	// AfterDeal, when set, sees every deal pass the duty ran and why.
	AfterDeal func(Wake, deal.Result)
	// WriteMargin is the lease time kept back from the bench sessions for the
	// pass's fenced writes (#3322); DefaultWriteMargin when zero.
	WriteMargin time.Duration

	mu        sync.Mutex
	instance  string              // the lease instance the state below belongs to
	groups    map[string]bool     // streams whose group this instance ensured
	up        map[string]bool     // bench -> beat present at the last pass
	lastDeal  time.Time           // the last deal pass that succeeded
	retry     bool                // the last deal pass failed
	unacked   map[string][]string // stream -> deal (and no-wake) ids read, not yet acked
	routeIDs  map[string][]string // stream -> route ids read, not yet routed and acked
	firstPass bool
}

// Run is one refill: read the wakes, deal when woken or when the sweep is
// due, acknowledge what the deal answered. It returns ErrFenced when the
// lease is lost, and the pass stops.
func (r *Refill) Run(ctx context.Context, l *Lease) (Counts, error) {
	if r.Client == nil || r.Deal == nil || l == nil {
		return Counts{}, fmt.Errorf("refill: client, deal pass and lease are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.instance != l.Instance() {
		// A new instance: the state of the old one is not evidence.
		r.instance = l.Instance()
		r.groups, r.up, r.unacked, r.routeIDs = map[string]bool{}, nil, map[string][]string{}, map[string][]string{}
		r.lastDeal, r.retry, r.firstPass = time.Time{}, false, true
	}
	consumer := r.consumer(l)

	streams, benches, err := r.registry(ctx)
	if err != nil {
		return Counts{}, fmt.Errorf("refill: %w", err)
	}
	if err := r.ensureGroups(ctx, streams); err != nil {
		return Counts{}, fmt.Errorf("refill: %w", err)
	}
	var w Wake
	if r.firstPass {
		w.Restart = true
		n, err := r.claim(ctx, streams, consumer)
		if err != nil {
			return Counts{}, fmt.Errorf("refill: restart claim: %w", err)
		}
		w.Claimed = n
	}
	// Route events still pending from a failed Route (or claimed at restart)
	// are replayed to Route this pass; they do not wake the deal.
	w.RouteReplay = pending(r.routeIDs)
	w.Retry = r.retry
	if !r.lastDeal.IsZero() && r.now().Sub(r.lastDeal) >= r.sweep() {
		w.Sweep = true
	}
	returned, err := r.benchReturns(ctx, benches)
	if err != nil {
		return Counts{}, fmt.Errorf("refill: bench beats: %w", err)
	}
	w.Benches = returned
	block := time.Duration(-1)
	if r.Block > 0 && !w.Dealing() && w.RouteReplay == 0 {
		block = r.Block
	}
	if err := r.read(ctx, streams, consumer, block, &w); err != nil {
		return Counts{}, fmt.Errorf("refill: read: %w", err)
	}

	var c Counts
	var errs []string
	// routed is true when the route events read so far may be acknowledged:
	// Route ran and succeeded, or there is no Route to hand them to.
	routed := true
	if w.Route+w.RouteReplay > 0 && r.Route != nil {
		n, err := r.Route(ctx, l, w)
		if errors.Is(err, ErrFenced) {
			return c, err
		}
		if err != nil {
			// Keep the route events pending: the next pass replays them.
			routed = false
			errs = append(errs, "route: "+err.Error())
		}
		c.Routed += n
	}
	if !w.Dealing() {
		// Only route events, or none: nothing to deal. Acknowledge the
		// no-wake events, and the route events only if they were routed.
		if err := r.ack(ctx, true, routed); err != nil {
			errs = append(errs, "ack: "+err.Error())
		}
		return c, joinErrs(errs)
	}
	res, err := r.pass(l).Run(ctx)
	if err != nil {
		if errors.Is(err, deal.ErrFenced) || errors.Is(err, ErrFenced) {
			return c, fmt.Errorf("refill: %w: %v", ErrFenced, err)
		}
		// Keep the deal wakes; the next pass deals again whatever it reads.
		// Routed events are done with and acknowledged.
		r.retry = true
		errs = append(errs, "deal: "+err.Error())
		if routed {
			if err := r.ack(ctx, false, true); err != nil {
				errs = append(errs, "ack: "+err.Error())
			}
		}
		return c, joinErrs(errs)
	}
	r.retry, r.firstPass = false, false
	r.lastDeal = r.now()
	c.Dealt += res.Launched()
	if r.AfterDeal != nil {
		r.AfterDeal(w, res)
	}
	if err := r.ack(ctx, true, routed); err != nil {
		errs = append(errs, "ack: "+err.Error())
	}
	return c, joinErrs(errs)
}

func joinErrs(errs []string) error {
	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "; "))
}

func (r *Refill) consumer(l *Lease) string {
	if r.Consumer != "" {
		return r.Consumer
	}
	return l.Instance()
}

func (r *Refill) actor() string {
	if r.Actor != "" {
		return r.Actor
	}
	return DefaultActor
}

func (r *Refill) sweep() time.Duration {
	if r.Sweep > 0 {
		return r.Sweep
	}
	return DefaultSweep
}

func (r *Refill) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// pass is the deal pass for this lease, with the Redis seams filled in.
func (r *Refill) pass(l *Lease) *deal.Pass {
	p := *r.Deal
	p.Fence = LeaseFence{l}
	if p.Dialer != nil {
		// Every bench session ends inside the lease, so a wedged sshd can
		// neither fence the pass nor strand its reservations (#3322).
		p.Dialer = LeaseBound{Dialer: p.Dialer, Lease: l, Margin: r.WriteMargin}
	}
	fns := &DealFunctions{Client: r.Client, Actor: r.actor()}
	if p.Source == nil {
		p.Source = deal.RedisSource{Client: r.Client}
	}
	if p.Reserver == nil {
		p.Reserver = fns
	}
	if p.Row == nil {
		p.Row = fns
	}
	if p.Gate == nil {
		p.Gate = fns
	}
	return &p
}

// registry reads the streams to follow (cap:log, then every open sprint's
// log in sprint order) and the registered benches, in one round.
func (r *Refill) registry(ctx context.Context) ([]string, []string, error) {
	pipe := r.Client.Pipeline()
	sprints := pipe.SMembers(ctx, "sprints")
	benches := pipe.SMembers(ctx, "benches")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, nil, err
	}
	names := sprints.Val()
	sort.Strings(names)
	streams := []string{CapLog}
	for _, s := range names {
		streams = append(streams, "s:"+s+":log")
	}
	b := benches.Val()
	sort.Strings(b)
	return streams, b, nil
}

// ensureGroups creates the group on a stream this instance has not seen,
// at the stream's end: what came before is covered by the restart sweep.
func (r *Refill) ensureGroups(ctx context.Context, streams []string) error {
	for _, s := range streams {
		if r.groups[s] {
			continue
		}
		err := r.Client.XGroupCreateMkStream(ctx, s, Group, "$").Err()
		if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("group %s on %s: %w", Group, s, err)
		}
		r.groups[s] = true
	}
	return nil
}

// claim takes every entry pending in the group (any consumer's: a dead
// instance's wakes) into this consumer, to be acknowledged by the restart
// sweep's deal.
func (r *Refill) claim(ctx context.Context, streams []string, consumer string) (int, error) {
	n := 0
	for _, s := range streams {
		start := "0-0"
		for {
			msgs, next, err := r.Client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream: s, Group: Group, Consumer: consumer, MinIdle: 0, Start: start, Count: DefaultEventCount,
			}).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return n, fmt.Errorf("%s: %w", s, err)
			}
			for _, m := range msgs {
				r.keep(s, m, r.actor())
			}
			n += len(msgs)
			if next == "" || next == "0-0" {
				break
			}
			start = next
		}
	}
	return n, nil
}

// benchReturns reads each registered bench's state in one round and
// names the benches whose state changed to UP since the last pass.
// The first pass records presence only (it deals anyway).
func (r *Refill) benchReturns(ctx context.Context, benches []string) ([]string, error) {
	if len(benches) == 0 {
		r.up = map[string]bool{}
		return nil, nil
	}
	pipe := r.Client.Pipeline()
	cmds := make([]*redis.StringCmd, len(benches))
	for i, b := range benches {
		cmds[i] = pipe.HGet(ctx, "bench:"+b+":state", "state")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	up := make(map[string]bool, len(benches))
	var returned []string
	for i, b := range benches {
		up[b] = cmds[i].Val() == "UP"
		if up[b] && r.up != nil && !r.up[b] {
			returned = append(returned, b)
		}
	}
	r.up = up
	return returned, nil
}

// read takes this consumer's new events on every stream in one call and
// classifies them into w. block < 0 never waits.
func (r *Refill) read(ctx context.Context, streams []string, consumer string, block time.Duration, w *Wake) error {
	args := make([]string, 0, 2*len(streams))
	args = append(args, streams...)
	for range streams {
		args = append(args, ">")
	}
	res, err := r.Client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: Group, Consumer: consumer, Streams: args, Count: DefaultEventCount, Block: block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, s := range res {
		for _, m := range s.Messages {
			switch r.keep(s.Stream, m, r.actor()) {
			case WakeDeal:
				w.Deal++
			case WakeRoute:
				w.Route++
			}
		}
	}
	return nil
}

// keep holds one read or claimed entry until it may be acknowledged: a route
// wake until Route succeeds, anything else until the deal it woke (or the
// pass that had nothing to deal) is done. It returns the entry's class.
func (r *Refill) keep(stream string, m redis.XMessage, actor string) string {
	class := Classify(stream, m.Values, actor)
	if class == WakeRoute {
		r.routeIDs[stream] = append(r.routeIDs[stream], m.ID)
	} else {
		r.unacked[stream] = append(r.unacked[stream], m.ID)
	}
	return class
}

func pending(ids map[string][]string) int {
	n := 0
	for _, v := range ids {
		n += len(v)
	}
	return n
}

// ack acknowledges, in one pipelined round, the deal and no-wake entries
// held since the last ack (when deals) and the route entries (when routes).
func (r *Refill) ack(ctx context.Context, deals, routes bool) error {
	var sets []map[string][]string
	if deals && pending(r.unacked) > 0 {
		sets = append(sets, r.unacked)
	}
	if routes && pending(r.routeIDs) > 0 {
		sets = append(sets, r.routeIDs)
	}
	if len(sets) == 0 {
		return nil
	}
	pipe := r.Client.Pipeline()
	for _, set := range sets {
		for s, ids := range set {
			if len(ids) > 0 {
				pipe.XAck(ctx, s, Group, ids...)
			}
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	if deals {
		r.unacked = map[string][]string{}
	}
	if routes {
		r.routeIDs = map[string][]string{}
	}
	return nil
}

// Wake classes of one event.
const (
	WakeNone  = ""
	WakeDeal  = "deal"
	WakeRoute = "route"
)

// Classify names what one stream event wakes (5.2). An event whose actor is
// the reconciler's own, and any beat, wakes nothing. On cap:log an event
// about a friend (consumer or target friend:<f>) is a route wake and every
// other one (a bench slot freed, a capacity or machine change, a pause or
// resume) is a deal wake. On s:<S>:log a task event is a route wake, a card or
// sprint event (push, end, release, priority, deps, landed, resume) a deal
// wake.
func Classify(stream string, v map[string]any, actor string) string {
	get := func(k string) string {
		s, _ := v[k].(string)
		return s
	}
	kind := get("kind")
	if get("actor") == actor || strings.HasSuffix(kind, " beat") || kind == "beat" {
		return WakeNone
	}
	if stream == CapLog {
		if strings.HasPrefix(get("consumer"), "friend:") || strings.HasPrefix(get("target"), "friend:") {
			return WakeRoute
		}
		return WakeDeal
	}
	switch {
	case strings.HasPrefix(kind, "task "), strings.HasPrefix(kind, "friend "):
		return WakeRoute
	case strings.HasPrefix(kind, "card "), strings.HasPrefix(kind, "sprint "):
		return WakeDeal
	}
	return WakeNone
}

// LeaseFence is the deal pass's Fence over this instance's lease: every
// deal function call presents this token and refuses FENCED on a mismatch.
type LeaseFence struct{ L *Lease }

// Token implements deal.Fence.
func (f LeaseFence) Token(context.Context) (string, error) {
	if f.L == nil || f.L.token == "" {
		return "", fmt.Errorf("no reconciler lease: %w", ErrFenced)
	}
	return f.L.token, nil
}

// DealFunctions is the deal pass's Reserver, Row and Gate over the
// nova_sprint functions of deal.lua (#3063): one ns_card_deal call per bench,
// one ns_card_undeal per returned batch, one ns_bench_ssh per bench row, one
// ns_card_gate per sprint. Each checks the fence token inside the function.
type DealFunctions struct {
	Client *redis.Client
	Actor  string
}

var (
	_ deal.Reserver = (*DealFunctions)(nil)
	_ deal.Row      = (*DealFunctions)(nil)
	_ deal.Gate     = (*DealFunctions)(nil)
)

// Reserve reads each card's attempt in one pipelined round, mints
// <attempt+1>.<128 random bits hex> per card (spec 2.1 rule 8) and moves the
// batch queued -> dealt in one call. The function skips a card whose attempt
// moved or that is no longer queued, and caps the batch at the bench's free
// slots over every sprint, so the result may be shorter than cards.
func (d *DealFunctions) Reserve(ctx context.Context, fence, bench string, cards []deal.Card) ([]deal.Reservation, error) {
	if len(cards) == 0 {
		return nil, nil
	}
	pipe := d.Client.Pipeline()
	attempts := make([]*redis.StringCmd, len(cards))
	for i, c := range cards {
		attempts[i] = pipe.HGet(ctx, "s:"+c.Sprint+":card:"+c.Label, "attempt")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	args := []any{bench, fence, d.actor(), ""}
	byKey := map[string]deal.Card{}
	for i, c := range cards {
		a, _ := strconv.Atoi(attempts[i].Val())
		token := fmt.Sprintf("%d.%s", a+1, randomHex(16))
		args = append(args, c.Sprint, c.Label, strconv.Itoa(a+1), token, tokenSHA(token))
		byKey[c.Sprint+"/"+c.Label] = c
	}
	reply, err := d.Client.FCall(ctx, "ns_card_deal", nil, args...).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("ns_card_deal: %w", err)
	}
	if len(reply) == 0 {
		return nil, fmt.Errorf("ns_card_deal: empty reply")
	}
	switch reply[0] {
	case "FENCED":
		return nil, deal.ErrFenced
	case "NONE":
		return nil, nil
	case "DEALT":
	default:
		return nil, fmt.Errorf("ns_card_deal: %v", reply)
	}
	var out []deal.Reservation
	for i := 1; i+3 < len(reply); i += 4 {
		a, _ := strconv.Atoi(reply[i+2])
		out = append(out, deal.Reservation{Card: byKey[reply[i]+"/"+reply[i+1]], Bench: bench, Attempt: a, Token: reply[i+3]})
	}
	return out, nil
}

// Unreserve returns a batch dealt -> queued in one call.
func (d *DealFunctions) Unreserve(ctx context.Context, fence, bench string, res []deal.Reservation, reason string) error {
	args := []any{bench, fence, reason, d.actor(), ""}
	for _, r := range res {
		args = append(args, r.Card.Sprint, r.Card.Label, strconv.Itoa(r.Attempt))
	}
	return d.call(ctx, "ns_card_undeal", "UNDEALT", args...)
}

// SSH writes the bench's ssh cell, bench:<b>:ssh.
func (d *DealFunctions) SSH(ctx context.Context, fence, bench, state, why string) error {
	return d.call(ctx, "ns_bench_ssh", "OK", bench, fence, state, why)
}

// Gate writes one sprint's DEPENDS-ON moves in one ns_card_gate call.
func (d *DealFunctions) Gate(ctx context.Context, fence, sprint string, moves []deal.GateMove) error {
	args := []any{fence, sprint, d.actor(), ""}
	for _, m := range moves {
		args = append(args, m.Label, m.Verb, m.Why)
	}
	return d.call(ctx, "ns_card_gate", "GATED", args...)
}

func (d *DealFunctions) call(ctx context.Context, name, ok string, args ...any) error {
	reply, err := d.Client.FCall(ctx, name, nil, args...).StringSlice()
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	switch {
	case len(reply) > 0 && reply[0] == "FENCED":
		return deal.ErrFenced
	case len(reply) > 0 && (reply[0] == ok || reply[0] == "NONE"):
		return nil
	}
	return fmt.Errorf("%s: %v", name, reply)
}

func (d *DealFunctions) actor() string {
	if d.Actor != "" {
		return d.Actor
	}
	return DefaultActor
}

func tokenSHA(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

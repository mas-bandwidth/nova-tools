package reconcile

// This file: the progress duty (nova-tools #4319). Glenn 2026-09-26 ~11:15 AM
// ET: "how can we make sure that when there are tasks in the 'ready' per-work
// stream, that the coordinator will INEVITABLY push these through the system
// until they are all complete, OR realize there is a problem, and stop work,
// and ask for help. This last one should be rare, and only if basically, the
// sprint is not CONVERGING and something needs to be fixed."
//
// Convergence is measured, never judged. Per stream, once per cfg:progress
// every_s (default 10 s):
//
//	left     = waiting + ready + working + review + merging (not landed)
//	delta    = left now minus left at the start of the current window
//	room     = some consumer is live, not down, not paused and has a free slot
//	in play  = no pit stop holds the stream, and working > 0 or (ready > 0 and room)
//
// A stream is converging while it is in play and inside the window since it
// last fell or since it came into play; idle when nothing is in play
// (nothing working and nothing ready a worker has room for, or a pit stop
// holds it); stalled when it has been in play for the whole window (default
// 30 min) without left falling once. A card that churns working -> ready ->
// working never falls, so it keeps the stream in play and stalls it (the
// cold read of #4361: counting only ready read that churn as idle). Every
// run prints, per stream whose numbers changed:
//
//	PROGRESS <stream> left=<n> delta=<n> ready=<n> landed_h=<n> retries_h=<n> oldest=<d> blocked=<d> window=<d> status=converging|idle|stalled
//
// landed_h and retries_h are ws:log moves of the last hour (to=landed; from
// working back to ready or waiting), oldest is the age of the oldest card not
// landed, blocked is how long the stream has been in play with no fall.
//
// The one ask path, rare: a stalled stream, a duty refusal repeating past
// cfg:progress refusals passes (default 20), or a release probe failing
// twice (the Probes seam; the release rung is #4319 item 2, another card).
// The system then sets the sprint's pit stop with the diagnosis as its why
// (PROGRESS STALLED <stream> <cause ...numbers>), scoped to that stream for a
// stall so nothing else stops, prints one EVENT line, records it on
// proc:progress for the table's EVENTS line, and sends one wake note to the
// cfg:progress ask list (default glenn,rowan) through friend:outbox, the
// zero-token outbox the bus relay reads. An episode asks once: a stall asks
// again only after the stream fell or was lifted and stalled anew; a refusal
// shape asks once per text. Every refusal of the duty's own (no open sprint,
// a stop already set, a wake that did not write) prints as a PROGRESS
// REFUSED line and counts on the DUTY line (Counts.Refused). proc:progress
// refused is the other duties' refusals of the last pass plus this run's
// own, each counted once.
//
// Cost: the every_s gate comes before any read, so a gated pass is no round
// trip and a run is three (the index, the measurement, the record), plus a
// pit stop read when no pass put the holds on ctx and the stop and wake
// calls of an ask.
//
// State lives in proc:progress:<stream> (left, ref_left, ref_at, fell_at,
// blocked_since, asked_at, status, at) so a restarted reconciler continues
// the same window, and in proc:progress (events, event, event_at, refused,
// asked:<duty>) which the table reads.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ProgressConfigKey is cfg:progress: window_s, refusals, every_s, ask.
const ProgressConfigKey = "cfg:progress"

// ProgressKey is the duty's record the table reads: events (asks so far),
// event (the last EVENT line), event_at, refused (the other duties'
// refusals of the last pass plus this run's own), asked:<duty> (the refusal
// text already asked for).
const ProgressKey = "proc:progress"

// ProgressStreamKey is one stream's progress state.
func ProgressStreamKey(stream string) string { return "proc:progress:" + stream }

// The defaults of cfg:progress.
const (
	DefaultProgressWindow   = 30 * time.Minute
	DefaultProgressRefusals = 20
	DefaultProgressEvery    = 10 * time.Second
	DefaultProgressAsk      = "glenn,rowan"
)

// ProgressBy is the by of the pit stop and the outbox note the duty writes.
const ProgressBy = "progress"

// ProgressConfig is cfg:progress as read.
type ProgressConfig struct {
	Window   time.Duration // window_s: no fall for this long while pushable is a stall
	Refusals int           // refusals: a duty error repeating this many passes asks
	Every    time.Duration // every_s: the duty's cadence
	Ask      []string      // ask: who the wake note goes to
}

// ParseProgressConfig reads an HGETALL of cfg:progress; a missing,
// non-numeric or non-positive field keeps its default.
func ParseProgressConfig(h map[string]string) ProgressConfig {
	cfg := ProgressConfig{Window: DefaultProgressWindow, Refusals: DefaultProgressRefusals, Every: DefaultProgressEvery}
	secs := func(field string, into *time.Duration) {
		if n, err := strconv.ParseInt(strings.TrimSpace(h[field]), 10, 64); err == nil && n > 0 {
			*into = time.Duration(n) * time.Second
		}
	}
	secs("window_s", &cfg.Window)
	secs("every_s", &cfg.Every)
	if n, err := strconv.Atoi(strings.TrimSpace(h["refusals"])); err == nil && n > 0 {
		cfg.Refusals = n
	}
	ask := strings.TrimSpace(h["ask"])
	if ask == "" {
		ask = DefaultProgressAsk
	}
	for _, a := range strings.Split(ask, ",") {
		if a = strings.TrimSpace(a); a != "" {
			cfg.Ask = append(cfg.Ask, a)
		}
	}
	return cfg
}

// Sample is one stream's measurement at one run.
type Sample struct {
	Stream                                           string
	Waiting, Ready, Working, Review, Merging, Landed int64
	LandedHour                                       int   // ws:log to=landed in the last hour
	RetriesHour                                      int   // ws:log working -> ready|waiting in the last hour
	OldestAt                                         int64 // created_at (ms) of the oldest card not landed; 0 none
	Held                                             bool  // a pit stop holds the stream
	// LandStalled is land:slow:<stream> stalled=1 (the land watch, #4324):
	// the stream's landing is past cfg:land wall. It is a stall whatever
	// the counts say.
	LandStalled bool
}

// Left is the cards not landed.
func (s Sample) Left() int64 { return s.Waiting + s.Ready + s.Working + s.Review + s.Merging }

// The statuses.
const (
	StatusConverging = "converging"
	StatusIdle       = "idle"
	StatusStalled    = "stalled"
)

// State is a stream's progress state between runs (proc:progress:<stream>).
// Times are ms on the duty's clock; a zero At is no state yet.
type State struct {
	Left         int64
	RefLeft      int64 // left at the start of the current window
	RefAt        int64
	FellAt       int64 // the last run at which left fell (the first run when none)
	BlockedSince int64 // since when the stream has been in play with no fall; 0 not blocked
	AskedAt      int64 // the BlockedSince already asked for
	Status       string
	At           int64
}

// Delta is left now minus left at the window's start.
func (st State) Delta() int64 { return st.Left - st.RefLeft }

// Blocked is how long the stream has been in play with no fall at now.
func (st State) Blocked(now time.Time) time.Duration {
	if st.BlockedSince == 0 {
		return 0
	}
	return time.Duration(now.UnixMilli()-st.BlockedSince) * time.Millisecond
}

// NeedsAsk is a stalled episode not yet asked for.
func (st State) NeedsAsk() bool {
	return st.Status == StatusStalled && st.AskedAt != st.BlockedSince
}

// Step is the one measurement: prev at the last run, s and room now.
func Step(prev State, s Sample, room bool, now time.Time, window time.Duration) State {
	ms := now.UnixMilli()
	st := State{Left: s.Left(), At: ms, AskedAt: prev.AskedAt}
	fell := false
	if prev.At == 0 {
		st.RefLeft, st.RefAt, st.FellAt = st.Left, ms, ms
	} else {
		st.RefLeft, st.RefAt, st.FellAt = prev.RefLeft, prev.RefAt, prev.FellAt
		if st.Left < prev.Left {
			st.FellAt, fell = ms, true
		}
		if ms-st.RefAt >= window.Milliseconds() {
			st.RefLeft, st.RefAt = st.Left, ms
		}
	}
	// In play: a worker holds a card, or a ready card has room. Working
	// counts so a card cycling working -> ready -> working with no fall
	// keeps the blocked clock running; only a fall or leaving play (a stop,
	// nothing working and nothing ready with room) resets it.
	inPlay := !s.Held && (s.Working > 0 || (s.Ready > 0 && room))
	switch {
	case !inPlay || fell:
		st.BlockedSince = 0
	case prev.BlockedSince != 0:
		st.BlockedSince = prev.BlockedSince
	default:
		st.BlockedSince = ms
	}
	switch {
	case s.LandStalled:
		// The landing is past its wall: stalled now, one ask per episode
		// (blocked_since is the episode key, kept across runs).
		switch {
		case st.BlockedSince != 0:
		case prev.BlockedSince != 0:
			st.BlockedSince = prev.BlockedSince
		default:
			st.BlockedSince = ms
		}
		st.Status = StatusStalled
	case !inPlay:
		st.Status = StatusIdle
	case st.BlockedSince != 0 && ms-st.BlockedSince >= window.Milliseconds():
		st.Status = StatusStalled
	default:
		st.Status = StatusConverging
	}
	return st
}

// ProgressLine is the stream's PROGRESS line at now.
func ProgressLine(s Sample, st State, now time.Time, window time.Duration) string {
	oldest := time.Duration(0)
	if s.OldestAt > 0 {
		oldest = time.Duration(now.UnixMilli()-s.OldestAt) * time.Millisecond
	}
	return fmt.Sprintf("PROGRESS %s left=%d delta=%d ready=%d landed_h=%d retries_h=%d oldest=%s blocked=%s window=%s status=%s",
		oneline.Field(s.Stream), st.Left, st.Delta(), s.Ready, s.LandedHour, s.RetriesHour,
		secs(oldest), secs(st.Blocked(now)), secs(window), st.Status)
}

// progressKey is the part of the line that decides whether it prints again:
// everything but the two ages, which move every run.
func progressKey(s Sample, st State) string {
	return fmt.Sprintf("%s %d %d %d %d %d %s", s.Stream, st.Left, st.Delta(), s.Ready, s.LandedHour, s.RetriesHour, st.Status)
}

func secs(d time.Duration) string { return d.Truncate(time.Second).String() }

// Repeat is a duty error the loop has seen unchanged for Passes passes in a
// row (the reconcile verb's namedDuties count them).
type Repeat struct {
	Duty   string
	Text   string
	Passes int
}

// Ask is one stop-and-ask: the cause and its numbers. Stream is empty for a
// cause that is not one stream's (a repeating refusal, a failed release
// probe): the stop is then scope=all.
type Ask struct {
	Stream string
	Cause  string // stall | refusal | release-probe
	Detail string // the numbers, receipt words
	Key    string // what marks the episode asked (a stream's blocked_since, a duty's text)
}

// Why is the pit stop's why: PROGRESS STALLED <stream> <cause> <detail>.
func (a Ask) Why() string {
	who := a.Stream
	if who == "" {
		who = "sprint"
	}
	return fmt.Sprintf("PROGRESS STALLED %s %s %s", who, a.Cause, a.Detail)
}

// Event is the one EVENT line the table shows.
func (a Ask) Event(sprint string, at time.Time) string {
	return fmt.Sprintf("EVENT %s sprint=%s at=%d", a.Why(), oneline.Field(sprint), at.UnixMilli())
}

// Decide is the asks a run makes: every stalled episode not yet asked for,
// every duty refusal repeating past cfg.Refusals whose text has not been
// asked for (asked maps duty -> text), and a release probe failing twice.
// Stream order, then duty order.
func Decide(samples []Sample, states map[string]State, repeats []Repeat, asked map[string]string,
	probeFails int, probeDetail string, cfg ProgressConfig, now time.Time) []Ask {
	var out []Ask
	for _, s := range samples {
		st := states[s.Stream]
		if !st.NeedsAsk() {
			continue
		}
		out = append(out, Ask{Stream: s.Stream, Cause: "stall",
			Detail: fmt.Sprintf("blocked=%s window=%s left=%d ready=%d landed_h=%d retries_h=%d",
				secs(st.Blocked(now)), secs(cfg.Window), st.Left, s.Ready, s.LandedHour, s.RetriesHour),
			Key: strconv.FormatInt(st.BlockedSince, 10)})
	}
	rs := append([]Repeat(nil), repeats...)
	sort.Slice(rs, func(i, j int) bool { return rs[i].Duty < rs[j].Duty })
	for _, r := range rs {
		if r.Passes < cfg.Refusals || r.Text == "" || asked[r.Duty] == r.Text {
			continue
		}
		out = append(out, Ask{Cause: "refusal",
			Detail: fmt.Sprintf("duty=%s repeats=%d limit=%d text=%s", oneline.Field(r.Duty), r.Passes, cfg.Refusals, oneline.Quote(r.Text)),
			Key:    r.Duty + "=" + r.Text})
	}
	if probeFails >= 2 && asked["release-probe"] != probeDetail {
		out = append(out, Ask{Cause: "release-probe",
			Detail: fmt.Sprintf("fails=%d limit=2 detail=%s", probeFails, oneline.Quote(probeDetail)),
			Key:    "release-probe=" + probeDetail})
	}
	return out
}

// Progress is the progress duty. Client is the sprint store; Out receives
// the PROGRESS, EVENT and PROGRESS REFUSED lines (nil discards them).
type Progress struct {
	Client redis.Cmdable
	Out    io.Writer
	// Now is the duty's clock; nil is the wall clock. A test injects it.
	Now func() time.Time
	// Stop sets the sprint's pit stop; nil is pitstop.Set with by=progress.
	Stop func(ctx context.Context, sprint, why, idem string, streams ...string) (pitstop.Result, error)
	// Wake sends the note to one friend; nil writes friend:outbox kind=notice.
	Wake func(ctx context.Context, to, note, idem string, at time.Time) error
	// Probes is the release rung's probe count (fails in a row, detail);
	// nil is none. #4319 item 2's release rung plugs in here.
	Probes func() (fails int, detail string)
	// Every overrides cfg:progress every_s (tests).
	Every time.Duration

	mu      sync.Mutex
	last    time.Time
	every   time.Duration     // the cadence the last run read (cfg:progress every_s)
	printed map[string]string // stream -> the key of the line last printed
	own     int               // this duty's refusals in the current pass
	refused int               // the other duties' refusals of the last pass (NotePass)
	repeats []Repeat          // the duty errors repeating (NotePass)
	// Last is the most recent run (tests and the receipt).
	Last ProgressRun
}

// ProgressRun is one run: what it measured and asked.
type ProgressRun struct {
	Sprint  string
	Room    bool
	Samples []Sample
	States  map[string]State
	Asks    []Ask
	Lines   []string
	Refused int
}

// NotePass is the loop's word after each pass: the pass's refusal sum over
// every duty and the duty errors repeating unchanged, for the next run's
// refusal trigger. The sum includes this duty's own refusals of that pass,
// which the next run's record already carries, so they are taken out here:
// each refusal counts once on proc:progress.
func (p *Progress) NotePass(refused int, repeats []Repeat) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refused = max(refused-p.own, 0)
	p.own = 0
	p.repeats = append(p.repeats[:0], repeats...)
}

func (p *Progress) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Run measures every stream of ws:order once per cfg:progress every_s.
func (p *Progress) Run(ctx context.Context, l *Lease) (Counts, error) {
	if p == nil || p.Client == nil || l == nil {
		return Counts{}, errors.New("progress: no store or no lease")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	// The gate before any read: a gated pass is no round trip. The cadence
	// is the one the last run read; a changed every_s applies from the next
	// run.
	every := p.Every
	if every <= 0 {
		every = p.every
	}
	if !p.last.IsZero() && now.Sub(p.last) < every {
		return Counts{}, nil
	}
	cfg, streams, sprints, roster, top, err := p.readIndex(ctx)
	if err != nil {
		return Counts{}, err
	}
	p.last, p.every = now, cfg.Every
	run, err := p.measure(ctx, now, cfg, streams, sprints, roster)
	if err != nil {
		return Counts{}, err
	}
	asked := map[string]string{}
	for f, v := range top {
		if d, ok := strings.CutPrefix(f, "asked:"); ok {
			asked[d] = v
		}
	}
	fails, detail := 0, ""
	if p.Probes != nil {
		fails, detail = p.Probes()
	}
	run.Asks = Decide(run.Samples, run.States, p.repeats, asked, fails, detail, cfg, now)
	if p.printed == nil {
		p.printed = map[string]string{}
	}
	for _, s := range run.Samples {
		st := run.States[s.Stream]
		key := progressKey(s, st)
		if p.printed[s.Stream] == key {
			continue
		}
		p.printed[s.Stream] = key
		run.Lines = append(run.Lines, ProgressLine(s, st, now, cfg.Window))
	}
	refused, err := p.act(ctx, &run, cfg, now)
	run.Refused = refused
	p.own = refused
	for _, line := range run.Lines {
		p.print(line)
	}
	p.Last = run
	return Counts{Refused: refused}, err
}

func (p *Progress) print(line string) {
	if p.Out != nil {
		fmt.Fprintln(p.Out, line)
	}
}

// readIndex is the first round trip: the config, the streams, the sprints,
// the consumer roster and the duty's own record.
func (p *Progress) readIndex(ctx context.Context) (cfg ProgressConfig, streams, sprints []string, roster []taskcard.Consumer, top map[string]string, err error) {
	pipe := p.Client.Pipeline()
	cfgCmd := pipe.HGetAll(ctx, ProgressConfigKey)
	order := pipe.ZRange(ctx, "ws:order", 0, -1)
	sprintOrder := pipe.ZRange(ctx, "sprint:order", 0, -1)
	consumers := pipe.SMembers(ctx, taskcard.ConsumersKey)
	topCmd := pipe.HGetAll(ctx, ProgressKey)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return cfg, nil, nil, nil, nil, fmt.Errorf("progress: index: %w", err)
	}
	cfg = ParseProgressConfig(cfgCmd.Val())
	names := append([]string(nil), consumers.Val()...)
	sort.Strings(names)
	for _, n := range names {
		if k, err := taskcard.ParseConsumer(n); err == nil {
			roster = append(roster, k)
		}
	}
	return cfg, order.Val(), sprintOrder.Val(), roster, topCmd.Val(), nil
}

// measure is the second round trip and the judgement: every stream's counts,
// oldest card and stored state, every consumer's room, the open sprint, the
// last hour of ws:log, then Step per stream.
func (p *Progress) measure(ctx context.Context, now time.Time, cfg ProgressConfig, streams, sprints []string, roster []taskcard.Consumer) (ProgressRun, error) {
	holds, err := pitstop.Current(ctx, p.Client)
	if err != nil {
		return ProgressRun{}, fmt.Errorf("progress: pitstop: %w", err)
	}
	notLanded := []string{ws.Waiting, ws.Ready, ws.Working, ws.Review, ws.Merging}
	type streamCmds struct {
		counts  [6]*ws.CardCountCmd
		oldest  [5]*redis.ZSliceCmd
		state   *redis.MapStringStringCmd
		stalled *redis.StringCmd // land:slow:<stream> stalled (the land watch)
	}
	type consumerCmds struct {
		desired *redis.SliceCmd
		working *redis.IntCmd
		down    *redis.IntCmd
		at, ci  *redis.StringCmd
	}
	pipe := p.Client.Pipeline()
	scs := make([]streamCmds, len(streams))
	for i, s := range streams {
		// the counts and the oldest card leave the stream's stop out (#4318):
		// a set's two oldest, so the sentinel beside a card never hides it
		for j, state := range ws.Stream {
			scs[i].counts[j] = ws.QueueCardCount(ctx, pipe, s, state)
		}
		for j, state := range notLanded {
			scs[i].oldest[j] = pipe.ZRangeWithScores(ctx, taskcard.StreamKey(s, state), 0, 1)
		}
		scs[i].state = pipe.HGetAll(ctx, ProgressStreamKey(s))
		scs[i].stalled = pipe.HGet(ctx, LandSlowKey(s), "stalled")
	}
	ccs := make([]consumerCmds, len(roster))
	for i, k := range roster {
		ccs[i] = consumerCmds{
			desired: pipe.HMGet(ctx, k.DesiredKey(), "slots", "paused"),
			working: pipe.ZCard(ctx, k.Key(ws.Working)),
			down:    pipe.Exists(ctx, k.DownKey()),
			at:      pipe.HGet(ctx, k.BeatKey(), "at"),
			ci:      pipe.HGet(ctx, k.MachineBeatKey(), "ci"),
		}
	}
	status := make([]*redis.StringCmd, len(sprints))
	for i, s := range sprints {
		status[i] = pipe.HGet(ctx, "s:"+s, "status")
	}
	hourAgo := now.Add(-time.Hour).UnixMilli()
	log := pipe.XRangeN(ctx, "ws:log", strconv.FormatInt(hourAgo, 10), "+", progressLogMax)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return ProgressRun{}, fmt.Errorf("progress: measure: %w", err)
	}
	run := ProgressRun{States: map[string]State{}}
	for i, s := range sprints {
		// The table's rule: "closed" is out, anything else (no status
		// included) is the open sprint whose stop the ask sets.
		if v, err := status[i].Result(); errors.Is(err, redis.Nil) || (err == nil && v != "closed") {
			run.Sprint = s
			break
		}
	}
	for i := range roster {
		// Room as the deal pass measures it (taskcard.DealPass): live by
		// beat, not down, not paused, slots above the CI legs running on
		// the consumer's machine (its beat's ci, nova-tools#4293) plus
		// working.
		d := ccs[i].desired.Val()
		slots, err := strconv.ParseInt(fmt.Sprint(valueAt(d, 0)), 10, 64)
		if err != nil {
			continue
		}
		t, ok := taskcard.BeatAt(ccs[i].at.Val())
		live := ok && now.Sub(t) < taskcard.Live && t.Sub(now) < taskcard.Live
		ci, _ := strconv.ParseInt(ccs[i].ci.Val(), 10, 64)
		if live && ccs[i].down.Val() == 0 && fmt.Sprint(valueAt(d, 1)) != "1" && taskcard.FreeSlots(slots, ccs[i].working.Val(), ci) > 0 {
			run.Room = true
		}
	}
	landed := map[string]int{}
	retries := map[string]int{}
	if msgs, err := log.Result(); err == nil || errors.Is(err, redis.Nil) {
		for _, m := range msgs {
			stream, _ := m.Values["stream"].(string)
			from, _ := m.Values["from"].(string)
			to, _ := m.Values["to"].(string)
			id, _ := m.Values["id"].(string)
			switch {
			case ws.IsSentinel(id): // the stream's stop is not a card
			case to == ws.Landed:
				landed[stream]++
			case from == ws.Working && (to == ws.Ready || to == ws.Waiting):
				retries[stream]++
			}
		}
	} else {
		return ProgressRun{}, fmt.Errorf("progress: ws:log: %w", err)
	}
	// TODO(#4369): take the stream sentinel out of left and the oldest age through the shared "counts less the sentinel" helper once it lands on dev.
	for i, s := range streams {
		smp := Sample{Stream: s, LandedHour: landed[s], RetriesHour: retries[s]}
		cells := []*int64{&smp.Waiting, &smp.Ready, &smp.Working, &smp.Review, &smp.Merging, &smp.Landed}
		for j, c := range scs[i].counts {
			*cells[j] = c.Val()
		}
		for _, z := range scs[i].oldest {
			for _, m := range z.Val() {
				if id, _ := m.Member.(string); ws.IsSentinel(id) {
					continue
				}
				if smp.OldestAt == 0 || int64(m.Score) < smp.OldestAt {
					smp.OldestAt = int64(m.Score)
				}
				break
			}
		}
		_, smp.Held = holds.Stream(s)
		smp.LandStalled = scs[i].stalled.Val() == "1"
		run.Samples = append(run.Samples, smp)
		run.States[s] = Step(stateFromHash(scs[i].state.Val()), smp, run.Room, now, cfg.Window)
	}
	return run, nil
}

// progressLogMax caps the ws:log window read per run.
const progressLogMax = 20000

func valueAt(v []any, i int) any {
	if i < len(v) {
		return v[i]
	}
	return nil
}

func stateFromHash(h map[string]string) State {
	n := func(f string) int64 { v, _ := strconv.ParseInt(h[f], 10, 64); return v }
	return State{Left: n("left"), RefLeft: n("ref_left"), RefAt: n("ref_at"), FellAt: n("fell_at"),
		BlockedSince: n("blocked_since"), AskedAt: n("asked_at"), Status: h["status"], At: n("at")}
}

func (st State) fields() []any {
	return []any{"left", st.Left, "ref_left", st.RefLeft, "ref_at", st.RefAt, "fell_at", st.FellAt,
		"blocked_since", st.BlockedSince, "asked_at", st.AskedAt, "status", st.Status, "at", st.At}
}

// act takes every ask (the stop, the EVENT line, the wake notes), then
// writes every stream's state and the duty's record in one round trip. It
// returns the refusals, each printed.
func (p *Progress) act(ctx context.Context, run *ProgressRun, cfg ProgressConfig, now time.Time) (int, error) {
	refused := 0
	refuse := func(what, why string) {
		refused++
		run.Lines = append(run.Lines, fmt.Sprintf("PROGRESS REFUSED %s why=%s", what, oneline.Quote(why)))
	}
	pipe := p.Client.Pipeline()
	for _, a := range run.Asks {
		idem := ProgressBy + ":" + a.Key
		if run.Sprint == "" {
			refuse("ask "+a.Cause, "no open sprint in sprint:order to stop")
		} else {
			stop := p.Stop
			if stop == nil {
				stop = func(ctx context.Context, sprint, why, idem string, streams ...string) (pitstop.Result, error) {
					return pitstop.Set(ctx, p.Client, sprint, ProgressBy, why, false, idem, streams...)
				}
			}
			var scope []string
			if a.Stream != "" {
				scope = []string{a.Stream}
			}
			switch r, err := stop(ctx, run.Sprint, a.Why(), idem, scope...); {
			case err != nil:
				refuse("pitstop set sprint="+oneline.Field(run.Sprint), err.Error())
			case r.Outcome == pitstop.Unknown:
				refuse("pitstop set sprint="+oneline.Field(run.Sprint), "sprint unknown (no s:"+run.Sprint+" status)")
			case r.Outcome == pitstop.Exists:
				refuse("pitstop set sprint="+oneline.Field(run.Sprint), fmt.Sprintf("already stopped by=%s why=%s", r.Prior.By, oneline.Quote(r.Prior.Why)))
			}
		}
		event := a.Event(run.Sprint, now)
		run.Lines = append(run.Lines, event)
		pipe.HIncrBy(ctx, ProgressKey, "events", 1)
		pipe.HSet(ctx, ProgressKey, "event", event, "event_at", now.UnixMilli())
		wake := p.Wake
		if wake == nil {
			wake = p.outbox
		}
		for _, to := range cfg.Ask {
			if err := wake(ctx, to, event, idem, now); err != nil {
				refuse("wake to="+oneline.Field(to), err.Error())
			}
		}
		// The episode is asked, refused or not: the human at the stop has
		// the EVENT; asking every run would be noise.
		if a.Stream != "" {
			st := run.States[a.Stream]
			st.AskedAt = st.BlockedSince
			run.States[a.Stream] = st
		} else {
			duty, text, _ := strings.Cut(a.Key, "=")
			pipe.HSet(ctx, ProgressKey, "asked:"+duty, text)
		}
	}
	for _, s := range run.Samples {
		pipe.HSet(ctx, ProgressStreamKey(s.Stream), run.States[s.Stream].fields()...)
	}
	pipe.HSet(ctx, ProgressKey, "refused", p.refused+refused, "at", now.UnixMilli())
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return refused, fmt.Errorf("progress: record: %w", err)
	}
	return refused, nil
}

// outbox is the default Wake: one friend:outbox entry, kind=notice, the
// shape the rung actions write (fn/lua/friend.lua fs_outbox) so the same
// relay carries it.
func (p *Progress) outbox(ctx context.Context, to, note, idem string, at time.Time) error {
	return p.Client.XAdd(ctx, &redis.XAddArgs{Stream: friend.OutboxKey, MaxLen: 100000, Approx: true, Values: []any{
		"friend", to, "kind", "notice", "rung", "0", "channel", "bus:To:" + to,
		"open", "0", "detail", note, "actor", ProgressBy, "idem", idem + ":" + to, "at", strconv.FormatInt(at.UnixMilli(), 10),
	}}).Err()
}

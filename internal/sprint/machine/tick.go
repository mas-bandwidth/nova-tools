// Package machine is nova-sprint's tick loop, layer 6 of the upper design
// (EVENT-DRIVEN-TICK version 2.1, section 1.4, with its errata; item IT17;
// the model is tla/SprintEvents.tla, its actions RT1, ReadEvents, RT2,
// PlanOrLook, Apply and ParkOnBug).
//
// One tick is at most three round trips, one when idle (1.4.2):
//
//	RT1  the lease step (take or renew, the last tick's heartbeat fields, the
//	     pop), the error step of the last tick's refusals, the page of lines
//	     after the cursor, and one atomic read: the clock and R, the cursor,
//	     the heads of the agenda and the held queue, the due count, the
//	     dropping marks' count, the parked keys the loop knows, and Layer 2's
//	     last. Held by another loop: done.
//	Go   events to keys; dispatch: each rule's keys cut to its read.
//	RT2  the ingest step, and one read for each rule with keys.
//	Go   each rule plans once over all its keys, on its own read; the builder
//	     cuts each plan into requests; the budget deals them round robin.
//	RT3  the steps, in one flush: the quarantine step first, then the rules'.
//
// Every write carries the lease generation (T1): the lease step decides it,
// and every later step of the tick names the one it decided. While STOPPED a
// tick is RT1 alone, and every StoppedLookEvery it looks (1.4.5, stopped.go).
package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/stepbuild"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The loop's own spans (1.4.2).
const (
	// TickEvery is the time between two ticks.
	TickEvery = time.Second
	// LeaseHold is how long a take or a renewal holds the lease: past it a
	// loop that has stopped ticking loses the lease to another (1.1).
	LeaseHold = 10 * time.Second
	// assumedLineBytes is the bytes a line is taken to be before a page has
	// said: the first page's line limit is PageBytes over it.
	assumedLineBytes = 512
)

// RulesFieldMax is the most bytes of the heartbeat's rules field (1.4.1): the
// heartbeat's read reserves 8 KiB for it (IT30). A tick whose rules are longer
// writes the field empty.
const RulesFieldMax = 8192

// rulesField is the heartbeat's rules field of a tick (1.4.1): its rules'
// parts as JSON, or empty when that is over RulesFieldMax, so the field never
// shows an earlier tick's.
func rulesField(rules map[string]*RuleStat) string {
	b, err := json.Marshal(rules)
	if err != nil || len(b) > RulesFieldMax {
		return ""
	}
	return string(b)
}

// The judgment types the tick opens (2.2; 1.3.5).
const (
	TypeStepRefused = "the machine's step was refused"
	TypeInvariant   = "an invariant is broken"
)

// CodeNotCarried is the code a key is parked with when its rule's plan names
// what no wire or store check carries (NotCarried): the loop's own, not a
// store's refusal, and not a size, so the key is never halved (1.3.5).
const CodeNotCarried = "NOTCARRIED"

// Builder is the step builder of 8.0 (step.Build; 1.3.6): it turns one rule's
// plan into bodies, each an atomic request inside b, every unit whole. A plan
// it cuts removes no key: no body of a cut plan carries Done (the loop holds
// that to be so). The design's Build takes a Coster beside the bounds; the
// builder a loop is given carries its own.
type Builder func(rp sprint.RulePlan, m sprintfn.Meta, b stepbuild.Bounds) ([]sprintfn.Body, error)

// Config is one run loop's.
type Config struct {
	// Names is the deployment's.
	Names sprint.Names
	// Owner is a random token of this process and Name the loop's actor, for
	// display (1.1, {p}lease).
	Owner, Name string
	// Rules are the tick's rules; nil is sprint.RuleTable().
	Rules []sprint.Rule
	// Build is the step builder (8.0's step.Build); nil is StepBuilder, the
	// adapter to stepbuild.Build that serves the real rules.
	Build Builder
	// Budget is the tick's; the zero Budget is DefaultBudget().
	Budget Budget
	// TickEvery and LeaseHold are the loop's spans; zero is the design's.
	TickEvery, LeaseHold time.Duration
	// Look is R17 at a look (IT10's StoppedLook); nil sends no step of R17.
	Look StoppedLook
	// Claims are R14 phase 1's claims in an applied step of a rule (IT10):
	// the pushes phase 2 makes after RT3. Nil claims none.
	Claims func(rule string, s *sprint.Snapshot, rp sprint.RulePlan) []Claim
	// Deliver is how a push goes down a route; nil is the present build's.
	Deliver Deliver
	// Sleep waits between ticks (Run); nil is a timer. Now is the loop's own
	// clock for that wait; nil is time.Now. Nothing the loop writes is
	// stamped from it: every stamp is the store's (1.0, "The clock").
	Sleep func(ctx context.Context, d time.Duration) error
	Now   func() time.Time
}

// Loop is one run loop's memory between ticks: what it learned and what it
// owes. A loop that dies loses it, and the store keeps what it wrote
// (SprintEvents.tla, TickCrash).
type Loop struct {
	cfg    Config
	rules  []sprint.Rule
	byName map[string]sprint.Rule
	budget Budget

	epoch    tset.Decimal
	cur      tset.Decimal // the cursor the last ingest reply or read said
	curKnown bool
	gen      uint64 // the lease generation the last lease step decided; 0 when not held
	stopped  bool   // the clock as the last read said
	lastLook int64  // the store's wall ms of the last look while STOPPED
	lineB    int    // bytes a line of the last page

	halvings map[string]int    // key -> halvings after a LIMIT or a BUDGET (1.3.5, 1.3.6)
	heldBack map[string]int    // key -> the dropping marks when it was held back (1.3.5)
	heldSeq  map[string]uint64 // a held back key -> its order in the agenda
	parked   map[string]bool   // keys this loop parked, until a read shows them gone
	owed     errorStep         // the error step of the next RT1
	// unwoken is the lines addressed to the coordinator that ingests have
	// moved the cursor past and no tick-end line has covered, waiting for a
	// page that reaches Layer 2's last (tickend.go).
	unwoken int

	hb       map[string]string // the heartbeat fields of the last tick (A3)
	ticks    uint64
	failures int
	lastErr  string

	pusher *pusher
}

// errorStep is what the next RT1 writes in a step of notes and sprint keys
// only (1.3.5): the parked keys, the judgments of the refused steps, the
// quarantine of the cards refused steps named, and R14's phase 3 (2.3). Each
// key is owed once, each card once, and each (type, cause, subject) of a note
// once, the latest note winning: J refuses a step that names one twice
// (REQUEST), and the sprint part a key named twice. A key owed to the park is
// not planned again while it is owed (SprintEvents.tla ParkOnBug: the key
// leaves the agenda in the step that names the bug).
type errorStep struct {
	park       []sprintfn.ParkedKey
	notes      []sprint.NoteReq
	quarantine []sprint.Quarantined
	keys, ids  map[string]bool
	// wake is a tick-end note an earlier tick owes, of that count (errata 3
	// amendment 8; tickend.go): it rides the error step's first chunk, after
	// its other notes, and counts the addressed ones among them too.
	wake int
}

func (e *errorStep) empty() bool {
	return len(e.park)+len(e.notes)+len(e.quarantine) == 0 && e.wake == 0
}

// owes says the key is owed to the park.
func (e *errorStep) owes(key string) bool { return e.keys[key] }

// addPark owes a key to the park, once; it says whether the key is new.
func (e *errorStep) addPark(k sprintfn.ParkedKey) bool {
	if e.keys == nil {
		e.keys = map[string]bool{}
	}
	if e.keys[k.Key] {
		return false
	}
	e.keys[k.Key] = true
	e.park = append(e.park, k)
	return true
}

// addQuarantine owes a card's quarantine, once: the first record of a card
// stands (as the sprint part keeps it).
func (e *errorStep) addQuarantine(q sprint.Quarantined) bool {
	if e.ids == nil {
		e.ids = map[string]bool{}
	}
	if e.ids[q.ID] {
		return false
	}
	e.ids[q.ID] = true
	e.quarantine = append(e.quarantine, q)
	return true
}

// addNotes owes notes: a subject a note names takes it out of every owed note
// of the same type and cause, and a note left with no subject goes, so no
// (type, cause, subject) is named twice in one step (J's REQUEST).
func (e *errorStep) addNotes(ns ...sprint.NoteReq) {
	for _, n := range splitNotes(ns) {
		if len(n.Subjects) == 0 {
			continue
		}
		named := map[string]bool{}
		for _, s := range n.Subjects {
			named[s] = true
		}
		kept := e.notes[:0]
		for _, o := range e.notes {
			if o.Type == n.Type && o.Cause == n.Cause {
				var left []string
				for _, s := range o.Subjects {
					if !named[s] {
						left = append(left, s)
					}
				}
				if len(left) == 0 {
					continue
				}
				o.Subjects = left
			}
			kept = append(kept, o)
		}
		n.Subjects = append([]string(nil), n.Subjects...)
		e.notes = append(kept, n)
	}
}

// splitNotes cuts a note of more than 2,000 subjects into notes of 2,000, the
// most one note names (L2 1.2; J refuses more with LIMIT).
func splitNotes(ns []sprint.NoteReq) []sprint.NoteReq {
	var out []sprint.NoteReq
	for _, n := range ns {
		for len(n.Subjects) > tset.MaxIDsPerLine {
			piece := n
			piece.Subjects = n.Subjects[:tset.MaxIDsPerLine:tset.MaxIDsPerLine]
			out = append(out, piece)
			n.Subjects = n.Subjects[tset.MaxIDsPerLine:]
		}
		out = append(out, n)
	}
	return out
}

// errChunk is how much of the owed error step one request carries: the
// store's limits cut it (1.3.5), and what is left rides the next tick's RT1.
type errChunk struct{ park, quarantine, notes int }

// chunk is the most of the owed step one request carries: SprintKeysMax
// parked keys (1,000), QuarantineMax cards (2,000), and the notes that fit
// Layer 1's 100 notes and 4,000 about ids (L1 6), with the invariant notes of
// the quarantine counted in.
func (e *errorStep) chunk() errChunk {
	c := errChunk{park: min(len(e.park), sprintfn.SprintKeysMax), quarantine: min(len(e.quarantine), sprintfn.QuarantineMax)}
	return e.fitNotes(c)
}

// fitNotes sets the notes of a chunk: in order, while they fit beside the
// quarantine's own notes.
func (e *errorStep) fitNotes(c errChunk) errChunk {
	inv := invariantNotes(e.quarantine[:c.quarantine])
	notes, about := len(inv), c.quarantine
	if e.wake > 0 {
		notes, about = notes+1, about+1 // the tick-end note owed
	}
	c.notes = 0
	for _, n := range e.notes {
		if notes+1 > tset.MaxNotes || about+len(n.Subjects) > tset.MaxAboutBeforeDedup {
			break
		}
		notes, about = notes+1, about+len(n.Subjects)
		c.notes++
	}
	return c
}

// cutMark ends the text of a note that was cut to fit a step.
const cutMark = "... (cut)"

// cutText halves the text of the owed note i, keeping its head and ending it
// with cutMark, and says whether it cut anything: a text already down to the
// mark is left. Repeated, it brings any text under any byte limit above the
// note's other fields, in log2 of its length cuts.
func (e *errorStep) cutText(i int) bool {
	t := strings.TrimSuffix(e.notes[i].Text, cutMark)
	if len(t) == 0 {
		return false
	}
	keep := len(t) / 2
	for keep > 0 && !utf8.RuneStart(t[keep]) {
		keep-- // never cut inside a rune
	}
	e.notes[i].Text = t[:keep] + cutMark
	return true
}

// halve is a chunk of half as much, each part that had any keeping one.
func (c errChunk) halve() errChunk {
	h := func(n int) int {
		if n == 0 {
			return 0
		}
		return max(1, n/2)
	}
	return errChunk{park: h(c.park), quarantine: h(c.quarantine), notes: h(c.notes)}
}

// settle takes an applied chunk off what is owed.
func (e *errorStep) settle(c errChunk) {
	for _, k := range e.park[:c.park] {
		delete(e.keys, k.Key)
	}
	e.park = e.park[c.park:]
	e.quarantine = e.quarantine[c.quarantine:]
	e.notes = e.notes[c.notes:]
	e.wake = 0 // every chunk carries the tick-end owed
}

// NewLoop is a loop over a config: the design's spans, budget and step
// builder where the config leaves them zero. A config with no owner or no name
// is refused.
func NewLoop(cfg Config) (*Loop, error) {
	if cfg.Build == nil {
		cfg.Build = StepBuilder(cfg.Names.Prefix)
	}
	if cfg.Owner == "" || cfg.Name == "" {
		return nil, errors.New("machine: a loop needs an owner token and a name for the lease")
	}
	if cfg.TickEvery <= 0 {
		cfg.TickEvery = TickEvery
	}
	if cfg.LeaseHold <= 0 {
		cfg.LeaseHold = LeaseHold
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleep
	}
	rules := cfg.Rules
	if rules == nil {
		rules = sprint.RuleTable()
	}
	b := cfg.Budget
	if b == (Budget{}) {
		b = DefaultBudget()
	}
	l := &Loop{cfg: cfg, rules: rules, byName: map[string]sprint.Rule{}, budget: b, epoch: "0",
		halvings: map[string]int{}, heldBack: map[string]int{}, heldSeq: map[string]uint64{}, parked: map[string]bool{}, hb: map[string]string{},
		pusher: newPusher(cfg.Deliver)}
	for _, r := range rules {
		l.byName[r.Name] = r
	}
	return l, nil
}

// sleep waits d, or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
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

// Run ticks every TickEvery until ctx is done (1.4.2). A tick that fails is
// counted in the heartbeat (failures, error) and the loop goes on: the next
// tick plans fresh, and nothing resends a step (1.0).
func Run(ctx context.Context, c sprintfn.Client, cfg Config) error {
	l, err := NewLoop(cfg)
	if err != nil {
		return err
	}
	for {
		start := l.cfg.Now()
		_, _ = Tick(ctx, c, l)
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := l.cfg.Sleep(ctx, l.cfg.TickEvery-l.cfg.Now().Sub(start)); err != nil {
			return err
		}
	}
}

// Report is what one tick did.
type Report struct {
	// RoundTrips is the pipelines the tick flushed (1.4.2: at most three, one
	// when idle).
	RoundTrips int
	// Held says the loop holds the lease, at Gen; Running the clock as read.
	Held    bool
	Gen     uint64
	Running bool
	Epoch   tset.Decimal
	// Looked says a STOPPED loop looked this tick, and MovesDue what its dry
	// plans found (1.4.5).
	Looked, MovesDue bool
	// Lines are the lines ingested and Keys the keys they queued; Cur the
	// cursor after the tick; Backlog Layer 2's last less it; PageLimit the
	// next page's line limit (1.0, "Bytes").
	Lines, Keys int
	Cur         tset.Decimal
	Backlog     uint64
	PageLimit   int
	// Read are the keys each rule read this tick, and Left those the reads did
	// not fit (they stay queued); Unserved are keys no rule serves.
	Read, Left map[string]int
	Unserved   []string
	// Dealt is the rule of each step RT3 sent, in order; Changes,
	// EntriesNotes and RequestBytes what they cost (1.0, the tick budget).
	Dealt                               []string
	Changes, EntriesNotes, RequestBytes int
	// HeldChanges are the members R16's steps change, against its own budget
	// (Budget.HeldCards).
	HeldChanges int
	// Applied counts the steps that applied, Refused the refusals by code.
	Applied int
	Refused map[string]int
	// Quarantined are the ids quarantined (RT3's step), Parked the keys the
	// next error step parks, Halved the keys planned next tick at half, HeldBack
	// the keys held back for a drop.
	Quarantined, Parked, HeldBack []string
	Halved                        map[string]int
	// Short names each rule whose read came back short this tick, with what
	// its plan read and the read did not load: the plan is refused, its keys
	// parked and named in "the machine's step was refused", and the tick fails
	// naming it (Tick's error) once its other steps are sent (1.3.5).
	Short []string
	// Wake is the count of the tick-end note this tick wrote (errata 3
	// amendment 8): the items it addressed to the coordinator, 0 when it wrote
	// none. WakeLate is that of a tick-end an earlier tick owed, which this
	// tick's error step wrote.
	Wake, WakeLate int
	// Outcomes are the pushes of R14's phase 2 that finished since the last
	// tick, which this tick's error step records (phase 3); Pushes the pushes
	// this tick started, off the tick, and PushesSkipped the claims past
	// PushesInFlightMax.
	Outcomes              []Outcome
	Pushes, PushesSkipped int
	// Rules is each rule's part of the tick, which the heartbeat's rules field
	// carries (1.4.1).
	Rules map[string]*RuleStat
	// Err is what failed the tick.
	Err error
}

// RuleStat is one rule's tick (1.4.1, the heartbeat's rules): the keys it
// read and left, the steps dealt to it, how many applied, the members they
// change, and its refusals by code.
type RuleStat struct {
	Keys    int            `json:"keys"`
	Left    int            `json:"left,omitempty"`
	Steps   int            `json:"steps,omitempty"`
	Applied int            `json:"applied,omitempty"`
	Changes int            `json:"changes,omitempty"`
	Refused map[string]int `json:"refused,omitempty"`
}

// stat is a rule's RuleStat in the report.
func (r *Report) stat(rule string) *RuleStat {
	if r.Rules == nil {
		r.Rules = map[string]*RuleStat{}
	}
	st := r.Rules[rule]
	if st == nil {
		st = &RuleStat{}
		r.Rules[rule] = st
	}
	return st
}

// refused counts a refusal of a rule's read or step.
func (r *Report) refused(rule, code string) {
	r.Refused[code]++
	st := r.stat(rule)
	if st.Refused == nil {
		st.Refused = map[string]int{}
	}
	st.Refused[code]++
}

// leaseReply is the lease part's reply (IT16): who holds the lease.
type leaseReply struct {
	Held bool         `json:"held"`
	Took bool         `json:"took"`
	Gen  tset.Decimal `json:"gen"`
	Name string       `json:"name"`
}

// Tick runs one tick (1.4.2; SprintEvents.tla, RT1 to Apply).
func Tick(ctx context.Context, c sprintfn.Client, l *Loop) (Report, error) {
	rep := Report{Read: map[string]int{}, Left: map[string]int{}, Refused: map[string]int{}, Halved: map[string]int{}}
	err := l.tick(ctx, c, &rep)
	if err == nil && len(rep.Short) != 0 {
		err = fmt.Errorf("machine: a rule's read came back short, its plan refused: %s", strings.Join(rep.Short, "; "))
	}
	l.ticks++
	if err != nil {
		l.failures++
		l.lastErr = err.Error()
		rep.Err = err
	} else {
		l.failures, l.lastErr = 0, ""
	}
	l.hb["ticks"] = strconv.FormatUint(l.ticks, 10)
	l.hb["failures"] = strconv.Itoa(l.failures)
	l.hb["error"] = l.lastErr
	l.hb["rules"] = rulesField(rep.Rules)
	rep.Cur, rep.Epoch = l.cur, l.epoch
	for k, h := range l.halvings {
		rep.Halved[k] = h
	}
	for k := range l.heldBack {
		rep.HeldBack = append(rep.HeldBack, k)
	}
	sort.Strings(rep.HeldBack)
	return rep, err
}

// rt1 is RT1's items and where each is.
type rt1 struct {
	items                     []sprintfn.Item
	lease, errStep, page, get int
	errChunk                  errChunk // what the error step carries of what is owed
	errWake                   int      // the owed tick-end's count the error step carries, 0 none
	bands                     int      // the agenda head's ranges (agendaRanges)
	pageLimit                 int
	parkedAsked               []string // the parked keys the read names
}

// The atomic read of RT1 (1.4.2; errata 1's addendum: the sprint-key reads):
// the agenda head's ranges (Layer 1 range, raw key form, one or more), then
// the held queue's head, Layer 2's last and the work table's rows (the
// streams, which a rule's read names: sprint.TickShape; R6's front(s) of every
// stream, 2.3 R6 "Read:"); the sprint keys follow.
const (
	rtHeldQ      = iota // after the agenda's ranges: the held queue's head
	rtLast              // Layer 2's last
	rtRows              // the work table's rows: the streams
	rtAfterBands        // the Layer 1 queries after the bands
)

// agendaBandsMax is the most ranges RT1 reads of the agenda's head: with the
// held queue's, ten ranges of 2,000 are the read's 20,000 range ids (L1 6;
// 1.3.5: "RT1 reads 2,000 + h keys of the agenda's head, h at most 18,000").
const agendaBandsMax = 9
const (
	rsClock = iota
	rsTick
	rsDue
	rsDropping
	rsParked
)

func (l *Loop) rt1() rt1 {
	b := l.budget
	r := rt1{errStep: -1, page: -1}
	lease := &sprintfn.Request{Epoch: l.epoch, Meta: sprintfn.Meta{Rule: "tick", Tick: true},
		Lease: &sprintfn.LeasePart{Owner: l.cfg.Owner, Name: l.cfg.Name, HoldMS: l.cfg.LeaseHold.Milliseconds(),
			Heartbeat: l.heartbeat(), Stopped: l.stopped},
		Pop: &sprintfn.PopPart{Limit: b.Pop}}
	r.items = append(r.items, sprintfn.Item{Step: lease})
	if !l.owed.empty() && l.gen != 0 {
		r.errStep = len(r.items)
		req, c := l.errorRequest()
		r.errChunk = c
		r.errWake = tickEndOf(req)
		r.items = append(r.items, sprintfn.Item{Step: req})
	}
	if l.curKnown {
		r.pageLimit = l.pageLimit()
		r.page = len(r.items)
		r.items = append(r.items, sprintfn.Item{Page: &tset.ReadPlan{Epoch: l.epoch, Mode: "page",
			Queries: []tset.ReadQuery{{Kind: "lines", AfterSeq: l.cur, Limit: r.pageLimit, IDsLimit: b.PageIDs}}}})
	}
	parked := make([]string, 0, len(l.parked))
	for k := range l.parked {
		parked = append(parked, k)
	}
	sort.Strings(parked)
	r.parkedAsked = parked
	var sq []sprintfn.SprintQuery
	for _, q := range []sprintfn.KeyQ{{Kind: sprintfn.KeyClock}, {Kind: sprintfn.KeyTick}, {Kind: sprintfn.KeyDueCount},
		{Kind: sprintfn.KeyDropping, Streams: []string{}}, {Kind: sprintfn.KeyParked, Keys: parked}} {
		w, _ := sprintfn.EncodeKeyQ(q) // each is a fixed shape, and the parked keys are keys this loop parked
		sq = append(sq, w)
	}
	r.get = len(r.items)
	ranges := l.agendaRanges()
	r.bands = len(ranges)
	r.items = append(r.items, sprintfn.Item{Read: &sprintfn.ReadRequest{Epoch: l.epoch, Tset: append(ranges,
		tset.ReadQuery{Kind: "range", Key: l.cfg.Names.Key("heldq") + "@" + string(l.epoch), Min: "-inf", Max: "+inf", Limit: b.HeldHead},
		tset.ReadQuery{Kind: "last"},
		tset.ReadQuery{Kind: "rows", Table: sprint.Work},
	), Sprint: sq}})
	return r
}

// agendaRanges are RT1's ranges of the agenda's head (1.3.5: "so that they
// never hide older keys, RT1 reads 2,000 + h keys of the agenda's head, h
// being the keys the loop holds back"). Layer 1 bounds one range at 2,000 and
// has no offset, so the head is read in bands cut at the orders of the held
// back keys: [-inf, s1], (s1, s2], ..., (sn, +inf], each of AgendaHead keys,
// at most agendaBandsMax of them. With no key held back it is one range.
// parseRT1 keeps the bands up to the first that was cut, so the keys it gives
// are always the agenda's true head: the held back keys sit at the ends of
// the bands, and a band that is cut holds AgendaHead keys ahead of them.
func (l *Loop) agendaRanges() []tset.ReadQuery {
	key := l.cfg.Names.Key("agenda") + "@" + string(l.epoch)
	seen := map[uint64]bool{}
	var cuts []uint64
	for _, s := range l.heldSeq {
		if !seen[s] {
			seen[s] = true
			cuts = append(cuts, s)
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i] < cuts[j] })
	cuts = cuts[:min(len(cuts), agendaBandsMax-1)]
	var out []tset.ReadQuery
	lo := "-inf"
	for _, c := range cuts {
		out = append(out, tset.ReadQuery{Kind: "range", Key: key, Min: lo, Max: string(decimal(c)), Limit: l.budget.AgendaHead})
		lo = "(" + string(decimal(c))
	}
	return append(out, tset.ReadQuery{Kind: "range", Key: key, Min: lo, Max: "+inf", Limit: l.budget.AgendaHead})
}

// pageLimit is the next page's line limit: the page's bytes over the last
// page's bytes a line, at least one line and at most PageLines (1.0, "Bytes":
// lines takes no byte limit, so the limit is set to expect at most 2 MiB).
func (l *Loop) pageLimit() int {
	per := l.lineB
	if per <= 0 {
		per = assumedLineBytes
	}
	return max(1, min(l.budget.PageLines, l.budget.PageBytes/per))
}

// heartbeat is the last tick's fields, which the lease part writes by field
// (1.4.1, A3).
func (l *Loop) heartbeat() map[string]string {
	out := map[string]string{}
	for k, v := range l.hb {
		out[k] = v
	}
	return out
}

// errorRequest is the error step (1.3.5): notes and sprint keys only, carrying
// the lease generation like every tick step, so a stale loop's is refused
// STALEGEN and writes nothing (errata 3, amendment 2). It parks the keys of
// the refused steps, opens their judgments, quarantines the cards the refused
// steps named (the quarantine rides "the RT1 step of the next tick"; it rides
// this step and not the lease step, whose other parts X does not hold to the
// generation), and records R14's outcomes (2.3, phase 3). What is owed is cut
// at the store's limits (errorStep.chunk) and at the tick's step bytes, and
// one chunk rides each RT1: the rest waits, its keys not planned.
func (l *Loop) errorRequest() (*sprintfn.Request, errChunk) {
	c := l.owed.chunk()
	for {
		req := l.errorChunk(c)
		n, ref := sprintfn.EncodedSize(l.cfg.Names.Prefix, req)
		if ref == nil && n <= l.budget.StepBytes {
			return req, c
		}
		if c == c.halve() {
			// One of each is over the bytes. A note alone over them is cut in
			// its text (its error text is of any length) until it fits;
			// anything else over goes as it is: the store names it.
			if c.notes > 0 && l.owed.cutText(0) {
				continue
			}
			return req, c
		}
		// The halved chunk keeps its notes at the halved count: fitNotes
		// refills them to the count limits, which would undo the halving.
		h := c.halve()
		c = l.owed.fitNotes(h)
		c.notes = min(c.notes, h.notes)
	}
}

// errorChunk is the error step of a chunk of what is owed.
func (l *Loop) errorChunk(c errChunk) *sprintfn.Request {
	qs := l.owed.quarantine[:c.quarantine]
	notes := append(invariantNotes(qs), l.owed.notes[:c.notes]...)
	if l.owed.wake > 0 {
		// The owed tick-end, last: it covers the addressed notes before it.
		notes = append(notes, sprint.TickEndNote(l.owed.wake+addressed(&sprintfn.Request{Body: sprintfn.Body{Notes: notes}})))
	}
	req := &sprintfn.Request{Epoch: l.epoch, Meta: sprintfn.Meta{Rule: "tick", Tick: true, Gen: l.gen},
		Body: sprintfn.Body{Notes: notes, Quarantine: qs}}
	if c.park != 0 || c.quarantine != 0 {
		req.Sprint = &sprintfn.SprintPart{Park: l.owed.park[:c.park], Quarantine: qs}
	}
	return req
}

// tick is one tick's work; its error fails the tick.
func (l *Loop) tick(ctx context.Context, c sprintfn.Client, rep *Report) error {
	// R14's phase 3: the outcomes of the pushes that finished ride this RT1's
	// error step (2.3).
	rep.Outcomes = l.pusher.take()
	for _, o := range rep.Outcomes {
		l.owed.addNotes(outcomeNote(o))
	}

	// RT1 (SprintEvents.tla RT1, ReadEvents).
	r1 := l.rt1()
	rep.PageLimit = r1.pageLimit
	res, err := pipeline(ctx, c, r1.items)
	rep.RoundTrips++
	if err != nil {
		return err
	}
	read := res[r1.get]
	if read.Read == nil {
		return resultErr("the tick's read", read)
	}
	rd, err := parseRT1(read.Read, r1.bands)
	if err != nil {
		return err
	}
	if rd.epoch != l.epoch {
		// A clear advanced the epoch: the loop's cursor and memory are of the
		// old one. The next tick reads the new one (STALE and EPOCHAHEAD are
		// races, 1.3.5).
		l.newEpoch(rd.epoch)
		return nil
	}
	ls := res[r1.lease]
	if ls.Step == nil {
		return resultErr("the lease step", ls)
	}
	var lr leaseReply
	if err := json.Unmarshal(ls.Step.Parts[sprintfn.PartLease], &lr); err != nil {
		return fmt.Errorf("machine: the lease part's reply: %w", err)
	}
	if !lr.Held {
		// Held by another loop: its step wrote idle_loop and idle_at, and this
		// loop does nothing more this tick (1.4.2).
		l.gen = 0
		return nil
	}
	gen, err := strconv.ParseUint(string(lr.Gen), 10, 64)
	if err != nil {
		return fmt.Errorf("machine: the lease's generation %q", lr.Gen)
	}
	// A take moves the generation: whatever the loop owed as another
	// generation's error step is sent at this one next tick.
	l.gen = gen
	rep.Held, rep.Gen = true, gen
	if r1.errStep >= 0 {
		if res[r1.errStep].Step != nil {
			rep.WakeLate = r1.errWake
		}
		if err := l.settleErrorStep(res[r1.errStep], r1.errChunk, rep); err != nil {
			return err
		}
	}
	rep.Running = !rd.clock.Stopped
	l.stopped = rd.clock.Stopped
	l.hb["tick_at"] = rd.timeMS
	l.hb["agenda"], l.hb["heldq"], l.hb["due_now"] = rd.agendaText(), rd.heldText(), strconv.Itoa(rd.due)
	for _, k := range r1.parkedAsked {
		if _, still := rd.parked[k]; !still {
			delete(l.parked, k) // acknowledged: its judgment closed, its line queues it again
		}
	}
	for k := range l.parked {
		rd.parked[k] = "" // parked by this tick's error step, after the read was asked
	}
	for _, k := range l.owed.park {
		rd.parked[k.Key] = "" // owed to the park: not planned while it is owed (ParkOnBug)
	}

	// The page: the lines after the real cursor (1.1, "Where the loop learns
	// cur": a page sent from a stale cur drops what is at or before the real
	// one).
	realCur := rd.cur
	l.cur, l.curKnown = realCur, true
	var events []sprint.Event
	pageTo := realCur
	if r1.page >= 0 {
		pg := res[r1.page]
		if pg.Page == nil {
			return resultErr("the page", pg)
		}
		events, pageTo, err = l.readPage(pg.Page, realCur)
		if err != nil {
			return err
		}
	}
	in := sprint.Ingest(events)
	rep.Lines, rep.Keys = len(events), len(in.Keys)
	// The page's lines addressed to the coordinator that no tick-end line
	// covers (tickend.go): the tick's N counts them once the ingest moves
	// the cursor past them and the page reaches Layer 2's last.
	unwoken := pageWake(l.unwoken, events)
	complete := seqOf(pageTo) >= rd.last
	backlog := rd.last - min(rd.last, seqOf(pageTo))
	rep.Backlog = backlog
	l.hb["backlog"] = strconv.FormatUint(backlog, 10)

	// The keys of this tick: the heads and the new keys, the parked left out,
	// and the held back released by a new line or a cleared mark (1.3.5).
	fresh := map[string]bool{}
	for _, k := range in.Keys {
		fresh[k.Key] = true
	}
	for k, marks := range l.heldBack {
		if fresh[k] || rd.marks != marks {
			delete(l.heldBack, k)
			delete(l.heldSeq, k)
		}
	}
	keys := mergeKeys(rd.agenda, rd.heldq, in.Keys, rd.parked)
	held := map[string]bool{}
	for k := range l.heldBack {
		held[k] = true
	}

	look := false
	if l.stopped {
		look = lookDue(l.lastLook, rd.wall)
		if !look {
			return nil // STOPPED: RT1 alone between looks (1.4.5)
		}
		l.lastLook = rd.wall
		rep.Looked = true
	}
	// RT2 (SprintEvents.tla RT2): the ingest, and one read a rule.
	batches := dispatch(l.rules, keys, held, l.halvings, l.budget, sprint.TickShape{Streams: rd.streams})
	var items []sprintfn.Item
	ingest := -1
	if seqOf(pageTo) > seqOf(realCur) {
		ingest = len(items)
		items = append(items, sprintfn.Item{Step: &sprintfn.Request{Epoch: l.epoch, Meta: sprintfn.Meta{Rule: "ingest", Tick: true, Gen: l.gen},
			Ingest: &sprintfn.IngestPart{From: realCur, To: pageTo, Keys: in.Keys},
			// The tick end rides on the step that moves the cursor, where the
			// backlog it records is decided (R18; 1.4.2 puts it on RT3's last).
			Sprint: &sprintfn.SprintPart{TickEnd: &sprintfn.TickEnd{Backlog: tset.Decimal(strconv.FormatUint(backlog, 10))}}}})
	}
	type sent struct {
		batch Batch
		at    int
	}
	var reads []sent
	var quarantine []sprint.Quarantined
	var notes []sprint.NoteReq
	for _, bt := range batches {
		if bt.NoRule {
			for _, k := range bt.Rest {
				rep.Unserved = append(rep.Unserved, k.Key)
			}
			continue
		}
		rep.Left[bt.Rule] += len(bt.Rest)
		rep.stat(bt.Rule).Left += len(bt.Rest)
		if len(bt.Keys) == 0 {
			continue
		}
		rr, err := readRequest(l.cfg.Names, l.epoch, bt.Plan)
		if err != nil {
			// Refused before it is sent: a query past a bound of L1 6 or 7 is
			// a LIMIT, read at half next tick; any other is a bug, parked (1.3.5).
			var ref *sprintfn.Refusal
			if errors.As(err, &ref) && ref.Code == sprintfn.CodeLimit {
				rep.refused(bt.Rule, ref.Code)
				notes = append(notes, l.halve(bt, ref.Code, ref.Detail.Budget, "its read", rep)...)
			} else {
				l.onBug(bt, sprintfn.CodeRequest, "", fmt.Sprintf("its read plan cannot be sent: %v", err), rep)
			}
			continue
		}
		rep.Read[bt.Rule] += len(bt.Keys)
		rep.stat(bt.Rule).Keys += len(bt.Keys)
		reads = append(reads, sent{bt, len(items)})
		items = append(items, sprintfn.Item{Read: rr})
	}
	// RT3 is sure to be empty (nothing read, no note, no look): the ingest step
	// is the tick's last and carries its tick-end (errata 3 amendment 8).
	var ingestWake int
	if ingest >= 0 && len(reads) == 0 && !look && len(notes) == 0 && complete && unwoken > 0 {
		ingestWake = unwoken
		st := items[ingest].Step
		st.Body.Notes = append(st.Body.Notes, sprint.TickEndNote(ingestWake))
	}
	if len(items) == 0 && !look && len(notes) == 0 {
		// Idle: no new line and no key to plan (the held back and the parked
		// are not), so the tick was its lease renewal alone (1.4.2, T5).
		return nil
	}
	if len(items) != 0 {
		res, err = pipeline(ctx, c, items)
		rep.RoundTrips++
		if err != nil {
			return err
		}
	}
	// The keys of the page the ingest found parked: a key another loop parked
	// is not planned (1.3.5), and the ingest reply names it, so the loop needs
	// no read of its own for it (the model owes the same filter: RT2 plans
	// agenda ∪ x.lk).
	pageParked := map[string]bool{}
	if ingest >= 0 {
		switch ir := res[ingest]; {
		case ir.Step != nil:
			l.cur = pageTo
			l.unwoken = unwoken
			if ingestWake > 0 {
				l.unwoken, rep.Wake = 0, ingestWake // written: the ingest step was the tick's last
			}
			var reply struct {
				ParkedKeys []string `json:"parked_keys"`
			}
			if raw := ir.Step.Parts[sprintfn.PartIngest]; len(raw) != 0 {
				if err := json.Unmarshal(raw, &reply); err != nil {
					return fmt.Errorf("machine: the ingest part's reply: %w", err)
				}
			}
			for _, k := range reply.ParkedKeys {
				pageParked[k] = true
			}
		case ir.Refusal != nil && ir.Refusal.Code == sprintfn.CodeIngestAt:
			l.cur = ir.Refusal.Detail.Cur // another loop ingested (1.1)
			rep.Refused[ir.Refusal.Code]++
		case ir.Refusal != nil && ir.Refusal.Code == sprintfn.CodeStaleGen:
			l.gen = 0
			rep.Refused[ir.Refusal.Code]++
			return nil // not the tick: nothing it planned would apply
		default:
			return resultErr("the ingest", ir)
		}
	}

	// Plan (SprintEvents.tla PlanOrLook): each rule once over its keys, on its
	// own read; while STOPPED, dry.
	var plans []*planned
	var dry []sprint.RulePlan
	snapshots := map[*planned]*sprint.Snapshot{}
	for _, s := range reads {
		r := res[s.at]
		if r.Read == nil {
			q, n := l.onReadRefused(s.batch, r, rep)
			quarantine, notes = append(quarantine, q...), append(notes, n...)
			continue
		}
		ans, err := readAnswer(s.batch.Plan, r.Read)
		if err != nil {
			return err
		}
		snap, err := sprint.LoadPartialRefusing(s.batch.Plan, ans)
		if err != nil {
			return fmt.Errorf("machine: rule %s's read: %w", s.batch.Rule, err)
		}
		rule := l.byName[s.batch.Rule]
		now := nowOf(rd, r.Read.TimeMS)
		bt := s.batch
		if len(pageParked) != 0 {
			bt.Keys = withoutKeys(bt.Keys, pageParked)
			if len(bt.Keys) == 0 {
				continue // every key of it is parked: nothing to plan
			}
		}
		rp := rule.Plan(snap, bt.Keys, now)
		if err := snap.UnloadedErr(); err != nil {
			// A plan on a short read decides nothing, and its read plan did not
			// load what the rule reads: a bug of the rule's read (1.3.5), never a
			// crash. The plan is refused by name, its keys parked and named in
			// "the machine's step was refused", and the tick fails naming it.
			l.onShortRead(bt, err, rep)
			continue
		}
		if !now.Running {
			dry = append(dry, rp)
			// R11's cut clock is judged while STOPPED (1.4.5; D4), key by key
			// (SprintEvents.tla PlanOrLook: {k ∈ ag : k[1] = "late:cut"}, each
			// planned alone): the cut keys whose plans are notes and sprint
			// keys only are sent at a look, whatever other keys share their
			// rule's batch.
			cut := cutClockKeys(rule, snap, bt.Keys, now)
			if len(cut) == 0 {
				continue
			}
			bt = Batch{Rule: bt.Rule, Keys: cut, Halvings: bt.Halvings, Plan: bt.Plan}
			rp = rule.Plan(snap, cut, now)
			if snap.UnloadedErr() != nil || !notesAndKeysOnly(rp) {
				continue
			}
		}
		quarantine = append(quarantine, rp.Quarantine...)
		for _, k := range rp.HeldBack {
			l.heldBack[k.Key], l.heldSeq[k.Key] = rd.marks, k.Seq
		}
		p, err := l.cut(rule, bt, rp, rep)
		if err != nil {
			return err
		}
		if p != nil {
			plans = append(plans, p)
			snapshots[p] = snap
		}
	}
	if look {
		rep.MovesDue = movesDue(dry)
		if l.cfg.Look != nil {
			rp := l.cfg.Look(dry, rd.clock, rd.wall)
			if !notesAndKeysOnly(rp) {
				return errors.New("machine: R17's step at a look changes a card, which no tick step does while STOPPED (T2)")
			}
			if p, err := l.cut(sprint.Rule{Name: "stopped"}, Batch{Rule: "stopped"}, rp, rep); err != nil {
				return err
			} else if p != nil {
				plans = append(plans, p)
			}
		}
	}

	// RT3 (SprintEvents.tla Apply): the quarantine step, then the rules' steps
	// as the budget deals them.
	var out []*sprintfn.Request
	var reserve spend
	qAt := -1
	if len(quarantine) != 0 || len(notes) != 0 {
		qreq := l.quarantineRequest(quarantine, notes)
		cost, ref := costOf(l.cfg.Names.Prefix, qreq)
		if ref != nil {
			return fmt.Errorf("machine: the quarantine step: %w", ref)
		}
		reserve = reserve.add(cost, false)
		qAt = 0
		out = append(out, qreq)
		for _, q := range quarantine {
			rep.Quarantined = append(rep.Quarantined, q.ID)
		}
	}
	// The tick-end (errata 3 amendment 8; tickend.go): the lines ingested and
	// not covered, once the page reached Layer 2's last, and the addressed
	// notes of the steps RT3 sends. Its step is reserved before the deal
	// whenever the tick may write it.
	wake := 0
	if complete {
		wake, l.unwoken = l.unwoken, 0
	}
	if qAt >= 0 {
		wake += addressed(out[qAt])
	}
	if wake > 0 || plansAddress(plans) {
		cost, ref := l.tickEndReserve()
		if ref != nil {
			return fmt.Errorf("machine: the tick-end step: %w", ref)
		}
		reserve = reserve.add(cost, false)
	}
	order, index, spent := deal(plans, l.budget, reserve)
	rep.Changes, rep.HeldChanges, rep.EntriesNotes, rep.RequestBytes = spent.changes, spent.heldChanges, spent.entriesNotes, spent.bytes
	for i, p := range order {
		out = append(out, p.reqs[index[i]])
		rep.Dealt = append(rep.Dealt, p.batch.Rule)
		st := rep.stat(p.batch.Rule)
		st.Steps++
		st.Changes += p.costs[index[i]].changes
		wake += addressed(p.reqs[index[i]])
	}
	endAt := -1
	if wake > 0 {
		if len(out) == 0 {
			// Nothing to carry it this tick: the next RT1's error step writes it.
			l.owed.wake += wake
			return nil
		}
		endAt = len(out)
		out = append(out, l.tickEndStep(wake))
		cost, _ := costOf(l.cfg.Names.Prefix, out[endAt]) // reserved above: it fits
		rep.EntriesNotes, rep.RequestBytes = rep.EntriesNotes+cost.entriesNotes, rep.RequestBytes+cost.bytes
	}
	if len(out) == 0 {
		return nil
	}
	items = make([]sprintfn.Item, 0, len(out))
	for _, req := range out {
		items = append(items, sprintfn.Item{Step: req})
	}
	res, err = pipeline(ctx, c, items)
	rep.RoundTrips++
	if err != nil {
		return err
	}
	if qAt >= 0 {
		if q := res[qAt]; q.Step == nil {
			// Not written: owed to the next RT1's error step.
			for _, q := range dedupQuarantine(quarantine) {
				l.owed.addQuarantine(q)
			}
			l.owed.addNotes(notes...)
			if q.Refusal != nil {
				rep.Refused[q.Refusal.Code]++
			}
		} else {
			rep.Applied++
		}
	}
	if endAt >= 0 {
		if res[endAt].Step != nil {
			rep.Wake = wake // Applied counts the rules' and the quarantine's steps
		} else {
			// Not written (refused STALEGEN, or an unknown outcome): owed to the
			// next RT1's error step, which writes it with the tick's count.
			l.owed.wake += wake
			if r := res[endAt].Refusal; r != nil {
				rep.Refused[r.Code]++
			}
		}
		res, out = res[:endAt], out[:endAt]
	}
	var claims []Claim
	base := len(out) - len(order)
	for i, p := range order {
		r := res[base+i]
		switch {
		case r.Step != nil:
			rep.Applied++
			rep.stat(p.batch.Rule).Applied++
			if p.whole {
				for _, k := range p.batch.Keys {
					delete(l.halvings, k.Key) // a plan of it applied whole (1.3.6)
				}
			}
			if l.cfg.Claims != nil && snapshots[p] != nil {
				claims = append(claims, l.cfg.Claims(p.batch.Rule, snapshots[p], p.plan)...)
			}
		case r.Refusal != nil:
			rep.refused(p.batch.Rule, r.Refusal.Code)
			l.onStepRefused(p, r.Refusal, rep)
		}
		// An unknown outcome (r.Err) is settled by the next tick's fresh plan:
		// the tick never resends a step (1.0).
	}
	if len(claims) != 0 {
		// R14 phase 2, after RT3, off the tick and outside every round trip
		// (2.3): the tick does not wait for a route.
		rep.Pushes, rep.PushesSkipped = l.pusher.start(ctx, claims)
	}
	return nil
}

// newEpoch starts the loop over at a new epoch: its cursor, its halvings,
// its held back keys and its parked keys are the old epoch's.
func (l *Loop) newEpoch(e tset.Decimal) {
	l.epoch = e
	l.cur, l.curKnown = "", false
	l.halvings, l.heldBack, l.heldSeq, l.parked = map[string]int{}, map[string]int{}, map[string]uint64{}, map[string]bool{}
	l.owed = errorStep{}
	l.unwoken = 0
}

// settleErrorStep takes the error step's result (1.3.5): applied, the chunk
// it carried is written and the rest stays owed; refused STALEGEN (a take
// since it was built), it is owed again at the new generation; an unknown
// outcome is owed again (the step is idempotent: a key parked or a card
// quarantined twice is written once). Any other refusal fails the tick: the
// error step is how a bug is named, and a refusal of it is never silent. The
// tick's failures count, the heartbeat's error names it, and "the tick keeps
// failing" shows (1.4.1), while what is owed stays owed and its keys stay
// out of every plan.
func (l *Loop) settleErrorStep(r sprintfn.Result, c errChunk, rep *Report) error {
	switch {
	case r.Step != nil:
		for _, p := range l.owed.park[:c.park] {
			l.parked[p.Key] = true
		}
		l.owed.settle(c)
		rep.Applied++
	case r.Refusal != nil:
		rep.Refused[r.Refusal.Code]++
		if r.Refusal.Code != sprintfn.CodeStaleGen {
			return fmt.Errorf("machine: the error step was refused (%d keys to park, %d cards to quarantine, %d notes): %w",
				c.park, c.quarantine, c.notes, r.Refusal)
		}
	}
	return nil
}

// cut builds a rule's plan into requests (1.3.6): cut at the tick's step
// bytes and at the chunk halved for the keys' halvings; a cut plan carries no
// Done. A builder that cannot cut the plan is a bug of the plan's size, parked
// LIMIT (1.3.5). A plan that names what nothing carries (NotCarried) is not a
// size: its keys are parked NOTCARRIED, "not carried: <what>", and never
// halved.
func (l *Loop) cut(rule sprint.Rule, bt Batch, rp sprint.RulePlan, rep *Report) (*planned, error) {
	meta := sprintfn.Meta{Rule: bt.Rule, Tick: true, Gen: l.gen}
	b := stepbuild.Contract()
	b.RequestBytes = min(b.RequestBytes, l.budget.StepBytes)
	b.Candidates = sprint.Halved(b.Candidates, bt.Halvings)
	bodies, err := l.cfg.Build(rp, meta, b)
	var nc *NotCarried
	switch {
	case errors.As(err, &nc):
		l.onBug(bt, CodeNotCarried, "", nc.Error(), rep)
		return nil, nil
	case err != nil:
		l.onBug(bt, sprintfn.CodeLimit, "", fmt.Sprintf("its plan cannot be cut into steps: %v", err), rep)
		return nil, nil
	}
	if len(bodies) == 0 {
		return nil, nil
	}
	p := &planned{rule: rule, batch: bt, plan: rp, whole: len(bodies) == 1}
	for i, body := range bodies {
		if !p.whole {
			body.Done = nil // a cut plan leaves its keys queued (1.3.6)
		}
		req := &sprintfn.Request{Epoch: l.epoch, Body: body, Meta: meta}
		if i == 0 {
			req.Sprint = TimePart(rp.Sprint) // the writes to the sprint's own keys ride the first request (IT16's sprint part)
		}
		c, ref := costOf(l.cfg.Names.Prefix, req)
		if ref != nil {
			l.onBug(bt, ref.Code, "", "its step is refused before it is sent: "+ref.Message, rep)
			return nil, nil
		}
		p.reqs, p.costs = append(p.reqs, req), append(p.costs, c)
	}
	return p, nil
}

// quarantineRequest is RT3's step of the quarantines of this tick's refused
// reads and planners' findings, and the judgments of refused reads (1.3.5):
// notes and sprint keys only. The sprint part writes each record, X takes each
// id out of elig, fresh, again and askwait, and J opens "an invariant is
// broken" on it.
func (l *Loop) quarantineRequest(qs []sprint.Quarantined, notes []sprint.NoteReq) *sprintfn.Request {
	qs = dedupQuarantine(qs)
	req := &sprintfn.Request{Epoch: l.epoch, Meta: sprintfn.Meta{Rule: "tick", Tick: true, Gen: l.gen},
		Body: sprintfn.Body{Quarantine: qs, Notes: append(invariantNotes(qs), splitNotes(notes)...)}}
	if len(qs) != 0 {
		req.Sprint = &sprintfn.SprintPart{Quarantine: qs}
	}
	return req
}

// dedupQuarantine keeps each id's first quarantine.
func dedupQuarantine(qs []sprint.Quarantined) []sprint.Quarantined {
	seen := map[string]bool{}
	var out []sprint.Quarantined
	for _, q := range qs {
		if !seen[q.ID] {
			seen[q.ID] = true
			out = append(out, q)
		}
	}
	return out
}

// invariantNotes are J's requests to open "an invariant is broken" on the
// quarantined cards, one for each refusal code and rule (1.3.5; J keeps one
// judgment a cause).
func invariantNotes(qs []sprint.Quarantined) []sprint.NoteReq {
	type cause struct{ code, rule string }
	by := map[cause][]string{}
	var order []cause
	for _, q := range qs {
		c := cause{q.Code, q.Rule}
		if _, ok := by[c]; !ok {
			order = append(order, c)
		}
		by[c] = append(by[c], q.ID)
	}
	var out []sprint.NoteReq
	for _, c := range order {
		out = append(out, sprint.NoteReq{Op: "open", Type: TypeInvariant, Cause: c.code, Subjects: by[c],
			Text: fmt.Sprintf("the lower layers refused %s naming these cards, as %s read or moved them", c.code, c.rule)})
	}
	return out
}

// The codes of 1.3.5, by what the tick does with them.
var (
	// raceCodes: the step wrote nothing; its keys stay; the next tick plans
	// again on a fresh read. EXISTS and NOROW are races only on a derived id
	// and a guard over the stream set; the loop cannot tell, and takes them
	// as races.
	raceCodes = map[string]bool{"PLACE": true, "REVISION": true, "CELLFULL": true, "RANGECOUNT": true,
		sprintfn.CodeStaleGen: true, sprintfn.CodeIngestAt: true, sprintfn.CodeCounter: true, sprintfn.CodeDropping: true,
		sprintfn.CodeStopped: true, sprintfn.CodeXGuard: true, sprintfn.CodeStale: true, sprintfn.CodeEpochAhead: true,
		"EXISTS": true, "NOROW": true,
		// PROPGUARD: a guard on a table property as read (L1 amendment 2026-09-30).
		"PROPGUARD": true}
	// cardCodes name a card: it is quarantined.
	cardCodes = map[string]bool{"DRIFT": true, "MISSING": true, "MEMBEREPOCH": true}
)

// onReadRefused is a rule's read refused (1.4.2, 1.3.5): DRIFT, MISSING or
// MEMBEREPOCH quarantine the ids its detail names in RT3, and the rule reads
// again next tick without them; BUDGET and LIMIT are a bug, named, and the
// keys read at half next tick (a read of one key at limits of one parks it);
// a race leaves the keys; any other code parks them.
func (l *Loop) onReadRefused(bt Batch, r sprintfn.Result, rep *Report) ([]sprint.Quarantined, []sprint.NoteReq) {
	ref := r.Refusal
	if ref == nil {
		return nil, nil // the read's reply is lost: the keys stay and read again
	}
	rep.refused(bt.Rule, ref.Code)
	switch {
	case cardCodes[ref.Code]:
		if len(ref.Detail.IDs) == 0 {
			// A refusal naming no id is a bug (1.3.5).
			l.onBug(bt, ref.Code, ref.Detail.Budget, "its read was refused naming no card", rep)
			return nil, nil
		}
		stream := streamOfRead(bt.Plan, ref.Detail)
		var qs []sprint.Quarantined
		for _, id := range ref.Detail.IDs {
			qs = append(qs, sprint.Quarantined{ID: id, Stream: stream, Code: ref.Code, Rule: bt.Rule, Cells: ref.Detail.Cells})
		}
		return qs, nil
	case raceCodes[ref.Code]:
		return nil, nil
	}
	if ref.Code == "BUDGET" || ref.Code == sprintfn.CodeLimit {
		return nil, l.halve(bt, ref.Code, ref.Detail.Budget, "its read", rep)
	}
	l.onBug(bt, ref.Code, ref.Detail.Budget, "its read was refused", rep)
	return nil, nil
}

// streamOfRead is the stream of the cards a read's refusal names: the stream
// of the index head or the front the refused query read, when it read one.
func streamOfRead(rp sprint.ReadPlan, d sprintfn.RefusalDetail) string {
	if d.QueryIndex == nil {
		return ""
	}
	i := *d.QueryIndex - len(rp.TsetSlots())
	if i < 0 || i >= len(rp.Sprint) {
		return ""
	}
	q := rp.Sprint[i]
	if q.Kind == sprint.QueryFront {
		return q.Stream
	}
	if q.Source.Kind == sprint.SourceHead {
		for _, idx := range []string{sprint.IndexElig, sprint.IndexFresh, sprint.IndexAgain, sprint.IndexSent} {
			if s, ok := strings.CutPrefix(q.Source.Key, idx+":"); ok {
				return s
			}
		}
	}
	return ""
}

// onStepRefused is a rule's step refused (1.3.5; SprintEvents.tla Apply and
// ParkOnBug): a race leaves its keys; a refusal naming cards quarantines them
// in the next RT1; a LIMIT plans the keys at half the chunk next tick, and
// parks them at a chunk of one; any other code parks them at once. Every bug
// is named in "the machine's step was refused".
func (l *Loop) onStepRefused(p *planned, ref *sprintfn.Refusal, rep *Report) {
	switch {
	case raceCodes[ref.Code]:
		if ref.Code == sprintfn.CodeStaleGen {
			l.gen = 0
		}
	case cardCodes[ref.Code] && len(ref.Detail.IDs) != 0:
		stream := ""
		if len(ref.Detail.Rows) == 1 {
			stream = ref.Detail.Rows[0]
		}
		// The invariant notes are made from the owed quarantine when the error
		// step is cut (errorChunk), one for each code and rule.
		for _, id := range ref.Detail.IDs {
			q := sprint.Quarantined{ID: id, Stream: stream, Code: ref.Code, Rule: p.batch.Rule, Cells: ref.Detail.Cells}
			if l.owed.addQuarantine(q) {
				rep.Quarantined = append(rep.Quarantined, id)
			}
		}
	case ref.Code == sprintfn.CodeLimit:
		l.owed.addNotes(l.halve(p.batch, ref.Code, ref.Detail.Budget, "its step", rep)...)
	default:
		l.onBug(p.batch, ref.Code, ref.Detail.Budget, "its step was refused", rep)
	}
}

// halve is a LIMIT or a BUDGET (1.3.5; IT10's OnBug): the plan was right and
// its size counted wrong, so the keys are planned next tick at half; only a
// batch of one key already at a chunk of one is parked. It returns the
// judgment's note for the step that carries it.
func (l *Loop) halve(bt Batch, code, budget, what string, rep *Report) []sprint.NoteReq {
	h := bt.Halvings + 1
	if len(bt.Keys) == 1 && sprint.Halved(stepbuild.LimitCandidates, bt.Halvings) == 1 {
		l.onBug(bt, code, budget, what+" was refused at a chunk of one card", rep)
		return nil
	}
	for _, k := range bt.Keys {
		l.halvings[k.Key] = h
	}
	return []sprint.NoteReq{stepRefusedNote(bt, code, budget, fmt.Sprintf("%s was refused %s; it is planned again at half", what, code))}
}

// CodeShortRead is the code the loop names a short read by (Report.Short, "the
// machine's step was refused"): a rule's plan read what its read plan did not
// load (sprint.ErrUnloaded). It is the loop's own, never a store's.
const CodeShortRead = "SHORTREAD"

// onShortRead refuses a plan made on a read that came back short (1.3.5; the
// guard of sprint.UnloadedErr): the rule's keys are parked as a bug's and
// named, with what the plan read, in "the machine's step was refused", and
// the report names it so that the tick fails (Report.Short).
func (l *Loop) onShortRead(bt Batch, err error, rep *Report) {
	what := fmt.Sprintf("rule %s: %v", bt.Rule, err)
	rep.Short = append(rep.Short, what)
	rep.refused(bt.Rule, CodeShortRead)
	l.onBug(bt, CodeShortRead, "", "its read came back short: "+err.Error(), rep)
}

// onBug parks a batch's keys at once and names the refusal (1.3.5): the
// error step of the next RT1 moves each key out of the agenda into
// {p}parked@e, the park written first, and it is not planned until the
// judgment closes.
//
// A batch is parked once: each key is owed once however many of its plan's
// requests are refused, and a batch whose keys are all owed already adds no
// judgment (J names each key once a step).
func (l *Loop) onBug(bt Batch, code, budget, why string, rep *Report) {
	keys := bt.Keys
	if len(keys) == 0 {
		keys = bt.Rest
	}
	added := false
	for _, k := range keys {
		if !l.owed.addPark(sprintfn.ParkedKey{Key: k.Key, Rule: bt.Rule, Code: code, Budget: budget}) {
			continue
		}
		added = true
		rep.Parked = append(rep.Parked, k.Key)
		delete(l.halvings, k.Key)
	}
	if added {
		l.owed.addNotes(stepRefusedNote(bt, code, budget, why))
	}
}

// stepRefusedNote is "the machine's step was refused" on a batch's keys,
// naming the rule, the keys, the code and the bound (1.3.5).
func stepRefusedNote(bt Batch, code, budget, why string) sprint.NoteReq {
	var subjects []string
	for _, k := range bt.Keys {
		subjects = append(subjects, k.Key)
	}
	if len(subjects) == 0 {
		for _, k := range bt.Rest {
			subjects = append(subjects, k.Key)
		}
	}
	text := fmt.Sprintf("rule %s: %s (%s", bt.Rule, why, code)
	if budget != "" {
		text += ", bound " + budget
	}
	text += fmt.Sprintf(", %d keys)", len(subjects))
	return sprint.NoteReq{Op: "open", Type: TypeStepRefused, Cause: code, Subjects: subjects, Text: text}
}

// cutClockKeys are the cut clocks' keys of a batch, late:cut:<op> (1.2), the
// one kind of rule key whose step is sent while STOPPED besides R17's
// (SprintEvents.tla PlanOrLook: the late:cut keys and StopK), each planned
// alone and kept when its plan is notes and sprint keys only (T2).
func cutClockKeys(rule sprint.Rule, snap *sprint.Snapshot, keys []sprint.AgendaKey, now sprint.Now) []sprint.AgendaKey {
	var out []sprint.AgendaKey
	for _, k := range keys {
		if !strings.HasPrefix(k.Key, "late:cut:") {
			continue
		}
		if rp := rule.Plan(snap, []sprint.AgendaKey{k}, now); snap.UnloadedErr() == nil && notesAndKeysOnly(rp) {
			out = append(out, k)
		}
	}
	return out
}

// withoutKeys is the keys less those named.
func withoutKeys(keys []sprint.AgendaKey, drop map[string]bool) []sprint.AgendaKey {
	out := make([]sprint.AgendaKey, 0, len(keys))
	for _, k := range keys {
		if !drop[k.Key] {
			out = append(out, k)
		}
	}
	return out
}

// nowOf is the clocks a rule plans against at its read's time: R moves with
// the wall while RUNNING and is still while STOPPED (1.2).
func nowOf(rd rt1Read, readMS tset.Decimal) sprint.Now {
	wall, err := strconv.ParseInt(string(readMS), 10, 64)
	if err != nil || wall < rd.wall {
		wall = rd.wall
	}
	r := rd.clock.R
	if !rd.clock.Stopped {
		r += wall - rd.wall
	}
	return sprint.Now{R: r, Wall: wall, Running: !rd.clock.Stopped}
}

// seqOf is a cursor or a seq as a number; empty is 0.
func seqOf(d tset.Decimal) uint64 {
	n, _ := strconv.ParseUint(string(d), 10, 64)
	return n
}

// readPage is the page's lines after the real cursor, as events, and the
// seq of the page's last line (the ingest's To); the page's bytes a line set
// the next page's limit.
func (l *Loop) readPage(p *tset.ReadReply, realCur tset.Decimal) ([]sprint.Event, tset.Decimal, error) {
	to := realCur
	bytes := 0
	var events []sprint.Event
	for _, raw := range p.Items {
		bytes += len(raw)
		seq, err := lineSeq(raw)
		if err != nil {
			return nil, realCur, fmt.Errorf("machine: the page: %w", err)
		}
		if seq <= seqOf(realCur) {
			continue // at or before the real cursor: another loop ingested it
		}
		e, err := sprint.ParseEvent(strconv.FormatUint(seq, 10)+"-0", lineBody(raw))
		if err != nil {
			return nil, realCur, fmt.Errorf("machine: the page: %w", err)
		}
		events = append(events, e)
		to = decimal(seq)
	}
	if len(p.Items) != 0 {
		l.lineB = max(1, bytes/len(p.Items))
	}
	return events, to, nil
}

// mergeKeys is the tick's keys: the agenda's head, the held queue's head and
// the page's new keys, each key once at its earliest order, the parked left
// out.
func mergeKeys(agenda, heldq, fresh []sprint.AgendaKey, parked map[string]string) []sprint.AgendaKey {
	at := map[string]uint64{}
	var out []sprint.AgendaKey
	for _, list := range [][]sprint.AgendaKey{agenda, heldq, fresh} {
		for _, k := range list {
			if _, isParked := parked[k.Key]; isParked {
				continue
			}
			if s, ok := at[k.Key]; ok {
				if k.Seq < s {
					at[k.Key] = k.Seq
				}
				continue
			}
			at[k.Key] = k.Seq
			out = append(out, k)
		}
	}
	for i := range out {
		out[i].Seq = at[out[i].Key]
	}
	sortAgenda(out)
	return out
}

// pipeline flushes one round trip and holds its results to its items.
func pipeline(ctx context.Context, c sprintfn.Client, items []sprintfn.Item) ([]sprintfn.Result, error) {
	res, err := c.Pipeline(ctx, items)
	if err != nil {
		return nil, err
	}
	if len(res) != len(items) {
		return nil, fmt.Errorf("machine: a pipeline of %d returned %d results", len(items), len(res))
	}
	return res, nil
}

// resultErr is a result that is not what the tick needs to go on.
func resultErr(what string, r sprintfn.Result) error {
	switch {
	case r.Refusal != nil:
		return fmt.Errorf("machine: %s was refused: %w", what, r.Refusal)
	case r.Err != nil:
		return fmt.Errorf("machine: %s: %w", what, r.Err)
	}
	return fmt.Errorf("machine: %s returned nothing", what)
}

// rt1Read is RT1's atomic read, decoded.
type rt1Read struct {
	epoch         tset.Decimal
	timeMS        string
	wall          int64
	clock         Clock
	cur           tset.Decimal
	due           int
	last          uint64
	agenda, heldq []sprint.AgendaKey
	agendaMore    bool
	heldMore      bool
	marks         int
	parked        map[string]string
	// streams are the work table's rows, in name order: the tick's shape
	// (sprint.TickShape), which R6's read names the fronts of.
	streams []string
}

func (r rt1Read) agendaText() string { return headText(len(r.agenda), r.agendaMore) }
func (r rt1Read) heldText() string   { return headText(len(r.heldq), r.heldMore) }

func headText(n int, more bool) string {
	s := strconv.Itoa(n)
	if more {
		s += "+"
	}
	return s
}

// parseRT1 decodes RT1's read, whose agenda head is in bands ranges
// (agendaRanges).
func parseRT1(rep *sprintfn.ReadReply, bands int) (rt1Read, error) {
	out := rt1Read{epoch: rep.ActiveEpoch, timeMS: string(rep.TimeMS)}
	var err error
	if out.wall, err = strconv.ParseInt(string(rep.TimeMS), 10, 64); err != nil {
		return out, fmt.Errorf("machine: the read's time %q", rep.TimeMS)
	}
	if bands < 1 || len(rep.Tset) != bands+rtAfterBands || len(rep.Sprint) != 5 {
		return out, errors.New("machine: the tick's read is not aligned with its queries")
	}
	keys := func(a tset.ReadAnswer) ([]sprint.AgendaKey, error) {
		var ks []sprint.AgendaKey
		for i, id := range a.IDs {
			f, err := strconv.ParseFloat(a.Scores[i], 64)
			if err != nil || f < 0 {
				return nil, fmt.Errorf("machine: the agenda's order %q of %s", a.Scores[i], id)
			}
			ks = append(ks, sprint.AgendaKey{Key: id, Seq: uint64(f)})
		}
		return ks, nil
	}
	for _, band := range rep.Tset[:bands] {
		ks, err := keys(band)
		if err != nil {
			return out, err
		}
		out.agenda = append(out.agenda, ks...)
		if band.HasMore {
			out.agendaMore = true // the bands after a cut one may not follow it
			break
		}
	}
	rest := rep.Tset[bands:]
	if out.heldq, err = keys(rest[rtHeldQ]); err != nil {
		return out, err
	}
	out.heldMore = rest[rtHeldQ].HasMore
	out.last = seqOf(rest[rtLast].LastSeq)
	for _, r := range rest[rtRows].Rows {
		out.streams = append(out.streams, r.Row)
	}
	sort.Strings(out.streams)
	decode := func(i int, kind string) (sprintfn.QueryResult, error) {
		return sprintfn.DecodeResult(kind, rep.Sprint[i])
	}
	cr, err := decode(rsClock, sprintfn.KeyClock)
	if err != nil {
		return out, err
	}
	out.clock = ClockOf(cr.(sprintfn.ClockResult))
	tr, err := decode(rsTick, sprintfn.KeyTick)
	if err != nil {
		return out, err
	}
	out.cur = "0"
	if c := tr.(sprintfn.TickResult).Cur; c != nil && *c != "" {
		out.cur = tset.Decimal(*c)
	}
	dr, err := decode(rsDue, sprintfn.KeyDueCount)
	if err != nil {
		return out, err
	}
	out.due = dr.(sprintfn.DueCountResult).Due
	mr, err := decode(rsDropping, sprintfn.KeyDropping)
	if err != nil {
		return out, err
	}
	out.marks = mr.(sprintfn.DroppingResult).Count
	pr, err := decode(rsParked, sprintfn.KeyParked)
	if err != nil {
		return out, err
	}
	out.parked = map[string]string{}
	for k, v := range pr.(sprintfn.ParkedResult).Notes {
		out.parked[k] = v
	}
	return out, nil
}

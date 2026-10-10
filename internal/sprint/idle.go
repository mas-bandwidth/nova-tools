package sprint

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"
)

// The fleet is idle (docs/SPEC-SPRINT.md section 14; the coordinator, 2026-10-04, measured at 1:30
// PM: the fleet ran 4 of 68 slots with 561 cards held, 311 of them behind 21 judgments
// "a primary is blocked on something dropped", and nothing said so; the owner at 1:36 PM:
// "I want this sort of oh no fleet is idle, do judgement, release more cards thing -- i
// want this more automated."). When the fleet works under half its width for IdleWindow
// while cards wait, the tick traces every waiting card to the root of its chain (TraceIdle)
// and pushes one note to the coordinator naming the roots by the cards behind them; once
// an episode, and one note more when it recovers. The episode is kept on the fleet table's
// properties (PropIdleSince, PropIdleSaid), so it lives across ticks and run loops. The
// model is tla/SprintRules.tla (AlarmOncePerEpisode, ClearFollowsAlarm).

// The idle alarm's notes: happened, addressed to the coordinator (the tick end wakes them,
// and inbox --push carries them to the bus).
const (
	NIdle        = "the fleet is idle"
	NIdleCleared = "the fleet is working again"
)

// The idle alarm's numbers and state.
const (
	// IdleWindow is how long, in running time, the fleet works under half its width with
	// cards waiting before the alarm is pushed.
	IdleWindow = 5 * time.Minute
	// IdleRoots is how many roots the note names, the most cards behind first.
	IdleRoots = 8
	// PropIdleSince and PropIdleSaid are the fleet table's properties of an episode: when
	// it began and when its note was pushed ("" or absent: none).
	PropIdleSince = "idle_since"
	PropIdleSaid  = "idle_said"
	// PartIdle is the tick part's name.
	PartIdle = "idle"
)

// The store slow alarm (docs/SPEC-SPRINT.md section 14, "store-latency-alarm-bb") is
// the server's measured p50 store round trip (store.StoreRTTRecord) told the
// coordinator once per episode: over StoreSlowBar it says so, and the episode ends
// under half the bar, one note more. The episode is kept on the fleet table's
// properties (PropStoreSlowSince, PropStoreSlowSaid), as the idle alarm's is.
const (
	// StoreSlowBar is the p50 store round trip above which the tick says the store is
	// slow; the episode ends under half of it.
	StoreSlowBar = 5 * time.Millisecond
	// NStoreSlow and NStoreSlowCleared are the notes the tick addresses to the
	// coordinator (the tick end wakes them, and inbox --push carries them to the bus).
	NStoreSlow        = "the store is slow"
	NStoreSlowCleared = "the store is working again"
	// PropStoreSlowSince and PropStoreSlowSaid are the fleet table's properties of an
	// episode: when it began and when its note was pushed ("" or absent: none).
	PropStoreSlowSince = "store_slow_since"
	PropStoreSlowSaid  = "store_slow_said"
	// PartStoreSlow is the tick part's name.
	PartStoreSlow = "store slow"
)

// FleetWorking is the work cards working on the machines up, and their widths' sum.
func FleetWorking(s *Snapshot) (working, width int) {
	for _, m := range s.UpMembers() {
		width += s.Width(m)
		working += s.Fleet.Count(m, Working)
	}
	return working, width
}

// waitingCards is the waiting primaries, sentinels aside.
func waitingCards(s *Snapshot) []*Card {
	var out []*Card
	for _, c := range s.Work.Column(Waiting) {
		if !IsSentinel(c) {
			out = append(out, c)
		}
	}
	return out
}

// idleRoot is where a waiting card's chain ends: its kind (the group the note counts it
// in), its subject (the card, stream or tier), and the words.
type idleRoot struct {
	kind, subject, say string
	since              time.Time // the root's judgment's time, when it is one
}

// The kinds of root, in the order the note breaks ties in.
const (
	rootDropped  = "drop-blocked"
	rootMissing  = "missing"
	rootJudgment = "judgment"
	rootSentinel = "sentinel"
	rootHeld     = "held"
	rootStopped  = "stopped"
	rootNoRoute  = "no route"
	rootFlight   = "in flight"
	rootNext     = "ready next tick"
)

// TraceIdle is the idle note's words: each waiting card's chain traced to its root (a
// drop-blocked or missing need, a held card, an unreleased sentinel, an in-flight card
// held by an open judgment, a stream stopped, a tier no route serves, or work in flight),
// the roots grouped and named by the cards behind them, the most first, at most IdleRoots.
// Every waiting card is traced once (memoized), so the counts are the table's.
func TraceIdle(s *Snapshot, r TickReq) string {
	judgedOn := map[string]Open{}
	for _, o := range s.Open {
		if o.Note.Kind != Judgment {
			continue
		}
		if was, ok := judgedOn[o.Subject()]; !ok || o.Note.At.Before(was.Note.At) {
			judgedOn[o.Subject()] = o
		}
	}
	memo := map[string]idleRoot{}
	var root func(c *Card, seen map[string]bool) idleRoot
	inFlight := func(c *Card) idleRoot {
		if o, ok := judgedOn[c.ID]; ok {
			return idleRoot{kind: rootJudgment, subject: c.ID, say: c.ID + " (" + o.Note.Type, since: o.Note.At}
		}
		if c.Col == Merging {
			if ctl := s.StreamCtl(c.Row); ctl.F("state") == StreamStopped {
				return idleRoot{kind: rootStopped, subject: c.Row, say: "stream " + c.Row + " stopped (" + orDash(ctl.F("cause")) + ")"}
			}
		}
		if c.Col == Ready {
			if OnlyFriend(c) {
				return idleRoot{kind: rootFlight, subject: "friends", say: "a friend's card ready"}
			}
			if tier, why := s.noRoute(escalating(s, c)); why != "" {
				return idleRoot{kind: rootNoRoute, subject: tier, say: "tier " + tier + ": no route serves it"}
			}
		}
		return idleRoot{kind: rootFlight, subject: c.Col, say: "work " + c.Col}
	}
	root = func(c *Card, seen map[string]bool) idleRoot {
		if x, ok := memo[c.ID]; ok {
			return x
		}
		if seen[c.ID] {
			return idleRoot{kind: rootMissing, subject: c.ID, say: "a cycle at " + c.ID}
		}
		seen[c.ID] = true
		var out idleRoot
		switch {
		case IsHeld(c):
			out = idleRoot{kind: rootHeld, subject: c.ID, say: c.ID + " (held)"}
			if IsSentinel(c) {
				out = idleRoot{kind: rootSentinel, subject: c.ID, say: "sentinel " + c.ID + " (held)"}
			}
		case IsSentinel(c) && c.F("reached") != "":
			out = idleRoot{kind: rootSentinel, subject: c.ID, say: "sentinel " + c.ID + " (reached, not released)"}
		default:
			best := idleRoot{kind: rootNext, subject: "ready", say: "ready next tick"}
			for _, w := range WaitsFor(s, c, nil) {
				var x idleRoot
				wc := s.Work.Card(w)
				switch {
				case wc == nil:
					x = idleRoot{kind: rootMissing, subject: c.ID, say: "missing needs"}
				case !wc.Placed() && wc.F("outcome") == "dropped":
					x = idleRoot{kind: rootDropped, subject: c.ID, say: "drop-blocked judgments"}
					if o, ok := judgedOn[c.ID]; ok && o.Note.Type == NBlocked {
						x.since = o.Note.At
					}
				case !wc.Placed() || wc.Col == Landed:
					continue
				case IsSentinel(wc) && wc.Col == Waiting && !IsHeld(wc) && wc.F("reached") == "":
					x = idleRoot{kind: rootSentinel, subject: wc.ID, say: "sentinel " + wc.ID + " (waiting for the cards before it)"}
				case wc.Col == Waiting:
					x = root(wc, seen)
				default:
					x = inFlight(wc)
				}
				if rank(x.kind) < rank(best.kind) {
					best = x
				}
			}
			out = best
		}
		memo[c.ID] = out
		return out
	}
	type group struct {
		say      string
		kind     string
		behind   int
		subjects map[string]bool
		oldest   time.Time
	}
	groups := map[string]*group{}
	for _, c := range waitingCards(s) {
		x := root(c, map[string]bool{})
		key := x.kind + "\x00" + x.subject
		if x.kind == rootDropped || x.kind == rootMissing || x.kind == rootFlight || x.kind == rootNext {
			key = x.kind // one group of the kind: the cards it blocks are its subjects
		}
		g := groups[key]
		if g == nil {
			g = &group{say: x.say, kind: x.kind, subjects: map[string]bool{}}
			groups[key] = g
		}
		g.behind++
		g.subjects[x.subject] = true
		if !x.since.IsZero() && (g.oldest.IsZero() || x.since.Before(g.oldest)) {
			g.oldest = x.since
		}
	}
	list := slices.Collect(maps.Values(groups))
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].behind != list[j].behind {
			return list[i].behind > list[j].behind
		}
		if rank(list[i].kind) != rank(list[j].kind) {
			return rank(list[i].kind) < rank(list[j].kind)
		}
		return list[i].say < list[j].say
	})
	var parts []string
	for i, g := range list {
		if i == IdleRoots {
			parts = append(parts, fmt.Sprintf("and %d roots more", len(list)-IdleRoots))
			break
		}
		age := func() string {
			if g.oldest.IsZero() {
				return ""
			}
			return ", " + s.Now.Sub(g.oldest).Round(time.Second).String()
		}
		switch g.kind {
		case rootDropped:
			a := ""
			if !g.oldest.IsZero() {
				a = " (oldest " + s.Now.Sub(g.oldest).Round(time.Second).String() + ")"
			}
			parts = append(parts, fmt.Sprintf("%d behind %d drop-blocked judgments%s", g.behind, len(g.subjects), a))
		case rootMissing:
			parts = append(parts, fmt.Sprintf("%d behind %d cards with missing needs", g.behind, len(g.subjects)))
		case rootJudgment:
			parts = append(parts, fmt.Sprintf("%d behind %s%s)", g.behind, g.say, age()))
		case rootFlight, rootNext:
			parts = append(parts, fmt.Sprintf("%d behind %s", g.behind, g.kind))
		default:
			parts = append(parts, fmt.Sprintf("%d behind %s", g.behind, g.say))
		}
	}
	return strings.Join(parts, "; ")
}

// rank orders root kinds: a root that needs a person before one the machine will clear.
func rank(kind string) int {
	return slices.Index([]string{rootDropped, rootMissing, rootJudgment, rootSentinel, rootHeld, rootStopped, rootNoRoute, rootFlight, rootNext}, kind)
}

// TickIdle is the idle alarm, the tick's part after the overdue judgments (TickReq.IdleAlarm,
// run --idle-alarm): the episode begins when the fleet works under half its width while a
// card waits, and is written on the fleet table (PropIdleSince); past IdleWindow of running
// time, one note to the coordinator says the fleet's working and width and the roots
// (TraceIdle), and the episode is marked said (PropIdleSaid); when the fleet works at half
// its width again, or no card waits, the episode ends, and a note says so when one was
// pushed.
func TickIdle(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if !r.IdleAlarm || s.Fleet == nil || s.Work == nil {
		return p, 0
	}
	working, width := FleetWorking(s)
	waiting := len(waitingCards(s))
	idle := width > 0 && waiting > 0 && 2*working < width && !s.FleetOff() // off: no work is dealt it
	since, _ := s.Fleet.Prop(PropIdleSince)
	said, _ := s.Fleet.Prop(PropIdleSaid)
	write := func(name, value string) {
		was, had := s.Fleet.Prop(name)
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: value, Was: was, WasAbsent: !had})
	}
	to := s.Coordinator
	switch {
	case idle && since == "":
		write(PropIdleSince, stamp(s.Now))
		p.Units = append(p.Units, Unit{Key: PartIdle, Moved: fmt.Sprintf("fleet %d/%d with %d waiting: idle since %s", working, width, waiting, stamp(s.Now))})
	case idle && said == "":
		if d, ok := r.running(s.Now, since); !ok || d < IdleWindow {
			return p, 0
		}
		n := happened(NIdle, "", s.Now)
		n.Who, n.To = r.who(), to
		n.What = fmt.Sprintf("fleet %d/%d: %s", working, width, TraceIdle(s, r))
		n.Hint = "run: nova-sprint rules (what the machine answers), nova-sprint inbox (what is yours)"
		write(PropIdleSaid, stamp(s.Now))
		p.Units = append(p.Units, Unit{Key: PartIdle, Notes: []Note{n}, Moved: "the fleet is idle: told " + orDash(to)})
	case !idle && since != "":
		write(PropIdleSince, "")
		write(PropIdleSaid, "")
		u := Unit{Key: PartIdle, Moved: fmt.Sprintf("fleet %d/%d: the idle episode since %s ended", working, width, since)}
		if said != "" {
			n := happened(NIdleCleared, "", s.Now)
			n.Who, n.To = r.who(), to
			took := ""
			if d, ok := r.running(s.Now, since); ok {
				took = " after " + d.Round(time.Second).String()
			}
			n.What = fmt.Sprintf("fleet %d/%d, %d waiting: working again%s", working, width, waiting, took)
			u.Notes = append(u.Notes, n)
		}
		p.Units = append(p.Units, u)
	}
	return p, 0
}

// TickStoreSlow is the store slow alarm, the tick's part beside the idle alarm
// (TickReq.IdleAlarm, run --idle-alarm): the server's measured p50 store round trip
// (TickReq.StoreRTTP50MS, TickReq.StoreRTTFresh) over StoreSlowBar begins an episode
// and, once, says so; the episode is written on the fleet table (PropStoreSlowSince)
// and marked said (PropStoreSlowSaid); under half the bar the episode ends, and a
// note says so when one was pushed. With no fresh record it does nothing
// (docs/SPEC-SPRINT.md section 14, "store-latency-alarm-bb").
func TickStoreSlow(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if !r.IdleAlarm || !r.StoreRTTFresh || s.Fleet == nil {
		return p, 0
	}
	bar := StoreSlowBar.Seconds() * 1000
	slow, cleared := r.StoreRTTP50MS > bar, r.StoreRTTP50MS < bar/2
	since, _ := s.Fleet.Prop(PropStoreSlowSince)
	said, _ := s.Fleet.Prop(PropStoreSlowSaid)
	write := func(name, value string) {
		was, had := s.Fleet.Prop(name)
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: value, Was: was, WasAbsent: !had})
	}
	to := s.Coordinator
	slowUnit := func() Unit {
		n := happened(NStoreSlow, "", s.Now)
		n.Who, n.To = r.who(), to
		n.What = fmt.Sprintf("the store is slow: %g ms", r.StoreRTTP50MS)
		return Unit{Key: PartStoreSlow, Notes: []Note{n}, Moved: "the store is slow: told " + orDash(to)}
	}
	switch {
	case slow && since == "":
		write(PropStoreSlowSince, stamp(s.Now))
		write(PropStoreSlowSaid, stamp(s.Now))
		p.Units = append(p.Units, slowUnit())
	case slow && said == "":
		write(PropStoreSlowSaid, stamp(s.Now))
		p.Units = append(p.Units, slowUnit())
	case cleared && since != "":
		write(PropStoreSlowSince, "")
		write(PropStoreSlowSaid, "")
		u := Unit{Key: PartStoreSlow, Moved: fmt.Sprintf("the store round trip p50 %g ms is under the bar: the slow episode since %s ended", r.StoreRTTP50MS, since)}
		if said != "" {
			n := happened(NStoreSlowCleared, "", s.Now)
			n.Who, n.To = r.who(), to
			n.What = fmt.Sprintf("the store is working again: p50 %g ms", r.StoreRTTP50MS)
			u.Notes = append(u.Notes, n)
		}
		p.Units = append(p.Units, u)
	}
	return p, 0
}

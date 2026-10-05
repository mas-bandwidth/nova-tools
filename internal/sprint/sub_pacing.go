package sprint

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// A subscription friend is paced to her plan's reset (docs/SPEC-SPRINT.md section 1,
// sub-pacingb.w1; docs/SPEC-CONFIG.md, friend; the owner, 2026-10-04: "There is another
// thing, which is PACING and offloading", "for example, if we find we are exhausting the
// rowan buds too quick for the weekly plan", "or grok, or emma, or stella", and "always
// make sure that subs are 100% utilized before spending on fleet"). The model is
// tla/SubPacing.tla (NoDealPastPace, LimitedNeverDealt, LimitedComesBack), each with a
// reversed witness that breaks it (tla/CASES.tsv, subpacing).
//
// A friend whose nova-config row names windows (config.FriendWindows: 5h, weekly, or
// both) is a subscription friend. The sprint keeps one record of her pace on the fleet
// table, PropSubPace(<friend>), as it keeps the rule-3 rests (route_rest.go): never a
// table or a column. Per window: when it started (the last reset seen, or the first
// usage record when none has been), the tokens burnt since (from the usage records of
// her cards as each ends, cost.go: never wall time), and its allowance, the burn of the
// last full window or, before one exists, the burn up to the first limit seen. The
// record also keeps the limits seen (her daemon's down with its until, friend_health.go,
// or a limit a usage reader parsed, usage_reader.go), bounded, and the paced width the
// deal uses for her: the sprint's, never config. The target is to spend evenly to the
// reset: paced burn at t is allowance x elapsed / window length; a friend ahead of pace
// in any window has her paced width lowered one per tick (never under 0), one behind
// has it raised one per tick (never over her configured width). Lowering the width takes
// back nothing she has started: the deal gives her nothing more, and the level
// (friend_level.go) moves her unstarted ready cards to a subscription friend with
// headroom. A friend at her limit is down until the reset: her unstarted cards are
// taken back at once (FriendTake, as friend down does) and dealt again, and at the reset
// she is dealt again and that window restarts. The fleet's paid routes take a card a
// subscription friend covers only when paid_width is on (Snapshot.PaidWidth).

// PropSubPace is the fleet table's property holding one subscription friend's pace
// record (FriendPace): one property per friend, so the table's properties grow with the
// subscription friends and never with the cards.
func PropSubPace(friend string) string { return "sub_pace_" + friend }

// MaxPaceLimits bounds the limits a pace record keeps.
const MaxPaceLimits = 8

// PaceWindow is one window of a friend's plan as the record keeps it.
type PaceWindow struct {
	Word  string    `json:"word"`           // 5h, weekly: the row's word
	Start time.Time `json:"start,omitzero"` // the last reset seen, or the first record; zero until one
	Burn  int64     `json:"burn"`           // tokens of her cards that ended since Start
	Cards int       `json:"cards"`          // records counted since Start
	// Allowance is what the window may spend: the burn of the last full window, or,
	// before one exists, the burn up to the first limit seen; Known says one is set.
	Allowance int64 `json:"allowance"`
	Known     bool  `json:"known"`
}

// Length is the window's length: its word as config.ParseWindows reads it.
func (w PaceWindow) Length() time.Duration {
	d, err := config.ParseWindows(w.Word)
	if err != nil || len(d) != 1 {
		return 0
	}
	return d[0]
}

// Reset is when the window resets: its start plus its length; zero before it started.
func (w PaceWindow) Reset() time.Time {
	if w.Start.IsZero() {
		return time.Time{}
	}
	return w.Start.Add(w.Length())
}

// Paced is the burn the window should be at by now to spend its allowance evenly to
// its reset; 0 before it started or while its allowance is not known.
func (w PaceWindow) Paced(now time.Time) int64 {
	l := w.Length()
	if !w.Known || w.Start.IsZero() || l <= 0 {
		return 0
	}
	elapsed := min(max(now.Sub(w.Start), 0), l)
	return int64(float64(w.Allowance) * (float64(elapsed) / float64(l)))
}

// Ahead says the window's burn is past its pace.
func (w PaceWindow) Ahead(now time.Time) bool { return w.Known && w.Burn > w.Paced(now) }

// PaceLimit is one limit seen: when, until when the friend is down, which window it
// belongs to (its index) and the words.
type PaceLimit struct {
	At     time.Time `json:"at"`
	Until  time.Time `json:"until"`
	Window int       `json:"window"`
	Reason string    `json:"reason,omitempty"`
}

// FriendPace is a subscription friend's pace record (PropSubPace).
type FriendPace struct {
	Windows []PaceWindow `json:"windows"`
	// Width is her paced width, the deal's width for her: at most her configured width,
	// never under 0.
	Width int `json:"width"`
	// Until is the reset of the last limit seen: she is down until then.
	Until time.Time `json:"until,omitzero"`
	// Taken is the until of the limit her unstarted cards were last taken back for.
	Taken  time.Time   `json:"taken,omitzero"`
	Limits []PaceLimit `json:"limits,omitempty"`
}

// Limited says the friend is at her limit at now: down until her reset.
func (p FriendPace) Limited(now time.Time) bool { return !p.Until.IsZero() && now.Before(p.Until) }

// AheadOfPace says her burn is ahead of pace in some window.
func (p FriendPace) AheadOfPace(now time.Time) bool {
	for _, w := range p.Windows {
		if w.Ahead(now) {
			return true
		}
	}
	return false
}

func (p FriendPace) value() string {
	b, _ := json.Marshal(p)
	return string(b)
}

// parsePace reads a record back; ok is false for a value that is not one.
func parsePace(v string) (FriendPace, bool) {
	var p FriendPace
	if v == "" || json.Unmarshal([]byte(v), &p) != nil {
		return FriendPace{}, false
	}
	return p, true
}

// SubPaceOf is the friend's pace record as the fleet table holds it; ok is false when
// there is none.
func SubPaceOf(fleet *Table, friend string) (FriendPace, bool) {
	if fleet == nil {
		return FriendPace{}, false
	}
	v, ok := fleet.Prop(PropSubPace(friend))
	if !ok {
		return FriendPace{}, false
	}
	return parsePace(v)
}

// Subscription says the seat is a subscription friend's: her row names windows.
func (f FriendSeat) Subscription() bool { return len(f.Windows) > 0 }

// paceOf is the friend's record fitted to her seat: read from the fleet table, or new at
// her configured width; its windows are her row's words in order (a word that changed
// starts fresh), and its width is clamped to her configured width.
func paceOf(s *Snapshot, f FriendSeat) FriendPace {
	p, had := SubPaceOf(s.Fleet, f.Name)
	if !had {
		p = FriendPace{Width: f.Width}
	}
	ws := make([]PaceWindow, len(f.Windows))
	for i, word := range f.Windows {
		if i < len(p.Windows) && p.Windows[i].Word == word {
			ws[i] = p.Windows[i]
		} else {
			ws[i] = PaceWindow{Word: word}
		}
	}
	p.Windows = ws
	p.Width = min(max(p.Width, 0), f.Width)
	return p
}

// roll moves the record to now: a window whose reset has passed ends (its burn becomes
// its allowance, and it restarts at its reset, whole lengths on), and a limit whose
// until has passed restarts its window there.
func (p *FriendPace) roll(now time.Time) {
	for i := range p.Windows {
		w := &p.Windows[i]
		l := w.Length()
		if w.Start.IsZero() || l <= 0 {
			continue
		}
		for !now.Before(w.Start.Add(l)) {
			w.Allowance, w.Known = w.Burn, true
			w.Burn, w.Cards = 0, 0
			w.Start = w.Start.Add(l)
		}
	}
	for _, lim := range p.Limits {
		if lim.Window < 0 || lim.Window >= len(p.Windows) || now.Before(lim.Until) {
			continue
		}
		w := &p.Windows[lim.Window]
		if w.Start.Before(lim.Until) {
			// the reset: the window that ended at the limit was a full one
			w.Allowance, w.Known = w.Burn, true
			w.Burn, w.Cards, w.Start = 0, 0, lim.Until
		}
	}
}

// burn adds the tokens of one of her cards that ended at now to every window, starting
// a window at its first record.
func (p *FriendPace) burn(now time.Time, tokens int64) {
	for i := range p.Windows {
		w := &p.Windows[i]
		if w.Start.IsZero() {
			w.Start = now
		}
		w.Burn += tokens
		w.Cards++
	}
}

// limit records a limit seen at now with its until, once (the same until again is the
// same limit): the window it belongs to is the shortest that spans the time to the
// reset, else the longest; before that window has an allowance, its burn so far becomes
// one. She is down until the reset.
func (p *FriendPace) limit(now, until time.Time, reason string) bool {
	if until.IsZero() || !until.After(now) || p.Until.Equal(until) {
		return false
	}
	wi := len(p.Windows) - 1
	for i, w := range p.Windows {
		if until.Sub(now) <= w.Length() {
			wi = i
			break
		}
	}
	if wi >= 0 {
		w := &p.Windows[wi]
		if w.Start.IsZero() {
			w.Start = now
		}
		if !w.Known {
			w.Allowance, w.Known = w.Burn, true
		}
	}
	p.Until = until
	p.Limits = append(p.Limits, PaceLimit{At: now, Until: until, Window: wi, Reason: reason})
	if len(p.Limits) > MaxPaceLimits {
		p.Limits = p.Limits[len(p.Limits)-MaxPaceLimits:]
	}
	return true
}

// pacePass is the tick's pacing of the subscription friends (subPace): the seats as the
// deal and the level see them, and what the tick writes for it.
type pacePass struct {
	seats   []FriendSeat
	props   []PropWrite
	units   []Unit
	refused []Refusal
	notes   []Note
}

// subPace is the tick's pacing pass, before its deal (TickDeal): for each subscription
// friend her record is fitted, her daemon's limit (down with its until) recorded, the
// record rolled to now, and then either she is at her limit, so her seat is down for the
// deal and her unstarted cards are taken back once per limit (FriendTake, Limit), or her
// paced width moves one toward her pace: down while she is ahead in any window, up
// while she is behind, and her seat's width is the paced one. A record that changed is
// written on the fleet table, guarded on the value read. A friend with no windows is
// unpaced: her seat is as it was.
func subPace(s *Snapshot, seats []FriendSeat, who string) pacePass {
	out := pacePass{seats: make([]FriendSeat, len(seats))}
	copy(out.seats, seats)
	for i, f := range out.seats {
		if !f.Subscription() {
			continue
		}
		was, had := s.Fleet.Prop(PropSubPace(f.Name))
		p := paceOf(s, f)
		if f.Status == Down && !f.Until.IsZero() {
			p.limit(s.Now, f.Until, f.Reason)
		}
		p.roll(s.Now)
		f.Configured = f.Width
		switch {
		case p.Limited(s.Now):
			f.Status = Down
			if !p.Taken.Equal(p.Until) {
				p.Taken = p.Until
				tp := FriendTake(s, FriendTakeReq{Friend: f.Name, All: true, Limit: true, Reason: "at her limit until " + stamp(p.Until), Who: who, Started: paceStarted(s, f)})
				out.units = append(out.units, tp.Units...)
				out.refused = append(out.refused, tp.Refused...)
				n := happened(NFriendLimited, "", s.Now)
				n.Who, n.To = who, s.Coordinator
				n.What = fmt.Sprintf("friend %s is at her limit until %s: %d unstarted cards taken back for the friends' deal; she is dealt again at the reset", f.Name, stamp(p.Until), len(tp.Units))
				out.notes = append(out.notes, n)
			}
		case p.AheadOfPace(s.Now):
			p.Width = max(p.Width-1, 0)
		default:
			p.Width = min(p.Width+1, f.Width)
		}
		f.Width = p.Width
		f.Pace = &p
		out.seats[i] = f
		if v := p.value(); !had || v != was {
			out.props = append(out.props, PropWrite{Table: Fleet, Name: PropSubPace(f.Name), Value: v, Was: was, WasAbsent: !had})
		}
	}
	return out
}

// NFriendLimited is the happened note of a friend at her limit, to the coordinator.
const NFriendLimited = "a friend is at her limit: her unstarted cards taken back"

// paceStarted is the cards on the friend's row she has started, by the store's own data
// (friendStarted), each with its why: what a limit's take-back leaves with her.
func paceStarted(s *Snapshot, f FriendSeat) map[string]string {
	out := map[string]string{}
	row := FriendRow(f.Name)
	for _, c := range append(append([]*Card(nil), s.Fleet.Cell(row, Ready)...), s.Fleet.Cell(row, Working)...) {
		if friendStarted(s, f, c) {
			out[c.ID] = "she has started it (her beat names it running, or progress was stamped)"
		}
	}
	return out
}

// paceBurns is the burn of the cards one step ends, by friend, before its props are
// written (paceWrites): the record is read once per friend and added to for each card.
type paceBurns struct {
	paces map[string]*FriendPace
	was   map[string]string
	had   map[string]bool
}

// burn adds the tokens of one card of the friend that ends at now; a friend with no
// windows, or not on the step's seats, burns nothing. Its record is read from the fleet
// table on the first card.
func (b *paceBurns) burn(s *Snapshot, friend string, tokens int64) {
	if tokens <= 0 {
		return
	}
	var seat FriendSeat
	for _, f := range s.Friends {
		if f.Name == friend {
			seat = f
		}
	}
	if !seat.Subscription() {
		return
	}
	if b.paces == nil {
		b.paces, b.was, b.had = map[string]*FriendPace{}, map[string]string{}, map[string]bool{}
	}
	p := b.paces[friend]
	if p == nil {
		b.was[friend], b.had[friend] = s.Fleet.Prop(PropSubPace(friend))
		x := paceOf(s, seat)
		p = &x
		b.paces[friend] = p
	}
	p.roll(s.Now)
	p.burn(s.Now, tokens)
}

// limit records a limit the run's text named (FinishReq.LimitUntil) on the friend's
// record: a subscription friend is at her limit until then, and the next tick takes her
// unstarted cards back (subPace). A zero until, or a friend with no windows, records
// nothing.
func (b *paceBurns) limit(s *Snapshot, friend string, until time.Time, reason string) {
	if until.IsZero() {
		return
	}
	var seat FriendSeat
	for _, f := range s.Friends {
		if f.Name == friend {
			seat = f
		}
	}
	if !seat.Subscription() {
		return
	}
	if b.paces == nil {
		b.paces, b.was, b.had = map[string]*FriendPace{}, map[string]string{}, map[string]bool{}
	}
	p := b.paces[friend]
	if p == nil {
		b.was[friend], b.had[friend] = s.Fleet.Prop(PropSubPace(friend))
		x := paceOf(s, seat)
		p = &x
		b.paces[friend] = p
	}
	p.roll(s.Now)
	p.limit(s.Now, until, reason)
}

// writes is the property writes of every record that changed, in name order.
func (b *paceBurns) writes() []PropWrite {
	var out []PropWrite
	for _, name := range slices.Sorted(maps.Keys(b.paces)) {
		v := b.paces[name].value()
		if !b.had[name] || v != b.was[name] {
			out = append(out, PropWrite{Table: Fleet, Name: PropSubPace(name), Value: v, Was: b.was[name], WasAbsent: !b.had[name]})
		}
	}
	return out
}

// subCovers says a subscription friend of the seats covers the tier: one of her tiers,
// whatever her status or room, so with paid_width off a card of that tier waits for the
// subscription friends and never goes to a paid route (docs/SPEC-SPRINT.md section 1,
// sub-pacingb.w1; tla/SubPacing.tla, NoDealPastPace).
func subCovers(seats []FriendSeat, tier string) bool {
	for _, f := range seats {
		if f.Subscription() && friendTakes(f, tier) {
			return true
		}
	}
	return false
}

// subHeadroom says a subscription friend of the seats is up with room for a card of the
// tier under her paced width (friendRoom): with paid_width on, the fleet takes such a
// card only when none has.
func subHeadroom(s *Snapshot, seats []FriendSeat, tier string) bool {
	for _, f := range seats {
		if !f.Subscription() || f.Status != Up || !friendTakes(f, tier) {
			continue
		}
		if room, _ := friendRoom(f); room-friendLoad(s, f.Name) > 0 {
			return true
		}
	}
	return false
}

// PaceWindowView is one window as view coordinator and where --json show it.
type PaceWindowView struct {
	Window    string    `json:"window"`
	Burn      int64     `json:"burn"`
	Target    int64     `json:"target"`
	Allowance int64     `json:"allowance"`
	Reset     time.Time `json:"reset,omitzero"`
	Ahead     bool      `json:"ahead"`
}

// FriendPaceView is a subscription friend's pace as it is shown: each window's burn
// against its paced target, her paced width over her configured width, and her reset.
type FriendPaceView struct {
	Friend     string           `json:"friend"`
	Windows    []PaceWindowView `json:"windows"`
	Width      int              `json:"width"`
	Configured int              `json:"configured"`
	Until      time.Time        `json:"until,omitzero"`
	Limited    bool             `json:"limited"`
}

// PaceView is the friend's pace as shown at now: her record rolled to now, or, with no
// record yet, her windows unstarted at her configured width.
func PaceView(fleet *Table, name string, windows []string, width int, now time.Time) FriendPaceView {
	p := paceOf(&Snapshot{Fleet: fleet}, FriendSeat{Name: name, Width: width, Windows: windows})
	p.roll(now)
	v := FriendPaceView{Friend: name, Width: p.Width, Configured: width, Until: p.Until, Limited: p.Limited(now), Windows: []PaceWindowView{}}
	for _, w := range p.Windows {
		v.Windows = append(v.Windows, PaceWindowView{Window: w.Word, Burn: w.Burn, Target: w.Paced(now), Allowance: w.Allowance, Reset: w.Reset(), Ahead: w.Ahead(now)})
	}
	return v
}

// Line is the view's one line for the friend: PACE <friend> <window> burn=<n>/<target> ...
// width=<paced>/<configured> reset=<in>; a window not started says so, and a limited
// friend her until.
func (v FriendPaceView) Line(now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PACE %s", v.Friend)
	for _, w := range v.Windows {
		fmt.Fprintf(&b, " %s burn=%d/%d", w.Window, w.Burn, w.Target)
		if w.Ahead {
			b.WriteString(" ahead")
		}
	}
	fmt.Fprintf(&b, " width=%d/%d", v.Width, v.Configured)
	if v.Limited {
		fmt.Fprintf(&b, " limited until=%s (in %s)", stamp(v.Until), v.Until.Sub(now).Round(time.Second))
		return b.String()
	}
	var next time.Time
	for _, w := range v.Windows {
		if !w.Reset.IsZero() && (next.IsZero() || w.Reset.Before(next)) {
			next = w.Reset
		}
	}
	if next.IsZero() {
		b.WriteString(" reset=none (no record yet)")
	} else {
		fmt.Fprintf(&b, " reset=%s (in %s)", stamp(next), next.Sub(now).Round(time.Second))
	}
	return b.String()
}

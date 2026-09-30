package sprint

import (
	"fmt"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
)

// A fleet member's presence (docs/SPEC-SPRINT.md, the fleet). A member says
// it is there by beating: nova-sprint fleet beat, run on the machine every
// few seconds, writes its last beat time and its measured load. Its status is
// derived, never typed: up while its last beat is within BeatDeadline, down
// past it or when it has never beaten, and held while the coordinator holds
// it down (fleet down; fleet up releases the hold), whatever it beats. The
// tick applies a change of status with the moves of fleet down and fleet up.

const (
	// BeatDeadline is how long a member stays up after its last beat.
	BeatDeadline = 15 * time.Second
	// LoadWindow is the span of beats whose highest load the load cell shows.
	LoadWindow = 10 * time.Second
)

// Held is the status of a member the coordinator holds down.
const Held = "held"

// HowGiven is a load given on the command line rather than measured.
const HowGiven = "given"

// LoadSample is one beat's load, a percent of all the machine's cores.
type LoadSample struct {
	At  time.Time `json:"at"`
	Pct float64   `json:"pct"`
}

// Beat is a member's presence record: its last beat, to the second, the
// highest load of its beats within LoadWindow of it, how the last one was
// measured, those beats' loads, and the measuring state the next beat starts
// from.
type Beat struct {
	At      time.Time      `json:"at"`
	Load    float64        `json:"load"`
	How     string         `json:"how,omitempty"`
	Samples []LoadSample   `json:"samples,omitempty"`
	Meter   hostload.State `json:"meter"`
}

// Beaten says the member has beaten at least once.
func (b Beat) Beaten() bool { return !b.At.IsZero() }

// Fresh says the last beat is within BeatDeadline of now.
func (b Beat) Fresh(now time.Time) bool {
	return b.Beaten() && now.Sub(b.At) <= BeatDeadline
}

// NextBeat is the record after a beat at now with the load pct: the beat at
// the second, the samples of the last LoadWindow with this one in place of any
// of the same second, and their highest as the load. Two beats in the same
// second with the same load write the same record.
func NextBeat(prev Beat, now time.Time, pct float64, how string, meter hostload.State) Beat {
	at := now.UTC().Truncate(time.Second)
	b := Beat{At: at, How: how, Meter: meter}
	for _, s := range prev.Samples {
		if s.At.After(at.Add(-LoadWindow)) && s.At.Before(at) {
			b.Samples = append(b.Samples, s)
		}
	}
	b.Samples = append(b.Samples, LoadSample{At: at, Pct: float64(int64(pct*10+0.5)) / 10})
	for _, s := range b.Samples {
		b.Load = max(b.Load, s.Pct)
	}
	return b
}

// MemberStatus is a member's derived status at now: held while the
// coordinator holds it, else up while its beat is fresh, else down.
func MemberStatus(ctl *Card, b Beat, now time.Time) string {
	switch {
	case ctl.F("held") != "":
		return Held
	case b.Fresh(now):
		return Up
	}
	return Down
}

// LoadText is the load cell: the highest load of the last LoadWindow with
// one decimal and a percent sign while the beat is fresh, else empty.
func LoadText(b Beat, now time.Time) string {
	if !b.Fresh(now) {
		return ""
	}
	return fmt.Sprintf("%.1f%%", b.Load)
}

// TickPresence applies the changes of the members' derived status (T0), every
// member whose status changes in the one plan (the design's R1 and R2, v2.1
// section 2.3: each member seen or down is its own key, none waits behind
// another's; the owner's rule, errata 3 amendment 10: every row of every table
// moves every tick, never a row at a time). Every member that should be down
// and is up goes down, its unfinished work cards dealt round the members up
// after the plan (downPlan: the loads and the rolling index shared across
// every down member, so the cards of all of them go round the fleet together)
// or withdrawn when none is; every member that should be up and is not comes
// up, up to TickMaxMoves of them. With no member going down, the first up
// levels the ready queues once over every member up after the plan; with
// downs, the level part of the same tick evens the queues after the deal. So
// a fleet that beats before start is up, whole, at the first tick, and a
// fleet that loses several machines at once redeals all their cards in that
// tick. The binding gives it the beats; with none given it does nothing.
func TickPresence(s *Snapshot, r TickReq) (Plan, int) {
	return presence(s, r) // the rest are due: the next ticks apply them
}

func presence(s *Snapshot, r TickReq) (Plan, int) {
	if r.Beats == nil {
		return Plan{}, 0
	}
	var live, ups, downs []string
	for _, m := range s.Fleet.Rows() {
		ctl := s.MemberCtl(m)
		if ctl == nil {
			continue
		}
		want := MemberStatus(ctl, r.Beats[m], s.Now) == Up
		have := ctl.F("status") == Up
		switch {
		case want && have:
			live = append(live, m)
		case want:
			ups = append(ups, m)
		case have:
			downs = append(downs, m)
		}
	}
	if len(ups) == 0 && len(downs) == 0 {
		return Plan{}, 0
	}
	n := min(len(ups), TickMaxMoves)
	// every member up after the plan: the receivers of the downs' cards, and
	// the members the first up levels over
	all := append(append([]string(nil), live...), ups[:n]...)
	// every placement goes round the fleet from the deal's index and moves it
	// (round.go, errata 3 amendment 5), written with the plan
	rr := dealRoundWith(s, all...)
	moves := roundMoves{}
	var p Plan
	add := func(q Plan) {
		p.Rows = append(p.Rows, q.Rows...)
		p.Units = append(p.Units, q.Units...)
		p.Refused = append(p.Refused, q.Refused...)
	}
	receivers := orderLike(s.Fleet.Rows(), all, "")
	q, widths := memberLoads(s, receivers), memberWidths(s, receivers)
	for _, m := range downs {
		why := "no beat for " + BeatDeadline.String()
		switch {
		case s.MemberCtl(m).F("held") != "":
			why = "held"
		case !r.Beats[m].Beaten():
			why = "it has never beaten"
		}
		add(downPlan(s, FleetReq{Op: "down", Member: m, Who: r.who(), Live: receivers, Why: why}, receivers, rr, moves, q, widths))
	}
	for i, m := range ups[:n] {
		// the first up levels the queues over every member up after the plan
		// when no member went down; the others are their control cards and
		// notifications alone
		levelWith := []string{}
		if i == 0 && len(downs) == 0 {
			levelWith = all
		}
		add(fleetStepPlan(s, FleetReq{Op: "up", Member: m, Who: r.who(), Live: levelWith, Why: "it beats"}, rr, moves))
	}
	p = Lawful(p)
	roundWrites(&p, rr, moves)
	return p, len(ups) - n
}

// StrangerNotes is the plan that tells the coordinator of each unknown machine
// that beats and was not told of yet: one happened notification for each name,
// in name order, none for a machine that has a row in the fleet by now.
func StrangerNotes(s *Snapshot, names []string) Plan {
	var p Plan
	for _, m := range slices.Sorted(slices.Values(names)) {
		if s.Fleet.HasRow(m) {
			continue
		}
		p.Notes = append(p.Notes, Note{Kind: Happened, Type: NUnknownMachine, Who: MachineActor, At: s.Now,
			What: "an unknown machine is beating: " + m + "; add it with nova-sprint fleet up " + m})
	}
	return p
}

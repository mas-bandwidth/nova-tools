package sprint

import (
	"fmt"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
)

// A fleet member's presence (docs/SPEC-SPRINT.md, the fleet). A member says
// it is there by beating: nova-sprint fleet beat, run on the machine every
// few seconds, writes its last beat time and its measured load. Its status is
// derived, never typed: up until it has missed MissedBeatsDown beat windows
// of BeatDeadline each in a row, down past that or when it has never beaten,
// and held while the coordinator holds it down (fleet down; fleet up releases
// the hold), whatever it beats. The tick applies a change of status with the
// moves of fleet down and fleet up. The model is tla/DirtyTick.tla (Miss and
// Lapse: a machine lapses only after MissedBeatsDown misses).

const (
	// BeatDeadline is how long a beat is fresh, and so the length of one beat
	// window: a window with no beat after the last is one missed beat.
	BeatDeadline = 15 * time.Second
	// MissedBeatsDown is how many beat windows in a row a member misses before
	// it is down: one missed beat is a store round trip that timed out or a
	// slow machine, and marking a working member down on it withdrew its card
	// and lost the child's finish (the member's beat is one store call of
	// several seconds on a tailnet); three in a row is a member that is gone.
	// A beat between misses resets the count: it is derived from the last
	// beat, never stored.
	MissedBeatsDown = 3
	// LoadWindow is the span of beats whose highest load the load cell shows.
	LoadWindow = 10 * time.Second
	// FriendBeatEvery is how often a friend's daemon beats (friend beat). The
	// beat is recorded and shown (her report, her load, its age), and it never
	// makes her up: it proves a daemon, not a session (FriendStatus).
	FriendBeatEvery = time.Second
	// FriendProofLive is how old the session proof her beat carries may be while she is
	// up (docs/SPEC-FRIEND.md, The push proof): her daemon asks the session eight
	// minutes after its last ask and waits up to five for the answer (nova-friend's
	// ProveEvery and SessionBound), so a session that answers is never proved longer
	// ago than thirteen minutes, inside this window. The owner, 2026-10-05: "nova-bus is useless if the friend using it
	// is deaf and is not listening to messages sent back."
	FriendProofLive = 15 * time.Minute
	// FriendPongWindow is how long a wake ping her session answered keeps her
	// up, and FriendFinishWindow how long a card of hers finished (working to
	// done) does (docs/SPEC-FRIEND.md, "Presence is her session's evidence").
	// The owner, 2026-10-05 ~9:30 AM ET: "there is no value in things that are
	// answered just by the daemon"; a friend is up only when her session
	// answered a wake ping within ten minutes or she finished a card within
	// thirty. On 2026-10-04 friends read up for hours on beats sent for them
	// while their sessions took no turn.
	FriendPongWindow   = 10 * time.Minute
	FriendFinishWindow = 30 * time.Minute
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
// measured, those beats' loads, the measuring state the next beat starts
// from, and the machine's logical cores, which a member with the default width
// takes half of (WidthOfCores; fleet sync).
type Beat struct {
	At      time.Time      `json:"at"`
	Load    float64        `json:"load"`
	Cores   int            `json:"cores,omitempty"`
	How     string         `json:"how,omitempty"`
	Samples []LoadSample   `json:"samples,omitempty"`
	Meter   hostload.State `json:"meter"`
	// Friend is what a friend's beat reports of her work (friend beat); nil on a
	// machine's beat.
	Friend *FriendReport `json:"friend,omitempty"`
	// Proof is a friend's session's last proof as her beat carried it (friend beat
	// --pong: her session's answer to a SESSION CHECK, or its own bus message), zero
	// when her beat carried none; the friend beat record keeps it under "pong".
	Proof time.Time `json:"pong,omitzero"`
	// StopReturns is how many stop-returns a member's lanes still owe after the machine's
	// stop (fleet beat --stop-returns; docs/SPEC-SPRINT.md section 14, stop cancels jobs):
	// start waits for zero. A friend's count is her report's (FriendReport.StopReturns).
	StopReturns int `json:"stop_returns,omitempty"`
	// NoRoom is a member's or reader's word that it starts no card, and why (fleet beat or
	// queue --no-room: its free disk under its floor, its usage source's rest); "" while it
	// starts cards. While its beat is fresh the deal gives it no card and the ask asks it no
	// read (NoRoomNow), so a card is not dealt to a machine that hands it straight back.
	NoRoom string `json:"no_room,omitempty"`
}

// NoRoomNow is the word of a beat fresh at now that its member or reader starts no card,
// "" when it says none or is not fresh.
func NoRoomNow(b Beat, now time.Time) string {
	if !b.Fresh(now) {
		return ""
	}
	return b.NoRoom
}

// FriendReport is what a friend's machinery reports with her beat, as a machine's beat
// reports its load (the beat's Load, given): the cards she is running (work card ids or her
// job names), which friend take and friend down leave with her (FriendTake), and her own
// counts as her daemon keeps them (working, queued, her width), each absent when not
// reported. They are her word, shown beside the table's counts, which stay the sprint's
// own (her row's cards) and her width the roster's; her last activity is the file system's.
type FriendReport struct {
	Running []string `json:"running,omitempty"`
	Working *int     `json:"working,omitempty"`
	Queue   *int     `json:"queue,omitempty"`
	Width   *int     `json:"width,omitempty"`
	// StopReturns is how many stop-returns her lanes still owe after the machine's stop
	// (friend beat --stop-returns; docs/SPEC-SPRINT.md section 14, stop cancels jobs):
	// start waits for zero. Absent when not reported.
	StopReturns *int `json:"stop_returns,omitempty"`
	// Active is the newest write under her working directory and outbox as her daemon
	// last walked them (friend beat --active), zero when it reported none: the signal that
	// her session moves, which a daemon pong does not say (docs/SPEC-FRIEND.md, last
	// session activity).
	Active time.Time `json:"active,omitzero"`
	// Paced and Window are her lanes' effective width under her subscription windows' pacing
	// and those windows' use as her harness last reported it ("5h 62% 7d 31%"), as her daemon
	// last read them (docs/SPEC-FRIEND.md, subscription pacing); absent when it reported none.
	Paced  *int   `json:"paced,omitempty"`
	Window string `json:"window,omitempty"`
	// Until and Reason are her daemon's word that she is down until then and why (friend
	// beat --until --reason: her harness at its usage limit or out of credits,
	// docs/SPEC-FRIEND.md, limits-mean-down-w-r5.w1~15); zero and empty while she is up.
	Until  time.Time `json:"until,omitzero"`
	Reason string    `json:"reason,omitempty"`
	// Build, Started and Present are her daemon's own facts (friend beat --build --started
	// --present): the build it runs (its version line's build), when it started, which is its
	// generation, and when it sent her the present on its start (the note that snaps her to
	// now and skips what is old); each absent when it reported none. A start the status
	// transitions have not seen is a new generation of her daemon (StatusTransitions).
	Build   string    `json:"build,omitempty"`
	Started time.Time `json:"started,omitzero"`
	Present time.Time `json:"present,omitzero"`
}

// SaysDown says the beat is her daemon's word that she is down (FriendReport.Until):
// however fresh, it never makes her up.
func (b Beat) SaysDown() bool { return b.Friend != nil && !b.Friend.Until.IsZero() }

// Beaten says the member has beaten at least once.
func (b Beat) Beaten() bool { return !b.At.IsZero() }

// Fresh says the last beat is within BeatDeadline of now.
func (b Beat) Fresh(now time.Time) bool {
	return b.Beaten() && now.Sub(b.At) <= BeatDeadline
}

// Missed is how many beat windows of BeatDeadline went by after the last beat
// with no beat: 0 while it is fresh, 1 until two windows have passed, and so
// on; a member that never beat has missed none (it is down by never beating).
func (b Beat) Missed(now time.Time) int {
	if !b.Beaten() || b.Fresh(now) {
		return 0
	}
	return int((now.Sub(b.At) - 1) / BeatDeadline)
}

// Alive says the member has beaten and has missed fewer than MissedBeatsDown
// beat windows in a row: the rule of the model's Lapse.
func (b Beat) Alive(now time.Time) bool {
	return b.Beaten() && b.Missed(now) < MissedBeatsDown
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
// coordinator holds it, else up while it has missed fewer than
// MissedBeatsDown beat windows, else down.
func MemberStatus(ctl *Card, b Beat, now time.Time) string {
	return PresenceStatus(ctl.F("held") != "", b, now)
}

// PresenceStatus is the one rule of a fleet member's status at now: held while
// the coordinator holds it (fleet down), else up while it has missed fewer
// than MissedBeatsDown beat windows of BeatDeadline, else down (never beaten,
// or lapsed).
func PresenceStatus(held bool, b Beat, now time.Time) string {
	switch {
	case held:
		return Held
	case b.Alive(now):
		return Up
	}
	return Down
}

// FriendStatus is the one rule of a friend's status at now: held while the
// coordinator holds her (friend down); else down while her last beat says so
// (SaysDown: her harness at its limit, until when and why; a beat can say down,
// never up); else up only on evidence from her own session within its window
// (FriendEvidence): a wake ping her session answered
// (the coordinator's friend health --state up, under the current seat
// generation) under FriendPongWindow old, or her session's answer to a check her
// daemon asked, proved on her beat (friend beat --check, then --pong naming its
// nonce; ProveBeat; Beat.Proof) under FriendProofLive old while her beat is
// fresh, or a card of hers finished under FriendFinishWindow old;
// else down. Her beat itself, whoever sends it, is never evidence: a daemon or a
// loop beating for her says an app is open, not that her session can work; only
// the session's answer it carries is. Releasing a hold (friend up) is no
// evidence either.
func FriendStatus(f FriendPresence, now time.Time) string {
	status, _ := FriendEvidence(f, now)
	return status
}

// FriendEvidence is FriendStatus with the evidence it rests on, as her row
// names it: for up, the evidence and its age ("session pong 3m0s ago",
// "finish 12m0s ago"); for down, what is missing, each with the age of the
// last one seen, and the beat's age when she beats, so a row read down while
// her daemon beats says why; for a beat that says down, until when and why;
// for held, "held".
func FriendEvidence(f FriendPresence, now time.Time) (string, string) {
	if f.Held {
		return Held, "held"
	}
	if f.Beat.SaysDown() {
		return Down, beatSaysDownWhy(f.Beat)
	}
	pong := !f.Health.Seen.IsZero() && f.Health.State == Up && f.Health.Generation == f.Generation
	if age := now.Sub(f.Health.Seen); pong && age >= 0 && age < FriendPongWindow {
		return Up, "session pong " + ago(age)
	}
	// the proof her beat record keeps is an answer to a check her daemon asked (ProveBeat),
	// and it counts only while her beat is fresh: a daemon that stopped proves nothing more
	proof := !f.Beat.Proof.IsZero()
	if age := now.Sub(f.Beat.Proof); proof && age >= 0 && age < FriendProofLive && f.Beat.Fresh(now) {
		return Up, "session proof " + ago(age)
	}
	if age := now.Sub(f.Finished); !f.Finished.IsZero() && age >= 0 && age < FriendFinishWindow {
		return Up, "finish " + ago(age)
	}
	why := "no session evidence: no wake ping answered by her session within " + FriendPongWindow.String()
	if pong {
		why += " (last " + ago(now.Sub(f.Health.Seen)) + ")"
	}
	why += ", no session proof on her beat within " + FriendProofLive.String()
	if proof {
		why += " (last " + ago(now.Sub(f.Beat.Proof))
		if !f.Beat.Fresh(now) {
			why += ", her beat stopped " + ago(now.Sub(f.Beat.At))
		}
		why += ")"
	}
	why += ", no card finished within " + FriendFinishWindow.String()
	if !f.Finished.IsZero() {
		why += " (last " + ago(now.Sub(f.Finished)) + ")"
	}
	if f.Beat.Beaten() {
		why += "; her beat " + ago(now.Sub(f.Beat.At)) + " is not evidence"
	}
	return Down, why
}

// beatSaysDownWhy is why a beat that says down puts her down (SaysDown): until
// when, and the reason her daemon gave.
func beatSaysDownWhy(b Beat) string {
	why := "her beat says down until " + b.Friend.Until.UTC().Format(time.RFC3339)
	if b.Friend.Reason != "" {
		why += ": " + b.Friend.Reason
	}
	return why
}

// ago is an age as a row says it, to the second: "3m0s ago", or "in the
// future" for a stamp after now, which is no evidence.
func ago(d time.Duration) string {
	if d < 0 {
		return "in the future"
	}
	return d.Truncate(time.Second).String() + " ago"
}

// LoadText is the load cell: the highest load of the last LoadWindow with
// one decimal and a percent sign while the beat is fresh, else empty; and
// beside it, while the machine's open file descriptors are over the member's
// warn bound, "fds <count> warn" (or alarm, fd.go FilesText), so the fleet
// table shows a machine running out of them before the alarm's judgment.
func LoadText(b Beat, now time.Time) string {
	if !b.Fresh(now) {
		return ""
	}
	load := fmt.Sprintf("%.1f%%", b.Load)
	if f := FilesText(b, now); f != "" {
		load += " fds " + f
	}
	return load
}

// TickPresence applies the changes of the members' derived status (T0), every
// member whose status changes in the one plan (the design's R1 and R2, v2.1
// section 2.3: each member seen or down is its own key, none waits behind
// another's; the owner's rule: every row of every table
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
// tick. The binding gives it the beats; with none given it does nothing. Each
// status that changed, a member's or a friend's, raises its one judgment in the
// same plan (StatusTransitions, judgments_status.go).
func TickPresence(s *Snapshot, r TickReq) (Plan, int) {
	p, due := presence(s, r) // the rest are due: the next ticks apply them
	t := StatusTransitions(s, r)
	p.Units = append(p.Units, t.Units...)
	p.Notes = append(p.Notes, t.Notes...)
	p.Closes = append(p.Closes, t.Closes...)
	p.Updates = append(p.Updates, t.Updates...)
	p.Props = append(p.Props, t.Props...)
	return p, due
}

func presence(s *Snapshot, r TickReq) (Plan, int) {
	if r.Beats == nil {
		return Plan{}, 0
	}
	var live, ups, downs []string
	for _, m := range s.Members() {
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
	// (round.go), written with the plan
	rr := dealRoundWith(s, all...)
	moves := roundMoves{}
	var p Plan
	add := func(q Plan) {
		p.Rows = append(p.Rows, q.Rows...)
		p.Units = append(p.Units, q.Units...)
		p.Refused = append(p.Refused, q.Refused...)
	}
	receivers := orderLike(s.Members(), all, "")
	q, widths := memberLoads(s, receivers), memberWidths(s, receivers)
	for _, m := range downs {
		why := "no beat for " + (MissedBeatsDown * BeatDeadline).String()
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

// FriendDownWhy is why FriendStatus does not say up at now, in the words a take refused
// for her names (takeOne): held by the coordinator; her beat says down, until when and why; or the session evidence she lacks as
// FriendEvidence names it (her beat is never evidence). "" while she is up.
func FriendDownWhy(f FriendPresence, now time.Time) string {
	if f.Held {
		return "held by the coordinator (friend down)"
	}
	word, why := FriendEvidence(f, now)
	if word == Up {
		return ""
	}
	return why
}

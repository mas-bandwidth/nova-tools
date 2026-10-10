package sprint

import (
	"fmt"
	"slices"
	"strings"
)

// The coordinator's hold (docs/SPEC-SPRINT.md section 11, hold and unhold): one verb
// pair for the four things that take cards, a fleet member, a reader, a friend and a
// stream. A hold takes no new cards; what is dealt and not begun is handed back now
// (a member's ready cards dealt round the fleet, a reader's reads asked and not begun
// asked of another, a stream's ready work cards withdrawn); what is begun finishes, or
// with --return is handed back now too. A held friend keeps no card, --return or
// not (FriendTake with Hold: her started ones carry their pushed head to the next taker). The status reads held, the reason beside it,
// and the hold is a line of the log. The member's and the stream's hold live on their
// control cards; the reader's and the friend's, as their presence does, in their
// records outside the tables (store/readers.go, store/friends.go), which the store
// writes after this step commits.

// What a hold names.
const (
	HoldMember = "member"
	HoldReader = "reader"
	HoldFriend = "friend"
	HoldStream = "stream"
)

// FieldHeldReason is a held member's or stream's control card's reason for its hold,
// and FieldHeldFinish marks a member's hold that lets its working cards finish (hold
// with no --return): the rebalance's sweep leaves those cards where they are.
const (
	FieldHeldReason = "held_reason"
	FieldHeldFinish = "held_finish"
)

// HoldReq is one hold or unhold of names, each a fleet member, a reader, a friend or a
// stream (docs/SPEC-SPRINT.md section 11).
type HoldReq struct {
	Names []string
	// Release is unhold: each name's hold is cleared.
	Release bool
	// Return hands back what each name holds begun, now: a member's working cards dealt
	// round the fleet, a reader's reads begun asked of another, a friend's cards and a
	// stream's working cards withdrawn to ready. Without it they finish.
	Return bool
	// Kind, when set, is the one kind the names may name (the old verbs, fleet down,
	// reader away and friend down, alias hold for theirs): a name of another kind, or
	// of none, is refused in the old verb's words.
	Kind   string `json:",omitempty"`
	Reason string
	Who    string
	// Friends is the friend roster's names (nova-config's friend rows): the snapshot
	// holds no table of them.
	Friends []string `json:",omitempty"`
	// Alive, with Release, is the members whose beat is alive now: one comes up at once.
	Alive []string `json:",omitempty"`
	// Started is the work cards the friends held have started, as friend down reads them
	// (a push on the branch at its tip, her beat naming it running), each with its why: the
	// hold takes them too, and a push's head is carried (FriendTake). It is read state, so
	// no part of the step's arguments.
	Started map[string]string `json:"-"`
}

// HoldTarget is a name of a hold and what it names.
type HoldTarget struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// HoldView is one hold in force, for where --json and handover: what is held, why, by
// whom and since when.
type HoldView struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
	By     string `json:"by,omitempty"`
	At     string `json:"at,omitempty"`
	// Return says the hold handed back the work begun (--return); else that work
	// finishes.
	Return bool `json:"return,omitempty"`
}

// HoldTargets is what each name names: a fleet member, a reader, a friend or a stream;
// a name that names none, or more than one, is refused naming every kind it matched.
func HoldTargets(s *Snapshot, r HoldReq) ([]HoldTarget, []Refusal) {
	var out []HoldTarget
	var refused []Refusal
	seen := map[string]bool{}
	for _, n := range r.Names {
		if seen[n] {
			continue
		}
		seen[n] = true
		var kinds []string
		if !IsFriendRow(n) && s.MemberCtl(n) != nil {
			kinds = append(kinds, HoldMember)
		}
		if s.Readers != nil && s.Readers.HasRow(n) {
			kinds = append(kinds, HoldReader)
		}
		if slices.Contains(r.Friends, n) {
			kinds = append(kinds, HoldFriend)
		}
		if s.StreamCtl(n) != nil {
			kinds = append(kinds, HoldStream)
		}
		switch {
		case r.Kind != "" && !slices.Contains(kinds, r.Kind):
			refused = append(refused, Refusal{n, "no " + holdKindWords[r.Kind] + " " + n + holdElsewhere(n, kinds, r.Release)})
		case r.Kind != "":
			out = append(out, HoldTarget{Name: n, Kind: r.Kind})
		case len(kinds) == 0:
			refused = append(refused, Refusal{n, "names no fleet member, reader, friend or stream of this sprint: nova-sprint where --all shows them"})
		case len(kinds) == 1:
			out = append(out, HoldTarget{Name: n, Kind: kinds[0]})
		default:
			refused = append(refused, Refusal{n, "names a " + strings.Join(kinds, " and a ") + ": a hold names one; rename one of them"})
		}
	}
	return out, refused
}

// holdKindWords is each kind a hold names, in words.
var holdKindWords = map[string]string{HoldMember: "fleet member", HoldReader: "reader", HoldFriend: "friend", HoldStream: "stream"}

// holdElsewhere is the rest of a kind's refusal for a name of another kind: what it is
// and the verb that holds or releases it (fleet hold for a member, friend hold for a
// friend, hold for a reader or a stream); "" for a name of no kind.
func holdElsewhere(n string, kinds []string, release bool) string {
	if len(kinds) != 1 {
		return ""
	}
	verb := map[string]string{HoldMember: "fleet hold", HoldFriend: "friend hold"}[kinds[0]]
	if verb == "" {
		verb = "hold"
	}
	tail := " --reason <text>"
	if release {
		verb = strings.Replace(verb, "hold", "unhold", 1)
		tail = ""
	}
	return ": " + n + " is a " + holdKindWords[kinds[0]] + "; run: nova-sprint " + verb + " " + n + tail
}

// HoldNames is hold and unhold as one step (docs/SPEC-SPRINT.md section 11): every name
// resolved first (HoldTargets), all or none; then each kind's part, and a happened
// note per name, the reason in it, so the log holds every hold and release.
func HoldNames(s *Snapshot, r HoldReq) Plan {
	targets, refused := HoldTargets(s, r)
	if len(r.Names) == 0 {
		refused = append(refused, Refusal{"hold", "names nothing: name a fleet member, a reader, a friend or a stream"})
	}
	if len(refused) > 0 {
		return Plan{Refused: refused}
	}
	var members []string
	keep := map[string]bool{} // streams held here: their hold withdraws their cards, a member's hold leaves them
	for _, t := range targets {
		switch {
		case t.Kind == HoldMember:
			members = append(members, t.Name)
		case t.Kind == HoldStream && !r.Release:
			keep[t.Name] = true
		}
	}
	var p Plan
	add := func(q Plan) {
		p.Rows = append(p.Rows, q.Rows...)
		p.Units = append(p.Units, q.Units...)
		p.Refused = append(p.Refused, q.Refused...)
		p.Notes = append(p.Notes, q.Notes...)
	}
	// every card placed on a member goes round the fleet from the deal's rolling index and
	// moves it (round.go), as a fleet step's does
	rr := dealRoundWith(s, members...)
	moves := roundMoves{}
	up := without(s.UpMembers(), members)
	q, widths := memberLoads(s, up), memberWidths(s, up)
	for _, t := range targets {
		var line string
		switch {
		case t.Kind == HoldMember && r.Release:
			add(fleetStepPlan(s, FleetReq{Op: "release", Member: t.Name, Who: r.Who, Fresh: slices.Contains(r.Alive, t.Name), Why: r.Reason}, rr, moves))
		case t.Kind == HoldMember:
			add(downPlan(s, FleetReq{Op: "hold", Member: t.Name, Who: r.Who, Why: holdWhy(r), Reason: r.Reason, Finish: !r.Return, keep: keep}, up, rr, moves, q, widths))
		case t.Kind == HoldStream:
			var sp Plan
			sp, line = holdStream(s, t.Name, r)
			add(sp)
		case t.Kind == HoldReader && r.Return && !r.Release:
			var rp Plan
			rp, line = returnReads(s, t.Name, r.Who)
			add(rp)
		case t.Kind == HoldFriend && !r.Release:
			var fp Plan
			fp, line = holdFriendCards(s, t.Name, r)
			add(fp)
		}
		p.Notes = append(p.Notes, holdNote(s, t, r, line))
	}
	p = Lawful(p)
	roundWrites(&p, rr, moves)
	return p
}

// holdWhy is a member hold's words on its status note: the reason, else who held it.
func holdWhy(r HoldReq) string {
	if r.Reason != "" {
		return r.Reason
	}
	return "held by " + r.Who
}

// holdNote is the happened note of one name held or released: the log's line of the
// hold, the reason in it.
func holdNote(s *Snapshot, t HoldTarget, r HoldReq, line string) Note {
	typ, word := NHeld, "held"
	if r.Release {
		typ, word = NUnheld, "released from its hold"
	}
	stream := ""
	if t.Kind == HoldStream {
		stream = t.Name
	}
	n := happened(typ, stream, s.Now)
	n.Who = r.Who
	n.What = t.Kind + " " + t.Name + " " + word
	if r.Reason != "" {
		n.What += ": " + r.Reason
	}
	if !r.Release {
		switch {
		case r.Return:
			n.What += "; --return: its work begun is handed back now"
		case t.Kind == HoldFriend:
			n.What += "; every card she holds, begun or not, is handed back now"
		default:
			n.What += "; its work begun finishes"
		}
	}
	if line != "" {
		n.What += "; " + line
	}
	return n
}

// StreamHeld says the coordinator holds the stream (hold <stream>): none of its ready
// primaries is dealt, to a machine or a friend, until unhold (docs/SPEC-SPRINT.md section
// 11, hold).
func StreamHeld(s *Snapshot, stream string) bool {
	return s.Merge != nil && s.StreamCtl(stream).F(FieldHeld) != ""
}

// StreamStateText is a stream's state cell: held while the coordinator holds it and it
// is not stopped (a stop is the merge's, and says more), else its control card's state
// (docs/SPEC-SPRINT.md section 11, hold).
func StreamStateText(fields map[string]string) string {
	if fields[FieldHeld] != "" && fields["state"] != StreamStopped {
		return Held
	}
	return fields["state"]
}

// HeldToFinish says a member's hold lets its working cards finish (hold with no
// --return): no sweep moves them, and the no-stall rule counts the member as holding
// them while their deadline runs (docs/SPEC-SPRINT.md sections 5 and 11).
func HeldToFinish(ctl *Card) bool { return ctl.F("held") != "" && ctl.F(FieldHeldFinish) != "" }

// holdStream is a stream's hold or its release: the control card's held stamp and
// reason, and with a hold the stream's work cards ready on members withdrawn (never
// begun, so no redeal is spent), its working ones too with --return (docs/SPEC-SPRINT.md
// section 11, hold).
func holdStream(s *Snapshot, stream string, r HoldReq) (Plan, string) {
	var p Plan
	ctl := s.StreamCtl(stream)
	if r.Release {
		if ctl.F(FieldHeld) == "" {
			return p, "it was not held"
		}
		p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: stream, Changes: []Change{change(Merge, setEntry(ctl, nil, FieldHeld, FieldHeldReason))},
			Moved: "stream " + stream + " released"})
		return p, ""
	}
	set := map[string]string{FieldHeld: stamp(s.Now)}
	var unset []string
	if r.Reason != "" {
		set[FieldHeldReason] = r.Reason
	} else {
		unset = append(unset, FieldHeldReason)
	}
	p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: stream, Changes: []Change{change(Merge, setEntry(ctl, set, unset...))},
		Moved: "stream " + stream + " held"})
	var withdrew []string
	for _, m := range s.Fleet.Rows() {
		cols := []string{Ready}
		if r.Return {
			cols = append(cols, Working)
		}
		for _, col := range cols {
			for _, c := range s.Fleet.Cell(m, col) {
				if c.F("stream") != stream || c.F("kind") != "work" {
					continue
				}
				p.Units = append(p.Units, withdrawCard(s, c, col == Working, NWithdrawn, r.Who, "stream "+stream+" is held"))
				withdrew = append(withdrew, c.ID)
			}
		}
	}
	if len(withdrew) == 0 {
		return p, ""
	}
	return p, fmt.Sprintf("withdrew %d: %s", len(withdrew), Preview(withdrew, ","))
}

// returnReads is a reader's hold with --return: its reads asked or begun are taken back
// (retired, as the readers' sweep takes a reader away's) wherever a reader up is free to
// read them at the next ask; one no reader up can take stays, and is said
// (docs/SPEC-SPRINT.md sections 6 and 11).
func returnReads(s *Snapshot, reader, who string) (Plan, string) {
	var p Plan
	cards := append(append([]*Card{}, s.Readers.Cell(reader, Asked)...), s.Readers.Cell(reader, Reading)...)
	SortCards(cards)
	var back, kept []string
	for _, c := range cards {
		if !readTakerUp(s, c, reader) {
			kept = append(kept, c.ID)
			continue
		}
		back = append(back, c.ID)
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"),
			Changes: []Change{change(Readers, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": RetiredByHold}))},
			Moved:   fmt.Sprintf("%s %s:%s -> taken back (%s is held); the ask asks it of a reader up", c.ID, reader, c.Col, reader)})
	}
	var parts []string
	if len(back) > 0 {
		parts = append(parts, fmt.Sprintf("took back %d: %s", len(back), Preview(back, ",")))
	}
	if len(kept) > 0 {
		parts = append(parts, fmt.Sprintf("kept %d no reader up is free to read: %s", len(kept), Preview(kept, ",")))
	}
	return p, strings.Join(parts, "; ")
}

// RetiredByHold is a read card's retired_by when the coordinator's hold --return took it
// back from its reader.
const RetiredByHold = "hold"

// readTakerUp says a reader up other than reader, with no read of c's primary at its
// attempt, can be asked the read (the readers' sweep's rule, sweepReads).
func readTakerUp(s *Snapshot, c *Card, reader string) bool {
	if pr := s.Work.Card(c.F("primary")); pr == nil || !enoughReadersUp(s, pr) {
		return false
	}
	for _, rd := range s.UpReaders() {
		if rd != reader && s.Readers.Card(ReadCardID(c.F("primary"), c.Int("attempt"), rd)) == nil {
			return true
		}
	}
	return false
}

// holdFriendCards is a friend's hold: a held friend keeps no card, begun or not (the owner,
// 2026-10-04, on a held friend still showing two working cards: "nonono"), so every card
// she has begun, working on her row or read as started, is taken back as friend down takes
// it (FriendTake with Hold), each primary ready again for the tick to deal to a friend up, a
// started one with its pushed head carried. Her ready cards not begun are taken back too, with
// --return or without: a held friend keeps no card at all, so a dealt card never waits on a
// friend who will not take it (the owner, 2026-10-09; docs/SPEC-SPRINT.md sections 1 and 11, hold).
func holdFriendCards(s *Snapshot, friend string, r HoldReq) (Plan, string) {
	p := FriendTake(s, FriendTakeReq{Friend: friend, All: true, Hold: true, Begun: false, Started: r.Started, Who: r.Who})
	var back, carried []string
	for _, u := range p.Units {
		back = append(back, u.Key)
		if PushedTip(r.Started[u.Key]) != "" {
			carried = append(carried, u.Key)
		}
	}
	if len(back) == 0 {
		return p, ""
	}
	line := fmt.Sprintf("withdrew %d: %s", len(back), Preview(back, ","))
	if len(carried) > 0 {
		line += fmt.Sprintf("; carried the pushed head of %d: %s", len(carried), Preview(carried, ","))
	}
	return p, line
}

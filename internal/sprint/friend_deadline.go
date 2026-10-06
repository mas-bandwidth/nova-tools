package sprint

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// A friend's card's deadline by friend (docs/SPEC-SPRINT.md section 1, a friend's card's
// deadline; the owner, 2026-10-04: friends get what machines have). The member rule of
// nova-tools#5300 (not on dev when this was written, so the same rule is written here for
// friends, under its own names) gives a card dealt to a machine the larger of its own
// deadline and DeadlineK times the machine's median run wall over its last DeadlineSamples
// ok attempts. A friend's card has no route deadline of its own: its own is
// DeadlineUnfinished, and the friend's is FriendDeadlineK times her median run wall over
// her last FriendDeadlineSamples ok attempts (RunWall: her take to her report), set on the
// card (FieldFriendDeadline) as it goes into working on her row, so a friend whose cards
// take long is not judged late on the fleet's number.

const (
	// FriendDeadlineK is the multiple of the friend's median run wall her card's working
	// deadline is at least (nova-tools#5300's DeadlineK).
	FriendDeadlineK = 3
	// FriendDeadlineSamples is how many of her latest ok attempts the median is over
	// (nova-tools#5300's DeadlineSamples).
	FriendDeadlineSamples = 50
)

// FriendMedianWall is the friend's median run wall in seconds over her last
// FriendDeadlineSamples ok attempts (RunWall of the ok work cards on her row, newest
// finished first), and how many samples it is over; 0 and 0 with none.
func FriendMedianWall(s *Snapshot, name string) (median float64, n int) {
	if s.Fleet == nil {
		return 0, 0
	}
	cards := append([]*Card(nil), s.Fleet.Cell(FriendRow(name), DoneOK)...)
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].F("finished") > cards[j].F("finished") })
	var walls []float64
	for _, c := range cards {
		if w, ok := wallSeconds(RunWall(c)); ok {
			walls = append(walls, w)
		}
		if len(walls) == FriendDeadlineSamples {
			break
		}
	}
	m := measure(walls)
	return m.Median, m.N
}

// FieldFriendDeadline is a friend's work card's working deadline in seconds, set when it is
// placed on her row (friendDeal, FriendLevel: friendDeadline), as #5300 sets a machine's
// card's when it is dealt: absent while she has no ok attempt, and DeadlineUnfinished holds.
const FieldFriendDeadline = "friend_deadline"

// friendDeadline is the fields a card placed on the friend's row carries for its working
// deadline: FieldFriendDeadline, the larger of DeadlineUnfinished and FriendDeadlineK times
// her median run wall, when she has an ok attempt; none otherwise (unset says so).
func friendDeadline(s *Snapshot, name string) (set map[string]string, unset []string) {
	median, n := FriendMedianWall(s, name)
	if n == 0 {
		return map[string]string{}, []string{FieldFriendDeadline}
	}
	d := max(DeadlineUnfinished, time.Duration(math.Ceil(FriendDeadlineK*median))*time.Second)
	return map[string]string{FieldFriendDeadline: itoa(int(d / time.Second))}, nil
}

// unfinishedLimit is how long a work card taken may run unfinished: its friend's deadline
// when it carries one (FieldFriendDeadline), else DeadlineUnfinished.
func unfinishedLimit(c *Card) time.Duration {
	if d := c.Int(FieldFriendDeadline); d > 0 {
		return time.Duration(d) * time.Second
	}
	return DeadlineUnfinished
}

// friendTaken is the fields of a friend's card going into working on her row now: taken
// stamps (takenStamps) and her deadline (friendDeadline), untaken_since unset.
func friendTaken(s *Snapshot, c *Card, name string) (set map[string]string, unset []string) {
	set, unset = friendDeadline(s, name)
	maps.Copy(set, takenStamps(c, s.Now))
	return set, append(unset, "untaken_since")
}

// The lane check (docs/SPEC-SPRINT.md section 1, a judgment checks the lane before it
// rises; a-judgment-checks-the-lane-before-it-rises.w1). On 2026-10-05 night about 250
// judgments reached the coordinator, most of them a stall, a deadline or "finishes none"
// for a friend whose lane was live and inside its normal run time, each a coordinator turn
// to read and acknowledge. Before such a judgment rises, the tick (store.tickRun.parts)
// passes the part's plan through LaneChecked: a friend's card her beat names running, or
// her daemon's lane holds (her seat's Running), inside its cap (unfinishedLimit, her
// friend's deadline else DeadlineUnfinished) raises nothing, and is a Quiet instead, its
// row "running 32m of 90m". A read tier question is answered by the rule (ReadTierRule)
// and never asked. The tick counts the quiets on its heartbeat (store.Heartbeat.Quiet).

// FriendLaneLive is how old a friend's beat may be while the cards it names running are
// her live lanes: her daemon beats every FriendBeatEvery, so a beat this old is a daemon
// that stopped, not a lane.
const FriendLaneLive = 2 * time.Minute

// RuleReadTier is the read tier rule: a stream's read tier is kept unless two substantive
// findings disagree (a reader's ok and another's broken with a finding, on one attempt at
// the stream's read tier), when it is raised; the question is never asked. A sprint row
// that turns it off (answer_rules_off) asks it again.
const RuleReadTier = "read-tier"

// LaneRun is a friend's card her lane is running: who, the card, how long it has run
// (running time from its first take, else its deal) and its cap, and what says it runs.
type LaneRun struct {
	Friend string
	Card   string
	Ran    time.Duration
	Cap    time.Duration
	Why    string
}

// Row is the run as the friend's row says it: "running 32m of 90m".
func (l LaneRun) Row() string {
	return "running " + minutesWord(l.Ran) + " of " + minutesWord(l.Cap)
}

// minutesWord is a duration in whole minutes: "32m".
func minutesWord(d time.Duration) string { return itoa(int(d/time.Minute)) + "m" }

// LiveLane is the friend's run of the work card when her lane is live on it: it is on her
// row, ready or working, her beat no older than FriendLaneLive names it running, or her
// seat (her daemon's lanes as the tick read them) does, and it has run less than its cap.
func LiveLane(s *Snapshot, r TickReq, c *Card) (LaneRun, bool) {
	if c == nil || (c.Col != Ready && c.Col != Working) {
		return LaneRun{}, false
	}
	friend, ok := FriendOfRow(c.Row)
	if !ok {
		return LaneRun{}, false
	}
	why := ""
	for _, key := range []string{friend, c.Row} {
		if b, ok := r.Beats[key]; ok && b.Friend != nil && why == "" && s.Now.Sub(b.At) <= FriendLaneLive &&
			friendStarted(s, FriendSeat{Running: b.Friend.Running}, withField(c, FieldProgress, "")) {
			why = "her beat names it running"
		}
	}
	for _, f := range r.Friends {
		if why == "" && f.Name == friend && friendStarted(s, FriendSeat{Running: f.Running}, withField(c, FieldProgress, "")) {
			why = "her daemon's lane holds it"
		}
	}
	if why == "" {
		return LaneRun{}, false
	}
	// the card's clock is WorkDeadline's: from its first take, else its deal
	field, _, _, _ := WorkDeadline(s, c)
	ran, ok := r.running(s.Now, c.F(field))
	limit := unfinishedLimit(c)
	if !ok || ran >= limit {
		return LaneRun{}, false
	}
	return LaneRun{Friend: friend, Card: c.ID, Ran: max(ran, 0), Cap: limit, Why: why}, true
}

// friendLive is the first live lane of the friend's cards on her row, working first.
func friendLive(s *Snapshot, r TickReq, friend string) (LaneRun, bool) {
	row := FriendRow(friend)
	for _, c := range append(append([]*Card(nil), s.Fleet.Cell(row, Working)...), s.Fleet.Cell(row, Ready)...) {
		if l, ok := LiveLane(s, r, c); ok {
			return l, true
		}
	}
	return LaneRun{}, false
}

// Quiet is a judgment the tick kept from rising: its type, its subject and why.
type Quiet struct {
	Type    string `json:"type"`
	Subject string `json:"subject"`
	Why     string `json:"why"`
}

// LaneChecked is the part's plan with the judgments the lane check keeps from rising taken
// out, and those quiets: a lateness (NWorkLate) of a friend's card on a live lane
// (LiveLane), a friend's stall (NStalled on her) or "finishes none" (NFriendIdle) while a
// card of hers is on one, and a read tier question (NRaiseReadTier), which ReadTierRule
// answers instead; with part the ask, the readers behind it held (ReadersFull). Only a
// judgment raised is checked: one open already is the coordinator's, as before.
func LaneChecked(s *Snapshot, r TickReq, part string, p Plan) (Plan, []Quiet) {
	var quiet []Quiet
	if part == "ask" {
		if why, ok := ReadersFull(s); ok {
			quiet = append(quiet, Quiet{Type: NReadersBehind, Subject: SprintSubject, Why: why})
		}
	}
	if s.Fleet == nil {
		return p, quiet
	}
	var units []Unit
	check := func(notes []Note) []Note {
		var out []Note
		for _, n := range notes {
			if n.Kind != Judgment {
				out = append(out, n)
				continue
			}
			var l LaneRun
			live := false
			subject := strings.Join(n.Primaries, ",")
			switch n.Type {
			case NWorkLate:
				l, live = LiveLane(s, r, s.Fleet.Placed(n.Card))
				subject = n.Card
			case NFriendIdle, NStalled:
				// finishes none is on her row, the stall ladder's stall on her name, with no
				// stream (a stall of a card names its stream)
				for _, sub := range n.Primaries {
					f, ok := FriendOfRow(sub)
					if n.Type == NStalled {
						f, ok = sub, n.Stream == ""
					}
					if ok && !live {
						l, live = friendLive(s, r, f)
					}
				}
			case NRaiseReadTier:
				if u, q, ok := ReadTierRule(s, n); ok {
					quiet = append(quiet, q)
					if u != nil {
						units = append(units, *u)
					}
					continue
				}
			}
			if live {
				quiet = append(quiet, Quiet{Type: n.Type, Subject: subject,
					Why: fmt.Sprintf("friend %s: %s %s (%s)", l.Friend, l.Card, l.Row(), l.Why)})
				continue
			}
			out = append(out, n)
		}
		return out
	}
	p.Notes = check(p.Notes)
	p.Units = slices.Clone(p.Units)
	for i := range p.Units {
		p.Units[i].Notes = check(p.Units[i].Notes)
	}
	p.Units = append(p.Units, units...)
	return p, quiet
}

// ReadTierRule answers the read tier question n by the rule (RuleReadTier) and never asks
// it: kept unless two substantive findings disagree on one of the stream's cards in review,
// a reader's ok and another's broken with a finding on one attempt at the stream's read
// tier, when the unit that raises the read tier to n.Tier, its reason the rule's, is
// returned. ok is false when the rule is off or the stream has no control card: the
// question rises as before.
func ReadTierRule(s *Snapshot, n Note) (raise *Unit, q Quiet, ok bool) {
	ctl := s.StreamCtl(n.Stream)
	if s.RuleOff(RuleReadTier) || ctl == nil || s.Work == nil || s.Readers == nil {
		return nil, Quiet{}, false
	}
	q = Quiet{Type: NRaiseReadTier, Subject: StreamSubject(n.Stream)}
	for _, c := range s.Work.Cell(n.Stream, Review) {
		attempt := c.Int("attempt")
		var oks, found []string
		for _, rc := range liveReadsAt(s, c, attempt) {
			switch {
			case rc.Col == OK:
				oks = append(oks, rc.Row)
			case rc.Col == Broken && rc.F("finding") != "":
				found = append(found, rc.Row)
			}
		}
		if len(oks) == 0 || len(found) == 0 || n.Tier == "" {
			continue
		}
		why := fmt.Sprintf("two substantive findings disagree on %s attempt %d (%s ok, %s broken with a finding)", c.ID, attempt, strings.Join(oks, ","), strings.Join(found, ","))
		q.Why = RuleSaid(RuleReadTier, "raised to "+n.Tier+": "+why)
		h := happened(NRuleAnswered, n.Stream, s.Now)
		h.Who, h.What = MachineActor, q.Why
		var closes []Open
		for _, o := range s.Open {
			if o.Note.Type == NRaiseReadTier && o.Subject() == StreamSubject(n.Stream) {
				closes = append(closes, o)
			}
		}
		return &Unit{Key: ctl.ID, Stream: n.Stream, Changes: []Change{change(Merge, setEntry(ctl, map[string]string{FieldReadTier: n.Tier, FieldReadTierReason: q.Why}))},
			Notes: []Note{h}, Closes: closes, Moved: "stream " + n.Stream + " read-tier " + n.Tier + " (" + RuleSaid(RuleReadTier, "raise") + ")"}, q, true
	}
	q.Why = RuleSaid(RuleReadTier, "kept at its tier: "+n.What+"; no two substantive findings disagree")
	return nil, q, true
}

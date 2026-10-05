package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A ready card pinned to a friend down or held (docs/SPEC-SPRINT.md section 8, the pin
// rule; the owner, 2026-10-05: 31 ready cards sat pinned to friends down for the week or
// held, some for 20 hours, with no judgment, and the coordinator found them by reading the
// rows; 2026-10-04: "pins are the exception; hard-pin only true ownership"). A hard pin
// (WHO: only friend <name>) waits ready for her alone (friendDeal), so a friend down or
// held strands it. When she has been down or held longer than the friend_idle setting
// (FriendIdleAfter), in running time, the pin rule answers each card pinned to her:
//
//   - a generator's preference (a hard pin with no owner mark) is unpinned by the machine
//     (Unpin, its WHO unpinned note the log, the card's rule_answer the mark), so the deal
//     offers it to the friends up and then the fleet;
//   - true ownership (the brief's `WHO: friend <name> (owner)`, written by the generator
//     only for ratings and a friend's own tool, or a rating card, rate-<name>-*) stays
//     pinned, and the friend gets one judgment (NPinnedDown, its subject her fleet row)
//     naming her cards, with the unpin printed complete among its decisions.
//
// With the rules off (TickReq.AnswerRules false) nothing is unpinned: every pinned card is
// named in her judgment instead, and a card Unpin refuses (it was dealt before) is too, so
// no card sits ready behind a friend down without a judgment. The judgment is written once,
// rewritten in place when her cards change, and closed when she is up or none waits. The
// episode's start is the fleet table's property PropPinDownSince, so it lives across ticks
// and run loops.

const (
	// RulePin is the pin rule. It is not one of RuleNames: nova-config's answer_rules_off
	// does not name it (config.AnswerRules); run --answer-rules=false turns it off.
	RulePin = "pin"
	// PartRulePin is its tick part, a rule part (TickRules).
	PartRulePin = "rule pin"
	// NPinnedDown is the judgment that ready cards wait pinned to a friend down or held.
	NPinnedDown = "ready cards wait pinned to a friend down or held"
	// ownerMark is the WHO line's last word on true ownership.
	ownerMark = "(owner)"
)

// PropPinDownSince is the fleet property recording when the tick first saw the friend not
// up: the start of her episode, cleared when she is up.
func PropPinDownSince(friend string) string { return "pin_down_since." + friend }

// cutOwnerMark is the brief with the owner mark cut from its WHO line (the first, as
// cardhdr.ReadWho reads it), and whether it carried one.
func cutOwnerMark(brief string) (string, bool) {
	first, rest, ok := strings.Cut(brief, "\n")
	if !ok {
		return brief, false
	}
	lines := strings.Split(rest, "\n")
	for i, l := range lines {
		k, v, ok := cardhdr.KeyValue(l)
		if !ok {
			break
		}
		if !strings.EqualFold(k, "who") {
			continue
		}
		f := strings.Fields(v)
		if len(f) < 3 || f[len(f)-1] != ownerMark {
			return brief, false
		}
		lines[i] = "WHO: " + strings.Join(f[:len(f)-1], " ")
		return first + "\n" + strings.Join(lines, "\n"), true
	}
	return brief, false
}

// PinOwned says the card's pin is true ownership: its brief's WHO line carries the owner
// mark, or it is a rating card of the friend it names (rate-<name>-*).
func PinOwned(c *Card) bool {
	if _, owned := cutOwnerMark(c.F("brief")); owned {
		return true
	}
	name, _ := FriendCard(c)
	return name != "" && strings.HasPrefix(c.ID, "rate-"+name+"-")
}

// pinHold is one friend's pin judgment as it should read now.
type pinHold struct {
	what      string
	decisions []string
}

// TickRulePin is the pin rule's part (the comment above): the episodes begun and ended,
// the preferences unpinned, and one judgment per friend for the cards that stay pinned.
func TickRulePin(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if s.Work == nil || s.Fleet == nil || r.Friends == nil {
		return p, 0
	}
	status := map[string]string{}
	for _, f := range r.Friends {
		status[f.Name] = f.Status
	}
	pinned := map[string][]*Card{}
	for _, c := range s.Work.Column(Ready) {
		if IsSentinel(c) || !OnlyFriend(c) {
			continue
		}
		name, _ := FriendCard(c)
		pinned[name] = append(pinned[name], c)
	}
	names := map[string]bool{}
	for n := range status {
		names[n] = true
	}
	for n := range pinned {
		names[n] = true
	}
	write := func(name, value string) {
		was, had := s.Fleet.Prop(name)
		if (!had && value == "") || (had && was == value) {
			return
		}
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: value, Was: was, WasAbsent: !had})
	}
	window := s.FriendIdleAfter()
	holds := map[string]pinHold{}
	for _, f := range slices.Sorted(maps.Keys(names)) {
		st, ok := status[f]
		word := st
		if !ok {
			st, word = Down, "down (no row in the friends roster)"
		}
		since, _ := s.Fleet.Prop(PropPinDownSince(f))
		switch {
		case st == Up:
			write(PropPinDownSince(f), "")
			continue
		case since == "":
			write(PropPinDownSince(f), stamp(s.Now))
			continue
		}
		d, ok := r.running(s.Now, since)
		if !ok || d <= window || len(pinned[f]) == 0 {
			continue
		}
		var judged, prefer []string
		for _, c := range pinned[f] {
			if PinOwned(c) || !r.AnswerRules {
				judged = append(judged, c.ID)
			} else {
				prefer = append(prefer, c.ID)
			}
		}
		if len(prefer) > 0 {
			said := RuleSaid(RulePin, fmt.Sprintf("friend %s %s since %s, past friend_idle %s: a generator's preference, not ownership", f, word, since, window))
			q := Unpin(s, UnpinReq{IDs: prefer, Reason: said, Who: ruleWho(RulePin)})
			for _, u := range q.Units {
				for i := range u.Changes {
					if e := &u.Changes[i].Entry; u.Changes[i].Table == Work {
						if e.Set == nil {
							e.Set = map[string]string{}
						}
						e.Set[FieldRuleAnswer] = RulePin + ": unpinned at " + stamp(s.Now)
					}
				}
				p.Units = append(p.Units, u)
			}
			for _, x := range q.Refused {
				judged = append(judged, x.Key) // dealt before: it needs a mind
			}
		}
		if len(judged) == 0 {
			continue
		}
		slices.Sort(judged)
		reason := fmt.Sprintf("friend %s %s since %s: share her ready cards", f, st, since)
		holds[f] = pinHold{
			what: fmt.Sprintf("friend %s is %s since %s, past friend_idle %s, and %d ready cards wait pinned to her: %s; unpin them so the deal offers them to the friends up and the fleet, or bring her up",
				f, word, since, window, len(judged), strings.Join(judged, ", ")),
			decisions: []string{"nova-sprint unpin " + strings.Join(judged, " ") + " --reason '" + reason + "'", "friend up " + f, "ack", "wait"},
		}
	}
	pinJudgments(&p, s, r, holds)
	return p, 0
}

// pinJudgments writes one judgment per friend held (its subject her fleet row), rewrites
// an open one in place when its words change, leaves one the coordinator acknowledged or
// waited, and closes every one, open or held, whose friend no longer holds; a wait whose
// time has run out is closed, and raised again when it still holds.
func pinJudgments(p *Plan, s *Snapshot, r TickReq, holds map[string]pinHold) {
	open := map[string]Open{}
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == NPinnedDown {
			open[o.Subject()] = o
		}
	}
	acked := map[string]bool{}
	for _, o := range s.Acked {
		if o.Note.Type != NPinnedDown {
			continue
		}
		f, _ := FriendOfRow(o.Subject())
		_, holding := holds[f]
		if !holding {
			p.Closes = append(p.Closes, o)
			continue
		}
		if !o.Note.Review.IsZero() {
			if d, ok := r.running(s.Now, stamp(o.Note.At)); ok && d >= o.Note.Review.Sub(o.Note.At) {
				p.Closes = append(p.Closes, o)
				continue
			}
		}
		acked[o.Subject()] = true
	}
	for _, f := range slices.Sorted(maps.Keys(holds)) {
		h, subj := holds[f], FriendRow(f)
		if o, ok := open[subj]; ok {
			if o.Note.What != h.what {
				n := o.Note
				n.What, n.Decisions = h.what, h.decisions
				p.Updates = append(p.Updates, n)
			}
			continue
		}
		if acked[subj] {
			continue
		}
		p.Notes = append(p.Notes, Note{Kind: Judgment, Type: NPinnedDown, Primaries: []string{subj}, Count: 1, What: h.what,
			Who: r.who(), At: s.Now, Marked: true, Decisions: h.decisions})
	}
	for _, subj := range slices.Sorted(maps.Keys(open)) {
		if f, _ := FriendOfRow(subj); holds[f].what == "" {
			p.Closes = append(p.Closes, open[subj])
		}
	}
}

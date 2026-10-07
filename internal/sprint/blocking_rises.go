package sprint

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Cards that block many rise to high (docs/SPEC-SPRINT.md section 1, "Priority", the
// blocking-rises rule; the owner, 2026-10-07, watching shrink 65, prose 31, use 24, harness
// 18 and reference 18 cards wait on a handful in review and merging while the fleet idled
// at 5 of 88 lanes: "when we find that certain cards are blocking a lot of new work, we
// should increase the priority of those cards naturally to high. This way they flow through
// the system quickly. I want the priority to make these cards go quicker through working,
// reading and merging."). The rule is a part of the work pump, run every tick before the
// resolve and the deal, so the level it writes orders the same tick's deal, ask and land:
// an open primary with BlockingHigh or more cards behind it (its weight, weight.go: the
// cards on the table, not landed, that wait on it through their needs, transitively) whose
// level no person set is raised to high, the field written as the verb priority writes it
// (FieldPriority), marked as the rule's (FieldPriorityBy = PriorityByRule), with a MOVED
// line and a note on the card's timeline. The chain carries the level: a need of a card at
// high or above is raised to high the same way, so a level never asks for a card ahead of
// its needs (the owner: "The priority for cards can't increase them past their dependencies
// they need"); a need of a card the threshold raised is over the threshold already (every
// card behind the needer is behind the need, and the needer too), so the chain adds only
// the needs of levels a person set. The deal still deals no card whose needs have not
// landed: a card with an unlanded need is waiting, and the deal offers ready cards only
// (TickDeal; TestTheRuleNeverDealsACardBeforeItsNeeds). When the count falls under the
// threshold (its dependents dropped) and no dependent is at high or above, a level the rule
// set drops back to normal; a level a person set (the verb priority, a brief's PRIORITY
// line, a stream's default: FieldPriorityBy names the actor, or is absent on a card set
// before the field existed) is never raised or lowered by the rule, whichever level it is:
// a hand-set normal or low is the opt-out. The threshold is the sprint's setting
// (nova-sprint set --blocking-high <n|default>, PropBlockingHigh; BlockingHighDefault). The
// model is tla/BlockingRises.tla.

// FieldPriorityBy is who set the primary's level (FieldPriority): PriorityByRule for the
// blocking-rises rule, else the actor of the verb priority or of the add that seeded it.
// Absent with a level set, the level counts as a person's.
const FieldPriorityBy = "priority_by"

// PriorityByRule is FieldPriorityBy's word for a level the blocking-rises rule set.
const PriorityByRule = "rule"

// PropBlockingHigh is the work table's property: the threshold of the blocking-rises
// rule, the cards behind a card at and above which it rises to high.
const PropBlockingHigh = "blocking_high"

// BlockingHighDefault is the threshold when the coordinator set none.
const BlockingHighDefault = 8

// PartBlockingRises is the pump's part that runs the rule (TickTables), after the drain and
// before the resolve, so the levels it writes order this tick's deal.
const PartBlockingRises = "blocking-rises"

// RuleBlockingRises is the rule's name on its MOVED lines and its notes.
const RuleBlockingRises = "blocking-rises"

// BlockingHigh is the rule's threshold: the sprint's setting, else BlockingHighDefault.
func (s *Snapshot) BlockingHigh() int {
	if s != nil && s.Work != nil {
		if v, ok := s.Work.Prop(PropBlockingHigh); ok {
			if n, err := strconv.Atoi(v); err == nil && n >= 1 {
				return n
			}
		}
	}
	return BlockingHighDefault
}

// blockingHighValid says the word set --blocking-high takes: a whole number from 1, or
// default.
func blockingHighValid(v string) bool {
	if v == ReadTierDefault {
		return true
	}
	n, err := strconv.Atoi(v)
	return err == nil && n >= 1
}

// TickBlockingRises is the rule as a part of the tick (BlockingRises).
func TickBlockingRises(s *Snapshot, r TickReq) (Plan, int) { return BlockingRises(s, r.who()), 0 }

// byHand says the primary's level is a person's: set, and not marked the rule's.
func byHand(c *Card) bool { return c.F(FieldPriority) != "" && c.F(FieldPriorityBy) != PriorityByRule }

// atLeastHigh says the level is high or above.
func atLeastHigh(level string) bool { return priorityRank(level) <= priorityRank(PriorityHigh) }

// blockingTargets is the open primaries the rule wants at high (BlockingRises): every card
// no person set with the threshold or more behind it, then, to a fixed point, every card no
// person set that an open card at high or above needs (the chain). The weights are the
// snapshot's (Weights), computed once per call.
func blockingTargets(s *Snapshot, threshold int) (targets map[string]bool, weights map[string]int, dependents map[string][]string) {
	open := openPrimaries(s)
	weights = weightsOver(open)
	byID := map[string]*Card{}
	for _, c := range open {
		byID[c.ID] = c
	}
	dependents = map[string][]string{} // a card -> the open cards that need it, the need not waived
	for _, c := range open {
		waived := Split(c.F("waived"))
		for _, n := range Split(c.F("needs")) {
			if byID[n] != nil && n != c.ID && !contains(waived, n) {
				dependents[n] = append(dependents[n], c.ID)
			}
		}
	}
	// high is every card at high or above once the rule has run: a person's level that is,
	// the threshold's raises, then the chain to a fixed point
	high := map[string]bool{}
	for _, c := range open {
		if byHand(c) {
			high[c.ID] = atLeastHigh(c.F(FieldPriority))
		} else if weights[c.ID] >= threshold {
			high[c.ID] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, c := range open {
			if high[c.ID] || byHand(c) {
				continue
			}
			for _, d := range dependents[c.ID] {
				if high[d] {
					high[c.ID], changed = true, true
					break
				}
			}
		}
	}
	targets = map[string]bool{}
	for _, c := range open {
		if high[c.ID] && !byHand(c) {
			targets[c.ID] = true
		}
	}
	return targets, weights, dependents
}

// BlockingRises is the rule's plan at s.Now: a raise to high for every open primary the
// rule wants there (blockingTargets) that is not at high, a drop to normal for every
// primary at a level the rule set that it no longer wants; each a MOVED line and a priority
// set note on the card's timeline by who. A level a person set is never changed.
func BlockingRises(s *Snapshot, who string) Plan {
	var p Plan
	if s == nil || s.Work == nil {
		return p
	}
	threshold := s.BlockingHigh()
	targets, weights, dependents := blockingTargets(s, threshold)
	cards := openPrimaries(s)
	SortCards(cards)
	for _, c := range cards {
		if byHand(c) {
			continue
		}
		was, _ := CardPriority(c)
		switch {
		case targets[c.ID] && c.F(FieldPriority) != PriorityHigh:
			why := fmt.Sprintf("behind=%d", weights[c.ID])
			if weights[c.ID] < threshold {
				// the chain: a need of a card at high or above rises with it
				var over []string
				for _, d := range dependents[c.ID] {
					if dc := s.Work.Placed(d); dc != nil && (targets[d] || byHand(dc) && atLeastHigh(dc.F(FieldPriority))) {
						over = append(over, d)
					}
				}
				sort.Strings(over)
				why += ", needed by " + Preview(over, ",")
			}
			line := fmt.Sprintf("rule %s: %s %s -> %s (%s)", RuleBlockingRises, c.ID, was, PriorityHigh, why)
			n := happened(NPrioritySet, c.Row, s.Now, c.ID)
			n.Who, n.What = who, line
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row,
				Changes: []Change{change(Work, setEntry(c, map[string]string{FieldPriority: PriorityHigh, FieldPriorityBy: PriorityByRule}))},
				Notes:   []Note{n}, Moved: line})
		case !targets[c.ID] && c.F(FieldPriorityBy) == PriorityByRule:
			line := fmt.Sprintf("rule %s: %s %s -> %s (behind=%d)", RuleBlockingRises, c.ID, was, PriorityNormal, weights[c.ID])
			n := happened(NPrioritySet, c.Row, s.Now, c.ID)
			n.Who, n.What = who, line
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.Row,
				Changes: []Change{change(Work, setEntry(c, nil, FieldPriority, FieldPriorityBy))},
				Notes:   []Note{n}, Moved: line})
		}
	}
	return p
}

// RuleRaised is each open primary at a level the rule set, with the cards behind it now
// (Weights): what where shows beside the high cards and card shows on its head line; nil
// when none.
func RuleRaised(s *Snapshot) map[string]int {
	if s == nil || s.Work == nil {
		return nil
	}
	w := Weights(s)
	out := map[string]int{}
	for _, c := range openPrimaries(s) {
		if c.F(FieldPriorityBy) == PriorityByRule {
			out[c.ID] = w[c.ID]
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// admissionOrder is the many-brief form's cards in the order add admits them (the owner,
// 2026-10-07: "Any cards that have a lot of dependencies on them, you should just put as
// early in the work order as possible, by default: just in the ordering in their work
// set"): within each run of cards between sentinels, a card comes as early as its own
// needs among the run allow, the cards with more dependents first (transitive, over the
// cards admitted and the open cards of the table that name them), equals in the order
// given; a sentinel keeps its place, and the cards after it stay after it. The simple
// forms (one brief, ids with one needs list) have nothing to order. A need that names no
// card of the run constrains nothing here: the table's needs are checked by Add.
func admissionOrder(s *Snapshot, cards []CardAdd) []CardAdd {
	if len(cards) < 2 {
		return cards
	}
	// the dependents of every card admitted, over the admitted and the open cards
	var all []*Card
	for _, c := range cards {
		if !c.Sentinel {
			all = append(all, &Card{ID: c.ID, Fields: map[string]string{"needs": joinIDs(c.Needs)}})
		}
	}
	if s != nil && s.Work != nil {
		all = append(all, openPrimaries(s)...)
	}
	weights := weightsOver(all)
	out := make([]CardAdd, 0, len(cards))
	run := func(seg []CardAdd) {
		in := map[string]int{} // the run's cards, by their place given
		for i, c := range seg {
			in[c.ID] = i
		}
		placed := map[string]bool{}
		for len(placed) < len(seg) {
			best := -1
			for i, c := range seg {
				if placed[c.ID] {
					continue
				}
				free := true
				for _, n := range c.Needs {
					if _, ok := in[n]; ok && !placed[n] && n != c.ID {
						free = false
						break
					}
				}
				if free && (best < 0 || weights[c.ID] > weights[seg[best].ID]) {
					best = i
				}
			}
			if best < 0 {
				// a cycle within the run: Add refuses it; the order given stands
				for _, c := range seg {
					if !placed[c.ID] {
						out = append(out, c)
						placed[c.ID] = true
					}
				}
				return
			}
			out = append(out, seg[best])
			placed[seg[best].ID] = true
		}
	}
	start := 0
	for i, c := range cards {
		if c.Sentinel {
			run(cards[start:i])
			out = append(out, c)
			start = i + 1
		}
	}
	run(cards[start:])
	return out
}

// joinIDs is the ids comma joined, each once.
func joinIDs(ids []string) string {
	var out []string
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return strings.Join(out, ",")
}

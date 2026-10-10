package sprint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// A card's weight (docs/SPEC-SPRINT.md, the deal, the ask, merging and the inbox; the
// owner, 2026-10-04: "It feels like these critical blockers should have some elevated
// priority or, something ... they should be at front of queue"). One night the root of
// about 140 schema cards was dealt, read and reworked like any other card, and its first
// attempt died with nobody seeing what waited behind it. The weight of a card is the number
// of cards transitively waiting on it through their needs (Weights, walked backwards over
// every card on the table that has not landed); every needs change writes it on the
// primary as `behind` (weighUnits, from add and drop). The inbox lists the heaviest
// judgments first (byWeight); the deal, the ask and the lander's batch keep the modelled
// order (tla/SprintTables.tla: stream turns, work order) until the model orders by weight
// too. A card with CriticalBehind or more behind it is critical: it starts on a pro route
// whatever its brief's tier says (ceilingTier), with pro's deadline and budget, the inbox
// prefixes its judgments, and where names the top five (Critical).

// FieldBehind is the primary's weight as the tick last wrote it.
const FieldBehind = "behind"

// CriticalBehind is the weight at and above which a card is critical.
const CriticalBehind = 10

// openPrimaries is every primary on the table not landed, sentinels aside.
func openPrimaries(s *Snapshot) []*Card {
	var out []*Card
	if s.Work == nil {
		return nil
	}
	for _, c := range s.Work.Column(Waiting, Ready, Working, Review, Merging) {
		if !IsSentinel(c) {
			out = append(out, c)
		}
	}
	return out
}

// Weights is each open primary's weight: how many cards on the table, not landed, wait on
// it through their needs, transitively. A card off the table or landed weighs nothing and
// counts for nothing, and a need the waiting card waived adds nothing to the need's weight.
func Weights(s *Snapshot) map[string]int { return weightsOver(openPrimaries(s)) }

// weightsOver is Weights over the cards given (their ids and needs).
func weightsOver(cards []*Card) map[string]int {
	open := map[string]bool{}
	needers := map[string][]string{} // need -> the cards that name it
	for _, c := range cards {
		open[c.ID] = true
	}
	for _, c := range cards {
		waived := Split(c.F("waived"))
		for _, n := range Split(c.F("needs")) {
			if open[n] && n != c.ID && !contains(waived, n) { // a waived need does not hold the card (unmet)
				needers[n] = append(needers[n], c.ID)
			}
		}
	}
	out := make(map[string]int, len(open))
	for id := range open {
		seen := map[string]bool{id: true}
		stack := append([]string(nil), needers[id]...)
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[x] {
				continue
			}
			seen[x] = true
			stack = append(stack, needers[x]...)
		}
		out[id] = len(seen) - 1
	}
	return out
}

// byWeight is the cards with the heavier first, stable: cards of one weight keep the order
// given (stream turns, work order).
func byWeight(cards []*Card, w map[string]int) []*Card {
	out := append([]*Card(nil), cards...)
	sort.SliceStable(out, func(i, j int) bool { return w[out[i].ID] > w[out[j].ID] })
	return out
}

// IsCritical says the primary's weight, as the tick last wrote it, makes it critical.
func IsCritical(c *Card) bool { return c.Int(FieldBehind) >= CriticalBehind }

// weighUnits is the units that write each open primary's weight where a needs change moves
// it (FieldBehind): adding is the cards an add admits (their ids and needs), dropping the
// cards a drop takes off. Every needs change writes the weights it changes, so the field is
// exact and no tick recounts it (docs/SPEC-SPRINT.md, weight).
func weighUnits(s *Snapshot, adding []*Card, dropping map[string]bool) []Unit {
	var cards []*Card
	for _, c := range openPrimaries(s) {
		if !dropping[c.ID] {
			cards = append(cards, c)
		}
	}
	w := weightsOver(append(cards, adding...))
	var out []Unit
	for _, c := range cards {
		n := w[c.ID]
		if c.Int(FieldBehind) == n && (n != 0 || c.F(FieldBehind) == "") {
			continue
		}
		set, unset := map[string]string{FieldBehind: itoa(n)}, []string(nil)
		if n == 0 {
			set, unset = nil, []string{FieldBehind}
		}
		out = append(out, Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, set, unset...))},
			Moved: fmt.Sprintf("%s behind=%d", c.ID, n)})
	}
	return out
}

// CriticalCard is one of where's critical lines: the primary, its weight and its state.
type CriticalCard struct {
	ID     string `json:"id"`
	Behind int    `json:"behind"`
	State  string `json:"state"`
}

// Critical is the n heaviest open primaries with a weight above zero, heaviest first, by id
// among equals; nil when none has a weight.
func Critical(s *Snapshot, n int) []CriticalCard {
	w := Weights(s)
	var cards []*Card
	for _, c := range s.Work.Column(Waiting, Ready, Working, Review, Merging) {
		if !IsSentinel(c) && w[c.ID] > 0 {
			cards = append(cards, c)
		}
	}
	SortCards(cards)
	cards = byWeight(cards, w)
	var out []CriticalCard
	for _, c := range cards {
		if len(out) == n {
			break
		}
		out = append(out, CriticalCard{ID: c.ID, Behind: w[c.ID], State: c.Col})
	}
	return out
}

// CriticalLine is where's line under the summary: "critical: <id> <n> behind, <state>;
// ..."; "" when nothing is critical.
func CriticalLine(cards []CriticalCard) string {
	if len(cards) == 0 {
		return ""
	}
	var parts []string
	for _, c := range cards {
		parts = append(parts, fmt.Sprintf("%s %d behind, %s", c.ID, c.Behind, c.State))
	}
	return "critical: " + strings.Join(parts, "; ")
}

// criticalTier is the tier a critical card starts on and never goes below (ceilingTier):
// pro, whatever its brief's line 1 says; a pinned model, a pinned tier and a frontier card
// keep their own.
func criticalTier(c *Card, m cardhdr.Model) (string, bool) {
	if IsCritical(c) && m.Pin == "" && m.Tier != cardhdr.RouteFrontier && c.F(FieldTier) == "" {
		return cardhdr.RoutePro, true
	}
	return "", false
}

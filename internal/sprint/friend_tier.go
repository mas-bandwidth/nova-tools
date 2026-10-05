package sprint

import (
	"cmp"
	"slices"
	"strings"
)

// A card re-tiered off its holder's tiers (docs/SPEC-SPRINT.md section 1,
// friend-deal-one-tier.w3; the owner, 2026-10-05: "You should automatically rebalance
// queues" and "What else is like this? Bugs in how cards are dealt and rebalanced?"). A
// friend's card dealt on one tier and re-tiered after (brief --tier, rework --tier) sat with
// a friend who does not serve its new tier: nothing dealt it again. Every deal and move to a
// friend writes the tier it is on (FieldDealtTier, CardTier as the deal leaves it), and every
// tick's level now takes back each card on a friend's row that she has not started
// (friendStarted), whose tier now is not the one it was dealt on and is not one of hers
// (friendTakes), as friend take does (FriendTake: withdrawn on her row, taken_from her, its
// primary ready); the friends' deal places it again on a friend serving the tier, or the
// machines' deal does when none is up with room. A started card stays with her and
// finishes. A card dealt before the field was written is read as re-tiered. A card the
// attempt cap's judgment deals to a stronger friend (AttemptCapDeal) is on the tier it was
// dealt on, so it stays: the take back answers a re-tier, never the cap's choice.

// FieldDealtTier is the tier a friend's work card was dealt or moved on (CardTier of its
// primary as that deal left it).
const FieldDealtTier = "dealt_tier"

// friendRetierTakes is the take back of every unstarted card on a friend's row re-tiered
// since its deal off her tiers, one FriendTake per friend, the friends by name; taken is
// each friend a card was taken from, whom the level leaves alone this tick (her counts are
// the take's).
func friendRetierTakes(s *Snapshot, seats []FriendSeat, who string) (p Plan, taken map[string]bool) {
	taken = map[string]bool{}
	seats = slices.Clone(seats)
	slices.SortFunc(seats, func(a, b FriendSeat) int { return cmp.Compare(a.Name, b.Name) })
	for _, f := range seats {
		row := FriendRow(f.Name)
		var ids, why []string
		for _, c := range append(slices.Clone(s.Fleet.Cell(row, Ready)), s.Fleet.Cell(row, Working)...) {
			pr := s.Work.Placed(c.F("primary"))
			if pr == nil || friendStarted(s, f, c) {
				continue
			}
			if tier := CardTier(pr); tier != c.F(FieldDealtTier) && !friendTakes(f, tier) {
				ids = append(ids, c.ID)
				why = append(why, c.ID+" "+orDash(c.F(FieldDealtTier))+"->"+tier)
			}
		}
		if len(ids) == 0 {
			continue
		}
		reason := "re-tiered off her tiers (" + orDash(strings.Join(friendTiers(f), ",")) + "): " + strings.Join(why, ", ")
		tp := FriendTake(s, FriendTakeReq{Friend: f.Name, IDs: ids, Reason: reason, Who: who})
		p.Units = append(p.Units, tp.Units...)
		p.Refused = append(p.Refused, tp.Refused...)
		taken[f.Name] = true
	}
	return p, taken
}

package sprint

import (
	"cmp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A card re-tiered after it was dealt (docs/SPEC-SPRINT.md section 1, "One tier for every
// friend decision", friend-deal-one-tier-b.w2; the owner, 2026-10-05: "it should just happen
// mechanically"). The deal and the level gate every placement on the card's tier (DealTier,
// friendTakes), but a card already on a friend's row when `brief --tier` or `rework --tier`
// moved it to a tier she does not serve stayed there: a friend serving flash and pro held
// heavy cards. Every tick takes such a card back, as friend take does, and the deal places it
// again on a friend that serves its tier.

// retierTakeBacks is the tick's take back of the cards each friend up holds, ready or
// working, that she has not started and whose tier (DealTier) her tiers do not hold: one
// FriendTake a friend, so a working card taken frees a lane only for a card that stays. A
// card the plan already moves (placed, an entry of placed: the level moves a ready card to a
// friend that serves its tier) is left to that move. A card pinned to its holder by the
// attempt cap's default answer (AttemptCapDeal: WHO names her and the brief's attempt count
// is stamped) is the one placement outside her tiers by design, and stays.
func retierTakeBacks(s *Snapshot, r TickReq, placed []Unit) Plan {
	var p Plan
	moved := map[string]bool{}
	for _, u := range placed {
		moved[u.Key] = true
	}
	for _, f := range r.Friends {
		if f.Status != Up {
			continue
		}
		row := FriendRow(f.Name)
		var ids []string
		tiers := map[string]bool{}
		for _, col := range []string{Ready, Working} {
			for _, wc := range s.Fleet.Cell(row, col) {
				pr := s.Work.Placed(wc.F("primary"))
				if wc.F("kind") != "work" || pr == nil || moved[wc.ID] || friendStarted(s, f, wc) {
					continue
				}
				tier := DealTier(pr)
				if friendTakes(f, tier) {
					continue
				}
				if who, ok := FriendCard(pr); ok && who == f.Name && pr.F(FieldBriefAttempt) != "" {
					continue
				}
				ids = append(ids, wc.ID)
				tiers[tier] = true
			}
		}
		if len(ids) == 0 {
			continue
		}
		slices.Sort(ids)
		why := "re-tiered: her tiers do not hold " + joinSorted(tiers)
		q := FriendTake(s, FriendTakeReq{Friend: f.Name, IDs: ids, Reason: why, Who: r.who()})
		p.Units, p.Refused = append(p.Units, q.Units...), append(p.Refused, q.Refused...)
	}
	return p
}

// joinSorted is the keys of set, sorted and comma joined.
func joinSorted(set map[string]bool) string {
	var out []string
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}

// DealTier is the one tier resolution of a card (docs/SPEC-SPRINT.md section 1, "One tier for
// every friend decision", friend-deal-one-tier-b.w2): the tier the primary is on now (cardTier:
// FieldTierNow, the tier pinned by rework or brief --tier, or its brief's line 1), the
// dealer's default, flash, when it names none or there is no primary. The friend deal, the
// friend level and the take back of a re-tiered card (retierTakeBacks) read it, and the
// packet's tier (DealtTier, deal_tier.go) resolves a card with no route tier the same way, so
// the lane filters, which read the packet, cannot disagree with the deal about a card.
func DealTier(primary *Card) string {
	if primary == nil {
		return cardhdr.RouteFlash
	}
	return cmp.Or(cardTierOf(primary), cardhdr.RouteFlash)
}

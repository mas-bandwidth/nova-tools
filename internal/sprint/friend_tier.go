package sprint

import (
	"slices"
	"strings"
)

// One tier for every friend decision (docs/SPEC-SPRINT.md section 1,
// friend-deal-one-tier-bb.w2; the owner, 2026-10-05: "it should just happen mechanically").
// The deal and the level gate every placement on the card's tier (Snapshot.DealTier,
// friendTakes), but a card already on a friend's row when `brief --tier` or `rework --tier`
// moved it to a tier she does not serve stayed there: a friend serving flash and pro held
// heavy cards. The friends' level, every tick, takes such a card back, as friend take does,
// and the deal places it again on a friend that serves its tier.

// retierTakeBacks is the level's take back of the cards each friend up holds, ready or
// working, that she has not started and whose tier (Snapshot.DealTier, the one tier
// resolution every friend decision reads) her tiers do not hold: one FriendTake a friend,
// so a working card taken frees a lane only for a card that stays. A card the plan already
// moves or starts (skip: the level's moves, the tick's earlier moves and starts) is left to
// that move, and a card the caller read as started (started) or friendStarted names stays.
// A card pinned to its holder by the attempt cap's default answer (AttemptCapDeal: WHO names
// her and the brief's attempt count is stamped) is the one placement outside her tiers by
// design, and stays.
func retierTakeBacks(s *Snapshot, seats []FriendSeat, who string, started map[string]string, skip map[string]bool) Plan {
	var p Plan
	for _, f := range seats {
		if f.Status != Up {
			continue
		}
		row := FriendRow(f.Name)
		var ids []string
		tiers := map[string]bool{}
		for _, col := range []string{Ready, Working} {
			for _, wc := range s.Fleet.Cell(row, col) {
				pr := s.Work.Placed(wc.F("primary"))
				if wc.F("kind") != "work" || pr == nil || skip[wc.ID] || started[wc.ID] != "" || friendStarted(s, f, wc) {
					continue
				}
				tier := s.DealTier(pr)
				if friendTakes(f, tier) {
					continue
				}
				if name, ok := FriendCard(pr); ok && name == f.Name && pr.F(FieldBriefAttempt) != "" {
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
		q := FriendTake(s, FriendTakeReq{Friend: f.Name, IDs: ids, Reason: why, Started: started, Who: who})
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

package sprint

import (
	"cmp"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// DealtTier is the tier a dealt card's packet names (Packet.Tier), never empty
// (dealt-packet-carries-the-tier.w1; on 2026-10-05 the audit cards were dealt with no tier
// in the packet, a flash friend's runner read "tier -" and handed back its own pinned
// cards): the tier the card's route was drawn from (FieldTier on the work or read card),
// else the tier its primary is on (cardTier: FieldTierNow, or the tier pinned, or its
// brief's line 1), else the stream's default, flash. It is the tier every friend deal
// gates on (friendTakes), so a friend's packet names a tier her class covers.
func DealtTier(c, primary *Card) string {
	if primary != nil {
		return DealTier(primary)
	}
	if t := c.F(FieldTier); t != "" {
		return t
	}
	return cardhdr.RouteFlash
}

// DealTier is the one tier resolution of a card (docs/SPEC-SPRINT.md section 1, "One tier for
// every friend decision", friend-deal-one-tier-b.w2): the tier the primary is on now (cardTier:
// FieldTierNow, the tier pinned by rework or brief --tier, or its brief's line 1), the
// dealer's default, flash, when it names none or there is no primary. The friend deal, the
// friend level, the take back of a re-tiered card (retierTakeBacks), the lane filters and
// the packet's tier (DealtTier) all read it, so no two of them can disagree about a card.
func DealTier(primary *Card) string {
	if primary == nil {
		return cardhdr.RouteFlash
	}
	return cmp.Or(cardTierOf(primary), cardhdr.RouteFlash)
}

package sprint

import "github.com/mas-bandwidth/nova-tools/pkg/cardhdr"

// DealtTier is the tier a dealt card's packet names (Packet.Tier), never empty
// (dealt-packet-carries-the-tier.w1; on 2026-10-05 the audit cards were dealt with no tier
// in the packet, a flash friend's runner read "tier -" and handed back its own pinned
// cards): the tier the card's route was drawn from (FieldTier on the work or read card),
// else the tier its primary is on (cardTier: FieldTierNow, or the tier pinned, or its
// brief's line 1), else the stream's default, flash. It is the tier every friend deal
// gates on (friendTakes), so a friend's packet names a tier her class covers.
func DealtTier(c, primary *Card) string {
	if t := c.F(FieldTier); t != "" {
		return t
	}
	if primary != nil {
		if now, _ := CardTiers(primary); now != "" {
			return now
		}
	}
	return cardhdr.RouteFlash
}

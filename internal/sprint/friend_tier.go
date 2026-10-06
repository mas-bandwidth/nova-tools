package sprint

import "github.com/mas-bandwidth/nova-tools/internal/cardhdr"

// FriendTier is the one tier every friend decision reads (docs/SPEC-SPRINT.md,
// friend-deal-one-tier-b.w1): the tier the card is on now (cardTierOf: the tier
// its last route deal drew, or the tier pinned, or its brief's line 1), and
// flash when that names none. A withdrawn attempt at its redeal bound below its
// ceiling is read at the tier it escalates to (escalating), the same tier the
// deal offers. The queue packet names this tier through DealtTier and CardTiers,
// which share cardTier, so a lane that reads the packet reads this resolution.
func FriendTier(s *Snapshot, c *Card) string {
	if c == nil {
		return cardhdr.RouteFlash
	}
	if s != nil {
		c = escalating(s, c)
	}
	if t := cardTierOf(c); t != "" {
		return t
	}
	return cardhdr.RouteFlash
}

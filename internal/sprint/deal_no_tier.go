package sprint

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// NNoTier is the tick's judgment of a ready primary whose tier is unset (TierUnset), one
// per card ("<card> has no tier"), closed when its tier is set (brief <card> --tier) or it
// leaves ready (a-card-without-a-tier-is-not-dealt.w1).
const NNoTier = "a card has no tier"

// TierUnset says the primary c has a brief and names no tier anywhere: no tier pinned
// (FieldTier), none drawn or written at add (FieldTierNow), no tier: on its brief's line 1,
// no model pin, and not critical (criticalTier). No deal, the friends' or the machines',
// places it (TickDeal, friendDeal): the dealer's default of flash once dealt such cards to a
// flash friend whose runner could not read their tier and ran them (the night of 2026-10-05,
// every lane ending with no result or a HOLD). A card with no brief (add
// --count: a worker handed no task) is not judged by it, its tier read when its brief is
// given; a sentinel has no tier and is never dealt.
func TierUnset(c *Card) bool {
	if c == nil || IsSentinel(c) || c.F("brief") == "" || c.F(FieldTier) != "" || c.F(FieldTierNow) != "" {
		return false
	}
	m, _ := cardhdr.ReadModel(c.F("brief"))
	if _, ok := criticalTier(c, m); ok {
		return false
	}
	return m.Tier == "" && m.Pin == ""
}

// noTierWhat is the words of a card's NNoTier judgment, with the command that sets its tier.
func noTierWhat(id string) string {
	return id + " has no tier: no deal places a card whose tier is unset; set it: nova-sprint brief " + id + " --tier <" + cardhdr.RouteList + ">"
}

// BriefNamesTier says the brief names its tier: a tier: word on line 1, or a model pin
// (cardhdr.ReadModel). The add verb refuses a brief that does not unless --tier is given.
func BriefNamesTier(brief string) bool {
	m, _ := cardhdr.ReadModel(brief)
	return m.Tier != "" || m.Pin != ""
}

// WithBriefTier is the brief with add --tier's tier, the tier of a brief that names none:
// " tier: <tier>" on the end of its line 1, as if written there (its ceiling: flash first,
// route.go), or a line 1 of its own when line 1 is a header line (headerKey). A
// brief that names its own tier, or pins a model, is unchanged; a tier that is none of
// cardhdr.RouteList is refused.
func WithBriefTier(brief, tier string) (string, string) {
	if !cardhdr.IsRoute(tier) {
		return "", "--tier wants " + cardhdr.RouteList + ", found " + tier
	}
	if BriefNamesTier(brief) {
		return brief, ""
	}
	first, rest, nl := strings.Cut(brief, "\n")
	if k, _, ok := cardhdr.KeyValue(first); ok && headerKey(k) {
		// line 1 is a header line (REPO:, BASE:, Needs: ...) whose value the word would
		// change: the tier is a line 1 of its own above it
		return "tier: " + tier + "\n" + brief, ""
	}
	out := strings.TrimRight(first, " \t") + " tier: " + tier
	if nl {
		out += "\n" + rest
	}
	return out, ""
}

// headerKey says a line 1 key is a header the tools read a value from (REPO:, BASE:, WHO:,
// PATHS:, Needs:, model: and the like: an upper-case key, or one of the lower-case keys
// read), never RESULT:, whose tier word is read on line 1, nor a title such as "c1: ...".
func headerKey(k string) bool {
	switch strings.ToLower(k) {
	case "result":
		return false
	case "needs", "model", "tokens", "deadline", "stage", "base-repo", "base-sha":
		return true
	}
	return k == strings.ToUpper(k) && strings.ToLower(k) != k
}

// NoTierWhy is the add verb's refusal of a brief that names no tier, given no --tier.
func NoTierWhy(what string) string {
	return what + " names no tier (no tier: on line 1, no model pin), and a card whose tier is unset is dealt to no one; write tier: <" + cardhdr.RouteList + "> on its line 1, or give --tier <" + cardhdr.RouteList + ">"
}

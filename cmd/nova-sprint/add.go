package main

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// addTierWords is add's --tier flag help.
const addTierWords = "the tier of every brief this add admits that names none (" + cardhdr.RouteList + "): written on the end of the brief's line 1 as ` tier: <tier>` (a line of its own above a header line 1), its ceiling (flash first); a brief naming its own tier, or pinning a model, keeps it. Without it a brief that names no tier on line 1 (and pins no model) is refused, nothing written: a card whose tier is unset is dealt to no one (a-card-without-a-tier-is-not-dealt.w1); the card generators pass it"

// addBriefTier is the brief add admits with --tier (a-card-without-a-tier-is-not-dealt.w1):
// the brief with that tier (sprint.WithBriefTier), or the refusal naming what; without
// --tier, or for an empty brief (the card lint names it), the brief as it is.
func addBriefTier(what, brief, tier string) (string, string) {
	if tier == "" || strings.TrimSpace(brief) == "" {
		return brief, ""
	}
	out, why := sprint.WithBriefTier(brief, tier)
	if why != "" {
		return "", what + ": " + why
	}
	return out, ""
}

// addNoTier is add's refusal of a brief that names no tier (sprint.BriefNamesTier), run
// after the card checks, whose tier-line finding speaks first for a card brief with a
// PATHS: line: "" for a brief that names one, and for an empty brief.
func addNoTier(what, brief string) string {
	if strings.TrimSpace(brief) == "" || sprint.BriefNamesTier(brief) {
		return ""
	}
	return sprint.NoTierWhy(what)
}

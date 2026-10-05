package sprint

import (
	"slices"
	"strings"
)

// A friend's model per tier (docs/SPEC-FRIEND.md, a friend's models; the owner,
// 2026-10-05: "how do friends know which of THEIR models should be used per-tier?" and "Is
// the decision made here? Do they decide? Do they even know?"). Her nova-config row maps each
// tier she serves to the model she runs it on (config.FriendModels), friend sync carries the
// map onto her seat (FriendSeat.Models), and the deal writes the model of the card's tier on
// the work card it places on her row (FieldModel), so the card's packet carries it
// (PacketOf) and her brief's tier line says it (FriendTierLine). A tier of hers with no model
// is still dealt (the migration keeps every tier she served), and its card carries none: she
// runs it on her session's model, and the checks warn.

// friendModelOf is the model the friend runs the tier's cards on, "" when her row names none.
func friendModelOf(f FriendSeat, tier string) string {
	return f.Models[tier]
}

// FriendModelOf is the model the friend of seats named name runs the tier's cards on, ""
// when she is not among them or her row names none for it.
func FriendModelOf(seats []FriendSeat, name, tier string) string {
	for _, f := range seats {
		if f.Name == name {
			return friendModelOf(f, tier)
		}
	}
	return ""
}

// withFriendModel is the deal's unit u with the tier it was dealt on and her row's model for
// that tier written on the work card it places (card, on the fleet table): FieldTier, which
// the card's packet reads (PacketOf), and FieldModel, in place of any model the card carried
// before, for a friend's card carries her row's model, never a fleet route's. An empty model
// writes neither: the card carries no model, and her brief no tier line.
func withFriendModel(u Unit, card, tier, model string) Unit {
	if model == "" {
		return u // no model: the card is dealt as before, its tier the brief's
	}
	for i, ch := range u.Changes {
		if ch.Table != Fleet || ch.Entry.ID != card {
			continue
		}
		e := ch.Entry
		set := map[string]string{}
		for k, v := range e.Set {
			set[k] = v
		}
		set[FieldTier], set[FieldModel] = tier, model
		e.Unset = slices.DeleteFunc(slices.Clone(e.Unset), func(f string) bool { return f == FieldModel || f == FieldTier })
		e.Set = nonEmpty(set)
		u.Changes[i].Entry = e
	}
	return u
}

// FriendTierLine is the tier line of a friend's brief: "tier: <tier> model: <model>", the
// model her row names for the tier, or "tier: <tier>" alone when it names none.
func FriendTierLine(tier, model string) string {
	if model == "" {
		return "tier: " + tier
	}
	return "tier: " + tier + " model: " + model
}

// ReportModel is the model a friend's REPORT.md says the card ran on: the value of its first
// Model: line, or the model= word of its first Usage: line, in lower case; "" when it says
// none.
func ReportModel(report string) string {
	for _, l := range strings.Split(report, "\n") {
		key, value, found := strings.Cut(strings.TrimLeft(l, "#*-_ \t"), ":")
		if !found {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(strings.Trim(key, "*_"))) {
		case "model":
			if w := strings.Fields(strings.Trim(strings.TrimSpace(value), "*_`")); len(w) > 0 {
				return strings.ToLower(strings.Trim(w[0], "*_`.,;"))
			}
		case "usage":
			for _, w := range strings.Fields(value) {
				if m, ok := strings.CutPrefix(w, "model="); ok && m != "" {
					return strings.ToLower(strings.Trim(m, "*_`.,;"))
				}
			}
		}
	}
	return ""
}

// FriendModelMismatch is why a friend's finish whose card carries a model is refused: her
// report names no model, or another; "" when it names the card's (case aside), or the card
// carries none.
func FriendModelMismatch(want, report string) string {
	if want == "" {
		return ""
	}
	switch got := ReportModel(report); {
	case got == "":
		return "the report names no model (want a line Model: " + want + ", her row's model for the card's tier)"
	case got != strings.ToLower(want):
		return "the report says the card ran on " + got + ", and her row's model for the card's tier is " + want
	}
	return ""
}

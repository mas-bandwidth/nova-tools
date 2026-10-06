package sprint

import (
	"maps"
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

// FriendRowModels sets each work packet's model to her row's for its tier (models), never
// the one its card carries: a card the level moved to her carried the giver's, and a tier her
// row names none for clears it, so her brief has no tier line, her lane no model flag and her
// finish no model check. A read packet is left as it is.
func FriendRowModels(packets []Packet, models map[string]string) {
	for i := range packets {
		if packets[i].Kind == "work" {
			packets[i].Model = models[packets[i].Tier]
		}
	}
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

// A friend's probe (the fifth way she knows, "proven before first use"; docs/SPEC-FRIEND.md,
// a friend's models). A tier her row maps to a model is dealt no real card until her probe
// of it has returned that model: friend sync puts one probe job of the tier in her inbox
// (FriendProbeJob, FriendProbeBrief: report your model and harness), reads her report, and
// records the model it names on her seat (FriendSeat.Probes); the deal and the level take
// the tier only once the two agree (friendTakes). A changed model is a new probe, by its own
// job. A tier with no model needs no probe and is dealt as before.

// FriendProven says the tier's cards may reach her: her row names no model for it, or her
// probe of it reported that model (case aside).
func FriendProven(models, probes map[string]string, tier string) bool {
	m := models[tier]
	return m == "" || strings.EqualFold(probes[tier], m)
}

// FriendProbesOwed is the tiers of models whose probe is owed (not FriendProven), sorted.
func FriendProbesOwed(models, probes map[string]string) []string {
	var out []string
	for _, t := range slices.Sorted(maps.Keys(models)) {
		if !FriendProven(models, probes, t) {
			out = append(out, t)
		}
	}
	return out
}

// FriendProbeJob is the job (inbox/<job>, outbox/<job>, and her queue file's task id) of
// her probe of the tier on the model: probe-<tier>-<model>, the model's characters other
// than letters, digits, '.', '_' and '-' written '-', so a changed model is a new job.
func FriendProbeJob(tier, model string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '-'
	}, model)
	return "probe-" + tier + "-" + clean
}

// FriendProbeBrief is the BRIEF.md of her probe of the tier on the model: no repository and
// no branch, only the two lines she reports, which friend sync reads (ReportModel).
func FriendProbeBrief(name, tier, model string) string {
	job := FriendProbeJob(tier, model)
	return "STATUS: nova-sprint probe " + job + " for friend " + name + "; no branch, nothing to push; when done, write outbox/" + job + "/REPORT.md and outbox/" + job + "/RESULT.md\n" +
		FriendTierLine(tier, model) + "\n\n" +
		"Your nova-config row now serves " + tier + " on " + model + ". Before any real " + tier + " card reaches you, prove it: run this probe in a child on " + model +
		" (or, if your harness cannot choose a child's model, on your session's model) and have that child write outbox/" + job + "/REPORT.md with exactly these lines:\n\n" +
		"Verdict: LAND\nModel: <the model id the child runs on, as the harness names it>\nHarness: <the harness and its version>\n\n" +
		"and outbox/" + job + "/RESULT.md with one line, RESULT: " + job + ". Report the model you are on, not the one asked for: a probe that names another model holds the tier, and the coordinator is told.\n"
}

// A card the level moves (FriendLevel, friendUnstartedLevel) that carries a model (its
// giver's row named one for its tier, withFriendModel) goes only to a friend whose row names
// a model for its tier too (friendReceives), and it goes with hers: a receiver with none
// would run on her session's model a card dealt to be run on a named one. A card that
// carries none moves as before, as the deal still deals a tier with no model (it is served
// until the coordinator fills it). A card the level would have moved but for that is
// refused, its refusal naming the tier and the friends (noModelRefusals).

// friendReceives says the level may move a card of the tier carrying the model carried to
// the friend: she takes it (friendTakes), and her row names a model for the tier or the card
// carries none.
func friendReceives(f FriendSeat, tier, carried string) bool {
	return friendTakes(f, tier) && (friendModelOf(f, tier) != "" || carried == "")
}

// noModelRefusal is a card's tier and the friends the level refused as its friend for want
// of a model for that tier.
type noModelRefusal struct {
	tier    string
	friends []string
}

// with is r with the friend refused for the tier, each friend once.
func (r noModelRefusal) with(tier, friend string) noModelRefusal {
	r.tier = tier
	if !slices.Contains(r.friends, friend) {
		r.friends = append(slices.Clone(r.friends), friend)
	}
	return r
}

// noModelRefusals is the level's refusals of the cards in refused that none of its units
// moved, by card id: "not moved: tier <tier> has no model on the row of friend <a>, <b>
// (nova-config friend set <name> --model <tier>=<model>)".
func noModelRefusals(refused map[string]noModelRefusal, moved []Unit) []Refusal {
	var out []Refusal
	for _, id := range slices.Sorted(maps.Keys(refused)) {
		if slices.ContainsFunc(moved, func(u Unit) bool { return u.Key == id }) {
			continue
		}
		r := refused[id]
		friends := slices.Sorted(slices.Values(r.friends))
		out = append(out, Refusal{Key: id, Why: "not moved: tier " + r.tier + " has no model on the row of friend " + strings.Join(friends, ", ") +
			" (nova-config friend set <name> --model " + r.tier + "=<model>)"})
	}
	return out
}

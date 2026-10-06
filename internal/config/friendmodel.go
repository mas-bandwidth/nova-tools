package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// A friend's models (docs/SPEC-FRIEND.md, a friend's models; the owner,
// 2026-10-05: "how do friends know which of THEIR models should be used
// per-tier?" and "Should this be made part of the friend configuration?"):
// her row maps each tier she serves to the model she runs it on, and says
// what her harness can do to run a card on that model. The row is the one
// source; the deal, the card's packet and brief, her daemon and the finish
// all read the model from it.
const (
	// FieldFriendModel is the row's model per tier, a TypeMap over Tiers.
	FieldFriendModel = "model"
	// FieldFriendChildren is whether her harness runs child agents.
	FieldFriendChildren = "children"
	// FieldFriendChildModel is whether a child's model can be chosen.
	FieldFriendChildModel = "child_model"
)

// The words of children and child_model, and the default (migration 0035
// gives every row there before it yes and yes, so every tier it served is
// served still).
const (
	FriendYes = "yes"
	FriendNo  = "no"
)

// FriendYesNo is the enum of children and child_model.
var FriendYesNo = []string{FriendYes, FriendNo}

// How a friend runs a card on its tier's model (FriendHow).
const (
	// HowLane is a one-shot friend: each card its own headless process,
	// launched with the harness's model flag.
	HowLane = "lane"
	// HowChild is a friend in one session whose children take a model: each
	// card runs in a child on the tier's model.
	HowChild = "child"
	// HowSession is a friend in one session whose children run one model, or
	// who has none: every card runs on her session's model, so her row serves
	// only the tiers that one model serves.
	HowSession = "session"
)

// FriendHow is how the friend of the row runs a card on its tier's model:
// a lane takes the model flag, a child that can be given a model runs on it,
// and otherwise the session's model runs every card.
func FriendHow(r Row) string {
	switch {
	case FriendMode(r) == FriendModeOneShot:
		return HowLane
	case r.Fields[FieldFriendChildren] != FriendNo && r.Fields[FieldFriendChildModel] != FriendNo:
		return HowChild
	}
	return HowSession
}

// FriendModels is the row's model per tier; empty when it names none.
func FriendModels(r Row) map[string]string {
	m, _ := parseMap(r.Fields[FieldFriendModel], Tiers)
	if m == nil {
		m = map[string]string{}
	}
	return m
}

// FriendModelWarnings is what the row leaves unfilled, a line each: a tier
// she serves with no model, which the deal still sends her (the migration
// keeps today's tiers served) and her harness runs on whatever model her
// session is set to, until the coordinator fills it.
func FriendModelWarnings(r Row) []string {
	models := FriendModels(r)
	var out []string
	for _, t := range commaWords(r.Fields["tiers"]) {
		if models[t] == "" {
			out = append(out, fmt.Sprintf("friend %s serves tier %s with no model: her cards of that tier run on whatever model her session is set to; run: nova-config friend set %s --model %s", r.Name, t, r.Name, FormatMap(withEntry(models, t, "<model>"))))
		}
	}
	return out
}

func withEntry(m map[string]string, k, v string) map[string]string {
	out := maps.Clone(m)
	out[k] = v
	return out
}

// checkFriendModels is the row's models against its tiers and its harness:
// a model only for a tier she serves, and a friend whose every card runs on
// her session's model (FriendHow HowSession) names one model, the session's,
// for every tier she serves; a row that asks more of her harness than it can
// do is refused. Fields that failed their own validation are absent and
// skipped.
func checkFriendModels(r Row) error {
	raw, ok := r.Fields[FieldFriendModel]
	if !ok || raw == "" {
		return nil
	}
	models, err := parseMap(raw, Tiers)
	if err != nil {
		return fmt.Errorf("friend %s: --model %v", r.Name, err)
	}
	if tiers, ok := r.Fields["tiers"]; ok {
		served := commaWords(tiers)
		for _, t := range slices.Sorted(maps.Keys(models)) {
			if !slices.Contains(served, t) {
				return fmt.Errorf("friend %s has a model for tier %s, which she does not serve (tiers %s): add the tier with --tiers, or drop it from --model", r.Name, t, tiers)
			}
		}
	}
	if FriendHow(r) == HowSession {
		distinct := slices.Compact(slices.Sorted(maps.Values(models)))
		if len(distinct) > 1 {
			return fmt.Errorf("friend %s runs every card on her session's model (mode %s, children %s, child_model %s), and her row names %d models (%s): her harness cannot run them; serve only the tiers of one model, or set --mode one-shot, or --children yes --child_model yes if her harness can",
				r.Name, FriendMode(r), orYes(r.Fields[FieldFriendChildren]), orYes(r.Fields[FieldFriendChildModel]), len(distinct), strings.Join(distinct, ", "))
		}
	}
	return nil
}

func orYes(s string) string {
	if s == "" {
		return FriendYes
	}
	return s
}

// commaWords is a comma list's words, none for "".
func commaWords(list string) []string {
	if list == "" {
		return nil
	}
	return strings.Split(list, ",")
}

// parseMap reads a TypeMap value: a comma list of key=value, each key one of
// keys and given once, each value one word with no blank, comma or =. ""
// is the empty map.
func parseMap(raw string, keys []string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		k, v, found := strings.Cut(strings.TrimSpace(pair), "=")
		switch {
		case !found || k == "" || v == "":
			return nil, fmt.Errorf("%q is not <key>=<value>: want a comma list of <%s>=<model>", pair, strings.Join(keys, "|"))
		case !slices.Contains(keys, k):
			return nil, fmt.Errorf("%q: want a key of %s", k, strings.Join(keys, ", "))
		case strings.ContainsAny(v, " \t=\n\r"):
			return nil, fmt.Errorf("%q: a value is one word, with no blank or =", v)
		case out[k] != "":
			return nil, fmt.Errorf("%q is given twice", k)
		}
		out[k] = v
	}
	return out, nil
}

// FormatMap is a map in its one spelling: key=value pairs sorted by key,
// comma joined.
func FormatMap(m map[string]string) string {
	var pairs []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		pairs = append(pairs, k+"="+m[k])
	}
	return strings.Join(pairs, ",")
}

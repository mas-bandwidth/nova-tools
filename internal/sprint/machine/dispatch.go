package machine

import (
	"sort"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Batch is one read of one rule this tick (1.4.2, "dispatch"): the keys it
// reads and plans, the keys of the rule its read did not fit (they stay
// queued for a later tick), the halvings it is read and cut at (1.3.5), and
// the read's plan. A key whose rule is not in the table has a Batch with no
// rule and all its keys in Rest: nothing serves it, and it stays queued,
// named in the report.
type Batch struct {
	Rule     string
	Keys     []sprint.AgendaKey
	Rest     []sprint.AgendaKey
	Halvings int
	Plan     sprint.ReadPlan
	// NoRule says no rule of the table serves the keys.
	NoRule bool
}

// Dispatch cuts a tick's keys into reads (1.4.2: "the head's keys and the new
// keys by rule, the keys held back for a drop left out (1.3.5), each rule's
// keys cut to what its read plan fits in layer 1's read bounds by the
// queries' declared costs"), over the tick's rules (sprint.RuleTable). A key
// held back is left out; a key planned at halvings h (after a BUDGET or a
// LIMIT, 1.3.5) is read with the other keys of its rule at the same h, and
// apart from those at another. The batches are in the rules' priority order,
// which is the round robin's (1.4.2).
func Dispatch(keys []sprint.AgendaKey, heldBack map[string]bool, halvings map[string]int, b Budget) []Batch {
	return dispatch(sprint.RuleTable(), keys, heldBack, halvings, b)
}

// dispatch is Dispatch over a given rule table.
func dispatch(rules []sprint.Rule, keys []sprint.AgendaKey, heldBack map[string]bool, halvings map[string]int, b Budget) []Batch {
	byName := map[string]sprint.Rule{}
	for _, r := range rules {
		byName[r.Name] = r
	}
	type group struct {
		rule string
		h    int
	}
	seen := map[string]bool{}
	groups := map[group][]sprint.AgendaKey{}
	for _, k := range keys {
		if heldBack[k.Key] || seen[k.Key] {
			continue
		}
		seen[k.Key] = true
		name := sprint.ServingRule(k.Key)
		h := 0
		if _, ok := byName[name]; ok {
			h = halvings[k.Key]
		}
		groups[group{name, h}] = append(groups[group{name, h}], k)
	}
	gs := make([]group, 0, len(groups))
	for g := range groups {
		gs = append(gs, g)
	}
	prio := func(name string) int {
		if r, ok := byName[name]; ok {
			return r.Priority
		}
		return int(^uint(0) >> 1)
	}
	sort.Slice(gs, func(i, j int) bool {
		pi, pj := prio(gs[i].rule), prio(gs[j].rule)
		if pi != pj {
			return pi < pj
		}
		if gs[i].rule != gs[j].rule {
			return gs[i].rule < gs[j].rule
		}
		return gs[i].h < gs[j].h
	})
	out := make([]Batch, 0, len(gs))
	for _, g := range gs {
		ks := groups[g]
		sortAgenda(ks)
		r, ok := byName[g.rule]
		if !ok {
			out = append(out, Batch{Rule: g.rule, Rest: ks, NoRule: true})
			continue
		}
		plan, rest := r.Read(ks, b.Read, g.h)
		restSet := map[string]bool{}
		for _, k := range rest {
			restSet[k.Key] = true
		}
		var take []sprint.AgendaKey
		for _, k := range ks {
			if !restSet[k.Key] {
				take = append(take, k)
			}
		}
		out = append(out, Batch{Rule: g.rule, Keys: take, Rest: rest, Halvings: g.h, Plan: plan})
	}
	return out
}

// sortAgenda orders keys as the agenda does: by order, then by name (1.1,
// Order).
func sortAgenda(ks []sprint.AgendaKey) {
	sort.SliceStable(ks, func(i, j int) bool {
		if ks[i].Seq != ks[j].Seq {
			return ks[i].Seq < ks[j].Seq
		}
		return ks[i].Key < ks[j].Key
	})
}

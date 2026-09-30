package refmodel

import (
	"fmt"
	"sort"
	"strings"
)

// Difference is one field where the engine's abstract state and the model's
// differ.
type Difference struct {
	Table  string // primary, work, read, merge, stream, member, open, acked, machine, epoch, pending
	ID     string
	Field  string
	Engine string
	Model  string
}

// Sig is the difference without its ids: the table and the field, for
// grouping differences of one kind.
func (d Difference) Sig() string { return d.Table + "." + d.Field }

func (d Difference) String() string {
	return fmt.Sprintf("%s %s %s: engine %q, model %q", d.Table, d.ID, d.Field, d.Engine, d.Model)
}

// Compare lists every field where e (the engine, abstracted) and m (the model)
// differ, in a fixed order.
func Compare(e, m State) []Difference {
	var out []Difference
	add := func(table, id, field string, ev, mv any) {
		es, ms := show(ev), show(mv)
		if es != ms {
			out = append(out, Difference{table, id, field, es, ms})
		}
	}
	for _, id := range union(Keys(e.Primaries), Keys(m.Primaries)) {
		ep, eok := e.Primaries[id]
		mp, mok := m.Primaries[id]
		if !eok || !mok {
			add("primary", id, "exists", eok, mok)
			continue
		}
		add("primary", id, "state", ep.State, mp.State)
		add("primary", id, "stream", ep.Stream, mp.Stream)
		add("primary", id, "kind", ep.Kind, mp.Kind)
		add("primary", id, "needs", ep.Needs, mp.Needs)
		add("primary", id, "waived", ep.Waived, mp.Waived)
		if ep.State != Off && mp.State != Off {
			add("primary", id, "score", ep.Score, mp.Score)
			add("primary", id, "attempt", ep.Attempt, mp.Attempt)
			add("primary", id, "head", ep.Head, mp.Head)
			add("primary", id, "pair", ep.Pair, mp.Pair)
			add("primary", id, "reached", ep.Reached, mp.Reached)
		}
	}
	for _, id := range union(Keys(e.Work), Keys(m.Work)) {
		ew, eok := e.Work[id]
		mw, mok := m.Work[id]
		if !eok || !mok {
			add("work", id, "exists", eok, mok)
			continue
		}
		add("work", id, "place", ew.Place, mw.Place)
		if ew.Place != Gone && mw.Place != Gone {
			add("work", id, "member", ew.Member, mw.Member)
		}
		add("work", id, "gen", ew.Gen, mw.Gen)
		add("work", id, "ok", ew.OK, mw.OK)
		add("work", id, "primary", ew.Primary, mw.Primary)
		add("work", id, "attempt", ew.Attempt, mw.Attempt)
	}
	for _, id := range union(Keys(e.Reads), Keys(m.Reads)) {
		er, eok := e.Reads[id]
		mr, mok := m.Reads[id]
		if !eok || !mok {
			add("read", id, "exists", eok, mok)
			continue
		}
		add("read", id, "place", er.Place, mr.Place)
		add("read", id, "verdict", er.Verdict, mr.Verdict)
		add("read", id, "reader", er.Reader, mr.Reader)
		add("read", id, "attempt", er.Attempt, mr.Attempt)
	}
	for _, id := range union(Keys(e.Merge), Keys(m.Merge)) {
		em, eok := e.Merge[id]
		mm, mok := m.Merge[id]
		if !eok || !mok {
			// a merge place that is gone and one never made are one
			if (eok && em.Place == Gone) || (mok && mm.Place == Gone) {
				continue
			}
			add("merge", id, "exists", eok, mok)
			continue
		}
		add("merge", id, "place", em.Place, mm.Place)
		// The need is compared where it acts: on a stuck card, which
		// resume holds until the need has landed (D6).
		if em.Place == Stuck || mm.Place == Stuck {
			add("merge", id, "need", em.Need, mm.Need)
		}
	}
	for _, id := range union(Keys(e.Streams), Keys(m.Streams)) {
		es, eok := e.Streams[id]
		ms, mok := m.Streams[id]
		if !eok || !mok {
			add("stream", id, "exists", eok, mok)
			continue
		}
		add("stream", id, "state", es.State, ms.State)
		add("stream", id, "cause", es.Cause, ms.Cause)
	}
	for _, id := range union(Keys(e.Members), Keys(m.Members)) {
		add("member", id, "status", e.Members[id], m.Members[id])
	}
	for _, j := range unionJ(e.Open, m.Open) {
		add("open", j.Subject, j.Type, e.Open[j], m.Open[j])
	}
	for _, j := range unionJ(e.Acked, m.Acked) {
		add("acked", j.Subject, j.Type, e.Acked[j], m.Acked[j])
	}
	add("machine", "", "state", e.Machine, m.Machine)
	add("epoch", "", "n", e.Epoch, m.Epoch)
	add("pending", "", "verb", e.Pending, m.Pending)
	add("round", "", "deal", e.DealLast, m.DealLast)
	add("round", "", "ask", e.AskLast, m.AskLast)
	return out
}

func show(v any) string {
	switch x := v.(type) {
	case []string:
		return strings.Join(x, ",")
	case bool:
		if x {
			return "yes"
		}
		return "no"
	}
	return fmt.Sprint(v)
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string(nil), a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func unionJ(a, b map[Judgment]bool) []Judgment {
	seen := map[Judgment]bool{}
	var out []Judgment
	for j := range a {
		seen[j] = true
		out = append(out, j)
	}
	for j := range b {
		if !seen[j] {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].String() < out[k].String() })
	return out
}

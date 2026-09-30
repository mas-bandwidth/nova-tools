package stepbuild

import (
	"container/heap"
	"math"
)

// The order the entries are placed in.
//
// Rows and members depend on each other (section 3). A member's destination
// row must be one the step adds or one that is already there, and a row the
// step deletes must be empty once the members that leave it have gone. Applied
// as one step, the order of the entries does not matter to that; applied as
// several steps, in order, it does: a member entry ahead of the rows entry
// that adds its destination fails NOROW in the step it is in, and a rows entry
// that deletes a row ahead of the member entries that empty it fails
// OCCUPIED. Where the bounds cut would then decide the result. So the entries
// are placed in the order of the input except that
//
//   - a rows entry that adds a row (table, row) is placed before the member
//     entries whose destination is that row, and
//   - the member entries whose source is a row (their from cell, and their
//     guards') are placed before the rows entry that deletes it,
//
// each entry placed as early as what it has to follow allows, so an input in
// which these already hold is placed as it stands. Entries that name one
// member keep their order (TWICE), as do the rows entries that add (a row's
// rank is its place in the order of first occurrence) and the rows entries
// that name one row (an add and a delete of it, in order). A rows entry that
// deletes a row is also kept out of the step of the member entries that name
// that row, and a member entry whose destination is a row a rows entry of the
// step deletes is kept out of that step (place.go: fitRows, fitMembers).
//
// A rows entry that adds a row and deletes another can wait for both ways: its
// adds have to come before the members that go into the new row, and its
// deletes after the members that leave the old one (the rename of a row: the
// adds, then the moves, then the deletes). Placed as one entry it would wait on
// itself. When the entries cannot be ordered so, the rows entries that both add
// and delete are each split into an entry of the adds and an entry of the
// deletes, which are then ordered like any two entries, and placed as the two
// wire entries they are (a step may hold both, as the input's one entry did).
// Entries that still cannot be ordered (each waits for the other) refuse the
// build.

// intHeap is a min-heap of entry indices: the entries ready to place, the
// lowest index first.
type intHeap []int

func (h intHeap) Len() int           { return len(h) }
func (h intHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h intHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *intHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *intHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// appendOnce appends i to a list of entry indices unless it is already its
// last: an entry that names a row twice is one entry.
func appendOnce(list []int, i int) []int {
	if n := len(list); n > 0 && list[n-1] == i {
		return list
	}
	return append(list, i)
}

// precedence says every entry of before is placed before every entry of after.
type precedence struct{ before, after []int }

// order puts the entries in the order they are placed in, sets each one's pos,
// and returns them so. An input in which a rows entry that both adds and
// deletes cannot be placed as one entry is placed with that entry split into
// its adds and its deletes (see the top of this file).
func (b *builder) order(states []*entryState) ([]*entryState, error) {
	out, err := orderStates(states)
	if err == nil {
		return out, nil
	}
	split, pairs := splitMixed(states)
	if pairs == nil {
		return nil, err
	}
	out, err = orderStates(split)
	if err != nil {
		return nil, err
	}
	// The notes of an entry follow its last part: the piece placed last holds them.
	for _, p := range pairs {
		if adds, dels := p[0], p[1]; adds.pos > dels.pos {
			adds.e.Notes, dels.e.Notes = dels.e.Notes, nil
			adds.notes, dels.notes = dels.notes, nil
		}
	}
	return out, nil
}

// splitMixed is the states with each rows entry that adds and deletes replaced
// by two, the adds and then the deletes, and the pairs it made; the pairs are
// nil when no entry was split. The two share the input entry (its index, and so
// its guards, which travel in every step that holds either), the costs of its
// rows and its head; the deletes hold the notes until the order says which is
// placed last.
func splitMixed(states []*entryState) ([]*entryState, [][2]*entryState) {
	var out []*entryState
	var pairs [][2]*entryState
	for _, es := range states {
		if es.class != classRows || len(es.e.Add) == 0 || len(es.e.Del) == 0 {
			out = append(out, es)
			continue
		}
		na := len(es.e.Add)
		adds, dels := *es, *es
		ea, ed := *es.e, *es.e
		ea.Del, ea.Notes = nil, nil
		ed.Add = nil
		adds.e, dels.e = &ea, &ed
		adds.n, dels.n = na, es.n-na
		adds.cost, dels.cost = es.cost[:na:na], es.cost[na:]
		adds.notes = nil
		out = append(out, &adds, &dels)
		pairs = append(pairs, [2]*entryState{&adds, &dels})
	}
	return out, pairs
}

// orderStates is the order of the entries under the rules of this file, without
// splitting any: the input's own when it is in that order, else the sort of it.
func orderStates(states []*entryState) ([]*entryState, error) {
	adders, deleters := map[memberKey][]int{}, map[memberKey][]int{}
	for i, es := range states {
		if es.class != classRows {
			continue
		}
		for _, r := range es.e.Add {
			k := memberKey{es.e.Table, r}
			adders[k] = appendOnce(adders[k], i)
		}
		for _, r := range es.e.Del {
			k := memberKey{es.e.Table, r}
			deleters[k] = appendOnce(deleters[k], i)
		}
	}
	var rules []precedence
	if len(adders) > 0 || len(deleters) > 0 {
		dst, src := map[memberKey][]int{}, map[memberKey][]int{}
		for i, es := range states {
			for _, k := range es.dstRows {
				if _, ok := adders[k]; ok {
					dst[k] = appendOnce(dst[k], i)
				}
			}
			for _, k := range es.srcRows {
				if _, ok := deleters[k]; ok {
					src[k] = appendOnce(src[k], i)
				}
			}
		}
		for k, members := range dst {
			rules = append(rules, precedence{before: adders[k], after: members})
		}
		for k, members := range src {
			rules = append(rules, precedence{before: members, after: deleters[k]})
		}
	}
	if !behindItself(rules) {
		for i, es := range states {
			es.pos = i
		}
		return states, nil
	}
	return sortEntries(states, rules, adders, deleters)
}

// behindItself says some entry that has to come before another is after it in
// the input: the input is not in its order.
func behindItself(rules []precedence) bool {
	for _, r := range rules {
		hi, lo := -1, math.MaxInt
		for _, i := range r.before {
			hi = max(hi, i)
		}
		for _, i := range r.after {
			lo = min(lo, i)
		}
		if hi > lo {
			return true
		}
	}
	return false
}

// A rule of many entries on each side is one node between them, not a product
// of edges.
const productEdges = 1024

// graph is the entries, with the nodes after them (from the number of entries
// on) that stand for a rule.
type graph struct {
	succ  [][]int
	indeg []int
}

func (g *graph) edge(a, z int) {
	if a != z {
		g.succ[a] = append(g.succ[a], z)
		g.indeg[z]++
	}
}

func (g *graph) node() int {
	g.succ, g.indeg = append(g.succ, nil), append(g.indeg, 0)
	return len(g.succ) - 1
}

// rule adds the edges of a rule: from each entry before to each entry after,
// through a node of their own when there are many and none is on both sides.
func (g *graph) rule(r precedence) {
	if len(r.before)*len(r.after) > productEdges {
		both := map[int]struct{}{}
		for _, i := range r.before {
			both[i] = struct{}{}
		}
		shared := false
		for _, i := range r.after {
			if _, ok := both[i]; ok {
				shared = true
			}
		}
		if !shared {
			v := g.node()
			for _, a := range r.before {
				g.edge(a, v)
			}
			for _, z := range r.after {
				g.edge(v, z)
			}
			return
		}
	}
	for _, a := range r.before {
		for _, z := range r.after {
			g.edge(a, z)
		}
	}
}

// sortEntries is the order of the entries under rules and the orders the
// entries keep: at each step the ready entry with the lowest index.
func sortEntries(states []*entryState, rules []precedence, adders, deleters map[memberKey][]int) ([]*entryState, error) {
	n := len(states)
	g := &graph{succ: make([][]int, n), indeg: make([]int, n)}
	for _, r := range rules {
		g.rule(r)
	}
	// What is kept in order: entries that name one member; rows entries that
	// add; rows entries that name one row.
	named := map[memberKey]int{}
	name := func(k memberKey, i int) {
		if p, ok := named[k]; ok {
			g.edge(p, i)
		}
		named[k] = i
	}
	lastAdd := -1
	for i, es := range states {
		if es.class == classChange || es.class == classGuard {
			for _, id := range es.e.IDs {
				name(memberKey{es.e.Table, id}, i)
			}
		}
		for _, k := range es.guardKeys {
			name(k, i)
		}
		if es.class == classRows && len(es.e.Add) > 0 {
			if lastAdd >= 0 {
				g.edge(lastAdd, i)
			}
			lastAdd = i
		}
	}
	for k, adds := range adders {
		keepOrder(adds, deleters[k], g.edge)
	}
	ready := &intHeap{}
	var nodes []int // rule nodes whose entries before are all placed
	for v := 0; v < len(g.succ); v++ {
		if g.indeg[v] == 0 {
			if v < n {
				heap.Push(ready, v)
			} else {
				nodes = append(nodes, v)
			}
		}
	}
	release := func(z int) {
		if g.indeg[z]--; g.indeg[z] == 0 {
			if z < n {
				heap.Push(ready, z)
			} else {
				nodes = append(nodes, z)
			}
		}
	}
	out := make([]*entryState, 0, n)
	placed := make([]bool, n)
	for {
		for len(nodes) > 0 {
			v := nodes[len(nodes)-1]
			nodes = nodes[:len(nodes)-1]
			for _, z := range g.succ[v] {
				release(z)
			}
		}
		if ready.Len() == 0 {
			break
		}
		i := heap.Pop(ready).(int)
		states[i].pos = len(out)
		out = append(out, states[i])
		placed[i] = true
		for _, z := range g.succ[i] {
			release(z)
		}
	}
	if len(out) < n {
		first := 0
		for placed[first] {
			first++
		}
		return nil, (site{entry: states[first].idx}).input("add/del", "", "cannot be placed: it and the entries it waits for wait on each other")
	}
	return out, nil
}

// keepOrder keeps each rows entry that adds a row and each that deletes it in
// the order of the input, the earlier first.
func keepOrder(adds, dels []int, edge func(a, z int)) {
	for _, a := range adds {
		for _, d := range dels {
			edge(min(a, d), max(a, d))
		}
	}
}

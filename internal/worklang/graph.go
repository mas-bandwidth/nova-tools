package worklang

import (
	"fmt"
	"strings"
)

// A plan's needs/blocks graph (docs/SPEC-WORKLANG.md, "Nodes, with needs and
// blocks"). `:needs` is the reference edge the work spec calls `:deps`;
// `:blocks` is its inverse, and the kernel derives whichever of the two a node
// did not give. A `:needs` cycle is refused at load by validator rule 3, an
// absent need is refused naming the field and the id, never silently dropped.
//
// The graph is the admission surface, not the readiness one: every node is in
// it and printed, and a node is ready only when every need is terminal accepted
// -- settled after merging and going green -- and no reverted need has left it
// needs-broken. An open-PR need blocks readiness, not admission.

// Status is the settled facts the journal holds for one node. A node is terminal
// accepted when it has merged and gone green and has not been reverted.
type Status struct {
	Merged   bool
	Green    bool
	Reverted bool
}

func (s Status) accepted() bool { return s.Merged && s.Green && !s.Reverted }

// State names a status for a row: open, merged, reverted, or accepted. A merged
// node that is not yet green is the blocker nova-pulse harvest settles.
func (s Status) State() string {
	switch {
	case s.Reverted:
		return "reverted"
	case s.Merged && s.Green:
		return "accepted"
	case s.Merged:
		return "merged"
	default:
		return "open"
	}
}

// Blocker is the exact reason a node cannot proceed: the first unmet need, its
// state, and the join that resolves it.
type Blocker struct {
	Need     string
	State    string
	Resolver string
}

// Graph is the closed needs/blocks graph a plan declares. order is seed order,
// the order every node is admitted in; needs is the forward edge and blocks the
// inverse, both derived from the same declarations so the graph never holds one
// without the other.
type Graph struct {
	file    string
	order   []string
	present map[string]bool
	needs   map[string][]string
	blocks  map[string][]string
	status  map[string]Status
}

// Graph closes the plan's nodes into a needs/blocks graph. It refuses a
// duplicate or empty id, an absent need naming the field and the id, and a
// `:needs` cycle by validator rule 3, before returning: a refused graph is never
// published.
func (p *Plan) Graph() (*Graph, error) {
	g := &Graph{
		file:    p.File,
		present: make(map[string]bool, len(p.Nodes)),
		needs:   make(map[string][]string, len(p.Nodes)),
		blocks:  make(map[string][]string, len(p.Nodes)),
		status:  make(map[string]Status, len(p.Nodes)),
	}
	for _, n := range p.Nodes {
		id := n.ID()
		if id == "" {
			return nil, refuse(p.File, "rule 1: a node has an empty id")
		}
		if g.present[id] {
			return nil, refuse(p.File, fmt.Sprintf("rule 1: duplicate node id %q", id))
		}
		g.present[id] = true
		g.order = append(g.order, id)
	}
	// The declarations, in seed order. needs edges run from the node that names
	// them; blocks edges run into it (the blocked node needs the blocker).
	for _, n := range p.Nodes {
		id := n.ID()
		if f, ok := n.Fields["needs"]; ok {
			ids, err := edgeIDs(p.File, ":needs", f)
			if err != nil {
				return nil, err
			}
			for _, need := range ids {
				if !g.present[need] {
					return nil, refuse(p.File, fmt.Sprintf(
						":needs names absent node %q at byte=%d; refusing to guess",
						need, f.Offset))
				}
				g.addEdge(id, need)
			}
		}
		if f, ok := n.Fields["blocks"]; ok {
			ids, err := edgeIDs(p.File, ":blocks", f)
			if err != nil {
				return nil, err
			}
			for _, blocked := range ids {
				if !g.present[blocked] {
					return nil, refuse(p.File, fmt.Sprintf(
						":blocks names absent node %q at byte=%d; refusing to guess",
						blocked, f.Offset))
				}
				g.addEdge(blocked, id)
			}
		}
	}
	// rule 3: a :needs cycle is a deadlock nobody can finish.
	if err := g.refuseCycle(); err != nil {
		return nil, err
	}
	return g, nil
}

// addEdge writes the needs edge dependent -> need and the reverse blocks edge in
// the same insert, so the two are never written apart. Duplicates collapse.
func (g *Graph) addEdge(dependent, need string) {
	if !contains(g.needs[dependent], need) {
		g.needs[dependent] = append(g.needs[dependent], need)
	}
	if !contains(g.blocks[need], dependent) {
		g.blocks[need] = append(g.blocks[need], dependent)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// edgeIDs reads one `:needs` or `:blocks` value: a list of id strings, possibly
// empty. A value that is not such a list is a refusal, never a guess.
func edgeIDs(file, field string, f Form) ([]string, error) {
	if f.Kind != List {
		return nil, refuse(file, fmt.Sprintf("%s must be a list of node ids at byte=%d; refusing to guess", field, f.Offset))
	}
	var out []string
	for _, item := range f.List {
		switch item.Kind {
		case String, Symbol:
			if item.Value == "" {
				return nil, refuse(file, fmt.Sprintf("%s names an empty id at byte=%d; refusing to guess", field, item.Offset))
			}
			out = append(out, item.Value)
		default:
			return nil, refuse(file, fmt.Sprintf("%s names a node that is not an id at byte=%d; refusing to guess", field, item.Offset))
		}
	}
	return out, nil
}

// refuseCycle is validator rule 3: a depth-first walk that names the exact cycle
// it finds, so the refusal says which nodes deadlock rather than only that one
// exists.
func (g *Graph) refuseCycle() error {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make(map[string]int, len(g.order))
	var path []string
	var visit func(id string) error
	visit = func(id string) error {
		switch color[id] {
		case grey:
			start := 0
			for start < len(path) && path[start] != id {
				start++
			}
			cycle := append(append([]string(nil), path[start:]...), id)
			return refuse(g.file, fmt.Sprintf("rule 3: :needs edges contain a cycle: %s", strings.Join(cycle, " -> ")))
		case black:
			return nil
		}
		color[id] = grey
		path = append(path, id)
		for _, need := range g.needs[id] {
			if err := visit(need); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		color[id] = black
		return nil
	}
	for _, id := range g.order {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// Order returns every node id in seed order: admission, not readiness.
func (g *Graph) Order() []string { return append([]string(nil), g.order...) }

// Len is the number of nodes. Edges is the number of needs edges.
func (g *Graph) Len() int { return len(g.order) }

// Edges returns the number of forward needs edges.
func (g *Graph) Edges() int {
	n := 0
	for _, needs := range g.needs {
		n += len(needs)
	}
	return n
}

// Needs returns the forward edges of id in seed order: the nodes it cannot begin
// before.
func (g *Graph) Needs(id string) []string { return append([]string(nil), g.needs[id]...) }

// Blocks returns the reverse edges of id in seed order. It is the inverse of
// Needs, written by the same insert.
func (g *Graph) Blocks(id string) []string { return append([]string(nil), g.blocks[id]...) }

// Settle records that id merged and went green: terminal accepted.
func (g *Graph) Settle(id string) {
	s := g.status[id]
	s.Merged, s.Green, s.Reverted = true, true, false
	g.status[id] = s
}

// Revert records that id was reverted after landing, flagging each dependent
// needs-broken rather than leaving it silently ready.
func (g *Graph) Revert(id string) {
	s := g.status[id]
	s.Reverted = true
	g.status[id] = s
}

// NeedsBroken reports whether a reverted need has left id flagged needs-broken.
func (g *Graph) NeedsBroken(id string) bool {
	for _, need := range g.needs[id] {
		if g.status[need].Reverted {
			return true
		}
	}
	return false
}

// accepted reports whether id has merged and gone green without a revert.
func (g *Graph) accepted(id string) bool { return g.status[id].accepted() }

// Ready reports whether id may proceed, and when it may not, the exact blocker
// and its resolver. A node is ready only when every need is terminal accepted
// and no reverted need has left it needs-broken. A node already terminal
// accepted has nothing left to begin.
func (g *Graph) Ready(id string) (bool, *Blocker) {
	if !g.present[id] {
		return false, nil
	}
	if g.accepted(id) {
		return false, nil
	}
	for _, need := range g.needs[id] {
		if g.status[need].Reverted {
			return false, &Blocker{Need: need, State: "needs-broken", Resolver: "nova-work revive"}
		}
	}
	for _, need := range g.needs[id] {
		if !g.accepted(need) {
			return false, &Blocker{Need: need, State: g.status[need].State(), Resolver: resolver(g.status[need])}
		}
	}
	return true, nil
}

// ReadySet is the mechanical ready set: the nodes with no unmet need, in seed
// order. The graph's own nodes are all admitted; only the ready ones are
// pullable.
func (g *Graph) ReadySet() []string {
	out := make([]string, 0, len(g.order))
	for _, id := range g.order {
		if ready, _ := g.Ready(id); ready {
			out = append(out, id)
		}
	}
	return out
}

// resolver names the join that settles a need. nova-merge queue lands an open
// PR; once it has merged, nova-pulse harvest settles the green and re-evaluates
// dependents.
func resolver(s Status) string {
	if !s.Merged {
		return "nova-merge queue"
	}
	return "nova-pulse harvest"
}

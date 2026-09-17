// Package jobs is the job graph of docs/SPEC-JOBS.md section 1: typed edges needs and
// blocks, a seeded graph refused acyclic by validator rule 3, and the mechanical ready
// set launch reads.
//
// A job graph carries two typed edges. needs is the forward edge: a job cannot begin
// until the job it needs completes. blocks is the reverse edge, written by the same
// insert, so the graph never holds one without the other. The scheduler never asks a
// person what may run: it evaluates the ready set as the nodes whose count of unmet
// needs is zero, and a completion is a join that decrements each dependent and promotes
// it at zero.
//
// A node is terminal accepted when it has merged and gone green. The ready set holds
// only nodes whose every need is terminal accepted; a node whose need is an open PR is
// never in it. A :deps cycle is refused before the graph is published, so the ready set
// is finite and the graph can never deadlock.
//
// This package reads and validates the graph. It performs no output and reaches no
// network: the caller renders the rows.
package jobs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Node is one job in the graph. Merged and Green are the two facts that make a need
// terminal accepted; a node that is neither is an open PR.
type Node struct {
	ID     string   `json:"id"`
	Needs  []string `json:"needs,omitempty"`
	Merged bool     `json:"merged,omitempty"`
	Green  bool     `json:"green,omitempty"`
}

// Graph is the job graph: the nodes in seed order, the forward needs edges and the
// reverse blocks edges built by the same insert.
type Graph struct {
	order  []string
	node   map[string]Node
	needs  map[string][]string
	blocks map[string][]string
}

// Seed builds and validates a graph from nodes in seed order. It refuses a duplicate or
// empty id, a need that does not resolve, and a :deps cycle -- validator rules 1, 2 and
// 3 -- before returning, so a refused graph is never published.
func Seed(nodes []Node) (*Graph, error) {
	g := &Graph{
		node:   make(map[string]Node, len(nodes)),
		needs:  make(map[string][]string, len(nodes)),
		blocks: make(map[string][]string, len(nodes)),
	}
	for _, n := range nodes {
		id := strings.TrimSpace(n.ID)
		if id == "" {
			return nil, fmt.Errorf("rule 1: a node has an empty id")
		}
		if _, dup := g.node[id]; dup {
			return nil, fmt.Errorf("rule 1: duplicate node id %q", id)
		}
		n.ID = id
		n.Needs = append([]string(nil), n.Needs...)
		g.node[id] = n
		g.order = append(g.order, id)
	}
	// rule 2: every need resolves, and the same insert writes the reverse edge.
	for _, id := range g.order {
		for _, dep := range g.node[id].Needs {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				return nil, fmt.Errorf("rule 2: %s names a dependency that is not a non-empty id", id)
			}
			if _, ok := g.node[dep]; !ok {
				return nil, fmt.Errorf("rule 2: %s needs %s which does not exist", id, dep)
			}
			g.needs[id] = append(g.needs[id], dep)
			g.blocks[dep] = append(g.blocks[dep], id)
		}
	}
	// rule 3: a :deps cycle is a deadlock nobody can finish.
	if err := g.refuseCycle(); err != nil {
		return nil, err
	}
	return g, nil
}

// ParseNodes reads the node list from JSON: a bare array of nodes, or an object with a
// "nodes" array. Bytes that are not a node list are a refusal, never a guess.
func ParseNodes(data []byte) ([]Node, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("the graph is empty")
	}
	if trimmed[0] == '[' {
		var nodes []Node
		if err := json.Unmarshal(trimmed, &nodes); err != nil {
			return nil, fmt.Errorf("the graph is not a JSON array of nodes: %w", err)
		}
		return nodes, nil
	}
	var doc struct {
		Nodes []Node `json:"nodes"`
	}
	if err := json.Unmarshal(trimmed, &doc); err != nil {
		return nil, fmt.Errorf("the graph is not a JSON object with nodes: %w", err)
	}
	return doc.Nodes, nil
}

// ParseSeed reads and seeds a graph from JSON.
func ParseSeed(data []byte) (*Graph, error) {
	nodes, err := ParseNodes(data)
	if err != nil {
		return nil, err
	}
	return Seed(nodes)
}

// MarshalNodes renders a node list as the graph file's JSON.
func MarshalNodes(nodes []Node) ([]byte, error) {
	out, err := json.MarshalIndent(struct {
		Nodes []Node `json:"nodes"`
	}{Nodes: nodes}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// refuseCycle is the validator's rule 3: a depth-first walk that names the exact cycle
// it finds, so the refusal says which nodes deadlock rather than only that one exists.
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
			return fmt.Errorf("rule 3: :deps edges contain a cycle: %s", strings.Join(cycle, " -> "))
		case black:
			return nil
		}
		color[id] = grey
		path = append(path, id)
		for _, dep := range g.needs[id] {
			if err := visit(dep); err != nil {
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

// Node returns the node with id, and whether it exists.
func (g *Graph) Node(id string) (Node, bool) {
	n, ok := g.node[id]
	return n, ok
}

// Needs returns the forward edges of id in seed order: the jobs it cannot begin before.
func (g *Graph) Needs(id string) []string {
	return append([]string(nil), g.needs[id]...)
}

// Blocks returns the reverse edges of id in seed order: the jobs that cannot begin
// before it completes. It is the inverse of Needs, written by the same insert.
func (g *Graph) Blocks(id string) []string {
	return append([]string(nil), g.blocks[id]...)
}

// Order returns the node ids in seed order.
func (g *Graph) Order() []string {
	return append([]string(nil), g.order...)
}

// Len is the number of nodes. Edges is the number of needs edges.
func (g *Graph) Len() int { return len(g.order) }

// Edges returns the number of forward needs edges.
func (g *Graph) Edges() int {
	n := 0
	for _, deps := range g.needs {
		n += len(deps)
	}
	return n
}

// Accepted reports whether id has merged and gone green: terminal accepted.
func (g *Graph) Accepted(id string) bool {
	n, ok := g.node[id]
	return ok && n.accepted()
}

func (n Node) accepted() bool { return n.Merged && n.Green }

// State names a node's progress: open, merged, or accepted. A merged node that is not
// yet green is the blocker nova-pulse harvest settles.
func (n Node) State() string {
	switch {
	case n.Merged && n.Green:
		return "accepted"
	case n.Merged:
		return "merged"
	default:
		return "open"
	}
}

// Blocker is the exact reason a node cannot proceed: the first unmet need, its state,
// and the join that resolves it.
type Blocker struct {
	Need     string
	State    string
	Resolver string
}

// Ready reports whether id may proceed, and when it may not, the exact blocker and its
// resolver. A node is ready only when every need is terminal accepted.
func (g *Graph) Ready(id string) (bool, *Blocker) {
	n, ok := g.node[id]
	if !ok {
		return false, nil
	}
	// A node already terminal accepted has nothing left to begin.
	if n.accepted() {
		return false, nil
	}
	for _, dep := range g.needs[id] {
		need := g.node[dep]
		if !need.accepted() {
			return false, &Blocker{Need: dep, State: need.State(), Resolver: resolver(need)}
		}
	}
	return true, nil
}

// resolver names the join that settles a need. nova-merge queue lands an open PR; once
// it has merged, nova-pulse harvest settles the green and re-evaluates dependents.
func resolver(n Node) string {
	if !n.Merged {
		return "nova-merge queue"
	}
	return "nova-pulse harvest"
}

// ReadySet is the mechanical ready set: the nodes with no unmet need, in seed order.
func (g *Graph) ReadySet() []string {
	out := make([]string, 0, len(g.order))
	for _, id := range g.order {
		if ready, _ := g.Ready(id); ready {
			out = append(out, id)
		}
	}
	return out
}

// Launch reads the ready set and nothing else. It returns the queued cards that may go
// on a slot, in queue order; a card whose need is an open PR is never among them.
func (g *Graph) Launch(queue []string) []string {
	out := make([]string, 0, len(queue))
	for _, id := range queue {
		if _, ok := g.node[id]; !ok {
			continue
		}
		if ready, _ := g.Ready(id); ready {
			out = append(out, id)
		}
	}
	return out
}

package pulse

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrDependencyCycle is the sentinel error returned when dependency cycle is detected.
var ErrDependencyCycle = errors.New("dependency cycle detected")

// CycleError records the exact cycle path that caused the refusal.
type CycleError struct {
	Cycle []string
}

func (e *CycleError) Error() string {
	return FormatCycle(e.Cycle)
}

func (e *CycleError) Unwrap() error {
	return ErrDependencyCycle
}

// FormatCycle returns the formatted diagnostic string for a dependency cycle.
// Format: CYCLE REFUSED: card-a -> card-b -> card-a
func FormatCycle(cycle []string) string {
	return "CYCLE REFUSED: " + strings.Join(cycle, " -> ")
}

// CardNode represents a card and its declared dependencies.
type CardNode struct {
	ID        string
	Path      string
	DependsOn []string
}

// DependencyGraph is a directed graph of card dependencies.
type DependencyGraph struct {
	nodes map[string]*CardNode
	adj   map[string][]string
}

// NewDependencyGraph creates an empty dependency graph.
func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		nodes: make(map[string]*CardNode),
		adj:   make(map[string][]string),
	}
}

// AddCard registers a card node in the graph.
func (g *DependencyGraph) AddCard(node *CardNode) {
	if node == nil || node.ID == "" {
		return
	}
	g.nodes[node.ID] = node
	if _, ok := g.adj[node.ID]; !ok {
		g.adj[node.ID] = nil
	}
	for _, dep := range node.DependsOn {
		g.AddDependency(node.ID, dep)
	}
}

// AddDependency adds a directed dependency edge from -> to (from depends on to).
func (g *DependencyGraph) AddDependency(from, to string) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return
	}
	if _, ok := g.nodes[from]; !ok {
		g.nodes[from] = &CardNode{ID: from}
	}
	if _, ok := g.nodes[to]; !ok {
		g.nodes[to] = &CardNode{ID: to}
	}

	// Avoid duplicate edges
	for _, existing := range g.adj[from] {
		if existing == to {
			return
		}
	}
	g.adj[from] = append(g.adj[from], to)
}

// NormalizeCycle normalizes a cycle path [n0, n1, ..., nk, n0] so that the
// lexicographically smallest node in the cycle appears at the start and end.
func NormalizeCycle(cycle []string) []string {
	if len(cycle) <= 1 {
		return cycle
	}
	// The unique nodes in the cycle
	unique := cycle[:len(cycle)-1]
	if len(unique) <= 1 {
		return cycle
	}

	// Find the index of the lexicographically smallest node
	minIndex := 0
	for i := 1; i < len(unique); i++ {
		if unique[i] < unique[minIndex] {
			minIndex = i
		}
	}

	if minIndex == 0 {
		return cycle
	}

	rotated := make([]string, 0, len(unique)+1)
	rotated = append(rotated, unique[minIndex:]...)
	rotated = append(rotated, unique[:minIndex]...)
	rotated = append(rotated, rotated[0])
	return rotated
}

// FindCycle searches for any directed cycle in the dependency graph.
// If one or more cycles exist, it returns the lexicographically canonical cycle path.
// If the graph is acyclic, it returns nil.
func (g *DependencyGraph) FindCycle() []string {
	allNodes := make([]string, 0, len(g.nodes))
	for id := range g.nodes {
		allNodes = append(allNodes, id)
	}
	sort.Strings(allNodes)

	// Sort adjacency lists for deterministic traversal
	for id := range g.adj {
		sort.Strings(g.adj[id])
	}

	// 0: unvisited, 1: visiting (in current path), 2: visited
	state := make(map[string]int)
	pos := make(map[string]int)
	var path []string
	var foundCycles [][]string

	var dfs func(u string)
	dfs = func(u string) {
		state[u] = 1
		pos[u] = len(path)
		path = append(path, u)

		for _, v := range g.adj[u] {
			if state[v] == 1 {
				// Cycle detected: from pos[v] to end of path, plus v
				idx := pos[v]
				cycleCandidate := append([]string(nil), path[idx:]...)
				cycleCandidate = append(cycleCandidate, v)
				foundCycles = append(foundCycles, NormalizeCycle(cycleCandidate))
			} else if state[v] == 0 {
				dfs(v)
			}
		}

		state[u] = 2
		delete(pos, u)
		path = path[:len(path)-1]
	}

	for _, node := range allNodes {
		if state[node] == 0 {
			dfs(node)
		}
	}

	if len(foundCycles) == 0 {
		return nil
	}

	// Sort found cycles lexicographically to pick the canonical one
	sort.Slice(foundCycles, func(i, j int) bool {
		c1, c2 := foundCycles[i], foundCycles[j]
		minLen := len(c1)
		if len(c2) < minLen {
			minLen = len(c2)
		}
		for k := 0; k < minLen; k++ {
			if c1[k] != c2[k] {
				return c1[k] < c2[k]
			}
		}
		return len(c1) < len(c2)
	})

	return foundCycles[0]
}

// CheckCycles verifies that the dependency graph has no cycles.
// If a cycle is found, it returns *CycleError naming the cycle.
func (g *DependencyGraph) CheckCycles() error {
	cycle := g.FindCycle()
	if len(cycle) == 0 {
		return nil
	}
	return &CycleError{Cycle: cycle}
}

// ParseCardDependencies extracts dependency labels from card text.
// Supports:
//
//	depends-on: <id>[, <id>...]
//	depends_on: ...
//	DEPENDS-ON: ...
//	:depends-on ("id1" "id2")
func ParseCardDependencies(content string) []string {
	var deps []string
	seen := make(map[string]bool)

	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)

		var rest string
		switch {
		case strings.HasPrefix(lower, "depends-on:"):
			rest = strings.TrimSpace(line[len("depends-on:"):])
		case strings.HasPrefix(lower, "depends_on:"):
			rest = strings.TrimSpace(line[len("depends_on:"):])
		case strings.HasPrefix(lower, "depends:"):
			rest = strings.TrimSpace(line[len("depends:"):])
		case strings.HasPrefix(lower, ":depends-on"):
			rest = strings.TrimSpace(line[len(":depends-on"):])
		case strings.HasPrefix(lower, ":depends_on"):
			rest = strings.TrimSpace(line[len(":depends_on"):])
		default:
			continue
		}

		// Clean enclosing brackets/parentheses
		rest = strings.Trim(rest, "()[]")

		// Split on comma or whitespace if lisp style
		tokens := strings.FieldsFunc(rest, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		})

		for _, tok := range tokens {
			clean := strings.Trim(tok, " \"'\t\r\n:()")
			if clean == "" {
				continue
			}
			// Strip .md extension if present
			clean = strings.TrimSuffix(clean, ".md")
			// If contains repo:path, keep the path or label
			if idx := strings.Index(clean, ":"); idx != -1 {
				clean = clean[idx+1:]
			}
			if clean != "" && !seen[clean] {
				seen[clean] = true
				deps = append(deps, clean)
			}
		}
	}

	return deps
}

// ParseCardFile parses a card markdown file into a CardNode.
func ParseCardFile(path string) (*CardNode, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading card %s: %w", path, err)
	}
	base := filepath.Base(path)
	id := strings.TrimSuffix(base, ".md")
	deps := ParseCardDependencies(string(b))

	return &CardNode{
		ID:        id,
		Path:      path,
		DependsOn: deps,
	}, nil
}

// LoadCardGraphFromCards constructs a DependencyGraph from a list of card file paths.
func LoadCardGraphFromCards(cardPaths []string) (*DependencyGraph, error) {
	g := NewDependencyGraph()
	for _, path := range cardPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		card, err := ParseCardFile(path)
		if err != nil {
			return nil, err
		}
		g.AddCard(card)
	}
	return g, nil
}

// LoadCardGraphFromDir constructs a DependencyGraph from card files in a directory.
func LoadCardGraphFromDir(dir string) (*DependencyGraph, error) {
	if strings.TrimSpace(dir) == "" {
		return NewDependencyGraph(), nil
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "card-*.md"))
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(dir, "*.md"))
	}
	sort.Strings(matches)
	return LoadCardGraphFromCards(matches)
}

// CheckCardCycles checks a list of card file paths for dependency cycles.
func CheckCardCycles(cardPaths []string) error {
	g, err := LoadCardGraphFromCards(cardPaths)
	if err != nil {
		return err
	}
	return g.CheckCycles()
}

// CheckDirCycles checks all cards in a directory for dependency cycles.
func CheckDirCycles(dir string) error {
	g, err := LoadCardGraphFromDir(dir)
	if err != nil {
		return err
	}
	return g.CheckCycles()
}

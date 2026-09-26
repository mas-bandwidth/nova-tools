package ws

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The stream's work order (nova-tools #4322, #4324; card land-order-1). A
// stream's order is the topological order of its cards' DEPENDS-ON edges,
// ties broken by PATHS overlap (two cards sharing a path are ordered by issue
// number, the lower first) and then by issue number (a card with no issue
// number after every numbered one, by id), with the stream's sentinel last.
// Order is the one pure function: `ws reorder` and every push onto a stream
// write it as the scores of the waiting, ready and merging sets
// (reorder.go), `ws show --order` prints it with one reason per edge, and
// `ws check` names a stream whose stored scores read another sequence as
// ORDER DRIFT. card cut --from orders its rows with Topo, the same core.

// The reasons an edge of the order carries.
const (
	WhyDependsOn = "depends-on" // the card's DEPENDS-ON names the card before it
	WhyPaths     = "paths"      // the two share a path; the lower issue goes first
	WhyIssue     = "issue"      // no edge: the tie-break (issue number, then id)
	WhySentinel  = "sentinel"   // the stream's stop, after every other card
)

// OrderCard is one card Order ranks.
type OrderCard struct {
	ID       string
	Issue    int      // the card's issue number; 0 when it has none
	Paths    []string // its PATHS entries
	Deps     []string // the ids its DEPENDS-ON names; an id that is not a card of the input is not an edge here
	Sentinel bool     // the stream's stop: always last
}

// Ordered is a card in the order and its 1-based rank.
type Ordered struct {
	OrderCard
	Rank int
}

// Reason is one edge of the order: Card comes after After because of Why;
// Path is the shared path of a paths edge. A sentinel's reason has no After
// (it follows every other card).
type Reason struct {
	Card, After, Why, Path string
}

// String is the reason as ws show prints it after `<- `.
func (r Reason) String() string {
	switch r.Why {
	case WhySentinel:
		return "every other card (reason: sentinel)"
	case WhyPaths:
		return r.After + " (reason: paths " + r.Path + ")"
	}
	return r.After + " (reason: " + r.Why + ")"
}

// CycleError is Order's refusal: the DEPENDS-ON edges close a cycle, named
// from its lowest card around to that card again.
type CycleError struct{ Cycle []string }

func (e *CycleError) Error() string {
	return "DEPENDS-ON cycle " + strings.Join(e.Cycle, " -> ")
}

// less is the tie-break: issue number (none last), then id.
func (c OrderCard) less(o OrderCard) bool {
	switch {
	case c.Issue != o.Issue && c.Issue != 0 && o.Issue != 0:
		return c.Issue < o.Issue
	case (c.Issue == 0) != (o.Issue == 0):
		return c.Issue != 0
	}
	return c.ID < o.ID
}

// Topo is the order's core, and card cut --from's: Kahn's algorithm over n
// nodes, before(i) naming the nodes that must precede i, taking the least
// ready node (less) at every step. stuck is every node that never became
// ready (on or behind a cycle, a node before itself included), in less
// order.
func Topo(n int, before func(i int) []int, less func(a, b int) bool) (order, stuck []int) {
	placed := make([]bool, n)
	waits := make([]int, n)
	after := make([][]int, n)
	for i := 0; i < n; i++ {
		for _, j := range before(i) {
			if j < 0 || j >= n {
				continue
			}
			waits[i]++
			after[j] = append(after[j], i)
		}
	}
	var ready []int
	for i := 0; i < n; i++ {
		if waits[i] == 0 {
			ready = append(ready, i)
		}
	}
	for len(ready) > 0 {
		best := 0
		for k := 1; k < len(ready); k++ {
			if less(ready[k], ready[best]) {
				best = k
			}
		}
		i := ready[best]
		ready = append(ready[:best], ready[best+1:]...)
		placed[i] = true
		order = append(order, i)
		for _, j := range after[i] {
			if waits[j]--; waits[j] == 0 {
				ready = append(ready, j)
			}
		}
	}
	for i := 0; i < n; i++ {
		if !placed[i] {
			stuck = append(stuck, i)
		}
	}
	sort.SliceStable(stuck, func(a, b int) bool { return less(stuck[a], stuck[b]) })
	return order, stuck
}

// Order ranks cards: it is pure and deterministic (the input's order never
// matters), refuses a DEPENDS-ON cycle with a *CycleError naming it, and
// returns the order with its reasons, card by card in order: each
// depends-on and paths edge into a card, an issue edge to the card before it
// when no edge explains that adjacency, and the sentinel's one reason.
func Order(cards []OrderCard) ([]Ordered, []Reason, error) {
	cs := append([]OrderCard(nil), cards...)
	sort.SliceStable(cs, func(a, b int) bool { return cs[a].less(cs[b]) })
	idx := make(map[string]int, len(cs))
	var body []int // the non-sentinel cards, in tie-break order
	var stops []int
	for i, c := range cs {
		if c.ID == "" {
			return nil, nil, fmt.Errorf("ws order: a card with no id")
		}
		if _, dup := idx[c.ID]; dup {
			return nil, nil, fmt.Errorf("ws order: card %s named twice", c.ID)
		}
		idx[c.ID] = i
		if c.Sentinel {
			stops = append(stops, i)
		} else {
			body = append(body, i)
		}
	}
	n := len(cs)
	deps := make([][]int, n) // i's depends-on edges: the cards before it
	for _, i := range body {
		seen := map[int]bool{}
		for _, d := range cs[i].Deps {
			j, ok := idx[d]
			if !ok || j == i || cs[j].Sentinel || seen[j] {
				continue
			}
			seen[j] = true
			deps[i] = append(deps[i], j)
		}
		sort.Ints(deps[i])
	}
	less := func(a, b int) bool { return a < b } // cs is sorted: the index is the tie-break
	if _, stuck := Topo(n, func(i int) []int { return deps[i] }, less); len(stuck) > 0 {
		return nil, nil, &CycleError{Cycle: cycleOf(cs, deps, stuck)}
	}

	// The paths edges: for each pair sharing a path, lower first, unless
	// the edges already order the pair (either way: DEPENDS-ON wins).
	paths := make([][]string, n)
	for _, i := range body {
		paths[i] = normPaths(cs[i].Paths)
	}
	before := make([][]int, n)
	for i := range deps {
		before[i] = append([]int(nil), deps[i]...)
	}
	pathOf := map[[2]int]string{} // (after, before) -> the shared path
	for a := 0; a < len(body); a++ {
		for b := a + 1; b < len(body); b++ {
			i, j := body[a], body[b]
			p := sharedPath(paths[i], paths[j])
			if p == "" || reaches(before, j, i) || reaches(before, i, j) {
				continue
			}
			before[j] = append(before[j], i)
			pathOf[[2]int{j, i}] = p
		}
	}
	// The body in order (acyclic: the paths edges never close a loop), then
	// the stops.
	order, _ := Topo(n, func(i int) []int { return before[i] }, less)
	var final []int
	for _, i := range order {
		if !cs[i].Sentinel {
			final = append(final, i)
		}
	}
	final = append(final, stops...)

	out := make([]Ordered, len(final))
	pos := make(map[int]int, len(final))
	for r, i := range final {
		out[r] = Ordered{OrderCard: cs[i], Rank: r + 1}
		pos[i] = r
	}
	var reasons []Reason
	for r, i := range final {
		c := cs[i]
		if c.Sentinel {
			reasons = append(reasons, Reason{Card: c.ID, Why: WhySentinel})
			continue
		}
		in := append([]int(nil), before[i]...)
		sort.Slice(in, func(a, b int) bool { return pos[in[a]] < pos[in[b]] })
		prevExplained := r == 0
		isDep := map[int]bool{}
		for _, j := range deps[i] {
			isDep[j] = true
		}
		for _, j := range in {
			rs := Reason{Card: c.ID, After: cs[j].ID, Why: WhyDependsOn}
			if !isDep[j] {
				rs.Why, rs.Path = WhyPaths, pathOf[[2]int{i, j}]
			}
			reasons = append(reasons, rs)
			if r > 0 && j == final[r-1] {
				prevExplained = true
			}
		}
		if !prevExplained {
			reasons = append(reasons, Reason{Card: c.ID, After: cs[final[r-1]].ID, Why: WhyIssue})
		}
	}
	return out, reasons, nil
}

// reaches is whether from waits on to through the before edges.
func reaches(before [][]int, from, to int) bool {
	seen := map[int]bool{}
	stack := []int{from}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, j := range before[i] {
			if j == to {
				return true
			}
			if !seen[j] {
				seen[j] = true
				stack = append(stack, j)
			}
		}
	}
	return false
}

// cycleOf names one cycle among the stuck nodes: from the least stuck node
// follow its first stuck dependency until a node repeats, then read the
// loop from its least card around to it again.
func cycleOf(cs []OrderCard, deps [][]int, stuck []int) []string {
	in := map[int]bool{}
	for _, i := range stuck {
		in[i] = true
	}
	at := map[int]int{}
	var path []int
	i := stuck[0]
	for {
		if k, ok := at[i]; ok {
			loop := path[k:]
			least := 0
			for x := range loop {
				if loop[x] < loop[least] {
					least = x
				}
			}
			var names []string
			for x := 0; x <= len(loop); x++ {
				names = append(names, cs[loop[(least+x)%len(loop)]].ID)
			}
			return names
		}
		at[i] = len(path)
		path = append(path, i)
		next := -1
		for _, j := range deps[i] {
			if in[j] {
				next = j
				break
			}
		}
		if next < 0 { // cannot happen: a stuck node waits on a stuck node
			return []string{cs[i].ID}
		}
		i = next
	}
}

// normPaths cleans PATHS entries: ./ and a trailing / or /... dropped, the
// empty and none dropped, each once.
func normPaths(ps []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range ps {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "./")
		p = strings.TrimSuffix(p, "/...")
		p = strings.TrimRight(p, "/")
		if p == "" || p == "none" || p == "-" || p == "." || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// sharedPath is the first path two cards share: equal, or one a directory
// of the other (the shorter is named); "" when none.
func sharedPath(a, b []string) string {
	for _, x := range a {
		for _, y := range b {
			switch {
			case x == y, strings.HasPrefix(y, x+"/"):
				return x
			case strings.HasPrefix(x, y+"/"):
				return y
			}
		}
	}
	return ""
}

// SplitPaths splits a stored PATHS value on commas and white space.
func SplitPaths(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

var issueRE = regexp.MustCompile(`(?:#|/issues/|/pull/)([1-9][0-9]*)/?$`)

// IssueOf is the first issue number the fields name (a ref or origin:
// owner/repo#n, #n, or an issues or pull URL); 0 when none does.
func IssueOf(fields ...string) int {
	for _, f := range fields {
		if m := issueRE.FindStringSubmatch(strings.TrimSpace(f)); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
	}
	return 0
}

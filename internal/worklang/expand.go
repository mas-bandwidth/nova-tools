package worklang

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/jobs"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The model classes a :model-floor may name, in ascending order. A route whose
// model class is below the floor is refused, never silently downgraded; an
// unknown class is refused rather than guessed.
var modelClasses = []string{"haiku", "sonnet", "opus"}

// modelRank returns the index of a known class and whether it is known.
func modelRank(name string) (int, bool) {
	for i, c := range modelClasses {
		if c == name {
			return i, true
		}
	}
	return 0, false
}

// str returns the string value of one node field, or "" when it is absent or
// not a string.
func (n Node) str(key string) string {
	if f, ok := n.Fields[key]; ok && f.Kind == String {
		return f.Value
	}
	return ""
}

// Budget is one card's wall: its minutes, its token ceiling and the lowest
// model class admitted.
type Budget struct {
	Minutes int64
	Tokens  int64
	Floor   string
}

// Affinity names which bench kind may pull the card and which route serves it.
// Model is the route's model class when the plan pins it; an empty Model means
// the class is a projection from facts this slice does not read.
type Affinity struct {
	Bench string
	Route string
	Model string
}

// Card is one expanded node: everything the bench needs, and nothing the plan
// did not write. Its fields are the spec's output contract, budget and
// affinity, plus the needs/blocks edges the graph derived.
type Card struct {
	Node     string
	Kind     string
	Repo     string
	Base     string
	Needs    []string
	Blocks   []string
	Ready    bool
	Inputs   []string
	Result   string
	Branch   string
	Green    []string
	Budget   Budget
	Affinity Affinity
}

// unadmitted are the top-level forms the smallest first slice does not accept:
// the hand-written :node only. Each is a refusal naming the form, never a
// silent skip.
var unadmitted = []string{"derive", "fold", "facts"}

// ExpandPlan validates a hand-written plan, builds the needs/blocks graph and
// returns one Card per node in plan order. It refuses an absent need or a
// cycle (through the job graph's rules 2 and 3), an unadmitted top-level form,
// a duplicate branch name, and a budget below its floor -- always before a card
// is written.
func ExpandPlan(plan *Plan) ([]Card, error) {
	if plan == nil {
		return nil, refuse("", "no plan to expand; refusing to guess")
	}
	for _, form := range plan.Unknown {
		if form.Kind == List && len(form.List) > 0 && form.List[0].Kind == Keyword {
			for _, name := range unadmitted {
				if form.List[0].Value == name {
					return nil, refuse(plan.File, fmt.Sprintf(
						":%s is not in this slice; only hand-written :nodes expand", name))
				}
			}
		}
	}

	specs := make([]Card, 0, len(plan.Nodes))
	for _, n := range plan.Nodes {
		c, err := expandNode(plan.File, n)
		if err != nil {
			return nil, err
		}
		specs = append(specs, c)
	}
	if len(specs) == 0 {
		return nil, refuse(plan.File, "a plan with no :node has no cards to expand")
	}

	// A branch name is unique across the plan; two nodes deriving the same name
	// is a refusal naming both, before any card is written.
	byBranch := map[string]string{}
	for _, c := range specs {
		if other, dup := byBranch[c.Branch]; dup {
			return nil, refuse(plan.File, fmt.Sprintf(
				":output :branch %q is used by both %s and %s; branch names are unique across a plan",
				c.Branch, other, c.Node))
		}
		byBranch[c.Branch] = c.Node
	}

	// The graph owns the needs/blocks edges: a node's :needs is the forward
	// edge and :blocks its inverse, so the one not given is derived here. The
	// job graph refuses an absent need (rule 2) and a cycle (rule 3).
	nodes := make([]jobs.Node, 0, len(specs))
	for _, c := range specs {
		nodes = append(nodes, jobs.Node{ID: c.Node, Needs: append([]string(nil), c.Needs...)})
	}
	// An explicit :blocks edge is the same edge seen from the other end: X
	// blocks Y means Y needs X. Adding it here keeps the two edges one.
	index := map[string]int{}
	for i, c := range specs {
		index[c.Node] = i
	}
	for _, c := range specs {
		for _, blocked := range c.Blocks {
			j, ok := index[blocked]
			if !ok {
				return nil, refuse(plan.File, fmt.Sprintf(
					":blocks names %q, which is not a node; refusing to guess", blocked))
			}
			nodes[j].Needs = append(nodes[j].Needs, c.Node)
		}
	}
	g, err := jobs.Seed(nodes)
	if err != nil {
		return nil, refuse(plan.File, err.Error())
	}

	for i := range specs {
		id := specs[i].Node
		specs[i].Needs = g.Needs(id)
		specs[i].Blocks = g.Blocks(id)
		ready, _ := g.Ready(id)
		specs[i].Ready = ready
	}
	return specs, nil
}

// expandNode reads one hand-written :node into a Card and validates its output
// contract, budget and affinity by field name.
func expandNode(file string, n Node) (Card, error) {
	id := n.ID()
	if id == "" {
		return Card{}, refuse(file, ":node has no :id; refusing to guess")
	}
	c := Card{Node: id, Kind: n.Kind(), Repo: n.str("repo"), Base: n.str("base")}
	c.Inputs = parseInputs(n.Fields["inputs"])
	c.Needs = parseStringList(n.Fields["needs"])
	c.Blocks = parseStringList(n.Fields["blocks"])

	var missing []string
	if c.Kind == "" {
		missing = append(missing, fmt.Sprintf(":node %s has no :kind; refusing to guess", id))
	}

	out, ok := n.Fields["output"]
	if !ok || out.Kind != List {
		missing = append(missing, fmt.Sprintf(":node %s has no :output; refusing to guess", id))
	}

	budget, ok := n.Fields["budget"]
	if !ok || budget.Kind != List {
		missing = append(missing, fmt.Sprintf(":node %s has no :budget; a budget-less plan refuses", id))
	}
	if len(missing) > 0 {
		return Card{}, refuse(file, strings.Join(missing, "; "))
	}

	result, branch, green, err := parseOutput(file, id, out)
	if err != nil {
		return Card{}, err
	}
	if err := checkBranch(file, id, branch); err != nil {
		return Card{}, err
	}
	c.Result, c.Branch, c.Green = result, branch, green

	b, err := parseBudget(file, id, budget)
	if err != nil {
		return Card{}, err
	}
	c.Budget = b

	if aff, ok := n.Fields["affinity"]; ok && aff.Kind == List {
		a, err := parseAffinity(file, id, aff)
		if err != nil {
			return Card{}, err
		}
		if a.Model != "" {
			rank, known := modelRank(a.Model)
			if !known {
				return Card{}, refuse(file, fmt.Sprintf(
					":node %s :affinity :model %s is not one of %s; refusing to guess",
					id, a.Model, strings.Join(modelClasses, ", ")))
			}
			floor, _ := modelRank(b.Floor)
			if rank < floor {
				return Card{}, refuse(file, fmt.Sprintf(
					":node %s route %s model %s is below model-floor %s; refusing to downgrade",
					id, a.Route, a.Model, b.Floor))
			}
		}
		c.Affinity = a
	}
	return c, nil
}

// parseOutput reads `(:result ... :branch ... :green (...))`. :branch is
// required; :result defaults to the spec's RESULT line, with {node} substituted
// and {verdict} left for the worker.
func parseOutput(file, id string, out Form) (result, branch string, green []string, err error) {
	result = "RESULT: " + id + " {verdict} <evidence>"
	for i := 0; i < len(out.List); i += 2 {
		key := out.List[i]
		if key.Kind != Keyword {
			return "", "", nil, refuse(file, fmt.Sprintf(
				":output of %s expects a keyword at byte=%d", id, key.Offset))
		}
		if i+1 >= len(out.List) {
			return "", "", nil, refuse(file, fmt.Sprintf(
				":output :%s of %s has no value; refusing to guess", key.Value, id))
		}
		val := out.List[i+1]
		switch key.Value {
		case "result":
			if val.Kind != String || val.Value == "" {
				return "", "", nil, refuse(file, fmt.Sprintf(
					":output :result of %s must be a string; refusing to guess", id))
			}
			result = strings.ReplaceAll(val.Value, "{node}", id)
		case "branch":
			if val.Kind != String || val.Value == "" {
				return "", "", nil, refuse(file, fmt.Sprintf(
					":output :branch of %s must be a string; refusing to guess", id))
			}
			branch = val.Value
		case "green":
			green = parseStringList(val)
		}
	}
	if branch == "" {
		return "", "", nil, refuse(file, fmt.Sprintf(
			":output of %s has no :branch; refusing to guess", id))
	}
	return result, branch, green, nil
}

// checkBranch holds a branch name to the spec's lower-case rule. The name is
// unique across the plan, checked by the caller.
func checkBranch(file, id, branch string) error {
	for _, r := range branch {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '/' {
			continue
		}
		return refuse(file, fmt.Sprintf(
			":node %s :output :branch %q must be lower-case [a-z0-9-/]; refusing to guess", id, branch))
	}
	return nil
}

// parseBudget reads `(:minutes n :tokens n :model-floor <class>)`. A missing or
// non-positive wall is a refusal, and an unknown floor class is refused, never
// guessed.
func parseBudget(file, id string, form Form) (Budget, error) {
	var b Budget
	for i := 0; i < len(form.List); i += 2 {
		key := form.List[i]
		if key.Kind != Keyword || i+1 >= len(form.List) {
			return b, refuse(file, fmt.Sprintf(
				":budget of %s expects keyword/value pairs; refusing to guess", id))
		}
		val := form.List[i+1]
		switch key.Value {
		case "minutes":
			if val.Kind != Integer || val.Int <= 0 {
				return b, refuse(file, fmt.Sprintf(
					":node %s :budget :minutes must be a positive integer; refusing to guess", id))
			}
			b.Minutes = val.Int
		case "tokens":
			if val.Kind != Integer || val.Int <= 0 {
				return b, refuse(file, fmt.Sprintf(
					":node %s :budget :tokens must be a positive integer; zero is refused", id))
			}
			b.Tokens = val.Int
		case "model-floor":
			name := val.Value
			if val.Kind != Keyword && val.Kind != Symbol {
				return b, refuse(file, fmt.Sprintf(
					":node %s :budget :model-floor must be a class; refusing to guess", id))
			}
			if _, known := modelRank(name); !known {
				return b, refuse(file, fmt.Sprintf(
					":node %s :budget :model-floor %s is not one of %s; refusing to guess",
					id, name, strings.Join(modelClasses, ", ")))
			}
			b.Floor = name
		}
	}
	if b.Minutes == 0 {
		return b, refuse(file, fmt.Sprintf(":node %s :budget has no :minutes; refusing to guess", id))
	}
	if b.Tokens == 0 {
		return b, refuse(file, fmt.Sprintf(":node %s :budget has no :tokens; refusing to guess", id))
	}
	if b.Floor == "" {
		return b, refuse(file, fmt.Sprintf(":node %s :budget has no :model-floor; refusing to guess", id))
	}
	return b, nil
}

// parseAffinity reads `(:bench <kind> :route <id>)` and the optional :model
// class the plan pins for the route.
func parseAffinity(file, id string, form Form) (Affinity, error) {
	var a Affinity
	for i := 0; i < len(form.List); i += 2 {
		key := form.List[i]
		if key.Kind != Keyword || i+1 >= len(form.List) {
			return a, refuse(file, fmt.Sprintf(
				":affinity of %s expects keyword/value pairs; refusing to guess", id))
		}
		val := form.List[i+1]
		switch key.Value {
		case "bench":
			a.Bench = val.Value
		case "route":
			a.Route = val.Value
		case "model":
			a.Model = val.Value
		}
	}
	if strings.TrimSpace(a.Bench) == "" {
		return a, refuse(file, fmt.Sprintf(":node %s :affinity has no :bench; refusing to guess", id))
	}
	if strings.TrimSpace(a.Route) == "" {
		return a, refuse(file, fmt.Sprintf(":node %s :affinity has no :route; refusing to guess", id))
	}
	return a, nil
}

// parseStringList reads a list of strings or bare symbols into its values. A
// non-list is a refusal, never a guess.
func parseStringList(f Form) []string {
	if f.Kind != List {
		return nil
	}
	out := make([]string, 0, len(f.List))
	for _, el := range f.List {
		switch el.Kind {
		case String, Symbol:
			out = append(out, el.Value)
		}
	}
	return out
}

// parseInputs renders the closed input list the card may read, one entry per
// named input, in plan order.
func parseInputs(f Form) []string {
	if f.Kind != List {
		return nil
	}
	out := make([]string, 0, len(f.List))
	for _, el := range f.List {
		if el.Kind == List && len(el.List) > 0 && el.List[0].Kind == Keyword {
			key := el.List[0].Value
			if len(el.List) > 1 {
				out = append(out, key+" "+renderInput(el.List[1]))
			} else {
				out = append(out, key)
			}
			continue
		}
		if el.Kind == String || el.Kind == Symbol {
			out = append(out, el.Value)
		}
	}
	return out
}

func renderInput(f Form) string {
	switch f.Kind {
	case String, Symbol, Keyword:
		return f.Value
	case Integer:
		return fmt.Sprintf("%d", f.Int)
	default:
		return ""
	}
}

// RenderCard renders one card deterministically: the same Card always produces
// the same bytes, so a re-expansion is byte-identical.
func RenderCard(c Card) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "card %s  kind=%s  repo=%s  base=%s  needs=(%s)  blocks=(%s)  ready=%s\n",
		oneline.Escape(c.Node), oneline.Escape(c.Kind), oneline.Escape(c.Repo), oneline.Escape(c.Base),
		oneline.Escape(strings.Join(c.Needs, ",")), oneline.Escape(strings.Join(c.Blocks, ",")), yesNo(c.Ready))
	fmt.Fprintf(&b, "RULES: read only the named inputs; branch rule %s; print the RESULT line; stop on a refused read.\n",
		oneline.Escape(c.Branch))
	fmt.Fprintf(&b, "IN:    %s\n", oneline.Escape(strings.Join(c.Inputs, ", ")))
	fmt.Fprintf(&b, "OUT:   %s; branch %s; green: %s\n",
		oneline.Escape(c.Result), oneline.Escape(c.Branch), oneline.Escape(strings.Join(c.Green, ", ")))
	fmt.Fprintf(&b, "BUDGET: %dm, %d tokens, model-floor %s   AFFINITY: bench=%s route=%s\n",
		c.Budget.Minutes, c.Budget.Tokens, oneline.Escape(c.Budget.Floor),
		oneline.Escape(c.Affinity.Bench), oneline.Escape(c.Affinity.Route))
	return []byte(b.String())
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// CardFile is the name of the file one card is written to inside its directory.
const CardFile = "card"

// ExpandDir writes one card directory per node under out, and returns the
// number of cards it wrote. A card already present is left byte-identical, so a
// re-expansion after one fact changes appends only the new card and mints no
// id: the directory name is the node's own stable id.
func ExpandDir(out string, cards []Card) (int, error) {
	written := 0
	for _, c := range cards {
		dir := filepath.Join(out, c.Node)
		if _, err := os.Stat(dir); err == nil {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(filepath.Join(dir, CardFile), RenderCard(c), 0o644); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

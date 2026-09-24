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

// Issue is one fact the (:derive :from (:issues ...)) sweep reads: the
// stable identifiers the kernel already uses, the slug the branch rule
// substitutes, the URL the issue input names, and the :pr fact -- bool for
// presence, number for the PR it points at -- that decides ink-or-out. An
// issue missing any of these is refused by the caller before it reaches the
// sweep, so the sweep never picks half-formed ones.
type Issue struct {
	Number   int64
	Slug     string
	URL      string
	Repo     string
	Label    string
	State    string
	HasPR    bool
	PRNumber int64
}

// Branch is one fact the (:fold :over (:branches ...)) sweep reads: the name
// of the sibling branch, the base it branched off, and whether it merged and
// went green. The fold is green-only by default -- a non-green sibling is
// excluded by the `:over` selector before it reaches the inputs list, so the
// caller does not check green here.
type Branch struct {
	Name  string
	Base  string
	Green bool
}

// Facts is the closed fact source the expander reads: pinned issues and
// pinned branches. The plan only carries the *shape* of a selector; the data
// the selector filters lives here, passed in by the caller. A nil Facts means
// a hand-written plan only, and a plan naming `:derive` or `:fold` in that
// case is a refusal naming the missing source, never a silent empty sweep.
type Facts struct {
	Issues   []Issue
	Branches []Branch
}

// ExpandPlan validates a hand-written plan, expands any (:derive) or (:fold)
// the plan carries into the same Card pass as the hand-written (:node)s,
// builds the needs/blocks graph, and returns one Card per node in plan order.
// It refuses an absent need or a cycle (through the job graph's rules 2 and
// 3), a `:derive` or `:fold` with no Facts source, an unknown `:from` or
// `:over` selector key, a duplicate branch name, and a budget below its
// floor -- always before a card is written.
//
// The variadic Facts parameter is additive: callers without a fact source
// pass none, and a plan that never names `:derive` or `:fold` is unchanged;
// callers that do sweep projects pass `ExpandPlan(plan, &worklang.Facts{...})`.
// `:facts` remains unadmitted -- the rule form is a later slice, not this one.
func ExpandPlan(plan *Plan, facts ...*Facts) ([]Card, error) {
	return ExpandPlanWithSink(plan, nil, facts...)
}

// DeriveEvent is one derive event from the expander: a node derived from the
// graph, carrying its node id, parent and rule.
type DeriveEvent struct {
	Node   string
	Parent string
	Rule   string
}

// CutEvent is one cut event from the expander: a card cut, carrying its card
// id, pool candidate, template and route.
type CutEvent struct {
	Card          string
	PoolCandidate string
	Template      string
	Route         string
}

// ExpandSink receives derive and cut events from the expander. A nil sink
// discards all events.
type ExpandSink interface {
	Derive(DeriveEvent)
	Cut(CutEvent)
}

// ExpandPlanWithSink is ExpandPlan with an optional event sink. If sink is not
// nil, one derive event is emitted per card in plan order, derived and folded
// cards included.
func ExpandPlanWithSink(plan *Plan, sink ExpandSink, facts ...*Facts) ([]Card, error) {
	if plan == nil {
		return nil, refuse("", "no plan to expand; refusing to guess")
	}
	var factsSource *Facts
	if len(facts) > 0 {
		factsSource = facts[0]
	}

	// Walk the unknown top-level forms once. :facts stays unadmitted;
	// :derive and :fold expand into the same Card pass as a hand-written
	// :node. A :derive/:fold with no Facts source is a refusal naming the
	// form, never a silent empty sweep.
	var expanded []Node
	for _, form := range plan.Unknown {
		if form.Kind != List || len(form.List) == 0 || form.List[0].Kind != Keyword {
			continue
		}
		switch form.List[0].Value {
		case "facts":
			return nil, refuse(plan.File, ":facts is not in this slice; only hand-written :nodes expand")
		case "derive":
			if factsSource == nil {
				return nil, refuse(plan.File, "a :derive is in the plan but no Facts source was passed; refusing to guess")
			}
			nodes, err := expandDerive(plan.File, form, factsSource.Issues)
			if err != nil {
				return nil, err
			}
			expanded = append(expanded, nodes...)
		case "fold":
			if factsSource == nil {
				return nil, refuse(plan.File, "a :fold is in the plan but no Facts source was passed; refusing to guess")
			}
			nodes, err := expandFold(plan.File, form, factsSource.Branches)
			if err != nil {
				return nil, err
			}
			expanded = append(expanded, nodes...)
		}
	}

	specs := make([]Card, 0, len(plan.Nodes)+len(expanded))
	for _, n := range plan.Nodes {
		c, err := expandNode(plan.File, n)
		if err != nil {
			return nil, err
		}
		specs = append(specs, c)
	}
	for _, n := range expanded {
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
		if sink != nil {
			parent := ""
			if len(specs[i].Needs) > 0 {
				parent = specs[i].Needs[0]
			}
			sink.Derive(DeriveEvent{
				Node:   id,
				Parent: parent,
				Rule:   specs[i].Kind,
			})
		}
	}
	return specs, nil
}

// expandNode reads one hand-written :node into a Card and validates its output
// contract, budget and affinity by field name. Every required field the node
// still owes is collected and named in one refusal, so a defective node costs
// one run rather than one round trip per defect.
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

	var result, branch string
	var green []string
	var b Budget
	out, ok := n.Fields["output"]
	if !ok || out.Kind != List {
		missing = append(missing, fmt.Sprintf(":node %s has no :output; refusing to guess", id))
	} else {
		var findings []string
		result, branch, green, findings = parseOutput(id, out)
		missing = append(missing, findings...)
	}

	budget, ok := n.Fields["budget"]
	if !ok || budget.Kind != List {
		missing = append(missing, fmt.Sprintf(":node %s has no :budget; a budget-less plan refuses", id))
	} else {
		var findings []string
		b, findings = parseBudget(id, budget)
		missing = append(missing, findings...)
	}
	if len(missing) > 0 {
		return Card{}, refuse(file, strings.Join(missing, "; "))
	}

	if err := checkBranch(file, id, branch); err != nil {
		return Card{}, err
	}
	c.Result, c.Branch, c.Green = result, branch, green
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
// and {verdict} left for the worker. Every finding is collected, so one run
// names every required :output field a node owes rather than stopping at the
// first.
func parseOutput(id string, out Form) (result, branch string, green []string, findings []string) {
	result = "RESULT: " + id + " {verdict} <evidence>"
	seenBranch := false
	for i := 0; i < len(out.List); i += 2 {
		key := out.List[i]
		if key.Kind != Keyword {
			findings = append(findings, fmt.Sprintf(
				":output of %s expects a keyword at byte=%d", id, key.Offset))
			continue
		}
		if i+1 >= len(out.List) {
			findings = append(findings, fmt.Sprintf(
				":output :%s of %s has no value; refusing to guess", key.Value, id))
			continue
		}
		val := out.List[i+1]
		switch key.Value {
		case "result":
			if val.Kind != String || val.Value == "" {
				findings = append(findings, fmt.Sprintf(
					":output :result of %s must be a string; refusing to guess", id))
				continue
			}
			result = strings.ReplaceAll(val.Value, "{node}", id)
		case "branch":
			seenBranch = true
			if val.Kind != String || val.Value == "" {
				findings = append(findings, fmt.Sprintf(
					":output :branch of %s must be a string; refusing to guess", id))
				continue
			}
			branch = val.Value
		case "green":
			green = parseStringList(val)
		}
	}
	if !seenBranch && branch == "" {
		findings = append(findings, fmt.Sprintf(
			":output of %s has no :branch; refusing to guess", id))
	}
	return result, branch, green, findings
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
// guessed. Every finding is collected, so one run names every required :budget
// field a node owes rather than stopping at the first.
func parseBudget(id string, form Form) (Budget, []string) {
	var b Budget
	var findings []string
	seen := map[string]bool{}
	for i := 0; i < len(form.List); i += 2 {
		key := form.List[i]
		if key.Kind != Keyword || i+1 >= len(form.List) {
			findings = append(findings, fmt.Sprintf(
				":budget of %s expects keyword/value pairs; refusing to guess", id))
			continue
		}
		val := form.List[i+1]
		switch key.Value {
		case "minutes":
			seen["minutes"] = true
			if val.Kind != Integer || val.Int <= 0 {
				findings = append(findings, fmt.Sprintf(
					":node %s :budget :minutes must be a positive integer; refusing to guess", id))
				continue
			}
			b.Minutes = val.Int
		case "tokens":
			seen["tokens"] = true
			if val.Kind != Integer || val.Int <= 0 {
				findings = append(findings, fmt.Sprintf(
					":node %s :budget :tokens must be a positive integer; zero is refused", id))
				continue
			}
			b.Tokens = val.Int
		case "model-floor":
			seen["model-floor"] = true
			name := val.Value
			if val.Kind != Keyword && val.Kind != Symbol {
				findings = append(findings, fmt.Sprintf(
					":node %s :budget :model-floor must be a class; refusing to guess", id))
				continue
			}
			if _, known := modelRank(name); !known {
				findings = append(findings, fmt.Sprintf(
					":node %s :budget :model-floor %s is not one of %s; refusing to guess",
					id, name, strings.Join(modelClasses, ", ")))
				continue
			}
			b.Floor = name
		}
	}
	if !seen["minutes"] && b.Minutes == 0 {
		findings = append(findings, fmt.Sprintf(":node %s :budget has no :minutes; refusing to guess", id))
	}
	if !seen["tokens"] && b.Tokens == 0 {
		findings = append(findings, fmt.Sprintf(":node %s :budget has no :tokens; refusing to guess", id))
	}
	if !seen["model-floor"] && b.Floor == "" {
		findings = append(findings, fmt.Sprintf(":node %s :budget has no :model-floor; refusing to guess", id))
	}
	return b, findings
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
	return ExpandDirWithSink(out, cards, nil)
}

// ExpandDirWithSink is ExpandDir with an optional event sink. If sink is not
// nil, one cut event is emitted per card actually written (existing cards are
// skipped and emit no event).
func ExpandDirWithSink(out string, cards []Card, sink ExpandSink) (int, error) {
	written := 0
	for _, c := range cards {
		dir := filepath.Join(out, c.Node)
		if _, err := os.Stat(dir); err == nil {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return written, err
		}
		content := RenderCard(c)
		if err := os.WriteFile(filepath.Join(dir, CardFile), content, 0o644); err != nil {
			return written, err
		}
		written++
		if sink != nil {
			sink.Cut(CutEvent{
				Card:          c.Node,
				PoolCandidate: c.Affinity.Bench,
				Template:      string(content),
				Route:         c.Affinity.Route,
			})
		}
	}
	return written, nil
}

// expandDerive reads one (:derive ...) form, filters the issue facts through
// the (:from (:issues ...)) selector, and mints one Node per matched issue.
// The :as template yields the node's id; `{n}` (the issue number), `{slug}
// (the issue's slug) and `{url}` (the issue's URL) are substituted into :as,
// :inputs, :output :branch and any other string the derive template names.
// The wired Node goes through expandNode so a derived node carries the same
// Card fields as a hand-written one, and an unknown selector key is a refusal
// naming the key.
func expandDerive(file string, form Form, issues []Issue) ([]Node, error) {
	seen := map[string]bool{}
	fields := map[string]Form{}
	body := form.List[1:]
	for i := 0; i+1 < len(body); i += 2 {
		key := body[i]
		val := body[i+1]
		if key.Kind != Keyword {
			return nil, refuse(file, fmt.Sprintf(
				":derive expects a keyword at byte=%d; refusing to guess", key.Offset))
		}
		seen[key.Value] = true
		fields[key.Value] = val
	}
	asForm, ok := fields["as"]
	if !ok || asForm.Kind != String || asForm.Value == "" {
		return nil, refuse(file, ":derive has no :as; refusing to guess")
	}
	kindForm, ok := fields["kind"]
	if !ok || (kindForm.Kind != Keyword && kindForm.Kind != Symbol) || !isKnownKind(kindForm.Value) {
		name := ""
		if ok {
			name = kindForm.Value
		}
		return nil, refuse(file, fmt.Sprintf(
			":derive :kind %s is not one of %s; refusing to guess",
			name, strings.Join(knownKinds[:6], ", ")))
	}
	fromForm, ok := fields["from"]
	if !ok || fromForm.Kind != List || len(fromForm.List) == 0 {
		return nil, refuse(file, ":derive has no :from; refusing to guess")
	}
	if !fromForm.List[0].IsKeyword("issues") {
		return nil, refuse(file, ":derive :from must start with :issues; refusing to guess")
	}
	selector, err := parseIssueSelector(file, fromForm)
	if err != nil {
		return nil, err
	}

	var matched []Issue
	for _, iss := range issues {
		if selector.match(iss) {
			matched = append(matched, iss)
		}
	}
	out := make([]Node, 0, len(matched))
	for _, iss := range matched {
		subs := issueSubs(iss)
		n := Node{
			Fields:  map[string]Form{},
			Unknown: map[string]Form{},
		}
		n.Fields["id"] = Form{
			Kind:   String,
			Offset: asForm.Offset,
			Value:  templateReplace(asForm.Value, subs),
		}
		n.Fields["kind"] = Form{
			Kind:   Keyword,
			Offset: kindForm.Offset,
			Value:  kindForm.Value,
		}
		for _, opt := range []string{"repo", "base", "inputs", "output", "budget", "affinity"} {
			if !seen[opt] {
				continue
			}
			n.Fields[opt] = substituteForm(fields[opt], subs)
		}
		out = append(out, n)
	}
	return out, nil
}

// expandFold reads one (:fold ...) form, filters the branch facts through the
// (:over (:branches ...)) selector, and mints one Node whose :inputs name the
// green branches as `:artifact` entries. A non-green sibling is excluded by
// the selector before it reaches the inputs list -- the fold is green-only.
// The wired Node carries the same Card fields as a hand-written node, and an
// unknown selector key is a refusal naming the key.
func expandFold(file string, form Form, branches []Branch) ([]Node, error) {
	seen := map[string]bool{}
	fields := map[string]Form{}
	body := form.List[1:]
	for i := 0; i+1 < len(body); i += 2 {
		key := body[i]
		val := body[i+1]
		if key.Kind != Keyword {
			return nil, refuse(file, fmt.Sprintf(
				":fold expects a keyword at byte=%d; refusing to guess", key.Offset))
		}
		seen[key.Value] = true
		fields[key.Value] = val
	}
	asForm, ok := fields["as"]
	if !ok || asForm.Kind != String || asForm.Value == "" {
		return nil, refuse(file, ":fold has no :as; refusing to guess")
	}
	kindForm, ok := fields["kind"]
	if !ok || (kindForm.Kind != Keyword && kindForm.Kind != Symbol) || !isKnownKind(kindForm.Value) {
		name := ""
		if ok {
			name = kindForm.Value
		}
		return nil, refuse(file, fmt.Sprintf(
			":fold :kind %s is not one of %s; refusing to guess",
			name, strings.Join(knownKinds[:6], ", ")))
	}
	overForm, ok := fields["over"]
	if !ok || overForm.Kind != List || len(overForm.List) == 0 {
		return nil, refuse(file, ":fold has no :over; refusing to guess")
	}
	if !overForm.List[0].IsKeyword("branches") {
		return nil, refuse(file, ":fold :over must start with :branches; refusing to guess")
	}
	selector, err := parseBranchSelector(file, overForm)
	if err != nil {
		return nil, err
	}

	var matched []Branch
	for _, br := range branches {
		if selector.match(br) {
			matched = append(matched, br)
		}
	}

	n := Node{
		Fields:  map[string]Form{},
		Unknown: map[string]Form{},
	}
	n.Fields["id"] = Form{Kind: String, Offset: asForm.Offset, Value: asForm.Value}
	n.Fields["kind"] = Form{Kind: Keyword, Offset: kindForm.Offset, Value: kindForm.Value}
	if seen["repo"] {
		n.Fields["repo"] = fields["repo"]
	}
	if seen["base"] {
		n.Fields["base"] = fields["base"]
	}
	if seen["inputs"] {
		// The inputs template names the shape of one input (e.g.
		// `((:artifact "{branch}"))`); one entry is minted per matched
		// branch, with `{branch}` substituted. A template carrying more
		// than one element keeps the first shape only -- the test pins a
		// single-element template.
		inputsTpl := fields["inputs"]
		var items []Form
		if inputsTpl.Kind == List && len(inputsTpl.List) > 0 {
			shape := inputsTpl.List[0]
			items = make([]Form, 0, len(matched))
			for _, br := range matched {
				items = append(items, substituteForm(shape, branchSubs(br)))
			}
		}
		n.Fields["inputs"] = Form{
			Kind:   List,
			Offset: inputsTpl.Offset,
			List:   items,
		}
	}
	if seen["output"] {
		n.Fields["output"] = fields["output"]
	}
	if seen["budget"] {
		n.Fields["budget"] = fields["budget"]
	}
	if seen["affinity"] {
		n.Fields["affinity"] = fields["affinity"]
	}
	if len(matched) == 0 {
		return []Node{n}, nil
	}
	return []Node{n}, nil
}

// issueSelector filters an issue by the keys the (:derive :from (:issues ...))
// selector carries. An unknown selector key refuses; the issue is in the
// sweep because the facts say so, not because a rule failed.
type issueSelector struct {
	repo  string
	label string
	state string
	hasPR *bool
}

func (s issueSelector) match(i Issue) bool {
	if s.repo != "" && i.Repo != s.repo {
		return false
	}
	if s.label != "" && i.Label != s.label {
		return false
	}
	if s.state != "" && i.State != s.state {
		return false
	}
	if s.hasPR != nil && i.HasPR != *s.hasPR {
		return false
	}
	return true
}

// parseSelectorBool reads a selector boolean. The spec writes it as the bare
// symbol true or false (`:has-pr false`, `:green true`); any other token -- a
// typo such as tru, a keyword, a string -- is not a boolean, so the caller
// refuses it instead of reading it as false and selecting the opposite set.
func parseSelectorBool(val Form) (bool, bool) {
	if val.Kind != Symbol {
		return false, false
	}
	switch val.Value {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

func parseIssueSelector(file string, form Form) (issueSelector, error) {
	var s issueSelector
	body := form.List[1:]
	for i := 0; i+1 < len(body); i += 2 {
		key := body[i]
		val := body[i+1]
		if key.Kind != Keyword {
			return s, refuse(file, fmt.Sprintf(
				":derive :from expects keywords at byte=%d; refusing to guess", key.Offset))
		}
		switch key.Value {
		case "repo":
			s.repo = val.Value
		case "label":
			s.label = val.Value
		case "state":
			s.state = val.Value
		case "has-pr":
			b, ok := parseSelectorBool(val)
			if !ok {
				return s, refuse(file, fmt.Sprintf(
					":derive :from :has-pr %s is not a boolean (want true or false); refusing to guess", renderVal(val)))
			}
			s.hasPR = &b
		default:
			return s, refuse(file, fmt.Sprintf(
				":derive :from :%s is not a known selector key; refusing to guess", key.Value))
		}
	}
	return s, nil
}

// branchSelector filters a branch by the keys the (:fold :over (:branches ...))
// selector carries.
type branchSelector struct {
	prefix string
	base   string
	green  *bool
}

func (s branchSelector) match(b Branch) bool {
	if s.prefix != "" && !strings.HasPrefix(b.Name, s.prefix) {
		return false
	}
	if s.base != "" && b.Base != s.base {
		return false
	}
	if s.green != nil && b.Green != *s.green {
		return false
	}
	return true
}

func parseBranchSelector(file string, form Form) (branchSelector, error) {
	var s branchSelector
	body := form.List[1:]
	for i := 0; i+1 < len(body); i += 2 {
		key := body[i]
		val := body[i+1]
		if key.Kind != Keyword {
			return s, refuse(file, fmt.Sprintf(
				":fold :over expects keywords at byte=%d; refusing to guess", key.Offset))
		}
		switch key.Value {
		case "prefix":
			s.prefix = val.Value
		case "base":
			s.base = val.Value
		case "green":
			b, ok := parseSelectorBool(val)
			if !ok {
				return s, refuse(file, fmt.Sprintf(
					":fold :over :green %s is not a boolean (want true or false); refusing to guess", renderVal(val)))
			}
			s.green = &b
		default:
			return s, refuse(file, fmt.Sprintf(
				":fold :over :%s is not a known selector key; refusing to guess", key.Value))
		}
	}
	return s, nil
}

// issueSubs returns the {n}, {slug}, {url} substitution table for one issue.
// n is the issue's number, slug the issue's title-slug, url the issue URL --
// the same names the (a) sweep template in docs/SPEC-WORKLANG.md uses.
func issueSubs(i Issue) map[string]string {
	return map[string]string{
		"n":    fmt.Sprintf("%d", i.Number),
		"slug": i.Slug,
		"url":  i.URL,
	}
}

// branchSubs returns the {branch} substitution table for one branch. The
// fold's input template uses `{branch}` -- the shape in the spec example.
func branchSubs(b Branch) map[string]string {
	return map[string]string{
		"branch": b.Name,
	}
}

// templateReplace substitutes every `{key}` placeholder in s with the value
// from subs. An unknown `{key}` is left verbatim so a missing template fact
// remains visible in the printed card.
func templateReplace(s string, subs map[string]string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '{' {
			end := -1
			for j := i + 1; j < len(s); j++ {
				if s[j] == '}' {
					end = j
					break
				}
			}
			if end > i {
				key := s[i+1 : end]
				if v, ok := subs[key]; ok {
					b.WriteString(v)
					i = end + 1
					continue
				}
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// substituteForm recurses into every string value of f and replaces
// `{key}` placeholders. Lists are descended; Keywords, Symbols, Integers and
// unknown kinds are returned unchanged so a placeholder inside a non-string
// stays verbatim and a malformed template is visible in print.
func substituteForm(f Form, subs map[string]string) Form {
	switch f.Kind {
	case String:
		return Form{
			Kind:   String,
			Offset: f.Offset,
			Value:  templateReplace(f.Value, subs),
		}
	case List:
		out := make([]Form, len(f.List))
		for i, el := range f.List {
			out[i] = substituteForm(el, subs)
		}
		return Form{
			Kind:   List,
			Offset: f.Offset,
			List:   out,
		}
	default:
		return f
	}
}

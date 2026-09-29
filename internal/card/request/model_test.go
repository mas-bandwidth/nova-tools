package request

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The lifecycle tables of lifecycle.go are modelled in TLA+ (tla/CardManager.tla).
// This file compares them: it reads the model's text, extracts the state names, the
// events with their source and destination states and the states a card can be
// replaced from, and holds them against the Go tables.
//
// The extractor relies on these conventions of the model, which its author must
// keep stable (or change together with this file):
//
//  1. every set of names is a top-level definition `Name == {"a", "b", ...}`
//     (possibly over several lines), and `States` is the set of state names;
//  2. `Events` is such a set, one string per event;
//  3. `To(c, e)` is a CASE whose arms are `e = "event" -> "state"` or
//     `e \in {"event", ...} -> "state"`: the state an event leaves a card in; a card
//     that is unplaced (has left the table) is the string "none", the value the model's
//     St(c) has for a card in no cell;
//  4. `OwnOK(c, e)` is a CASE with one arm `e = "event" -> ...` per event, whose
//     source states are the literals of the arm's `St(c) = "state"` and
//     `St(c) \in {...}` (or `St(c) \in Name`, a set defined by rule 1) terms; a
//     disjunct of an arm that mentions `Broken =` is a reversed witness, not the
//     machine, and is not read;
//  5. `Replaceable == {c \in ActiveIds : St(c) \in {...}}` (or `\in Name`) is the
//     states a card can be replaced from.
//
// The model's events are named as the model names them; modelEventOf maps each
// Go lifecycle input to the model event that stands for it.

// modelInfo is what the extractor read from the model.
type modelInfo struct {
	states      []string
	edges       map[string]map[string]bool // event -> "from>to"
	replaceable []string
}

var (
	setDefRE   = regexp.MustCompile(`(?m)^(\w+)\s*==\s*\{([^}]*)\}`)
	stringsRE  = regexp.MustCompile(`"([^"]*)"`)
	armStartRE = regexp.MustCompile(`(?:CASE|\[\])\s+e\s*(=|\\in)\s*(\{[^}]*\}|"[^"]*")\s*->`)
	stTermRE   = regexp.MustCompile(`St\(c\)\s*(=|\\in)\s*(\{[^}]*\}|"[^"]*"|\w+)`)
	replDefRE  = regexp.MustCompile(`(?m)^Replaceable\s*==\s*\{c \\in ActiveIds : St\(c\) \\in (\{[^}]*\}|\w+)\}`)
	headerRE   = regexp.MustCompile(`(?m)^(\w+)(\([^)]*\))?\s*==`)
)

func stripComments(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if i := strings.Index(l, `\*`); i >= 0 {
			l = l[:i]
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func literals(s string) []string {
	var out []string
	for _, m := range stringsRE.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// definitionBody is the text of the top-level definition `name(...) ==` up to the
// next top-level definition or the end.
func definitionBody(text, name string) (string, bool) {
	locs := headerRE.FindAllStringSubmatchIndex(text, -1)
	for i, l := range locs {
		if text[l[2]:l[3]] != name {
			continue
		}
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		return text[l[1]:end], true
	}
	return "", false
}

func extractModel(text string) (*modelInfo, error) {
	text = stripComments(text)
	sets := map[string][]string{}
	for _, m := range setDefRE.FindAllStringSubmatch(text, -1) {
		sets[m[1]] = literals(m[2])
	}
	resolve := func(expr string) []string {
		expr = strings.TrimSpace(expr)
		if strings.HasPrefix(expr, "{") || strings.HasPrefix(expr, `"`) {
			return literals(expr)
		}
		return sets[expr]
	}
	info := &modelInfo{states: sets["States"], edges: map[string]map[string]bool{}}
	if len(info.states) == 0 {
		return nil, fmt.Errorf("no `States == {...}` set in the model")
	}
	events := sets["Events"]
	if len(events) == 0 {
		return nil, fmt.Errorf("no `Events == {...}` set in the model")
	}
	to := map[string]string{}
	body, ok := definitionBody(text, "To")
	if !ok {
		return nil, fmt.Errorf("no `To(c, e) ==` definition in the model")
	}
	arms := armStartRE.FindAllStringSubmatchIndex(body, -1)
	for i, a := range arms {
		end := len(body)
		if i+1 < len(arms) {
			end = arms[i+1][0]
		}
		dest := literals(body[a[1]:end])
		if len(dest) != 1 {
			return nil, fmt.Errorf("To: the arm for %s names %d destinations, want 1", body[a[4]:a[5]], len(dest))
		}
		for _, ev := range literals(body[a[4]:a[5]]) {
			to[ev] = dest[0]
		}
	}
	body, ok = definitionBody(text, "OwnOK")
	if !ok {
		return nil, fmt.Errorf("no `OwnOK(c, e) ==` definition in the model")
	}
	arms = armStartRE.FindAllStringSubmatchIndex(body, -1)
	for i, a := range arms {
		end := len(body)
		if i+1 < len(arms) {
			end = arms[i+1][0]
		}
		var sources []string
		for _, disj := range strings.Split(body[a[1]:end], `\/`) {
			if strings.Contains(disj, "Broken =") {
				continue
			}
			for _, m := range stTermRE.FindAllStringSubmatch(disj, -1) {
				sources = append(sources, resolve(m[2])...)
			}
		}
		for _, ev := range literals(body[a[4]:a[5]]) {
			if info.edges[ev] == nil {
				info.edges[ev] = map[string]bool{}
			}
			d, has := to[ev]
			if !has {
				return nil, fmt.Errorf("event %s has a source in OwnOK but no destination in To", ev)
			}
			for _, s := range sources {
				info.edges[ev][s+">"+modelDest(d)] = true
			}
		}
	}
	for _, ev := range events {
		if len(info.edges[ev]) == 0 {
			return nil, fmt.Errorf("event %s has no source state in OwnOK", ev)
		}
	}
	m := replDefRE.FindStringSubmatch(text)
	if m == nil {
		return nil, fmt.Errorf("no `Replaceable == {c \\in ActiveIds : St(c) \\in ...}` in the model")
	}
	info.replaceable = resolve(m[1])
	return info, nil
}

// modelDest reads the destination the model names: "none" is a card that left the
// table.
func modelDest(s string) string {
	if s == "none" {
		return string(Unplaced) + "-"
	}
	return s
}

// modelEventOf maps each model event to the Go lifecycle inputs that stand for it.
var modelEventOf = map[string][]InputType{
	"start":    {InStart},
	"result":   {InResult},
	"head":     {InHead},
	"merge":    {InVerdictAccept},
	"rework":   {InVerdictRetry, InVerdictRework},
	"requeue":  {InQueueRejected},
	"land":     {InLanding, InExternalLanding},
	"cancel":   {InCancel},
	"depfail":  {InDependencyFailed},
	"complete": nil, // no input takes a card to an end: there is no done state
}

func goEdges(inputs []InputType) map[string]bool {
	out := map[string]bool{}
	for _, it := range inputs {
		for _, s := range SourceStates(it) {
			d, _ := Destination(it, s)
			out[string(s)+">"+destinationOrUnplaced(d)] = true
		}
	}
	return out
}

func destinationOrUnplaced(d State) string {
	if d == Unplaced {
		return string(Unplaced) + "-"
	}
	return string(d)
}

// compareModel returns the differences between the model and the Go tables, sorted,
// one line each; none when they agree.
func compareModel(m *modelInfo) []string {
	var diff []string
	goStates := map[string]bool{}
	for _, s := range States() {
		goStates[string(s)] = true
	}
	modelStates := map[string]bool{}
	for _, s := range m.states {
		modelStates[s] = true
		if !goStates[s] {
			diff = append(diff, fmt.Sprintf("state %q is in the model and not in the Go states", s))
		}
	}
	for s := range goStates {
		if !modelStates[s] {
			diff = append(diff, fmt.Sprintf("state %q is in the Go states and not in the model", s))
		}
	}
	mapped := map[InputType]bool{}
	for ev, inputs := range modelEventOf {
		for _, it := range inputs {
			mapped[it] = true
		}
		me, in := m.edges[ev]
		if !in {
			diff = append(diff, fmt.Sprintf("event %q is mapped to Go inputs but the model has no such event", ev))
			continue
		}
		ge := goEdges(inputs)
		if len(inputs) == 0 && len(me) > 0 {
			diff = append(diff, fmt.Sprintf("the model's event %q (%s) has no Go input", ev, joinKeys(me)))
			continue
		}
		for e := range me {
			if !ge[e] {
				diff = append(diff, fmt.Sprintf("event %q: transition %s is in the model and not in Go (%v)", ev, e, inputs))
			}
		}
		for e := range ge {
			if !me[e] {
				diff = append(diff, fmt.Sprintf("event %q: transition %s is in Go (%v) and not in the model", ev, e, inputs))
			}
		}
	}
	for ev := range m.edges {
		if _, ok := modelEventOf[ev]; !ok {
			diff = append(diff, fmt.Sprintf("the model's event %q has no Go input (add it to modelEventOf if it is one)", ev))
		}
	}
	for _, it := range InputTypes() {
		if !mapped[it] {
			diff = append(diff, fmt.Sprintf("Go input %q is not mapped to a model event", it))
		}
	}
	rep := map[string]bool{}
	for _, s := range m.replaceable {
		rep[s] = true
	}
	for _, s := range States() {
		if Replaceable(s) && !rep[string(s)] {
			diff = append(diff, fmt.Sprintf("a card in %q can be replaced in Go and not in the model", s))
		}
	}
	for s := range rep {
		if !Replaceable(State(s)) {
			diff = append(diff, fmt.Sprintf("a card in %q can be replaced in the model and not in Go", s))
		}
	}
	sort.Strings(diff)
	return diff
}

func joinKeys(m map[string]bool) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

// TestLifecycleAgreesWithTheModel compares the Go lifecycle with tla/CardManager.tla
// when the model is in the tree. On this branch it is not (it is on the branch of
// PR #4599, rowan/card-layer-model), so the test skips and says so: it does not pass.
func TestLifecycleAgreesWithTheModel(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "tla", "CardManager.tla")
	text, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("tla/CardManager.tla is not in this tree: the model of the card manager is on the branch of PR #4599 (rowan/card-layer-model), " +
			"so the Go lifecycle in lifecycle.go has NOT been compared with it; this test compares the states and transitions when the model lands")
	}
	if err != nil {
		t.Fatal(err)
	}
	m, err := extractModel(string(text))
	if err != nil {
		t.Fatalf("the extractor cannot read the model (its conventions changed; see model_test.go): %v", err)
	}
	if diff := compareModel(m); len(diff) > 0 {
		t.Fatalf("the Go lifecycle (lifecycle.go) and the TLA+ model (tla/CardManager.tla) differ; a change to one is a change to the other, in the same PR, with TLC run:\n  %s",
			strings.Join(diff, "\n  "))
	}
}

func readSnapshot(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/CardManager.4599.snapshot.tla")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The extractor reads the model text of PR #4599 as the model's author wrote it.
func TestExtractorReadsTheModelOfPR4599(t *testing.T) {
	t.Parallel()
	m, err := extractModel(readSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.states, ",") != "waiting,ready,working,review,merging,landed,done" {
		t.Errorf("states = %v", m.states)
	}
	want := map[string]string{
		"start":    "ready>working",
		"result":   "working>review",
		"head":     "merging>review,review>review",
		"merge":    "review>merging",
		"rework":   "review>ready",
		"requeue":  "merging>review",
		"land":     "merging>landed,ready>landed,waiting>landed,working>landed",
		"complete": "review>done",
		"cancel":   "merging>done,ready>done,review>done,waiting>done,working>done",
		"depfail":  "waiting>done",
	}
	if len(m.edges) != len(want) {
		t.Errorf("%d events extracted, want %d", len(m.edges), len(want))
	}
	for ev, w := range want {
		if got := joinKeys(m.edges[ev]); got != w {
			t.Errorf("event %s: %s, want %s", ev, got, w)
		}
	}
	// the reversed witness of `land` (a card resurrected from done) is not the machine
	if m.edges["land"]["done>landed"] {
		t.Error("a Broken = witness disjunct was read as the machine")
	}
	if strings.Join(m.replaceable, ",") != "waiting,ready,review,merging" {
		t.Errorf("replaceable = %v", m.replaceable)
	}
}

// The comparison fails, naming what differs, when the model is not the Go table:
// the model of PR #4599 predates the owner's rulings, so it differs in exactly the
// ways the rulings changed the lifecycle.
func TestComparisonNamesTheDifferencesFromTheModelOfPR4599(t *testing.T) {
	t.Parallel()
	m, err := extractModel(readSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	diff := strings.Join(compareModel(m), "\n")
	for _, want := range []string{
		`state "done" is in the model and not in the Go states`,
		`the model's event "complete" (review>done) has no Go input`,
		`event "cancel": transition merging>done is in the model and not in Go`,
		`event "cancel": transition merging>- is in Go ([cancel]) and not in the model`,
		`event "depfail": transition waiting>- is in Go ([dependency-failed]) and not in the model`,
		`a card in "review" can be replaced in the model and not in Go`,
		`a card in "merging" can be replaced in the model and not in Go`,
	} {
		if !strings.Contains(diff, want) {
			t.Errorf("the differences do not include %q; they are:\n%s", want, diff)
		}
	}
	// what agrees is not reported
	for _, unwanted := range []string{`event "start"`, `event "result"`, `event "merge"`, `event "rework"`, `event "requeue"`, `event "land"`, `event "head"`} {
		if strings.Contains(diff, unwanted) {
			t.Errorf("an agreeing event is reported: %q in\n%s", unwanted, diff)
		}
	}
}

// A model rewritten to the lifecycle of lifecycle.go agrees with it: same states,
// the stopped cards leave the table (the model's "none"), no done and no complete,
// replace from waiting and ready.
func TestComparisonAgreesWithAModelOfTheGoLifecycle(t *testing.T) {
	t.Parallel()
	text := readSnapshot(t)
	text = strings.Replace(text, `, "done"}`, `}`, 1) // States
	text = strings.Replace(text, `Terminal == {"landed", "done"}`, `Terminal == {"landed"}`, 1)
	text = strings.Replace(text, `, "cancel", "depfail"}`, `, "cancel", "depfail"}`, 1)
	text = strings.Replace(text, `"complete", "cancel", "depfail"}`, `"cancel", "depfail"}`, 1)
	text = strings.Replace(text, `[] e \in {"complete", "cancel", "depfail"} -> "done"`, `[] e \in {"cancel", "depfail"} -> "none"`, 1)
	text = regexpMust(`\[\] e = "complete".*\n`).ReplaceAllString(text, "")
	text = strings.Replace(text, `[] e = "cancel"   -> St(c) \in Open`, `[] e = "cancel"   -> St(c) \in Open`, 1)
	text = strings.Replace(text, `St(c) \in {"waiting", "ready", "review", "merging"}`, `St(c) \in {"waiting", "ready"}`, 1)
	// the model's events are also the events the Go inputs map to, less complete
	saved := modelEventOf["complete"]
	delete(modelEventOf, "complete")
	defer func() { modelEventOf["complete"] = saved }()
	m, err := extractModel(text)
	if err != nil {
		t.Fatal(err)
	}
	if diff := compareModel(m); len(diff) != 0 {
		t.Fatalf("a model of the Go lifecycle differs from it:\n  %s", strings.Join(diff, "\n  "))
	}
}

func regexpMust(s string) *regexp.Regexp { return regexp.MustCompile(s) }

// The extractor refuses, loudly, a model whose conventions changed: a silent zero
// would be a comparison that passes on nothing.
func TestExtractorRefusesAModelItCannotRead(t *testing.T) {
	t.Parallel()
	good := readSnapshot(t)
	for name, text := range map[string]string{
		"empty":                     "",
		"no States":                 strings.Replace(good, "States   ==", "Statez   ==", 1),
		"no Events":                 strings.Replace(good, "Events ==", "Eventz ==", 1),
		"no To":                     strings.Replace(good, "To(c, e) ==", "Tox(c, e) ==", 1),
		"no OwnOK":                  strings.Replace(good, "OwnOK(c, e) ==", "OwnOx(c, e) ==", 1),
		"no Replaceable":            strings.Replace(good, "Replaceable ==", "Replaceabl ==", 1),
		"an event without a source": strings.Replace(good, `[] e = "depfail"  -> St(c) = "waiting"`, `[] e = "depfail"  -> TRUE`, 1),
	} {
		if _, err := extractModel(text); err == nil {
			t.Errorf("%s: the extractor read a model it cannot", name)
		}
	}
}

// The skip message says what it is for.
func TestTheSkipNamesThePR(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("model_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "PR #4599") || !strings.Contains(string(b), "has NOT been compared") {
		t.Fatal("the skip does not name the PR and say the comparison did not happen")
	}
}

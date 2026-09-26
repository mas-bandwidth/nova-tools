package decide

// THE WORK TYPE OF A CARD (#2943, Glenn 2026-09-22).
//
// A card's KIND header says what the cutter called it; it does not say what the
// work is. The 2026-09-22 work-types report (rowan-new
// reports/work-types-and-model-fit-2026-09-22.md, section 3) found eight
// shapes of work across 2,396 model-eligible attempts, separating OK rate from
// 15% to 93% and cost per useful card over a 67x range, each with one test a
// router can run on the card text alone. Those tests are the rules below.
//
// The rules answer first and a rule that fires makes NO model call: where the
// header is present and honest the rule was right more often than Jev (the
// report's five disagreements, read by hand). Jev is asked only where no rule
// fires -- a card with no usable header -- once, over the bounded card text,
// and only an answer among the eight is taken. With no decider such a card is
// `unclassified`, never guessed.
//
// The answer is written ON the card, as two header lines the router and every
// reader after it read instead of inferring:
//
//	WORKTYPE: <type> by=<rules|jev> rule=<rule> conf=<x> allowed=<route,route|->
//	ROUTE: jev=<rung|fallback> conf=<x> rung=<name> model=<id|ask-...> worktype=<type> why=<...>
//
// allowed= is allowed_routes[type]: the routes the per-type ranking lets this
// card run on, read from a file keyed by work type (LoadWorkTypeRoutes), and a
// dash where no table was given or the table has no row for the type.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The eight work types, in the report's order (cheapest per useful card
// first), and the answer for a card no rule and no Jev could read.
const (
	WorkTypeIssueColdRead          = "issue-cold-read"
	WorkTypeTranscriptReproduction = "transcript-reproduction"
	WorkTypeSpecRuleConformance    = "spec-rule-conformance"
	WorkTypeCodeAuditRead          = "code-audit-read"
	WorkTypeSpecContractProbe      = "spec-contract-probe"
	WorkTypeIssueFixRedFirst       = "issue-fix-red-first"
	WorkTypeRecutAtTip             = "recut-at-tip"
	WorkTypeConformanceCell        = "conformance-cell"

	WorkTypeUnclassified = "unclassified"
)

// WorkTypes is the closed set, in the report's order.
var WorkTypes = []string{
	WorkTypeIssueColdRead, WorkTypeTranscriptReproduction, WorkTypeSpecRuleConformance,
	WorkTypeCodeAuditRead, WorkTypeSpecContractProbe, WorkTypeIssueFixRedFirst,
	WorkTypeRecutAtTip, WorkTypeConformanceCell,
}

// workTypeCriteria is one line per type, the report's definitions: the options
// Jev chooses among when no rule fires.
var workTypeCriteria = map[string]string{
	WorkTypeIssueColdRead:          "read one tracked issue or pull request at a named head and return a finding or verdict about it; no code written",
	WorkTypeTranscriptReproduction: "replay a documented command transcript (a named heading in docs/CLI.md or docs/TESTS.md) against the tree and say whether it still reproduces line for line; a closed verdict, no code written",
	WorkTypeSpecRuleConformance:    "decide whether the code at a named base satisfies one written rule of a SPEC document; a closed verdict such as CONFORMS or GAP, no code written",
	WorkTypeCodeAuditRead:          "read everything a commit or open pull request changes and brief a human in prose; no code written",
	WorkTypeSpecContractProbe:      "answer a design question against a specification or contract document; read-only, a prose judgement, no code written",
	WorkTypeIssueFixRedFirst:       "write a code fix for a tracked issue together with a new test, deriving the paths itself; it produces a branch",
	WorkTypeRecutAtTip:             "rewrite an existing pull request's change at a newer base, red test first; it produces a branch",
	WorkTypeConformanceCell:        "write ONE new test file at a path the card names, in a named language leg, with a RUN command and a DONE-WHEN demanding the test fail on the base and pass at the head",
}

// WorkTypeQuestion is the name the work-type question is asked under.
const WorkTypeQuestion = "worktype"

// Who answered.
const (
	WorkTypeByRules = "rules"
	WorkTypeByJev   = "jev"
)

// confWorkTypeRule is a rule's confidence: the rules were right where the
// header was honest, and not certain -- the report's hand read had them wrong
// in 2 of 5 disagreements.
const confWorkTypeRule = 0.90

// workTypeBound is how much of the card Jev is shown: the report's question
// bound, from the head, because a card says what it is at the start.
const workTypeBound = 4096

// WorkTypeResult is one card's work type and how it was reached.
type WorkTypeResult struct {
	Type       string
	By         string // rules | jev
	Rule       string // the rule that fired, or why none did
	Confidence float64
	Usage      RouteUsage
}

// Known reports whether the card has one of the eight types.
func (r WorkTypeResult) Known() bool { return KnownWorkType(r.Type) }

// KnownWorkType reports whether t is one of the eight.
func KnownWorkType(t string) bool {
	for _, w := range WorkTypes {
		if w == t {
			return true
		}
	}
	return false
}

// ClassifyWorkType reads one card. The rules answer first, with no call; only
// where none fires and d is not nil is Jev asked, once.
func ClassifyWorkType(ctx context.Context, d Decider, card string) (WorkTypeResult, error) {
	if t, rule, ok := workTypeByRules(card); ok {
		return WorkTypeResult{Type: t, By: WorkTypeByRules, Rule: rule, Confidence: confWorkTypeRule}, nil
	}
	res := WorkTypeResult{Type: WorkTypeUnclassified, By: WorkTypeByRules, Rule: "no-rule"}
	if d == nil {
		return res, nil
	}
	state := stripStamp(card)
	if len(state) > workTypeBound {
		state = state[:workTypeBound]
	}
	start := time.Now()
	answers, usage, err := d.Decide(ctx, state, map[string]Question{WorkTypeQuestion: {
		Instructions: "Below is the text of one work card given to a coding model. Answer which ONE option describes the kind of work the card asks for. Judge the work the card demands, not the subject matter it is about.",
		Choice:       workTypeCriteria,
	}})
	res.By = WorkTypeByJev
	res.Usage = RouteUsage{Calls: 1, Failed: err != nil, Ms: int(time.Since(start).Milliseconds()), HasMs: true}
	if err != nil {
		res.Rule = "no-rule; jev refused: " + oneline.Err(err)
		return res, nil
	}
	res.Usage.InputTokens, res.Usage.HasInput = usage.InputTokens, usage.HasInput
	res.Usage.OutputTokens, res.Usage.HasOutput = usage.OutputTokens, usage.HasOutput
	a, ok := answers[WorkTypeQuestion]
	switch {
	case !ok:
		res.Rule = "no-rule; jev named no type"
	case !KnownWorkType(a.Choice):
		res.Rule = "no-rule; jev named " + oneline.Field(a.Choice) + ", not one of the eight"
	default:
		res.Type, res.Rule, res.Confidence = a.Choice, "no-rule; jev", a.Confidence
	}
	return res, nil
}

var (
	headerLine   = regexp.MustCompile(`^([A-Z][A-Z0-9-]*):\s*(.*)$`)
	wordRecut    = regexp.MustCompile(`(?i)\brecut\b`)
	cellWording  = regexp.MustCompile(`(?i)\b(weak-cell|matrix-cell|conformance[- ]cell)\b`)
	testPath     = regexp.MustCompile(`(?i)(_test\.[a-z]+|\.test\.[a-z]+|(^|/)test_[^/\s]*|(^|/)tests?/)`)
	auditMarkers = regexp.MustCompile(`(?i)(pre-read of|the tests that landed with dev commit|the files the pull request changes)`)
	transcriptOf = regexp.MustCompile(`docs/(CLI|TESTS)\.md`)
	transcriptV  = regexp.MustCompile(`(?i)line for line|\bCLEAN\b|\bDRIFT\b`)
	specDoc      = regexp.MustCompile(`docs/SPEC-[A-Za-z0-9_-]+\.md`)
	specVerdict  = regexp.MustCompile(`\b(CONFORMS|GAP)\b`)
	readOnly     = regexp.MustCompile(`(?i)read-only|read only|write nothing|no diff`)
	redGreen     = regexp.MustCompile(`(?i)red[- ]then[- ]green|red first|red on (the )?base|fails? on (the )?base`)
	issueRef     = regexp.MustCompile(`#\d+|(?i)\bissue\b`)
)

// cardHeader is the card's KEY: value lines in its first 40 lines, the stamp
// lines left out so a stamped card classifies as it did before.
type cardHeader struct {
	first  string
	fields map[string]string
	text   string
}

func readCardHeader(card string) cardHeader {
	h := cardHeader{fields: map[string]string{}, text: stripStamp(card)}
	lines := strings.Split(h.text, "\n")
	for i, line := range lines {
		if i == 0 {
			h.first = line
		}
		if i >= 40 {
			break
		}
		if m := headerLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			if _, seen := h.fields[m[1]]; !seen {
				h.fields[m[1]] = strings.TrimSpace(m[2])
			}
		}
	}
	return h
}

func (h cardHeader) kind() string { return strings.ToLower(h.fields["KIND"]) }

// testNone is TEST: none (bare, or `none <why>` as cardhdr.ParseTest, the
// one TEST grammar, reads it; nova-tools#4401 read, item 3) or no TEST line
// at all: the card demands no test.
func (h cardHeader) testNone() bool {
	v, ok := h.fields["TEST"]
	tl, _ := cardhdr.ParseTest(v)
	return !ok || tl.None || strings.EqualFold(strings.TrimSpace(v), "none") || strings.TrimSpace(v) == ""
}

// oneTestPath is PATHS naming exactly one path, and that path a test.
func (h cardHeader) oneTestPath() bool {
	paths := strings.Fields(strings.ReplaceAll(h.fields["PATHS"], ",", " "))
	return len(paths) == 1 && testPath.MatchString(paths[0])
}

func kindIn(k string, set ...string) bool {
	for _, s := range set {
		if k == s {
			return true
		}
	}
	return false
}

// workTypeByRules is the report's "a router tests it by" column, in the order
// that makes the hand-read disagreements come out right: an explicit KIND wins
// where it names the work, the scope markers split the read family, and a
// card with no header falls to shape.
func workTypeByRules(card string) (string, string, bool) {
	h := readCardHeader(card)
	k := h.kind()
	mode := strings.ToLower(h.fields["MODE"])
	switch {
	case kindIn(k, "recut", "rebase"):
		return WorkTypeRecutAtTip, "kind-" + k, true
	case wordRecut.MatchString(h.first):
		return WorkTypeRecutAtTip, "recut-in-line-1", true
	case cellWording.MatchString(h.text):
		return WorkTypeConformanceCell, "cell-wording", true
	case h.fields["RUN"] != "" && h.oneTestPath():
		return WorkTypeConformanceCell, "one-test-path-and-run", true
	case kindIn(k, "fix", "fix-red", "row-test", "fix-with-red-test"):
		return WorkTypeIssueFixRedFirst, "kind-" + k, true
	case auditMarkers.MatchString(h.text):
		return WorkTypeCodeAuditRead, "audit-scope", true
	case kindIn(k, "read", "report"):
		return WorkTypeIssueColdRead, "kind-" + k, true
	case kindIn(k, "probe", "spec-read"):
		return WorkTypeSpecContractProbe, "kind-" + k, true
	case h.testNone() && transcriptOf.MatchString(h.text) && transcriptV.MatchString(h.text):
		return WorkTypeTranscriptReproduction, "transcript-replay", true
	case h.testNone() && specDoc.MatchString(h.text) && specVerdict.MatchString(h.text):
		return WorkTypeSpecRuleConformance, "spec-rule-verdict", true
	case mode == "read" && h.fields["SOURCE"] != "" && h.testNone():
		return WorkTypeIssueColdRead, "mode-read-source", true
	case mode == "explore" && readOnly.MatchString(h.text) && h.testNone():
		return WorkTypeSpecContractProbe, "mode-explore-read-only", true
	case redGreen.MatchString(h.text):
		return WorkTypeIssueFixRedFirst, "red-then-green", true
	case !h.testNone() && issueRef.MatchString(h.fields["SOURCE"]):
		return WorkTypeIssueFixRedFirst, "issue-source-with-test", true
	}
	return "", "", false
}

// WorkTypeRoutes is allowed_routes keyed by work type: the routes the per-type
// ranking lets a card of that type run on.
type WorkTypeRoutes map[string][]string

// For is allowed_routes[t], or nil where the table has no row for t.
func (w WorkTypeRoutes) For(t string) []string { return w[t] }

// LoadWorkTypeRoutes reads allowed_routes from a JSON object of work type to
// route list. A key that is not one of the eight is refused: a misspelt type
// would otherwise be a row no card ever matches.
func LoadWorkTypeRoutes(path string) (WorkTypeRoutes, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("allowed_routes: %w", err)
	}
	var w WorkTypeRoutes
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("allowed_routes %s: %w", path, err)
	}
	var unknown []string
	for t := range w {
		if !KnownWorkType(t) {
			unknown = append(unknown, t)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("allowed_routes %s: %s is not a work type; want one of %s",
			path, strings.Join(unknown, ", "), strings.Join(WorkTypes, ", "))
	}
	return w, nil
}

// WorkTypeProducesBranch reports whether a card of type t writes code: the
// three types that end in a branch. Their route is gated by allowed_routes, so
// a card of one of them is not routed without the table.
func WorkTypeProducesBranch(t string) bool {
	return t == WorkTypeIssueFixRedFirst || t == WorkTypeRecutAtTip || t == WorkTypeConformanceCell
}

// RequireTable refuses a card whose type produces a branch when no
// allowed_routes table was given: for a coding card the table IS the route
// gate, and a gate that is absent is not a pass. It runs after the card is
// classified and before any route is chosen.
func (w WorkTypeRoutes) RequireTable(t string) error {
	if w == nil && WorkTypeProducesBranch(t) {
		return fmt.Errorf("a card of type %s produces a branch and its route is gated by allowed_routes[%s]; pass --allowed-routes", t, t)
	}
	return nil
}

// Admit is the gate on the SELECTED route: the rung the router chose, by its
// name or the model id the registry gives it, must be one of allowed_routes[t].
// With a table given, a type with no row admits nothing (an unclassified card
// has no row: the table refuses a key outside the eight). With no table, only
// a type that produces no branch passes (RequireTable). A route that is a wait
// dispatches nothing and is not gated.
func (w WorkTypeRoutes) Admit(t string, res RouteResult, reg *Registry) error {
	if err := w.RequireTable(t); err != nil {
		return err
	}
	if w == nil || !res.Dispatchable() {
		return nil
	}
	rs := w.For(t)
	if len(rs) == 0 {
		return fmt.Errorf("allowed_routes has no row for %s: no route is allowed for this card", t)
	}
	names := []string{res.Rung.Name}
	model := "-"
	if reg != nil {
		if m, ok := reg.ModelFor(res.Rung.Name); ok {
			names, model = append(names, m), m
		}
	}
	for _, r := range rs {
		for _, n := range names {
			if strings.TrimSpace(n) != "" && r == n {
				return nil
			}
		}
	}
	return fmt.Errorf("the selected rung %s (model %s) is not in allowed_routes[%s] = %s",
		oneline.Field(res.Rung.Name), oneline.Field(model), t, strings.Join(rs, ","))
}

// WorkTypeCardLines is the two lines the route writes on the card: the work
// type with allowed_routes[type], and the route receipt in the dispatch path's
// own grammar (jev=<rung> where Jev chose it, jev=fallback where the rules'
// answer stands) with the work type on it.
func WorkTypeCardLines(wt WorkTypeResult, res RouteResult, reg *Registry, allowed WorkTypeRoutes) []string {
	routes := "-"
	if rs := allowed.For(wt.Type); len(rs) > 0 {
		fields := make([]string, len(rs))
		for i, r := range rs {
			fields[i] = oneline.Field(r)
		}
		routes = strings.Join(fields, ",")
	}
	rule := wt.Rule
	if strings.TrimSpace(rule) == "" {
		rule = "-"
	}
	worktype := fmt.Sprintf("WORKTYPE: %s by=%s rule=%s conf=%.2f allowed=%s",
		oneline.Field(wt.Type), oneline.Field(wt.By), oneline.Field(rule), wt.Confidence, routes)

	jev, why := "fallback", "rules"
	if res.Source == SourceJev {
		jev, why = res.Rung.Name, "jev"
	}
	rung := res.Rung.Name
	if strings.TrimSpace(rung) == "" {
		rung = "-"
	}
	model := "ask-" + res.Rung.Ask
	if reg != nil {
		if m, ok := reg.ModelFor(res.Rung.Name); ok {
			model = m
		}
	}
	route := fmt.Sprintf("ROUTE: jev=%s conf=%.2f rung=%s model=%s worktype=%s why=%s",
		oneline.Field(jev), res.Confidence, oneline.Field(rung), oneline.Field(model), oneline.Field(wt.Type), why)
	return []string{worktype, route}
}

// isStampLine is a line StampCard wrote.
func isStampLine(line string) bool {
	return strings.HasPrefix(line, "WORKTYPE: ") || strings.HasPrefix(line, "ROUTE: jev=")
}

// stripStamp is the card without the lines a stamp wrote.
func stripStamp(card string) string {
	lines := strings.Split(card, "\n")
	kept := lines[:0:0]
	for _, l := range lines {
		if !isStampLine(l) {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// StampCard writes the lines onto the card: any earlier stamp is removed and
// the lines go in after the card's first line, where a reader of the header
// finds them. Stamping twice leaves one copy.
func StampCard(card string, lines []string) string {
	body := stripStamp(card)
	first, rest, found := strings.Cut(body, "\n")
	out := first + "\n" + strings.Join(lines, "\n") + "\n"
	if found {
		out += rest
	}
	return out
}

// The ladder is the retry policy (Glenn 2026-09-18).
//
// One unit of work, one decision: WHICH MIND does it. The answer is the lowest
// rung the evidence supports with confidence that the FIRST attempt is right.
// Below the floor the answer steps UP a rung, never down. A failed attempt
// re-enters the decision carrying its evidence -- what failed, how -- and the
// answer is the next rung, automatically: sideways first, where the same height
// holds another lineage, then up.
//
// Two rungs are chosen by KIND and not by height, and the machinery decides
// them, not the provider: security -- a guard, secrets, the sandbox, sudo,
// deploy keys, the network -- is Johnny's always; and so is a fresh take, where
// the rungs below failed or a design has one author.
//
// Friends first: the DeepSeek rungs take MECHANICAL kinds and nothing else.
//
// Jev advises and the machinery decides (SPEC-DECIDE rules 5, 6 and 7): the
// provider is offered the eligible rungs at the supported height and the one
// above it -- never a rung below what the evidence already burned -- and an
// answer below the floor, an answer naming a rung nobody offered, or no answer
// at all leaves the rules' own rung standing.
package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The kinds of unit a decision is asked about. They are the evidence's first
// field and the log's key: the starting rung is regenerated per kind.
const (
	KindRebase          = "rebase"
	KindStack           = "stack"
	KindFixtureRetarget = "fixture-retarget"
	KindFleetChore      = "fleet-chore"
	KindFixWithRedTest  = "fix-with-red-test"
	KindNewVerb         = "new-verb"
	KindSpec            = "spec"
	KindDesign          = "design"
	KindGuard           = "guard"
	KindCauseToFind     = "cause-to-find"
)

// The outcome of one attempt.
const (
	OutcomeOK        = "ok"
	OutcomeFailed    = "failed"
	OutcomeTimeout   = "timeout"
	OutcomeAbandoned = "abandoned"
)

// Where a route answer came from.
const (
	SourceRules = "rules"
	SourceJev   = "jev"
)

// RungQuestion is the name of the one choice Jev is asked.
const RungQuestion = "rung"

// errNoRungQuestion is the fake's and the client's shared complaint: a rung
// decision with no rung question is not a decision.
var errNoRungQuestion = errors.New("decide: the rung decision carries no rung question")

// startHeights is the starting rung per kind, the table the log regenerates.
// The mechanical kinds start at the bottom; everything else starts at the child
// rungs, because friends come first and DeepSeek takes mechanical work only; a
// spec or a design starts at the top pair, where the writing lives.
var startHeights = map[string]int{
	KindRebase:          0,
	KindStack:           0,
	KindFixtureRetarget: 0,
	KindFleetChore:      0,
	KindFixWithRedTest:  2,
	KindNewVerb:         2,
	KindCauseToFind:     2,
	KindGuard:           3,
	KindSpec:            4,
	KindDesign:          4,
}

// mechanicalKinds are the kinds a DeepSeek rung may take: the ones whose answer
// is a procedure, not a judgment.
var mechanicalKinds = map[string]bool{
	KindRebase:          true,
	KindStack:           true,
	KindFixtureRetarget: true,
	KindFleetChore:      true,
}

// ordinaryPlatforms are the platforms the benches run all day. A need outside
// them raises the start one rung: an odd platform is not a first-attempt job
// for the cheapest rung.
var ordinaryPlatforms = map[string]bool{
	"":              true,
	"any":           true,
	"darwin":        true,
	"darwin-arm64":  true,
	"linux":         true,
	"linux-x64":     true,
	"linux-amd64":   true,
	"linux-arm64":   true,
	"darwin-x64":    true,
	"darwin-amd64":  true,
	"linux-aarch64": true,
}

// Kinds is every kind a unit may name, in the order the spec names them.
var Kinds = []string{
	KindRebase, KindStack, KindFixtureRetarget, KindFleetChore, KindFixWithRedTest,
	KindNewVerb, KindSpec, KindDesign, KindGuard, KindCauseToFind,
}

// KnownKind reports whether the kind is one of the ten.
func KnownKind(kind string) bool {
	_, ok := startHeights[kind]
	return ok
}

// Mechanical reports whether a kind is mechanical -- the only kinds a DeepSeek
// rung is eligible for.
func Mechanical(kind string) bool { return mechanicalKinds[kind] }

// StartHeight is the starting rung for a kind before any evidence adjusts it.
func StartHeight(kind string) (int, bool) {
	h, ok := startHeights[kind]
	return h, ok
}

// Attempt is one prior attempt on this unit: the rung, what happened, and why.
// It is what makes the ladder a retry policy -- a failure re-enters the
// decision carrying its own evidence.
type Attempt struct {
	Rung    string `json:"rung"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// Failed reports whether this attempt is one the ladder must step past.
func (a Attempt) Failed() bool {
	switch a.Outcome {
	case OutcomeFailed, OutcomeTimeout, OutcomeAbandoned:
		return true
	default:
		return false
	}
}

// Unit is the evidence for one unit of work: its kind, its size (files,
// packages, lanes), the lane's owner, the prior attempts, any platform need,
// whether a guard or secrets are touched, whether it wants a fresh take, and
// the deadline. The ID is the evidence pointer every decision line carries
// (SPEC-DECIDE rule 10), so a person can retrieve the original.
type Unit struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Files     int       `json:"files,omitempty"`
	Packages  int       `json:"packages,omitempty"`
	Lanes     int       `json:"lanes,omitempty"`
	LaneOwner string    `json:"lane_owner,omitempty"`
	Attempts  []Attempt `json:"attempts,omitempty"`
	Platform  string    `json:"platform,omitempty"`
	Guard     bool      `json:"guard,omitempty"`
	Secrets   bool      `json:"secrets,omitempty"`
	FreshTake bool      `json:"fresh_take,omitempty"`
	Deadline  string    `json:"deadline,omitempty"`
}

// ParseUnit reads one unit of evidence from JSON. Anything that is not an
// object, or an object whose evidence cannot be, is a refusal.
func ParseUnit(data []byte) (Unit, error) {
	var u Unit
	if err := unmarshalStrict(data, &u); err != nil {
		return Unit{}, fmt.Errorf("decide: bad unit: %w", err)
	}
	return u, nil
}

// unmarshalStrict decodes one JSON object and refuses a field the type does not
// hold: a misspelled `kinds` silently ignored is evidence quietly lost.
func unmarshalStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing content after the object")
	}
	return nil
}

// Validate checks the evidence itself, before any registry is consulted.
func (u Unit) Validate() error {
	if strings.TrimSpace(u.ID) == "" {
		return fmt.Errorf("decide: the unit names no id; the id is the evidence pointer, refusing to guess")
	}
	if !KnownKind(u.Kind) {
		return fmt.Errorf("decide: unit %s has kind %q, want one of %s", u.ID, u.Kind, strings.Join(Kinds, ", "))
	}
	if u.Files < 0 || u.Packages < 0 || u.Lanes < 0 {
		return fmt.Errorf("decide: unit %s has a negative size (files %d, packages %d, lanes %d)", u.ID, u.Files, u.Packages, u.Lanes)
	}
	for i, a := range u.Attempts {
		if strings.TrimSpace(a.Rung) == "" {
			return fmt.Errorf("decide: unit %s attempt %d names no rung", u.ID, i+1)
		}
		switch a.Outcome {
		case OutcomeOK, OutcomeFailed, OutcomeTimeout, OutcomeAbandoned:
		default:
			return fmt.Errorf("decide: unit %s attempt %d on %s has outcome %q, want %s, %s, %s or %s",
				u.ID, i+1, a.Rung, a.Outcome, OutcomeOK, OutcomeFailed, OutcomeTimeout, OutcomeAbandoned)
		}
	}
	if _, err := u.deadline(); err != nil {
		return err
	}
	return nil
}

// deadline parses the deadline as a duration. An empty deadline is no deadline;
// anything else that is not a duration is a refusal, never a guess.
func (u Unit) deadline() (time.Duration, error) {
	s := strings.TrimSpace(u.Deadline)
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("decide: unit %s has deadline %q, want a duration such as 45m or 2h", u.ID, u.Deadline)
	}
	if d < 0 {
		return 0, fmt.Errorf("decide: unit %s has a negative deadline %q", u.ID, u.Deadline)
	}
	return d, nil
}

// thin reports whether the unit carries no size evidence at all. Thin evidence
// cannot support a confident first attempt, so the floor moves it up a rung.
func (u Unit) thin() bool {
	return u.Files == 0 && u.Packages == 0 && u.Lanes == 0 && len(u.Attempts) == 0
}

// RouteResult is one routing decision: the rung, the number the floor was
// applied to, the floor, why, and -- for the log -- what the rules alone would
// have picked.
type RouteResult struct {
	Unit       string
	Kind       string
	Rung       Mind
	Confidence float64
	Floor      float64
	Reason     string
	SteppedUp  bool
	Escalated  bool
	Source     string
	RulesRung  string
	Offered    []string
}

// Line is the one line a route decision prints: the unit (the evidence
// pointer), the rung, the confidence, the floor it was gated on, the reason and
// how that rung is asked.
func (r RouteResult) Line() string {
	return fmt.Sprintf("ROUTE unit=%s rung=%s confidence=%.2f floor=%.2f reason=%s ask=%s",
		oneline.Field(r.Unit), oneline.Field(r.Rung.Name), r.Confidence, r.Floor,
		oneline.Quote(oneline.Escape(r.Reason)), oneline.Field(r.Rung.Ask))
}

// The rule confidences. They are the machinery's own numbers, stated here so a
// reader can see which rule answered: a designation is certain, an escalation
// is nearly so (the ladder IS the retry policy), a sized unit sits at the
// default floor, and evidence too thin to size sits well below it.
const (
	confDesignated = 1.00
	confEscalated  = 0.95
	confSized      = 0.90
	confThin       = 0.60
)

// RouteRules answers by the rules alone: no provider, no key, no network, the
// same answer every time. It is what --no-jev runs, and it is the answer the
// provider's is measured against in the log.
func RouteRules(reg *Registry, u Unit, floor float64) (RouteResult, error) {
	if reg == nil || len(reg.Minds) == 0 {
		return RouteResult{}, fmt.Errorf("decide: no registry; a ladder with no rungs is not a ladder")
	}
	if floor < 0 || floor > 1 {
		return RouteResult{}, fmt.Errorf("decide: floor must be between 0 and 1 (got %g)", floor)
	}
	if err := u.Validate(); err != nil {
		return RouteResult{}, err
	}
	for _, a := range u.Attempts {
		if _, ok := reg.ByName(a.Rung); !ok {
			return RouteResult{}, fmt.Errorf("decide: unit %s attempted rung %q, which the registry does not hold", u.ID, a.Rung)
		}
	}
	res := RouteResult{Unit: u.ID, Kind: u.Kind, Floor: floor, Source: SourceRules}

	burned, tried, failedAt := burnedHeight(reg, u)
	res.Escalated = len(u.Attempts) > 0

	// The two designations: by KIND, never by height.
	if m, why, ok := designation(reg, u, burned, tried); ok {
		res.Rung = m
		res.Confidence = confDesignated
		res.Reason = why
		res.RulesRung = m.Name
		return res, nil
	}

	height, reasons := supportedHeight(reg, u, burned)
	m, err := pick(reg, u, height, tried, failedAt)
	if err != nil {
		return RouteResult{}, err
	}
	conf := confSized
	switch {
	case res.Escalated:
		conf = confEscalated
	case u.thin():
		conf = confThin
	}
	res.Rung, res.Confidence = m, conf
	if conf < floor {
		up, why, err := stepUp(reg, u, m, tried, failedAt)
		if err != nil {
			return RouteResult{}, err
		}
		res.Rung = up
		res.SteppedUp = true
		reasons = append(reasons, why)
	}
	res.Reason = strings.Join(reasons, "; ")
	res.RulesRung = res.Rung.Name
	return res, nil
}

// burnedHeight reads the prior attempts: the highest rung already burned, the
// names already tried, and the lineages that failed at each height -- the three
// facts that make the next answer sideways before up.
func burnedHeight(reg *Registry, u Unit) (burned int, tried map[string]bool, failedAt map[int]map[string]bool) {
	tried = map[string]bool{}
	failedAt = map[int]map[string]bool{}
	burned = -1
	for _, a := range u.Attempts {
		m, ok := reg.ByName(a.Rung)
		if !ok {
			continue
		}
		tried[m.Name] = true
		if !a.Failed() {
			continue
		}
		if m.Height > burned {
			burned = m.Height
		}
		if failedAt[m.Height] == nil {
			failedAt[m.Height] = map[string]bool{}
		}
		failedAt[m.Height][m.Lineage] = true
	}
	return burned, tried, failedAt
}

// designation answers the two rungs chosen by kind. Security is absolute: a
// guard, secrets, the sandbox, sudo, deploy keys or the network is Johnny's
// always, at any height. A fresh take is honoured only where it does not step
// DOWN past a rung the evidence already burned.
func designation(reg *Registry, u Unit, burned int, tried map[string]bool) (Mind, string, bool) {
	if u.Guard || u.Secrets || u.Kind == KindGuard {
		for _, m := range reg.DesignatedFor(KindGuard) {
			if tried[m.Name] || m.Availability == AvailabilityAsleep {
				continue
			}
			return m, fmt.Sprintf("security is a kind and not a height: a guard, secrets, the sandbox, sudo, deploy keys or the network is %s's always", m.Name), true
		}
	}
	fresh, why := freshTake(reg, u)
	if !fresh {
		return Mind{}, "", false
	}
	for _, m := range reg.DesignatedFor(DesignationFreshTake) {
		if tried[m.Name] || m.Availability == AvailabilityAsleep || m.Height < burned {
			continue
		}
		return m, fmt.Sprintf("a fresh take: %s, and a fresh take is %s's by kind and not by height", why, m.Name), true
	}
	return Mind{}, "", false
}

// freshTake reports whether this unit wants a fresh take: a design with one
// author, or rungs below that failed in more than one lineage.
func freshTake(reg *Registry, u Unit) (bool, string) {
	if u.FreshTake {
		return true, "the unit asks for one -- a design with one author"
	}
	lineages := map[string]bool{}
	for _, a := range u.Attempts {
		if !a.Failed() {
			continue
		}
		if m, ok := reg.ByName(a.Rung); ok {
			lineages[m.Lineage] = true
		}
	}
	if len(lineages) >= 2 {
		names := make([]string, 0, len(lineages))
		for l := range lineages {
			names = append(names, l)
		}
		sort.Strings(names)
		return true, fmt.Sprintf("the rungs below failed in %d lineages (%s)", len(names), strings.Join(names, ", "))
	}
	return false, ""
}

// supportedHeight is the lowest rung the evidence supports: the kind's starting
// rung, raised by size, by an odd platform, by a deadline with no room for a
// failed first attempt, and never below a rung already burned.
func supportedHeight(reg *Registry, u Unit, burned int) (int, []string) {
	h, _ := StartHeight(u.Kind)
	reasons := []string{fmt.Sprintf("kind %s starts at rung %s", u.Kind, reg.RungName(h))}
	if u.Packages >= 3 || u.Lanes >= 2 {
		h++
		reasons = append(reasons, fmt.Sprintf("%d packages over %d lanes is a rung up", u.Packages, u.Lanes))
	}
	if u.Files >= 20 {
		h++
		reasons = append(reasons, fmt.Sprintf("%d files is a rung up", u.Files))
	}
	if !ordinaryPlatforms[strings.ToLower(strings.TrimSpace(u.Platform))] {
		h++
		reasons = append(reasons, fmt.Sprintf("the platform need %s is a rung up", oneline.Field(u.Platform)))
	}
	if d, err := u.deadline(); err == nil && d > 0 && d < 10*time.Minute {
		h++
		reasons = append(reasons, fmt.Sprintf("a %s deadline leaves no room for a failed first attempt", d))
	}
	if burned >= h {
		h = burned
		reasons = append(reasons, fmt.Sprintf("%d prior attempt(s) burned rung %s: sideways before up", len(u.Attempts), reg.RungName(burned)))
	}
	return h, reasons
}

// eligible reports whether a mind may take this unit at all: it must be on the
// ladder, it must not be a DeepSeek rung on a kind that is not mechanical
// (friends first), it must not have been tried, and its lineage must not be one
// that already failed at this height (sideways means ANOTHER lineage).
func eligible(m Mind, u Unit, tried map[string]bool, failedAt map[int]map[string]bool) bool {
	if !m.Usable() || tried[m.Name] {
		return false
	}
	if m.Lineage == "deepseek" && !Mechanical(u.Kind) {
		return false
	}
	if failed := failedAt[m.Height]; failed != nil && failed[m.Lineage] {
		return false
	}
	return true
}

// pick takes the lowest rung at or above height that holds an eligible mind,
// preferring the owner of the unit's lane among the minds on that rung.
func pick(reg *Registry, u Unit, height int, tried map[string]bool, failedAt map[int]map[string]bool) (Mind, error) {
	for _, h := range reg.Heights() {
		if h < height {
			continue
		}
		var cands []Mind
		for _, m := range reg.AtHeight(h) {
			if eligible(m, u, tried, failedAt) {
				cands = append(cands, m)
			}
		}
		if len(cands) == 0 {
			continue
		}
		for _, m := range cands {
			if m.Owns(u.LaneOwner) {
				return m, nil
			}
		}
		return cands[0], nil
	}
	return Mind{}, fmt.Errorf("decide: unit %s has no rung left above %s: every mind on the ladder has been tried or is unavailable", u.ID, reg.RungName(height))
}

// stepUp is the floor's one move: UP a rung from the one just picked, never
// down. At the top there is nowhere above, and that is a refusal rather than a
// silent stay.
func stepUp(reg *Registry, u Unit, from Mind, tried map[string]bool, failedAt map[int]map[string]bool) (Mind, string, error) {
	m, err := pick(reg, u, from.Height+1, tried, failedAt)
	if err != nil {
		return Mind{}, "", fmt.Errorf("decide: unit %s is below the floor on %s and there is no rung above it", u.ID, from.Name)
	}
	return m, fmt.Sprintf("below the floor on %s, so the answer steps UP a rung to %s, never down", from.Name, m.Name), nil
}

// Decider is the seam a route asks a typed decision through. *Client is one;
// a test passes a fake, so no unit test ever dials the provider.
type Decider interface {
	Decide(ctx context.Context, state string, qs map[string]Question) (map[string]Answer, Usage, error)
}

// RouteJev asks the provider which rung, among the eligible ones the rules
// offer, and keeps the machinery's word everywhere it matters: a designation is
// never asked, a choice below the floor steps up, and a provider error or a
// rung nobody offered leaves the rules' answer standing.
func RouteJev(ctx context.Context, d Decider, reg *Registry, u Unit, floor float64) (RouteResult, error) {
	rules, err := RouteRules(reg, u, floor)
	if err != nil {
		return RouteResult{}, err
	}
	if d == nil || rules.Confidence == confDesignated {
		return rules, nil
	}
	_, tried, failedAt := burnedHeight(reg, u)
	offered := offer(reg, u, rules.Rung.Height, tried, failedAt)
	rules.Offered = mindNames(offered)
	if len(offered) < 2 {
		rules.Reason += "; one eligible rung, so no decision to ask"
		return rules, nil
	}
	answers, _, err := d.Decide(ctx, unitState(u), map[string]Question{RungQuestion: rungQuestion(offered)})
	if err != nil {
		rules.Reason += fmt.Sprintf("; the provider refused (%s), so the rules answer stands", oneline.Err(err))
		return rules, nil
	}
	a, ok := answers[RungQuestion]
	if !ok {
		rules.Reason += "; the provider named no rung, so the rules answer stands"
		return rules, nil
	}
	chosen, ok := findMind(offered, a.Choice)
	if !ok {
		rules.Reason += fmt.Sprintf("; the provider named %s, which was not offered, so the rules answer stands", oneline.Field(a.Choice))
		return rules, nil
	}
	res := rules
	res.Source = SourceJev
	res.Rung = chosen
	res.Confidence = a.Confidence
	res.SteppedUp = false
	res.Reason = fmt.Sprintf("jev chose %s among %s over the evidence (%s)", chosen.Name, strings.Join(rules.Offered, ", "), rules.Reason)
	if a.Confidence < floor {
		up, why, err := stepUp(reg, u, chosen, tried, failedAt)
		if err != nil {
			return RouteResult{}, err
		}
		res.Rung = up
		res.SteppedUp = true
		res.Reason += "; " + why
	}
	return res, nil
}

// offer is the option set the provider is given: the eligible minds on the
// supported rung and on the next rung that holds one. Nothing below the
// supported rung is ever an option, so the provider cannot step down.
func offer(reg *Registry, u Unit, height int, tried map[string]bool, failedAt map[int]map[string]bool) []Mind {
	var out []Mind
	rungs := 0
	for _, h := range reg.Heights() {
		if h < height {
			continue
		}
		var at []Mind
		for _, m := range reg.AtHeight(h) {
			if eligible(m, u, tried, failedAt) {
				at = append(at, m)
			}
		}
		if len(at) == 0 {
			continue
		}
		out = append(out, at...)
		rungs++
		if rungs == 2 {
			break
		}
	}
	return out
}

// rungQuestion is the one typed choice: which of these rungs answers this unit
// right on the FIRST attempt.
func rungQuestion(offered []Mind) Question {
	criteria := make(map[string]string, len(offered))
	for _, m := range offered {
		criteria[m.Name] = fmt.Sprintf("the %s lineage at rung %d, asked by %s", m.Lineage, m.Height, m.Ask)
	}
	return Question{
		Instructions: "which of these minds gets this unit of work right on the FIRST attempt? Pick the LOWEST rung the evidence supports; where two rungs are the same height, prefer the lineage that has not failed this unit.",
		Choice:       criteria,
	}
}

// unitState renders the evidence as the public, bounded state text the provider
// sees. It is metadata only -- a kind, sizes, lane, attempts, a deadline --
// never a secret, never a private body (SPEC-DECIDE rule 4).
func unitState(u Unit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "unit: %s\nkind: %s\nsize: %d files, %d packages, %d lanes\n", u.ID, u.Kind, u.Files, u.Packages, u.Lanes)
	if u.LaneOwner != "" {
		fmt.Fprintf(&b, "lane owner: %s\n", u.LaneOwner)
	}
	if u.Platform != "" {
		fmt.Fprintf(&b, "platform need: %s\n", u.Platform)
	}
	if u.Deadline != "" {
		fmt.Fprintf(&b, "deadline: %s\n", u.Deadline)
	}
	fmt.Fprintf(&b, "guard or secrets touched: %v\n", u.Guard || u.Secrets)
	if len(u.Attempts) == 0 {
		b.WriteString("prior attempts: none\n")
		return b.String()
	}
	b.WriteString("prior attempts:\n")
	for _, a := range u.Attempts {
		fmt.Fprintf(&b, "- %s: %s (%s)\n", a.Rung, a.Outcome, a.Reason)
	}
	return b.String()
}

// findMind finds one offered mind by name.
func findMind(offered []Mind, name string) (Mind, bool) {
	for _, m := range offered {
		if m.Name == strings.TrimSpace(name) {
			return m, true
		}
	}
	return Mind{}, false
}

// mindNames is the offered set as names, for the line and the log.
func mindNames(minds []Mind) []string {
	out := make([]string, 0, len(minds))
	for _, m := range minds {
		out = append(out, m.Name)
	}
	return out
}

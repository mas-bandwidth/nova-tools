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
	"math"
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
	KindDogfood         = "dogfood"
	KindRowTest         = "row-test"
	KindFixWithRedTest  = "fix-with-red-test"
	KindNewVerb         = "new-verb"
	KindSpec            = "spec"
	KindDesign          = "design"
	KindGuard           = "guard"
	KindCauseToFind     = "cause-to-find"
	KindTranscriptTest  = "transcript-test"
)

// The outcome of one attempt.
const (
	OutcomeOK        = "ok"
	OutcomeFailed    = "failed"
	OutcomeTimeout   = "timeout"
	OutcomeAbandoned = "abandoned"
)

// OutcomeSkipped is the fourth ROUTE-OUTCOME word, and it is deliberately not
// one of the four above: an attempt has an outcome because it RAN, and a unit
// skipped for an unmet precondition was never asked to run. It is an outcome
// row all the same -- coverage counts it -- but it is no rung's success and no
// rung's failure, so it moves no floor in either direction (SPEC-DECIDE H2).
const OutcomeSkipped = "skipped"

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
// spec or a design starts at the top pair, where the writing lives. A row test
// is the exception that measurement bought: its shape is a proven card and its
// size is one file, so the cheapest rung looks right and is not -- it starts at
// pro (2026-09-18, the schema campaign's row cards).
var startHeights = map[string]int{
	KindRebase:          0,
	KindStack:           0,
	KindFixtureRetarget: 0,
	KindFleetChore:      0,
	KindDogfood:         0,
	KindTranscriptTest:  0,
	KindRowTest:         1,
	KindFixWithRedTest:  2,
	KindNewVerb:         2,
	KindCauseToFind:     2,
	KindGuard:           3,
	KindSpec:            4,
	KindDesign:          4,
}

// LineageDeepSeek is the lineage of the two card rungs. It is named here
// because two rules turn on it: friends first, and a mechanical kind that
// failed on one of them is not mechanical after all.
const LineageDeepSeek = "deepseek"

// mechanicalKinds are the kinds a DeepSeek rung may take: the ones whose answer
// is a procedure, not a judgment.
var mechanicalKinds = map[string]bool{
	KindRebase:          true,
	KindStack:           true,
	KindFixtureRetarget: true,
	KindFleetChore:      true,
	KindDogfood:         true,
	KindTranscriptTest:  true,
	KindRowTest:         true,
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
	KindRebase, KindStack, KindFixtureRetarget, KindFleetChore, KindDogfood, KindTranscriptTest, KindRowTest,
	KindFixWithRedTest, KindNewVerb, KindSpec, KindDesign, KindGuard, KindCauseToFind,
}

// kindAliases are the names a caller may use for a kind the table already
// holds. `chore` is the one the manager lanes actually typed on 2026-09-19 and
// it cost them `ROUTE REFUSED reason=no-rung ... want one of rebase, stack,
// fixture-retarget, fleet-chore, ...` (schema-issues HANDOFF D1). A chore of
// the fleet is a fleet-chore under a shorter name, with the same start height
// and the same mechanical eligibility; it is an alias, not a new kind, so it
// adds no row to Kinds and no rung to the ladder.
var kindAliases = map[string]string{
	"chore": KindFleetChore,
}

// CanonicalKind resolves an alias to the kind the table holds and returns
// anything else unchanged. It is applied ONCE, where the evidence is read, so
// the kind the ladder decides on and the kind the route log records are the
// same name -- an alias that reached the log would split every per-kind floor.
func CanonicalKind(kind string) string {
	if canon, ok := kindAliases[kind]; ok {
		return canon
	}
	return kind
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

// The six things that make a unit security work. They are an enumeration and
// not free text: a touch the table does not hold is a refusal.
const (
	TouchGuard      = "guard"
	TouchSecrets    = "secrets"
	TouchSandbox    = "sandbox"
	TouchSudo       = "sudo"
	TouchDeployKeys = "deploy-keys"
	TouchNetwork    = "network"
)

// Touches is every touch a unit may name.
var Touches = []string{TouchGuard, TouchSecrets, TouchSandbox, TouchSudo, TouchDeployKeys, TouchNetwork}

// knownTouches is the same set, for the lookup.
var knownTouches = map[string]bool{
	TouchGuard: true, TouchSecrets: true, TouchSandbox: true,
	TouchSudo: true, TouchDeployKeys: true, TouchNetwork: true,
}

// Attempt is one prior attempt on this unit: the rung, what happened, why, and
// -- for a timeout -- whether the attempt is known to have TERMINATED. It is
// what makes the ladder a retry policy: a confirmed failure re-enters the
// decision carrying its own evidence.
//
// Terminated is the lease rule (Stella): a timeout is a silence, not a death.
// Until something proves the attempt is dead, its expiry is UNKNOWN, and a rung
// whose attempt may still be running is not a rung to step off.
type Attempt struct {
	Rung       string `json:"rung"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason,omitempty"`
	Terminated bool   `json:"terminated,omitempty"`
}

// Failed reports whether this attempt is a CONFIRMED failure -- one the ladder
// may step past. A timeout counts only once termination is proved: an attempt
// that may still be running has not failed, it has not finished.
func (a Attempt) Failed() bool {
	switch a.Outcome {
	case OutcomeFailed, OutcomeAbandoned:
		return true
	case OutcomeTimeout:
		return a.Terminated
	default:
		return false
	}
}

// Open reports whether this attempt timed out with no proof that it died. The
// answer for an open attempt is the same rung, not the next one.
func (a Attempt) Open() bool { return a.Outcome == OutcomeTimeout && !a.Terminated }

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
	Touches   []string  `json:"touches,omitempty"`
	FreshTake bool      `json:"fresh_take,omitempty"`
	Deadline  string    `json:"deadline,omitempty"`
}

// Security reports whether this unit is security work: a guard, secrets, the
// sandbox, sudo, deploy keys or the network. It is a KIND and not a height, and
// it is answered here rather than by any provider.
func (u Unit) Security() bool {
	return u.Guard || u.Secrets || u.Kind == KindGuard || len(u.Touches) > 0
}

// ParseUnit reads one unit of evidence from JSON. Anything that is not an
// object, or an object whose evidence cannot be, is a refusal.
func ParseUnit(data []byte) (Unit, error) {
	var u Unit
	if err := unmarshalStrict(data, &u); err != nil {
		return Unit{}, fmt.Errorf("decide: bad unit: %w", err)
	}
	// An alias is resolved here, once, before any validation or logging.
	u.Kind = CanonicalKind(u.Kind)
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
	for _, touch := range u.Touches {
		if !knownTouches[touch] {
			return fmt.Errorf("decide: unit %s touches %q, want one of %s", u.ID, touch, strings.Join(Touches, ", "))
		}
	}
	if _, err := u.deadline(); err != nil {
		return err
	}
	return nil
}

// ValidFloor refuses a floor that is not a number between 0 and 1, with the one
// remedy in the message. NaN compares false against every bound, so a bare
// `floor < 0 || floor > 1` lets it through -- which is exactly how it got in.
func ValidFloor(floor float64) error {
	if math.IsNaN(floor) || math.IsInf(floor, 0) || floor < 0 || floor > 1 {
		return fmt.Errorf("decide: floor %v is not a confidence; it wants a number between 0 and 1, such as 0.9", floor)
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
// The typed actions a route answers with beside the rung. A wait is NOT
// permission to retry: the rung named is the one an attempt may still be
// running on, and the caller's next move is to establish what happened to it.
const (
	// WaitNone is the absence, rendered as a dash so every line carries the
	// field and no reader has to infer it.
	WaitNone = "-"
	// WaitAwaitingTermination is the lease rule: an attempt timed out, its
	// expiry is UNKNOWN, and nothing is dispatched until it is known dead.
	WaitAwaitingTermination = "awaiting_termination"
)

// RouteUsage is what one routing decision spent with the provider: how many
// calls it made, the counters the provider reported, and -- PER COUNTER --
// whether it reported them at all. A call that failed, and a call that answered
// without saying what it cost, both spent something we cannot measure, and
// UNKNOWN is not zero (SPEC-TOKENS rule 14).
type RouteUsage struct {
	Calls        int
	InputTokens  int
	OutputTokens int
	HasInput     bool
	HasOutput    bool
	Failed       bool
}

// Known reports whether any counter was measured.
func (u RouteUsage) Known() bool { return u.HasInput || u.HasOutput }

// RouteResult is one routing decision: the rung, the number the floor was
// applied to, the floor, why, the typed wait, what it spent, and -- for the
// log -- what the rules alone would have picked.
//
// A REFUSED decision is still a RouteResult. Where the route ends in an error,
// the result comes back populated with everything that was already true --
// above all what a completed provider call spent -- and Refusal says why it
// ended. A refusal cannot unspend tokens, so the caller persists the row and
// then exits on the refusal (Stella, #1327).
type RouteResult struct {
	Unit       string
	Kind       string
	Rung       Mind
	Confidence float64
	Floor      float64
	Reason     string
	Wait       string
	Refusal    string
	SteppedUp  bool
	Escalated  bool
	Designated bool
	Source     string
	RulesRung  string
	Offered    []string
	Usage      RouteUsage
	// Next is the rung ABOVE the one answered, named whenever the answer is
	// below the floor. The step-up signal used to name only the question that
	// fell short, which left a reader to work out where the work goes next from
	// a ladder they cannot see (edge 24). It is empty at or above the floor,
	// and empty where there is no rung above.
	Next string
	// Steps is how many decisions this answer took: 1 for an ordinary route,
	// and one more for each re-ask --step-up made.
	Steps int
}

// refuse populates the result with the refusal that ended it and returns both.
// Every early return in a route goes through here, so a refused decision is
// never an empty one.
func (r RouteResult) refuse(err error) (RouteResult, error) {
	r.Refusal = err.Error()
	if r.Wait == "" {
		r.Wait = WaitNone
	}
	return r, err
}

// AwaitingTermination reports whether this decision is a wait on an attempt
// that is not known to have terminated.
func (r RouteResult) AwaitingTermination() bool { return r.Wait == WaitAwaitingTermination }

// Dispatchable reports whether the caller may hand the unit to the rung named.
// A wait is an answer about WHO owns the work, not permission to start it.
func (r RouteResult) Dispatchable() bool { return r.Wait == WaitNone || r.Wait == "" }

// Line is the one line a route decision prints: the unit (the evidence
// pointer), the rung, the confidence, the floor it was gated on, the rung ABOVE
// it where the answer fell below that floor, how many steps the answer took,
// the reason and how that rung is asked. Every field is on every line: next is
// the dash where there is nothing above and nothing to step to, so no reader
// has to infer an absence.
func (r RouteResult) Line() string {
	wait := r.Wait
	if wait == "" {
		wait = WaitNone
	}
	next := "-"
	if strings.TrimSpace(r.Next) != "" {
		next = oneline.Field(r.Next)
	}
	steps := r.Steps
	if steps < 1 {
		steps = 1
	}
	return fmt.Sprintf("ROUTE unit=%s rung=%s confidence=%.2f floor=%.2f wait=%s next=%s steps=%d reason=%s ask=%s",
		oneline.Field(r.Unit), oneline.Field(r.Rung.Name), r.Confidence, r.Floor,
		oneline.Field(wait), next, steps, oneline.Quote(oneline.Escape(r.Reason)), oneline.Field(r.Rung.Ask))
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
	return routeRules(reg, u, floor, nil)
}

// routeRules is RouteRules with an exclusion set: the rungs a step-up has
// already answered below the floor, which are off the ladder for this ask the
// way a tried rung is. Nothing else about the rules changes, so an ordinary
// route (an empty set) is the same answer it always was.
func routeRules(reg *Registry, u Unit, floor float64, excluded map[string]bool) (RouteResult, error) {
	res := RouteResult{Unit: u.ID, Kind: u.Kind, Floor: floor, Source: SourceRules, Wait: WaitNone, Steps: 1}
	if reg == nil || len(reg.Minds) == 0 {
		return res.refuse(fmt.Errorf("decide: no registry; a ladder with no rungs is not a ladder"))
	}
	if err := ValidFloor(floor); err != nil {
		return res.refuse(err)
	}
	if err := u.Validate(); err != nil {
		return res.refuse(err)
	}
	for _, a := range u.Attempts {
		if _, ok := reg.ByName(a.Rung); !ok {
			return res.refuse(fmt.Errorf("decide: unit %s attempted rung %q, which the registry does not hold", u.ID, a.Rung))
		}
	}

	burned, tried, failedAt := burnedHeight(reg, u)
	for name := range excluded {
		tried[name] = true
	}
	res.Escalated = len(u.Attempts) > 0
	open, openRung, hasOpen := openAttempt(reg, u)

	// Security first, and absolutely. It is a KIND and not a height, so the
	// height rules -- sideways, up, the floor, never down -- do not apply to it
	// at all: the designated rung answers on every path, and where that rung
	// cannot, the work WAITS for it rather than spilling onto another mind.
	//
	// The owner and the wait are two different facts, and neither hides the
	// other: where an attempt on this unit is still open, the answer names the
	// designated owner AND waits.
	if u.Security() {
		m, err := securityRung(reg, u)
		if err != nil {
			return res.refuse(err)
		}
		res.Rung = m
		res.Confidence = confDesignated
		res.Designated = true
		res.Reason = fmt.Sprintf("security is a kind and not a height: %s is %s's always, at any height, at any floor and after any attempt", u.securityWhy(), m.Name)
		res.RulesRung = m.Name
		if hasOpen {
			res.Wait = WaitAwaitingTermination
			res.Reason += "; " + waitReason(open, openRung)
		}
		return res, nil
	}

	// An attempt that timed out with no proof it died leaves its rung occupied.
	// The lease rule: expiry stays UNKNOWN until termination, so the answer is
	// the SAME rung -- not the next one -- until the attempt is known dead.
	if hasOpen {
		res.Rung = openRung
		res.Confidence = confDesignated
		res.Wait = WaitAwaitingTermination
		res.Reason = waitReason(open, openRung)
		res.RulesRung = openRung.Name
		return res, nil
	}

	// The fresh-take designation: by KIND, never by height.
	if m, why, ok := designation(reg, u, burned, tried); ok {
		res.Rung = m
		res.Confidence = confDesignated
		res.Designated = true
		res.Reason = why
		res.RulesRung = m.Name
		return res, nil
	}

	height, reasons := supportedHeight(reg, u, burned)
	m, err := pick(reg, u, height, tried, failedAt)
	if err != nil {
		res.Reason = strings.Join(reasons, "; ")
		return res.refuse(err)
	}
	conf := confSized
	switch {
	case res.Escalated:
		conf = confEscalated
	case u.thin():
		conf = confThin
		// Say it on the line. Thin evidence is below every usable floor, so it
		// steps the answer up BEFORE the provider is offered anything -- and
		// the rung the evidence would have supported is then not in the offer
		// set at all, so no provider answer can recover it. A caller who simply
		// forgot --files reads "below the floor" and looks for a floor problem;
		// what they have is a unit with no size on it (2026-09-18: a manager's
		// fix-with-red-test units answered astra for exactly this reason).
		reasons = append(reasons, "the unit carries no size evidence (no files, packages, lanes or attempts), which no floor can support for a first attempt")
	}
	res.Rung, res.Confidence = m, conf
	if conf < floor {
		up, why, err := stepUp(reg, u, m, tried, failedAt)
		if err != nil {
			res.Reason = strings.Join(reasons, "; ")
			return res.refuse(err)
		}
		res.Rung = up
		res.SteppedUp = true
		reasons = append(reasons, why)
	}
	res.Reason = strings.Join(reasons, "; ")
	res.RulesRung = res.Rung.Name
	res.Next = nextRung(reg, u, res, tried, failedAt)
	return res, nil
}

// nextRung is the rung ABOVE the one answered: where the work goes if this
// answer does not get it right on the first attempt. It is named only where the
// answer is below the floor -- above it there is no step to signal -- and it is
// empty at the top of the ladder, where there is nothing above.
func nextRung(reg *Registry, u Unit, res RouteResult, tried map[string]bool, failedAt map[int]map[string]bool) string {
	if res.Confidence >= res.Floor || !res.Dispatchable() || res.Designated {
		return ""
	}
	up, err := pick(reg, u, res.Rung.Height+1, tried, failedAt)
	if err != nil {
		return ""
	}
	return up.Name
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

// securityRung is the rung security work goes to, and there is no other answer.
// The designated mind takes it whatever its height, whatever the floor, and
// however many attempts have already been made -- including its own. Where no
// mind is designated, or the designated one is asleep, the work WAITS: handing
// a guard, a secret or a deploy key to another mind because the right one is
// busy is the failure this rule exists to prevent.
func securityRung(reg *Registry, u Unit) (Mind, error) {
	designated := reg.DesignatedFor(KindGuard)
	if len(designated) == 0 {
		return Mind{}, fmt.Errorf("decide: unit %s is security work (%s) and no mind in the registry is designated for %s; refusing to route it to another rung",
			u.ID, u.securityWhy(), KindGuard)
	}
	for _, m := range designated {
		if m.Availability != AvailabilityAsleep {
			return m, nil
		}
	}
	return Mind{}, fmt.Errorf("decide: unit %s is security work (%s) and every mind designated for %s is asleep; it waits for one of them rather than going to another rung",
		u.ID, u.securityWhy(), KindGuard)
}

// securityWhy names, in enumerated words, what makes this unit security work.
func (u Unit) securityWhy() string {
	var why []string
	if u.Kind == KindGuard {
		why = append(why, "kind "+KindGuard)
	}
	if u.Guard {
		why = append(why, TouchGuard)
	}
	if u.Secrets {
		why = append(why, TouchSecrets)
	}
	why = append(why, u.Touches...)
	if len(why) == 0 {
		return "no touch named"
	}
	return strings.Join(why, ", ")
}

// waitReason says what is being waited on and, in as many words, that waiting
// is not permission to retry: the rung named owns the work, and the next move
// is to find out what happened to the attempt, not to start another one.
func waitReason(a Attempt, m Mind) string {
	return fmt.Sprintf("the attempt on %s timed out (%s) and is not known to have terminated: its expiry is UNKNOWN, so this is a WAIT on the same rung and NOT permission to retry -- establish termination first",
		m.Name, oneline.Field(a.Outcome))
}

// openAttempt is the most recent attempt that timed out with no proof it
// terminated, and the rung it is still occupying.
func openAttempt(reg *Registry, u Unit) (Attempt, Mind, bool) {
	for i := len(u.Attempts) - 1; i >= 0; i-- {
		a := u.Attempts[i]
		if !a.Open() {
			continue
		}
		if m, ok := reg.ByName(a.Rung); ok {
			return a, m, true
		}
	}
	return Attempt{}, Mind{}, false
}

// designation answers the rung chosen by kind that is left once security has
// been answered: a fresh take, honoured only where it does not step DOWN past a
// rung the evidence already burned.
func designation(reg *Registry, u Unit, burned int, tried map[string]bool) (Mind, string, bool) {
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
// (friends first), it must not be a DeepSeek rung on a unit a DeepSeek rung has
// already CONFIRMED-failed, it must not have been tried, and its lineage must
// not be one that already failed at this height (sideways means ANOTHER
// lineage).
//
// The second of those is the one the failures taught. A mechanical kind is one
// whose answer is a procedure rather than a judgement; an attempt that failed is
// the evidence that the procedure was not given after all, so the work is not
// mechanical and the other card rung is not a retry, it is the same mistake one
// height up. The per-height rule above cannot catch it, because flash and pro
// are the only two minds of one lineage sitting at two DIFFERENT heights: every
// other lineage puts its two minds far enough apart that this never arose.
func eligible(m Mind, u Unit, tried map[string]bool, failedAt map[int]map[string]bool) bool {
	if !m.Usable() || tried[m.Name] {
		return false
	}
	if m.Lineage == LineageDeepSeek && (!Mechanical(u.Kind) || lineageFailed(failedAt, LineageDeepSeek)) {
		return false
	}
	if failed := failedAt[m.Height]; failed != nil && failed[m.Lineage] {
		return false
	}
	return true
}

// lineageFailed reports whether any CONFIRMED failure on this unit was a mind
// of this lineage, at any height. burnedHeight has already keyed the failures by
// height and lineage, so this reads them rather than walking the attempts again.
func lineageFailed(failedAt map[int]map[string]bool, lineage string) bool {
	for _, at := range failedAt {
		if at[lineage] {
			return true
		}
	}
	return false
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
// offer, and keeps the machinery's word everywhere it matters: security and a
// designation are never asked, a rung that may still be running is never asked
// (there is no choice to make while an attempt is alive), a choice below the
// floor steps up, and a provider error or a rung nobody offered leaves the
// rules' answer standing.
func RouteJev(ctx context.Context, d Decider, reg *Registry, u Unit, floor float64) (RouteResult, error) {
	return routeJev(ctx, d, reg, u, floor, nil)
}

// DefaultMaxSteps is how many decisions --step-up makes before it stops. Three
// is the ladder's own shape: the rung the evidence supports, one sideways, one
// up. A step-up that has not landed by then is not a confidence problem.
const DefaultMaxSteps = 3

// RouteStepUp turns the step-up SIGNAL into the rung above it (edge 24).
//
// Exit 3 said "below the floor" and named the question that fell short, and
// there it stopped: nothing re-asked, and the caller was left to read a ladder
// it cannot see. This asks, and where the answer is below the floor it EXCLUDES
// that rung from the criteria and asks the same question again, up to maxSteps
// times.
//
// Every step is a decision in its own right: the slice returned holds them all,
// in order, each carrying its own reason and its own step number, and the
// caller logs every one of them. The last is the answer, and its exit code is
// the ordinary one -- a step-up that never got above the floor is still a
// suggestion, never an authorization.
func RouteStepUp(ctx context.Context, d Decider, reg *Registry, u Unit, floor float64, maxSteps int) ([]RouteResult, error) {
	if maxSteps < 1 {
		return nil, fmt.Errorf("decide: --max-steps %d asks nothing; it wants at least 1, such as --max-steps %d", maxSteps, DefaultMaxSteps)
	}
	excluded := map[string]bool{}
	var order []string
	steps := make([]RouteResult, 0, maxSteps)
	for i := 1; i <= maxSteps; i++ {
		res, err := routeJev(ctx, d, reg, u, floor, excluded)
		res.Steps = i
		if len(order) > 0 {
			res.Reason = fmt.Sprintf("step %d: %s answered below the floor and %s excluded from the criteria; %s",
				i, pluralRungs(order), strings.Join(order, ", "), res.Reason)
		}
		steps = append(steps, res)
		if err != nil {
			return steps, err
		}
		// Nothing steps past an answer the floor accepts, a wait (which is not
		// permission to move at all) or a designation (a KIND, not a height:
		// security does not step anywhere).
		if res.Confidence >= floor || !res.Dispatchable() || res.Designated {
			return steps, nil
		}
		// The same rung twice is the ladder saying there is nothing left to
		// exclude. Stopping here is the answer; asking again would only spend.
		if excluded[res.Rung.Name] || strings.TrimSpace(res.Rung.Name) == "" {
			return steps, nil
		}
		excluded[res.Rung.Name] = true
		order = append(order, res.Rung.Name)
	}
	return steps, nil
}

// pluralRungs keeps the step reason readable in both directions.
func pluralRungs(order []string) string {
	if len(order) == 1 {
		return "a rung"
	}
	return fmt.Sprintf("%d rungs", len(order))
}

// routeJev is RouteJev with an exclusion set; see routeRules.
func routeJev(ctx context.Context, d Decider, reg *Registry, u Unit, floor float64, excluded map[string]bool) (RouteResult, error) {
	rules, err := routeRules(reg, u, floor, excluded)
	if err != nil {
		// The rules refused before any call could be made: that result is
		// already populated, and it carries the refusal.
		return rules, err
	}
	if d == nil || rules.Designated || rules.AwaitingTermination() || u.Security() {
		return rules, nil
	}
	_, tried, failedAt := burnedHeight(reg, u)
	for name := range excluded {
		tried[name] = true
	}
	offered := offer(reg, u, rules.Rung.Height, tried, failedAt)
	rules.Offered = mindNames(offered)
	if len(offered) < 2 {
		rules.Reason += "; one eligible rung, so no decision to ask"
		return rules, nil
	}
	state, err := unitState(reg, u)
	if err != nil {
		// The boundary refused: nothing goes to the provider, and the rules
		// answer -- which needed no provider -- stands.
		rules.Reason += fmt.Sprintf("; the public projection refused (%s), so nothing was sent and the rules answer stands", oneline.Err(err))
		return rules, nil
	}
	answers, usage, err := d.Decide(ctx, state, map[string]Question{RungQuestion: rungQuestion(offered, u)})
	// A call was made, and what it spent is part of the record whether it
	// answered or not: a failed call's cost is UNKNOWN, never zero.
	rules.Usage = RouteUsage{Calls: 1, Failed: err != nil}
	if err == nil {
		// Presence travels per counter: a 200 that named no usage has told us
		// nothing about what it cost, and nothing is not zero.
		rules.Usage.InputTokens, rules.Usage.HasInput = usage.InputTokens, usage.HasInput
		rules.Usage.OutputTokens, rules.Usage.HasOutput = usage.OutputTokens, usage.HasOutput
	}
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
			// The call is already made and already paid for: the refusal comes
			// back carrying it, so the caller writes the row before it exits.
			return res.refuse(err)
		}
		res.Rung = up
		res.SteppedUp = true
		res.Reason += "; " + why
	}
	res.Next = nextRung(reg, u, res, tried, failedAt)
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

// optionID is the opaque name one offered rung is known by on the wire:
// position in the offered set and nothing else. A mind's name, its lineage and
// the lanes it owns never leave this process, so the provider chooses between
// "rung-1" and "rung-2" and the answer is mapped back here.
func optionID(i int) string { return fmt.Sprintf("rung-%d", i+1) }

// rungQuestion is the one typed choice: which of these rungs answers this unit
// right on the FIRST attempt. Every option is an opaque id, and every fact
// about it is one of our own enumerations -- the step above the lowest rung
// offered, a per-call lineage LABEL (so "same lineage" and "another lineage"
// survive without the lineage's name), whether that mind owns the unit's lane,
// and how it is asked (bus | card | child, the closed set the registry schema
// enforces).
func rungQuestion(offered []Mind, u Unit) Question {
	base := offered[0].Height
	labels := map[string]string{}
	criteria := make(map[string]string, len(offered))
	for i, m := range offered {
		label, ok := labels[m.Lineage]
		if !ok {
			label = fmt.Sprintf("lineage-%c", 'a'+len(labels))
			labels[m.Lineage] = label
		}
		criteria[optionID(i)] = fmt.Sprintf("step %d above the lowest rung offered, %s, owns this lane: %s, asked by %s",
			m.Height-base, label, yesNo(m.Owns(u.LaneOwner)), m.Ask)
	}
	return Question{
		Instructions: "which of these rungs gets this unit of work right on the FIRST attempt? Pick the LOWEST step the evidence supports; where two rungs are the same step, prefer a lineage that has not failed this unit.",
		Choice:       criteria,
	}
}

// The buckets the evidence is reduced to before any of it leaves this process.
// They are ENUMERATIONS: every value the provider ever sees is one of these
// tokens, so no title, path, branch name, error text or attempt reason can ride
// out on a state line (SPEC-DECIDE rule 4).
var (
	// SizeBuckets are the buckets a file, package or lane count falls into.
	SizeBuckets = []string{"none", "1-3", "4-9", "10-19", "20+"}
	// AttemptBuckets are the buckets a prior-attempt count falls into.
	AttemptBuckets = []string{"0", "1", "2", "3+"}
	// PlatformBuckets say whether a platform need is one of the benches we run
	// all day, or one outside them -- never which.
	PlatformBuckets = []string{"ordinary", "named"}
	// DeadlineBuckets are the buckets a deadline falls into.
	DeadlineBuckets = []string{"none", "under-10m", "under-2h", "over-2h"}
)

// shapeFields is the order the public projection is rendered in. The set is
// closed: a field added here is a field the provider starts seeing, which is a
// decision about rule 4 and belongs in the spec first.
var shapeFields = []string{"kind", "files", "packages", "lanes", "lane", "attempts", "platform", "security", "deadline"}

// publicAllowlist IS the public-data boundary: every field the provider may be
// told, and the closed set of values each may carry. Nothing reaches a payload
// without passing checkPublic, so a string that is not on this list -- a lane's
// own spelling, a mind's name, a lineage, a path, a title -- cannot leave this
// process, whatever a registry or a caller puts in it. Being configured locally
// does not make a value public (Stella, #1327).
var publicAllowlist = map[string][]string{
	"kind":     Kinds,
	"files":    SizeBuckets,
	"packages": SizeBuckets,
	"lanes":    SizeBuckets,
	"lane":     {"none", "owned", "other"},
	"attempts": AttemptBuckets,
	"platform": PlatformBuckets,
	"security": {"yes", "no"},
	"deadline": DeadlineBuckets,
}

// Public is the unit's public projection: the typed, enumerated evidence the
// provider is given, and the whole of what it is given about the unit. The
// unit's id, its lane's spelling, its platform's name, its deadline and every
// attempt reason stay here. A value that is not on the allowlist is a refusal
// rather than a payload.
func (u Unit) Public(reg *Registry) (map[string]string, error) {
	shape := map[string]string{
		"kind":     u.Kind,
		"files":    sizeBucket(u.Files),
		"packages": sizeBucket(u.Packages),
		"lanes":    sizeBucket(u.Lanes),
		"lane":     laneBucket(reg, u.LaneOwner),
		"attempts": attemptBucket(len(u.Attempts)),
		"platform": platformBucket(u.Platform),
		"security": yesNo(u.Security()),
		"deadline": deadlineBucket(u),
	}
	if err := checkPublic(shape); err != nil {
		return nil, err
	}
	return shape, nil
}

// checkPublic is the boundary check: every field named, every value on its
// allowlist. It runs on the way out, so a future field that forgets to bucket
// its input is a refusal here rather than a disclosure there.
func checkPublic(shape map[string]string) error {
	for field, value := range shape {
		allowed, ok := publicAllowlist[field]
		if !ok {
			return fmt.Errorf("decide: %q is not a public field; the provider is told only %s", field, strings.Join(shapeFields, ", "))
		}
		found := false
		for _, a := range allowed {
			if a == value {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("decide: the field %s cannot carry %q to a provider; it is one of %s", field, value, strings.Join(allowed, ", "))
		}
	}
	return nil
}

// unitState renders the public projection as the bounded state text, one
// `field: value` line per field, in a fixed order.
func unitState(reg *Registry, u Unit) (string, error) {
	shape, err := u.Public(reg)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, field := range shapeFields {
		fmt.Fprintf(&b, "%s: %s\n", field, shape[field])
	}
	return b.String(), nil
}

// sizeBucket puts a count in its bucket.
func sizeBucket(n int) string {
	switch {
	case n <= 0:
		return SizeBuckets[0]
	case n <= 3:
		return SizeBuckets[1]
	case n <= 9:
		return SizeBuckets[2]
	case n <= 19:
		return SizeBuckets[3]
	default:
		return SizeBuckets[4]
	}
}

// attemptBucket puts a prior-attempt count in its bucket.
func attemptBucket(n int) string {
	switch {
	case n <= 0:
		return AttemptBuckets[0]
	case n == 1:
		return AttemptBuckets[1]
	case n == 2:
		return AttemptBuckets[2]
	default:
		return AttemptBuckets[3]
	}
}

// laneBucket answers whether the unit's lane is one a mind on this ladder OWNS,
// and never which lane it is. A registry's lane strings are the registry's
// business: validation does not make them public, and a lane copied out of a
// private project must not become provider input.
func laneBucket(reg *Registry, lane string) string {
	lane = strings.TrimSpace(lane)
	if lane == "" {
		return "none"
	}
	if reg != nil {
		for _, m := range reg.Minds {
			if m.Owns(lane) {
				return "owned"
			}
		}
	}
	return "other"
}

// platformBucket says whether a platform need is ordinary or named, never which
// platform it is.
func platformBucket(platform string) string {
	if ordinaryPlatforms[strings.ToLower(strings.TrimSpace(platform))] {
		return PlatformBuckets[0]
	}
	return PlatformBuckets[1]
}

// deadlineBucket puts a deadline in its bucket. An unparseable deadline never
// reaches here -- Validate refused it first -- and is reported as none.
func deadlineBucket(u Unit) string {
	d, err := u.deadline()
	switch {
	case err != nil || d <= 0:
		return DeadlineBuckets[0]
	case d < 10*time.Minute:
		return DeadlineBuckets[1]
	case d < 2*time.Hour:
		return DeadlineBuckets[2]
	default:
		return DeadlineBuckets[3]
	}
}

// yesNo renders a flag as one of two tokens.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// findMind maps an opaque option id back to the mind it stands for. Nothing
// else is accepted: an answer naming a mind outright is an answer to a question
// this process never asked.
func findMind(offered []Mind, id string) (Mind, bool) {
	id = strings.TrimSpace(id)
	for i, m := range offered {
		if optionID(i) == id {
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

package swarm

// THE LADDER ON THE DISPATCH PATH (Glenn 2026-09-19).
//
// This sits BESIDE route.go and answers a different question. route.go asks
// what KIND of work a card is and picks the WORKER DESCRIPTION for it from a
// routes table. This asks which MIND does the unit -- over the registry ladder,
// with the escalation policy on it -- and names the MODEL ID that mind runs.
// One chooses the harness a card runs under; the other chooses who does the
// work. A card can want both, and neither answer is the other's.
//
// "Are we all using Jev yet when selecting which model to send work to?
// Because that is important." The route verb existed and nothing called it:
// every model on every dispatch path was a string a person had written by
// hand. This is the seam that makes the decision the mechanism.
//
// Before a card is assigned a model the batch asks the ladder ONE typed
// question -- which mind does this unit of work -- in process through
// internal/decide, the same seam triage --decide already asks its abstain
// reason through, and dispatches the card with THAT rung's model id from the
// registry. Nothing about the ladder is written here: the rungs, their
// heights, their lineages and their model ids all come from the registry file.
//
// The fallback is today's behaviour, exactly as SPEC-DECIDE rule 5 requires:
// the model the fill script already wrote in the cards TSV. It stands when
//
//   - no key is set, so no call is made at all;
//   - the provider refused, or named a rung nobody offered;
//   - the confidence is under the floor (a suggestion, never an
//     authorization); or
//   - the rung the ladder answered is a mind a card cannot be dispatched to --
//     a friend, a child, Glenn -- because those are ASKED, not run.
//
// and the card's receipt line says which of them happened, so no reader has to
// infer it:
//
//	ROUTE jev=<rung|fallback> conf=<x> rung=<name> model=<id>
//
// The last case is the one worth reading in the log: a card the ladder says is
// not mechanical work at all, running on a mechanical model because the batch
// has no other way to dispatch it. That is evidence for the escalation log,
// not a silent success.
//
// Accounting is not optional (SPEC-DECIDE, "Accounting is not optional"): a
// decision that called the provider writes its log row and its usage row, in
// the fleet's own columns, through the same appender a card's usage is written
// with. A decision that made no call writes no usage row, because an empty row
// is a claim that a call was made.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultRouteFloor is the confidence floor the dispatch route starts at. It is
// the adoption default of SPEC-DECIDE rule 5 and it is stated HERE, beside the
// call site, because a floor belongs to the call it gates.
const DefaultRouteFloor = 0.9

// routeUsageProvider is who the tokens were spent with, in the usage file's own
// vocabulary: the Jev endpoint is TypeSafe's.
const routeUsageProvider = "typesafe"

// RouteInput is one dispatch route's seam: the ladder, the decider, the floor
// and where the two records go.
//
// Decide is the same decideFunc the triage route uses -- a *decide.Client's
// Decide method in production, an httptest fake or an in-process function in a
// test -- so nothing here ever dials a provider on its own. A nil Decide is the
// key being absent: the rules answer, no call is made, and today's model
// stands.
type RouteInput struct {
	Registry *decide.Registry
	Decide   decideFunc
	Floor    float64
	// Log is the escalation log every decision is written to (rule 8), and
	// Usage is the fleet's usage TSV a provider call's spend is written to.
	// Both are required before a call is made; see Accountable.
	Log   string
	Usage string
	Now   func() time.Time
}

// Accountable reports whether this input may ask the provider at all: token
// spend reporting is an obligation and every decision is logged, so a route
// that cannot say where both records go does not make the call. The reason is
// the line the caller prints once.
func (in RouteInput) Accountable() (bool, string) {
	var missing []string
	if strings.TrimSpace(in.Usage) == "" {
		missing = append(missing, "--route-usage")
	}
	if strings.TrimSpace(in.Log) == "" {
		missing = append(missing, "--route-log")
	}
	if len(missing) == 0 {
		return true, ""
	}
	return false, fmt.Sprintf("a jev call must be accounted for: %s missing, so the route answers by the rules alone and every card keeps today's model",
		strings.Join(missing, " and "))
}

// CardRoute is one card's routing answer: the model it is dispatched with, the
// rung that answered, where the answer came from, the confidence the floor was
// applied to, and the one receipt line the card carries.
type CardRoute struct {
	Model      string
	Rung       string
	Source     string
	Confidence float64
	Floor      float64
	// Fallback is true where today's model stands: no key, a provider refusal,
	// a confidence under the floor, or a rung no card can be dispatched to.
	Fallback bool
	// Why names, in one enumerated token, which fallback this was. It is empty
	// where the answer decided the model.
	Why     string
	Receipt string
}

// The enumerated reasons a route fell back. They are tokens and not prose, so
// the log can be counted by them.
const (
	RouteFallbackNoKey    = "no-key"
	RouteFallbackBelow    = "below-floor"
	RouteFallbackNoModel  = "rung-is-asked-not-run"
	RouteFallbackRefused  = "refused"
	RouteFallbackNoLadder = "no-ladder"
	// RouteFallbackNoAccount is the refusal to ask at all: a call nobody can
	// account for is not made, so the rules answer and today's model stands.
	RouteFallbackNoAccount = "no-accounting"
)

// RouteCard is the decision one card's model is chosen by. It never fails: a
// route that cannot be made is today's model with a receipt that says so,
// because a dispatch path that refuses to dispatch is worse than one that
// keeps the behaviour it had.
func RouteCard(ctx context.Context, in RouteInput, u decide.Unit, todaysModel string) CardRoute {
	floor := in.Floor
	if floor <= 0 {
		floor = DefaultRouteFloor
	}
	out := CardRoute{Model: todaysModel, Floor: floor, Fallback: true, Source: decide.SourceRules}
	if in.Registry == nil || len(in.Registry.Minds) == 0 {
		out.Why = RouteFallbackNoLadder
		out.Receipt = routeReceipt(out)
		return out
	}
	if err := decide.ValidFloor(floor); err != nil {
		out.Why = RouteFallbackNoLadder
		out.Receipt = routeReceipt(out)
		return out
	}

	// The provider is asked only where a key opened a client AND both records
	// have a home. Everything else answers by the rules: no key, no network,
	// deterministic, so the loop runs on a bench with no API at all.
	ask := in.Decide != nil
	unaccounted := false
	if ok, _ := in.Accountable(); !ok && ask {
		ask, unaccounted = false, true
	}
	var res decide.RouteResult
	var err error
	if ask {
		res, err = decide.RouteJev(ctx, deciderFunc(in.Decide), in.Registry, u, floor)
	} else {
		res, err = decide.RouteRules(in.Registry, u, floor)
	}
	// The record is written whether the decision stood or not: a call that was
	// made has already been paid for, and a decision that could not be made is
	// still evidence (SPEC-DECIDE, "A refusal cannot unspend a call").
	in.persist(res, u)
	if err != nil {
		out.Why = RouteFallbackRefused
		out.Receipt = routeReceipt(out)
		return out
	}

	out.Rung = res.Rung.Name
	out.Confidence = res.Confidence
	out.Source = res.Source
	switch {
	case !ask:
		// No call was made, so nothing about the model was decided: today's
		// model stands, and the rung the rules named is on the receipt.
		out.Why = RouteFallbackNoKey
		if unaccounted {
			out.Why = RouteFallbackNoAccount
		}
	case !res.Dispatchable():
		// A wait is an answer about WHO owns the work, not permission to start
		// it. Nothing here starts anything, so the card keeps what it had.
		out.Why = RouteFallbackRefused
	case res.Confidence < floor:
		out.Why = RouteFallbackBelow
	default:
		model, ok := in.Registry.ModelFor(res.Rung.Name)
		if !ok {
			// The ladder answered a mind that is ASKED, not run. The card
			// cannot be dispatched to it, so today's model stands -- and the
			// receipt says which rung should have had it.
			out.Why = RouteFallbackNoModel
			break
		}
		out.Model = model
		out.Fallback = false
		out.Why = ""
	}
	out.Receipt = routeReceipt(out)
	return out
}

// routeReceipt is the one line a routed card carries. jev= is the rung where
// the answer chose the model and the literal `fallback` where today's model
// stands; rung= always names what the ladder answered, so a fallback never
// hides the rung, and model= is what the card actually runs with.
func routeReceipt(r CardRoute) string {
	token := "fallback"
	if !r.Fallback {
		token = r.Rung
	}
	rung := r.Rung
	if strings.TrimSpace(rung) == "" {
		rung = "-"
	}
	why := r.Why
	if why == "" {
		why = "-"
	}
	return fmt.Sprintf("ROUTE jev=%s conf=%.2f rung=%s model=%s why=%s",
		oneline.Field(token), r.Confidence, oneline.Field(rung), oneline.Field(r.Model), oneline.Field(why))
}

// AfterGateFailure is the escalation rule (Glenn, 2026-09-18: "if we fail then
// we automatically promote up the intelligence ladder"). A card that failed its
// gate re-enters the SAME decision carrying that failure as CONFIRMED
// evidence, and the ladder does the rest: the rung that failed is out of the
// eligible set and so is its lineage at that height, so the answer is another
// lineage on the same rung where there is one -- sideways before up -- and the
// rung above where there is not. It is never a retry on the rung that failed.
//
// The reason travels in the unit and never to a provider: the public
// projection carries an attempt COUNT and nothing else.
func AfterGateFailure(u decide.Unit, rung, reason string) decide.Unit {
	next := u
	next.Attempts = append(append([]decide.Attempt(nil), u.Attempts...), decide.Attempt{
		Rung:    rung,
		Outcome: decide.OutcomeFailed,
		Reason:  reason,
	})
	return next
}

// deciderFunc adapts the decideFunc seam to the decide.Decider interface the
// ladder asks through. A nil function is a nil Decider, which is the ladder's
// own signal that there is no provider to ask.
func deciderFunc(do decideFunc) decide.Decider {
	if do == nil {
		return nil
	}
	return deciderAdapter{do: do}
}

type deciderAdapter struct{ do decideFunc }

func (d deciderAdapter) Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	return d.do(ctx, state, qs)
}

// persist writes the decision's two records: the log row for any decision that
// got as far as being one, and the usage row for a provider call that was
// actually made. A failure to write either is not a failure to dispatch -- the
// card still runs on today's model -- so nothing here returns an error the
// caller could only ignore; the receipt already says what was decided.
func (in RouteInput) persist(res decide.RouteResult, u decide.Unit) {
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if res.Unit == "" {
		return // nothing got as far as being a decision
	}
	if strings.TrimSpace(in.Log) != "" {
		_ = decide.AppendEntry(in.Log, decide.EntryFor(res, u, now()))
	}
	// A decision that made no call writes no usage row.
	if strings.TrimSpace(in.Usage) == "" || res.Usage.Calls == 0 {
		return
	}
	stamp := now().UTC().Format(time.RFC3339)
	row := UsageRow{
		"job":      u.ID,
		"attempt":  strconv.Itoa(len(u.Attempts) + 1),
		"started":  stamp,
		"ended":    stamp,
		"rc":       "0",
		"provider": routeUsageProvider,
		"model":    decide.DefaultModel,
	}
	// Presence is per counter: an unreported one is left empty and written as
	// "-", while a reported zero is the measurement it is (SPEC-TOKENS rule 14).
	if res.Usage.HasInput {
		row["tokens_in"] = strconv.Itoa(res.Usage.InputTokens)
	}
	if res.Usage.HasOutput {
		row["tokens_out"] = strconv.Itoa(res.Usage.OutputTokens)
	}
	if res.Usage.Failed {
		row["rc"] = "2"
	}
	_ = AppendCardUsage(in.Usage, row)
}

// THE CARD'S EVIDENCE.
//
// A card is a text file, and the ladder wants typed evidence: a kind, a size,
// a lane, a platform need, what security it touches. CardUnit reads that
// evidence from the card's OWN text and nothing else, and what the provider
// ever sees is the bucketed public projection of it (SPEC-DECIDE rule 4) --
// never the card, never its title, never a path.
//
// A card may state its evidence outright, one `FIELD: value` line anywhere in
// its text, which is what a fill script should write from now on:
//
//	KIND: fix-with-red-test
//	FILES: 4
//	PACKAGES: 1
//	LANES: 1
//	LANE: code
//	PLATFORM: windows
//	TOUCHES: sandbox
//
// Where the card states no kind, the kind is read from its contract line by a
// deterministic table -- our cards say what they are in words, and this reads
// those words, never a model. A card whose kind cannot be read is NOT routed:
// no evidence is no decision, and today's model stands.

// cardKindPhrases maps a phrase in a card's contract line to the kind it names.
// Security phrases come first, because security is a KIND and not a height and
// it must never fall through to a cheaper reading of the same line.
var cardKindPhrases = []struct {
	phrase string
	kind   string
}{
	{"sandbox", decide.KindGuard},
	{"secrets", decide.KindGuard},
	{"deploy key", decide.KindGuard},
	{"guard", decide.KindGuard},
	{"red test", decide.KindFixWithRedTest},
	{"fixture", decide.KindFixtureRetarget},
	{"rebase", decide.KindRebase},
	{"lane stack", decide.KindStack},
	{"stack", decide.KindStack},
	{"fleet", decide.KindFleetChore},
	{"new verb", decide.KindNewVerb},
	{"spec", decide.KindSpec},
	{"design", decide.KindDesign},
	{"read of", decide.KindCauseToFind},
	{"cause", decide.KindCauseToFind},
	// LAST, and deliberately. `chore` is an ALIAS for fleet-chore and not a
	// kind, and it reads only a contract line no other phrase already types:
	// a chore to move a spec section is a spec card, a chore on the sandbox
	// guard is a guard card. What comes out of here is canonicalized by
	// CardUnit, so the alias never reaches validation or a route row.
	{"chore", "chore"},
}

// CardUnit reads one card's typed evidence. ok is false where the card names
// no kind and its contract line reads as none of them: a card nobody can type
// is a card nobody routes, and today's model stands.
func CardUnit(label, contract, text string) (decide.Unit, bool) {
	u := decide.Unit{ID: label}
	fields := cardFields(text)
	u.Kind = fields["kind"]
	if u.Kind == "" {
		u.Kind = kindFromContract(contract)
	}
	// A CARD is not JSON and never went through decide.ParseUnit, so the
	// alias table the route flags resolve was never reached from here: a card
	// whose header said `KIND: chore` was untyped, ok was false, and it fell
	// back to today's model with no rung and no route row (Stella, r2 of the
	// #1925 hold). The alias is resolved ONCE, here, at the DECIDE read --
	// before validation and before anything logs a kind, so the route log
	// keeps the canonical name and no per-kind floor splits in two.
	u.Kind = decide.CanonicalKind(u.Kind)
	if !decide.KnownKind(u.Kind) {
		return decide.Unit{}, false
	}
	u.Files = cardInt(fields["files"])
	u.Packages = cardInt(fields["packages"])
	u.Lanes = cardInt(fields["lanes"])
	u.LaneOwner = fields["lane"]
	u.Platform = fields["platform"]
	u.Deadline = fields["deadline"]
	for _, t := range strings.Split(fields["touches"], ",") {
		if t = strings.TrimSpace(t); t != "" {
			u.Touches = append(u.Touches, t)
		}
	}
	if err := u.Validate(); err != nil {
		// Evidence the ladder would refuse is evidence we do not send: the kind
		// alone is kept, which is the one field the table above could read.
		return decide.Unit{ID: label, Kind: u.Kind}, true
	}
	return u, true
}

// cardHeaders are the field lines a card may state its own evidence with. The
// set is closed: a line the card carries that is not one of these is the
// card's business and is never read as evidence.
var cardHeaders = []string{"kind", "files", "packages", "lanes", "lane", "platform", "touches", "deadline"}

// cardFields reads the stated evidence lines, first occurrence winning.
func cardFields(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		for _, h := range cardHeaders {
			if name != h || out[h] != "" {
				continue
			}
			out[h] = strings.ToLower(strings.TrimSpace(value))
		}
	}
	return out
}

// kindFromContract reads the kind from a card's contract line by the table
// above: the first phrase that occurs wins, and security phrases occur first.
func kindFromContract(contract string) string {
	lower := strings.ToLower(contract)
	for _, p := range cardKindPhrases {
		if strings.Contains(lower, p.phrase) {
			return p.kind
		}
	}
	return ""
}

// cardInt reads a stated count; anything that is not one is no evidence at all,
// which the ladder already reads as thin.
func cardInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

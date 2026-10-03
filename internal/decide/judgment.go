package decide

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The judgment decision (SPEC-NOVA-DECIDE section 9): a routine judgment of a sprint's
// inbox, for one of its cards, answered by choosing one of the verbs the judgment prints.
// The state is the judgment's kind and text, the card, its log, and the verbs allowed;
// the answers are the verb with a probability per verb, the fix a rework carries and the
// reason a drop gives, each one of a few canned texts (a decision chooses, it does not
// write). Choose turns the answers into what nova-sprint answer does: apply the
// verb at or above the bar, or list it for the coordinator. A drop is always listed, and
// a judgment whose card a provider refused for want of payment is never asked at all.

// JudgmentName is the judgment decision's name in the record.
const JudgmentName = "judgment"

// The verbs a judgment can be answered with: the verb question's options.
const (
	VerbRework     = "rework"
	VerbDrop       = "drop"
	VerbWait       = "wait"
	VerbAck        = "ack"
	VerbAccept     = "accept"
	VerbRelease    = "release"
	VerbAskAnother = "ask-another"
)

// Verbs is every verb, in the order a row prints them.
var Verbs = []string{VerbRework, VerbAskAnother, VerbAccept, VerbAck, VerbWait, VerbDrop, VerbRelease}

// The fixes a rework that takes --fix carries, and their texts.
var Fixes = map[string]string{
	"own": "",
	"from-tip": "Your head does not merge onto the sprint tip: a file you touched was also changed by a card that landed after your base. " +
		"Start again from the current tip of the sprint branch, redo only the lines your card lists, and if a ledger must change, " +
		"lower its ceiling from the value at the tip by exactly your own count.",
	"pro-tier": "attempts ended without a RESULT.md on the flash tier; run on the pro tier (RESULT line tier: pro); the task is unchanged",
	"retry":    "the attempt ended for a cause outside the card (a provider, a member, staging); run it again; the task is unchanged",
}

// The reasons a drop gives, and their texts.
var Reasons = map[string]string{
	"already-done":    "the listed edits were already made, by an earlier attempt or by a card that landed first: done, not failed",
	"cannot-be-done":  "the card cannot be done as written: its brief is wrong, or names lines that are not there",
	"need-dropped":    "what it needs was dropped, and it cannot run without it",
	"reads-exhausted": "no reader can read it on the installed build (reads exhausted); added again after the fix",
}

// AckReason is the reason an applied ack gives: the decision's, with its probability.
func AckReason(p float64) string {
	return fmt.Sprintf("nova-decide (p=%.2f): the card can run without the dropped need; a conflict is handled at merge", p)
}

// JudgmentSchema is the judgment decision's three questions.
func JudgmentSchema() Schema {
	return Schema{Name: JudgmentName, Questions: map[string]Question{
		"verb": {Type: Choice, Instructions: "The coordinator's answer to this JUDGMENT for this CARD: the one verb, of the ALLOWED VERBS, " +
			"that moves the card toward landing at the least cost. A verb outside ALLOWED VERBS is never the answer.", Criteria: map[string]string{
			VerbRework:     "send the card back for another attempt, carrying its own finding or report, or the FIX: right when another attempt can succeed",
			VerbAskAnother: "ask another reader: the finding is wrong, or the read ended with no verdict, and the work itself looks done",
			VerbAccept:     "accept it for merge: the readers it needs said ok at its head",
			VerbAck:        "acknowledge it: the card can run without the dropped need it names, so nothing is to be done",
			VerbWait:       "leave it open for 30 minutes: the cause is outside the card (a member working through its queue, a reader still reading) and time alone clears it",
			VerbDrop:       "take the card off the table: it is done already, cannot be done as written, or cannot run without what was dropped; never for a failure a retry or a fix cures",
			VerbRelease:    "release the sentinel: the gate for its wave is passed",
		}},
		"fix": {Type: Choice, Instructions: "The fix a rework carries when the verb is rework and the card's own finding or report is not enough.", Criteria: map[string]string{
			"own":      "the card's own finding or report is the fix",
			"from-tip": "the head does not merge onto the sprint tip: start again from the tip and redo only the listed lines",
			"pro-tier": "attempts on the flash tier ended without a result: run on the pro tier, the task unchanged",
			"retry":    "the attempt ended for a cause outside the card (a provider, a member, staging): run it again, the task unchanged",
		}},
		"reason": {Type: Choice, Instructions: "Why the card is dropped, when the verb is drop.", Criteria: map[string]string{
			"already-done":    "the listed edits were already made, by an earlier attempt or by a card that landed first",
			"cannot-be-done":  "the card cannot be done as written: its brief is wrong, or names lines that are not there",
			"need-dropped":    "what it needs was dropped, and it cannot run without it",
			"reads-exhausted": "no reader can read it now: reads exhausted on the installed build",
		}},
	}}
}

// Kinds maps each routine judgment type, as the inbox names it, to its short name: the
// kinds nova-sprint answer asks the decision for. Every other type is left.
var Kinds = map[string]string{
	"a reader found it broken":                  "broken",
	"work came back failed":                     "failed",
	"a primary is blocked on something dropped": "blocked",
	"stalled":                            "stalled",
	"stream stopped: conflict on a card": "conflict",
	"a work card is past its deadline":   "deadline",
	"cannot ask":                         "cannot-ask",
	"ready to accept":                    "ready",
	"a card reached its bound":           "bound",
}

// VerbOf is the verb a printed decision makes, "" for a decision that is no verb the
// judgment decision chooses (look at the card, resolve and resume, reader add).
func VerbOf(decision string) string {
	switch {
	case strings.HasPrefix(decision, "rework"):
		return VerbRework
	case decision == "ask another reader", decision == "ask --another":
		return VerbAskAnother
	case decision == "drop", decision == "accept", decision == "ack", decision == "wait", decision == "release":
		return decision
	}
	return ""
}

// JudgmentInput is what one judgment decision is asked over.
type JudgmentInput struct {
	Kind    string   // the judgment's type, as the inbox names it
	Text    string   // the judgment note's text
	Card    string   // the card decided
	Cards   int      // how many cards the judgment holds
	History []string // the card's log, oldest first
	Allowed []string // the verbs the judgment prints
}

// MaxHistory bounds the log lines a state carries, and MaxLine each line.
const (
	MaxHistory = 40
	MaxLine    = 300
)

// JudgmentState is the text the judgment decision is asked over: the judgment, the verbs
// allowed, and the card's last MaxHistory log lines, each cut at MaxLine.
func JudgmentState(in JudgmentInput) string {
	var b strings.Builder
	b.WriteString("JUDGMENT (the sprint asks the coordinator):\n")
	fmt.Fprintf(&b, "kind: %s\n", in.Kind)
	fmt.Fprintf(&b, "card: %s (one of %d cards in this judgment)\n", in.Card, max(in.Cards, 1))
	fmt.Fprintf(&b, "text: %s\n", cut(strings.Join(strings.Fields(in.Text), " "), 4*MaxLine))
	b.WriteString("\nALLOWED VERBS (the answer is one of these):\n")
	b.WriteString(strings.Join(in.Allowed, ", "))
	b.WriteString("\n\nHISTORY (the card's log, oldest first):\n")
	h := in.History
	if len(h) > MaxHistory {
		fmt.Fprintf(&b, "(%d earlier lines not shown)\n", len(h)-MaxHistory)
		h = h[len(h)-MaxHistory:]
	}
	for _, l := range h {
		b.WriteString(cut(l, MaxLine))
		b.WriteString("\n")
	}
	return b.String()
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// paymentRefusal matches a provider's refusal for want of payment: HTTP 402, out of
// credit, insufficient funds or quota.
var paymentRefusal = regexp.MustCompile(`(?i)(\b(http|status|code|error)[ :=]*402\b|\b402\b.{0,60}(payment|fund|credit|billing|balance)|` +
	`(payment|fund|credit|billing|balance).{0,60}\b402\b|insufficient (account )?(funds|credit|balance)|out[- ]of[- ](credit|funds)|` +
	`payment required|insufficient_quota|credit balance is too low)`)

// PaymentRefusal says a text carries a provider's refusal for want of payment. Such a
// judgment is never answered by a decision: a payment is the owner's.
func PaymentRefusal(texts ...string) bool {
	return slices.ContainsFunc(texts, paymentRefusal.MatchString)
}

// The actions a judgment's card is given.
const (
	ActApply = "apply" // the verb is applied
	ActList  = "list"  // listed for the coordinator
)

// Chosen is what the decision chose for one card and what is done with it.
type Chosen struct {
	Verb   string  `json:"verb"`
	P      float64 `json:"p"`
	Fix    string  `json:"fix,omitempty"`    // a rework's fix option, when its command takes one
	Reason string  `json:"reason,omitempty"` // a drop's reason option
	Act    string  `json:"act"`
	Why    string  `json:"why,omitempty"` // why it is listed
}

// Choose reads the decision's answers for one card: the verb and its probability, the fix
// and the reason; the verb is applied when it is allowed, is not drop or release, and its
// probability is at or above bar. Anything else is listed, saying why.
func Choose(answers map[string]Answer, allowed []string, bar float64) Chosen {
	v := answers["verb"]
	c := Chosen{Verb: v.Value, P: v.Prob(v.Value), Act: ActList}
	switch c.Verb {
	case VerbRework:
		c.Fix = answers["fix"].Value
	case VerbDrop:
		c.Reason = answers["reason"].Value
	}
	switch {
	case !slices.Contains(allowed, c.Verb):
		c.Why = fmt.Sprintf("chose %s, which the judgment does not print (it prints %s)", c.Verb, strings.Join(allowed, ", "))
	case c.Verb == VerbDrop:
		c.Why = "a drop is the coordinator's: " + Reasons[c.Reason]
	case c.Verb == VerbRelease:
		c.Why = "a release is the coordinator's: the gate for the wave"
	case c.P < bar:
		c.Why = "p=" + strconv.FormatFloat(c.P, 'f', 2, 64) + " is under the bar " + strconv.FormatFloat(bar, 'f', 2, 64)
	default:
		c.Act = ActApply
	}
	return c
}

// The outcomes a judgment decision is labelled with, once the card's fate is known.
const (
	OutcomeLanded   = "landed"
	OutcomeDropped  = "dropped"
	OutcomeCameBack = "came-back"
)

// JudgmentOutcome is the outcome a card's state says, "" while it is not known: landed,
// dropped (off the table), or came back (another judgment open on it).
func JudgmentOutcome(col string, placed, otherJudgment bool) string {
	switch {
	case col == "landed":
		return OutcomeLanded
	case !placed:
		return OutcomeDropped
	case otherJudgment:
		return OutcomeCameBack
	}
	return ""
}

// ParseBar reads a judgment bar: a probability.
func ParseBar(raw string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || v < 0 || v > 1 {
		return 0, fmt.Errorf("decide_judgment_bar %q is not a probability in [0, 1]", raw)
	}
	return v, nil
}

// HistoryOf is a card's log as a judgment state carries it: the lines nova-sprint log
// --card prints, less its LOG OK line and the cost and field-change lines (the record of
// a read's price or a field set tells the decision nothing), each line trimmed.
func HistoryOf(log string) []string {
	var out []string
	for _, l := range strings.Split(log, "\n") {
		t := strings.TrimSpace(l)
		switch {
		case t == "", strings.HasPrefix(t, "LOG OK"), strings.Contains(t, " cost: "), strings.Contains(t, " changed by "):
			continue
		}
		out = append(out, t)
	}
	return out
}

package sprint

import "sort"

// The judgment fields a card can hold (1.3.1: {p}jopen:<subject>@e is a hash
// of <type>|<cause>). J closes a judgment by its type, cause and subject
// (1.3.4), and no checked read enumerates a subject's fields, so a verb that
// closes every judgment on a card (drop: the model's DropEff, JCloseSubj of
// the card's subjects, tla/SprintEvents.tla) names each field a card can
// hold. The list is the rows of table 2.2 whose subject is a card (a sentinel,
// a waiter, a primary, a card), each with the causes its raisers keep it under,
// read from the raisers' own tables where they have one. The judgments on a
// primary's work card and read cards are the two lateness rows, which J
// closes itself when a step ends the timed state (1.3.4), a removal included.

// JudgmentField is one field of jopen: a judgment's type (the words of 2.2)
// and the cause it is raised under.
type JudgmentField struct {
	Type, Cause string
}

// Field is the field's name in jopen: <type>|<cause>.
func (f JudgmentField) Field() string { return f.Type + "|" + f.Cause }

// CauseNone is the cause of a judgment whose raiser names none (the model's
// "-": one judgment of the type on the subject).
const CauseNone = "-"

// TypeCannotAsk and TypeCIRed are 2.2's words for R8's judgment and ci's.
// The constants NCannotAsk and NCIRed hold other words (the present planners',
// which J refuses as no type of 2.2): a defect of their owners (IT09, IT01),
// bridged by TableWord and PlannerWord until one constant holds the table's word.
const (
	TypeCannotAsk = "cannot ask"
	TypeCIRed     = "ci red on a primary"
)

// plannerWords are the constants whose words are not 2.2's, by the table's.
var plannerWords = map[string]string{TypeCannotAsk: NCannotAsk, TypeCIRed: NCIRed}

// TableWord is a planner's judgment type as table 2.2 words it (the type J
// keys jopen on).
func TableWord(typ string) string {
	for table, planner := range plannerWords {
		if planner == typ {
			return table
		}
	}
	return typ
}

// PlannerWord is a type of 2.2 as the present planners word it (the type
// their lists, ReworkResolves and the like, name).
func PlannerWord(typ string) string {
	if w, ok := plannerWords[typ]; ok {
		return w
	}
	return typ
}

// CauseCI and CauseReturn are the causes of the two judgments the verbs raise
// on a primary: "ci red on a primary" by ci, "returned to review" by return.
const (
	CauseCI     = "ci"
	CauseReturn = "return"
)

// The could-not-move causes: the rules that raise "the machine could not
// move a card" (2.2: R3, R6, R8, R9, R10, R11), by the rule's name, the cause
// reviewRefused and the deal (causeCouldNot) raise it under.
var couldNotMoveCauses = []string{ruleResolve, ruleDeal, ruleAsk, ruleAccept, ruleRework, ruleLate}

// CouldNotMoveFields are the fields of "the machine could not move a card",
// one for each rule that raises it (1.3.5; the model's rework closes it).
func CouldNotMoveFields() []JudgmentField {
	out := make([]JudgmentField, 0, len(couldNotMoveCauses))
	for _, c := range couldNotMoveCauses {
		out = append(out, JudgmentField{typeCouldNotMove, c})
	}
	return out
}

// CardJudgmentFields is every field of 2.2's rows on a card subject but the
// two blocked rows, whose cause is the need (BlockedJudgmentFields), sorted by
// field. "An invariant is broken" has no raiser yet (repair is no verb before
// AL7, IT26), so no cause of it is listed.
func CardJudgmentFields() []JudgmentField {
	seen := map[JudgmentField]bool{}
	var out []JudgmentField
	add := func(typ, cause string) {
		f := JudgmentField{typ, cause}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	add(NSentinelReached, CauseNone)
	add(TypeCannotAsk, reviewCauses[NCannotAsk])
	add(NBound, BoundAttempts)
	add(NBound, boundRedeals)
	add(TypeCIRed, CauseCI)
	add(NReturned, CauseReturn)
	for typ, rs := range reviewStalls { // R16's two review rows
		add(typ, rs.Cause)
	}
	for _, row := range holdRows { // R16's stalled, by the row that found nothing
		cause := row.Cause
		if cause == "" {
			cause = row.Name
		}
		add(NStalled, cause)
	}
	for _, f := range CouldNotMoveFields() {
		add(f.Type, f.Cause)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field() < out[j].Field() })
	return out
}

// BlockedJudgmentFields are the fields of the two blocked rows on a waiter:
// one of each for every need it names (2.2: the cause is the need).
func BlockedJudgmentFields(needs []string) []JudgmentField {
	out := make([]JudgmentField, 0, 2*len(needs))
	for _, n := range needs {
		out = append(out, JudgmentField{NBlocked, n}, JudgmentField{NMissingNeed, n})
	}
	return out
}

// AskResolves and AskAnotherResolves are the judgments an ask answers on its
// primary (IT09's Ask): R16's stranded and stalled, and for --another also
// the exhausted reads and a broken read.
var (
	AskResolves        = []string{NStranded, NStalled}
	AskAnotherResolves = []string{NReadBroken, NReadsExhausted, NStranded, NStalled}
)

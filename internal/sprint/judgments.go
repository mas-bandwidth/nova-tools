package sprint

import (
	"fmt"
	"slices"
	"strings"
)

// The judgment and notice tables of the event-driven tick's upper layers (the
// upper design, version 2.1, IT06): section 2.2 as Judgments, section 2.5 as
// Notices, what the inbox prints of one open judgment now (Printed), and the
// invariant that every decision printed is one its verb accepts (Answerable,
// section 5).
//
// Every mapping is a row of a table, so a change to the design is a changed
// row. A decision is printed only where its row's condition holds in the
// snapshot, and the condition is the row's own words; Answerable then holds
// each decision printed to the planner of its verb, the present ones of this
// package (Release, Add, Drop, Rework, Ask, Accept, Return, Resume, MergeStep,
// Rank, FleetStep and Ack; the present Ack refuses what would leave a primary
// held by nobody, which section 3's ack does not, and no seeded state meets
// it). The design's wait has no present planner: the present Wait refuses a
// judgment the tick does not keep, where the design's sets a review time, so
// its guard is the coordinator. The verbs with no planner in this package
// (reader add, goal set and drop, start, stop, clear, fleet beat and the
// reads) are accepted in every state a judgment names: their refusals are
// bounds of their own. Answerable wants the snapshot to hold all four tables,
// as the planners do; Printed reads only the cards its conditions name, and
// nothing when a table is not loaded.
//
// Where the design is silent, or asks what a snapshot cannot say, the narrower
// reading is taken and the rest is left out. What the reading took, and what
// the design's rows leave to ask:
//
//	the owner key's argument (2.2): OwnerKey takes the word its key is made
//	of. That is the subject, except for "sentinel reached" (resolve:s, the
//	stream of the sentinel) and the two lateness rows (late:<kind>:<card>,
//	which say the kind of deadline as well as the card): a subject alone
//	does not carry either.
//	two decisions on one line (2.2): "return <suspect> and resume --did" is
//	two Decisions, since a Decision has one verb.
//	the four "stream stopped" rows (2.2): a stopped stream stays stopped, and
//	its judgment open, until resume (spec rule 9), whatever the coordinator
//	does to its cards, so a decision on a card is printed only while a card
//	the judgment names (cardsOf: the card it stopped on, the suspects, the
//	batch) is one its verb takes: return while one is in merging, drop while
//	one is open on the table, rework while one is in review or at its redeal
//	bound (rework refuses a card in merging: return it first), and rank of
//	the card a cross stop needs while that card is on the table and not
//	landed. After the decision, resume is still printed, and the decision
//	just taken is not. Answerable holds these three verbs to their planner
//	on the cards the judgment names: accepted when it takes at least one.
//	a repeat (2.2): a judgment marked for a primary that came back a second
//	time for the same cause gets card, "stop and look" (RepeatDecision), as
//	its last decision when its row does not list it.
//	"ci red on a primary" lists accept with no condition, and accept needs ok
//	reads from two different readers at the head: it is printed only while
//	they stand, as the row "returned to review" says of it. "reads
//	exhausted" lists ask --another with no condition, and it needs a reader
//	that has not read the attempt and fewer than 15 read cards: printed only
//	then, and so does "a read card is past its deadline". "A primary is
//	blocked on something missing" lists add <n> with no condition, and add of
//	a card that has a record is refused (EXISTS): it is printed, as its ack
//	is, only while a need has no record and a name a card can have. "A stream
//	has had no merge step past its deadline" lists merge --stream with no
//	condition, and merge refuses a stream that is stopped or has nothing
//	queued: printed only when it does not. "Sentinel reached" lists add
//	--before with no condition, and add refuses, naming rank, where no score
//	lies between the sentinel and the card before it (3): printed only where
//	one does.
//	what a snapshot does not hold (2.2): stop "while RUNNING" (the clock), drop
//	"only for a card the sweep quarantined" (the quarantine record), and the
//	decisions of "a verb in parts stopped before its end" that depend on the
//	op's verb (the op's verb). None is printed until a snapshot carries what
//	its condition reads. "The same command with --op <op>" is no decision of
//	one verb: it is in the row as verbSameCommand, behind the same condition,
//	and its guard refuses until a snapshot holds the op's verb.
//	fleet down <member> "its own whole deadline" (2.2) counts wall time from
//	the member's own stamp, since a snapshot has no STOPPED history; the
//	verb accepts the member either way.
//	the wait column (2.2) has three values (holds, review, never overdue) and
//	TickKept is one bool: "never overdue" is written as review is, false.
//	repair (2.2) is no verb before layer 1's AL7 and the check (IT26), so
//	"an invariant is broken" has no row for it; the row gains it with them.
//	the notices (2.5): Notice has no shape in 8.0, so it takes the columns of
//	the table. Two rows hold several notices, one for each: the machine
//	started and stopped (start, stop), and the three replaced by R11. A type
//	with variables (k, s, p, r, G) is the row's own words.
//	RuleAnswerable is 15, the next number after the spec's rules (section 9
//	of docs/SPEC-SPRINT.md); the spec has no rule for it.
//	the redeal bound is the present MaxRedeals (3), and the attempts bound is
//	the card's field bound, which the design names (1.3.1): Answerable holds a
//	decision to today's planners, so the conditions use today's number; the
//	design's is 5 (R2, R11), and the switch (IT23) moves both through
//	AtRedealBound.
//	the condition of a decision takes the Open (Decision.Accepted; 8.0 has the
//	subject), since what a stopped stream's decision acts on is named by its
//	note.

// JudgmentType is one row of table 2.2: a type of judgment, who raises it and
// on what, the key that wakes its owner rule when it closes, the decisions it
// offers, and how wait and ack treat it.
type JudgmentType struct {
	// Type is the judgment's type, the words of the table.
	Type string
	// Subject is what the judgment is on: sentinel, waiter, primary, sprint,
	// work card, read card, stream, card, rule key, person or op.
	Subject string
	// OwnerKey is the agenda key a close by a decision or ack queues, made
	// from the word the key names (see the header: the subject, but for the
	// stream of a sentinel and the kind and card of a lateness); "" when the
	// design queues none.
	OwnerKey func(subject string) string
	// Decisions are the decisions the row lists, in its order. A row whose
	// decisions follow the place of its card (stalled) adds them in Printed.
	Decisions []Decision
	// TickKept says the judgment is a condition the tick keeps: wait holds
	// it (1.3.4), and the tick raises it again if it stands after the hold.
	TickKept bool
	// Ack says ack answers it.
	Ack bool
	// RaisedBy are the rules and verbs that raise it.
	RaisedBy []string
}

// Decision is one decision a judgment offers: the verb that makes it and the
// arguments as the table prints them. Accepted is the row's condition, the
// state in which the verb accepts it; nil means every state the judgment is
// open in. It says why not, in the row's words, when it does not hold.
type Decision struct {
	// Verb is the verb of section 3 that makes the decision.
	Verb string
	// Args are the words after the verb, as the table prints them.
	Args []string
	// Accepted is whether the decision is offered in the state s for the open
	// judgment o. It takes the Open, not only its subject (8.0 has the
	// subject): a stopped stream's judgment names the cards its decisions act
	// on in its note, and they are in no other place of a snapshot.
	Accepted func(s *Snapshot, o Open) (bool, string)
}

// String is the decision as a command line: the verb and its arguments.
func (d Decision) String() string {
	return strings.TrimSpace(d.Verb + " " + strings.Join(d.Args, " "))
}

// Notice is one row of table 2.5: a notice (KNOW), who raises it, and what it
// is about. A notice needs no decision.
type Notice struct {
	// Type is the notice's type, the words of the table.
	Type string
	// Subject is what the notice is on: stream, member, reader, primary,
	// primaries, sentinel, sprint, card or note.
	Subject string
	// RaisedBy are the rules and verbs that raise it.
	RaisedBy []string
}

// RuleAnswerable is the number Answerable reports its violations under: the
// next after the rules of docs/SPEC-SPRINT.md section 9.
const RuleAnswerable = 15

// The subjects of the tables: the words of their subject columns.
const (
	subjSentinel  = "sentinel"
	subjWaiter    = "waiter"
	subjPrimary   = "primary"
	subjPrimaries = "primaries"
	subjSprint    = "sprint"
	subjWorkCard  = "work card"
	subjReadCard  = "read card"
	subjStream    = "stream"
	subjCard      = "card"
	subjRuleKey   = "rule key"
	subjPerson    = "person"
	subjOp        = "op"
	subjMember    = "member"
	subjReader    = "reader"
	subjNote      = "note"
)

// What the guards and conditions use besides the tables.
const (
	// maxReadCards is the most read cards a primary has: rcards at most 15
	// (1.3.1), and ask --another is refused past it (3).
	maxReadCards = 15
	// typeStalled is the row whose decisions follow the place of its card.
	typeStalled = "stalled"
	// verbCard is the verb that shows a card: the decision "stop and look"
	// (RepeatDecision) is.
	verbCard = "card"
	// verbSameCommand is the decision of "a verb in parts stopped before its
	// end" that repeats the op's own command with --op (2.2): the op's verb is
	// in no snapshot, so it is a decision no guard can hold to a planner.
	verbSameCommand = "the same command"
	// probeIDBase names the card, and the stream, a guard probes an add with.
	probeIDBase = "probe"
	// The words a guard gives a verb that wants one.
	answerReason = "reason"
	answerFix    = "fix"
	answerDid    = "did"
	// answerBatch is the batch a merge guard asks for: one card decides it.
	answerBatch = 1
)

// keyOf is the owner key of a rule and a word: prefix and the word.
func keyOf(prefix string) func(string) string {
	return func(word string) string { return prefix + word }
}

// keyFixed is the owner key of a sprint-level rule: the same key for any
// subject.
func keyFixed(key string) func(string) string {
	return func(string) string { return key }
}

// keySelf is the owner key that is its subject: the parked key is the key of
// the rule whose step was refused.
func keySelf(word string) string { return word }

// keyNone is the owner key of a row the design queues none for.
func keyNone(string) string { return "" }

// decide is a decision of a verb with its arguments, offered in every state.
func decide(verb string, args ...string) Decision {
	return Decision{Verb: verb, Args: args}
}

// decideWhen is a decision offered only where the condition holds in the
// state of the subject the judgment is open on.
func decideWhen(cond func(*Snapshot, string) (bool, string), verb string, args ...string) Decision {
	return Decision{Verb: verb, Args: args, Accepted: func(s *Snapshot, o Open) (bool, string) { return cond(s, o.Subject()) }}
}

// decideWhenNamed is a decision offered only where the condition holds in the
// state of the cards the judgment names (cardsOf).
func decideWhenNamed(cond func(*Snapshot, Open) (bool, string), verb string, args ...string) Decision {
	return Decision{Verb: verb, Args: args, Accepted: cond}
}

// judgmentRows is section 2.2, row by row and in its order.
var judgmentRows = []JudgmentType{
	{Type: "sentinel reached", Subject: subjSentinel, OwnerKey: keyOf("resolve:"), RaisedBy: []string{"R3"},
		Decisions: []Decision{decide("release", "G", "--reason"), decideWhen(scoreBefore, "add", "--before", "G"), decide("drop", "G")}},
	{Type: "a primary is blocked on something dropped", Subject: subjWaiter, OwnerKey: keyOf("held:"), RaisedBy: []string{"R4", "add"}, Ack: true,
		Decisions: []Decision{decide("ack"), decide("drop", "w")}},
	{Type: "a primary is blocked on something missing", Subject: subjWaiter, OwnerKey: keyOf("held:"), RaisedBy: []string{"add"}, Ack: true,
		Decisions: []Decision{decideWhen(needCreatable, "add", "<n>"), decideWhen(needMissing, "ack"), decide("drop", "w")}},
	{Type: "cannot ask", Subject: subjPrimary, OwnerKey: keyOf("ask:"), RaisedBy: []string{"R8"}, TickKept: true,
		Decisions: []Decision{decide("reader add"), decide("rework", "--fix"), decide("drop"), decide("wait")}},
	{Type: "no fleet member is up", Subject: subjSprint, OwnerKey: keyFixed("deal"), RaisedBy: []string{"R6"}, TickKept: true,
		Decisions: []Decision{decide("fleet beat"), decide("fleet up", "<m>"), decide("wait")}},
	{Type: "a card reached its bound", Subject: subjPrimary, OwnerKey: keyOf("held:"), RaisedBy: []string{"R2", "R10"}, TickKept: true,
		Decisions: []Decision{decideWhen(atABound, "rework", "--fix"), decide("drop"), decide("wait")}},
	{Type: "a work card is past its deadline", Subject: subjWorkCard, OwnerKey: keyOf("late:"), RaisedBy: []string{"R11"}, TickKept: true,
		Decisions: []Decision{decideWhen(heldItsOwnDeadline, "fleet down", "<member>"), decide("drop", "<p>"), decide("wait")}},
	{Type: "a read card is past its deadline", Subject: subjReadCard, OwnerKey: keyOf("late:"), RaisedBy: []string{"R11"}, TickKept: true,
		Decisions: []Decision{decideWhen(anotherReaderFree, "ask", "--another", "<p>"), decide("drop", "<p>"), decide("wait")}},
	{Type: "a stream has had no merge step past its deadline", Subject: subjStream, OwnerKey: keyOf("late:mergeidle:"), RaisedBy: []string{"R11"}, TickKept: true,
		Decisions: []Decision{decideWhen(somethingQueued, "merge", "--stream", "s"), decide("card"), decide("wait")}},
	{Type: "stream stopped: conflict on a card", Subject: subjStream, OwnerKey: keyNone, RaisedBy: []string{"merge"},
		Decisions: []Decision{decide("resume", "--stream", "s", "--did"), decideWhenNamed(cardsReworkable, "rework", "<card>"), decideWhenNamed(cardsOpen, "drop", "<card>")}},
	{Type: "stream stopped: stream branch red", Subject: subjStream, OwnerKey: keyNone, RaisedBy: []string{"merge"},
		Decisions: []Decision{decideWhenNamed(cardsReturnable, "return", "<suspect>"), decide("resume", "--did"), decideWhenNamed(cardsReworkable, "rework", "<suspect>")}},
	{Type: "stream stopped: needs a card of another stream first", Subject: subjStream, OwnerKey: keyFixed("cross"), RaisedBy: []string{"merge"},
		Decisions: []Decision{decideWhenNamed(neededCardOpen, "rank", "<needed card>"), decide("card"), decideWhenNamed(cardsReturnable, "return", "<card>"),
			decideWhenNamed(cardsOpen, "drop", "<card>"), decide("wait")}},
	{Type: "stream stopped: the merge queue rejected", Subject: subjStream, OwnerKey: keyNone, RaisedBy: []string{"merge"},
		Decisions: []Decision{decide("resume", "--did"), decideWhenNamed(cardsReturnable, "return"), decideWhenNamed(cardsOpen, "drop")}},
	{Type: "ci red on a primary", Subject: subjPrimary, OwnerKey: keyNone, RaisedBy: []string{"ci"}, Ack: true,
		Decisions: []Decision{decide("rework", "--fix"), decideWhen(readsStand, "accept"), decide("drop"), decide("card"), decide("ack")}},
	{Type: "returned to review", Subject: subjPrimary, OwnerKey: keyNone, RaisedBy: []string{"return"},
		Decisions: []Decision{decide("rework", "--fix"), decideWhen(readsStand, "accept"), decide("drop")}},
	{Type: "reads exhausted", Subject: subjPrimary, OwnerKey: keyOf("held:"), RaisedBy: []string{"R16"},
		Decisions: []Decision{decideWhen(anotherReaderFree, "ask", "--another"), decide("rework", "--fix"), decide("drop")}},
	{Type: "stranded in review", Subject: subjPrimary, OwnerKey: keyOf("held:"), RaisedBy: []string{"R16"},
		Decisions: []Decision{decideWhen(neverAsked, "ask"), decide("rework", "--fix"), decide("drop")}},
	{Type: typeStalled, Subject: subjCard, OwnerKey: keyOf("held:"), RaisedBy: []string{"R16"}, TickKept: true,
		Decisions: []Decision{decide("card"), decide("drop"), decide("wait")}},
	{Type: "the machine could not move a card", Subject: subjCard, OwnerKey: keyOf("held:"), Ack: true, TickKept: true,
		RaisedBy:  []string{"R3", "R6", "R8", "R9", "R10", "R11"},
		Decisions: []Decision{decide("ack"), decide("drop"), decide("card"), decide("wait")}},
	{Type: "an invariant is broken", Subject: subjCard, OwnerKey: keyOf("held:"), TickKept: true,
		RaisedBy: []string{"a lower-layer refusal naming the card", "the sweep"},
		Decisions: []Decision{decide("card"), decide("clear", "--confirm", "sprint"),
			decideWhen(judgmentUnread("the quarantine record"), "drop"), decide("wait")}},
	{Type: "the machine's step was refused", Subject: subjRuleKey, OwnerKey: keySelf, RaisedBy: []string{"the tick"}, Ack: true, TickKept: true,
		Decisions: []Decision{decide("log", "--since"), decide("ack"), decideWhen(judgmentUnread("the machine's clock"), "stop"), decide("wait")}},
	{Type: "the sprint is done", Subject: subjSprint, OwnerKey: keyFixed("done"), RaisedBy: []string{"R15"},
		Decisions: []Decision{decide("clear"), decide("add")}},
	{Type: "a reminder could not be delivered", Subject: subjPerson, OwnerKey: keyOf("remind:"), RaisedBy: []string{"R14"}, Ack: true, TickKept: true,
		Decisions: []Decision{decide("goal set"), decide("goal drop"), decide("ack")}},
	{Type: "the machine is falling behind", Subject: subjSprint, OwnerKey: keyFixed("behind"), RaisedBy: []string{"R18"}, TickKept: true,
		Decisions: []Decision{decide("wait"), decideWhen(judgmentUnread("the machine's clock"), "stop"), decide("where")}},
	{Type: "the machine is STOPPED and moves are due", Subject: subjSprint, OwnerKey: keyNone, RaisedBy: []string{"R17"}, TickKept: true,
		Decisions: []Decision{decide("start"), decide("wait", "--for", "d", "--reason")}},
	{Type: "a verb in parts stopped before its end", Subject: subjOp, OwnerKey: keyOf("late:cut:"), RaisedBy: []string{"R11"}, Ack: true, TickKept: true,
		Decisions: []Decision{decideWhen(judgmentUnread("the op's verb"), verbSameCommand, "with", "--op", "<op>"),
			decideWhen(judgmentUnread("the op's verb"), "drop", "--abort", "--op", "<op>"),
			decideWhen(judgmentUnread("the op's verb"), "remove", "--abort", "--op", "<op>"),
			decideWhen(judgmentUnread("the op's verb"), "ack"), decide("wait")}},
}

// noticeRows is section 2.5, row by row and in its order.
var noticeRows = []Notice{
	{Type: "stream started merging", Subject: subjStream, RaisedBy: []string{"accept", "R9"}},
	{Type: "batch landed", Subject: subjStream, RaisedBy: []string{"merge"}},
	{Type: "stream landed", Subject: subjStream, RaisedBy: []string{"merge"}},
	{Type: "work came back ok", Subject: subjPrimaries, RaisedBy: []string{"finish"}},
	{Type: "fleet member up", Subject: subjMember, RaisedBy: []string{"R1", "fleet up"}},
	{Type: "fleet member down", Subject: subjMember, RaisedBy: []string{"R2", "fleet down"}},
	{Type: "a member's ok rate fell below OkRateFloor", Subject: subjMember, RaisedBy: []string{"finish"}},
	{Type: "a reader's broken rate rose above BrokenRateCeiling", Subject: subjReader, RaisedBy: []string{"read --broken"}},
	{Type: "a read of p by r: its one-line summary", Subject: subjPrimary, RaisedBy: []string{"read --ok", "read --broken"}},
	{Type: "stream s has landed nothing for IdleSpan", Subject: subjStream, RaisedBy: []string{"R11"}},
	{Type: "an unknown machine is beating", Subject: subjMember, RaisedBy: []string{"R1"}},
	{Type: "cards returned to ready because no member is up", Subject: subjPrimaries, RaisedBy: []string{"R2"}},
	{Type: "ci green", Subject: subjPrimary, RaisedBy: []string{"ci"}},
	{Type: "sentinel landed", Subject: subjSentinel, RaisedBy: []string{"release"}},
	{Type: "the machine started", Subject: subjSprint, RaisedBy: []string{"start"}},
	{Type: "the machine stopped", Subject: subjSprint, RaisedBy: []string{"stop"}},
	{Type: "the sprint was cleared, and is STOPPED", Subject: subjSprint, RaisedBy: []string{"clear"}},
	{Type: "stream resumed: the card it needed landed", Subject: subjStream, RaisedBy: []string{"R5"}},
	{Type: "k cards of s made ready", Subject: subjStream, RaisedBy: []string{"R3"}},
	{Type: "k ready cards of s went back to waiting behind G", Subject: subjStream, RaisedBy: []string{"R19"}},
	{Type: "accepted by the machine", Subject: subjPrimaries, RaisedBy: []string{"R9"}},
	{Type: "reworked by the machine", Subject: subjPrimaries, RaisedBy: []string{"R10"}},
	{Type: "replaced a work card not taken", Subject: subjCard, RaisedBy: []string{"R11"}},
	{Type: "replaced a late work card", Subject: subjCard, RaisedBy: []string{"R11"}},
	{Type: "replaced a late read", Subject: subjCard, RaisedBy: []string{"R11"}},
	{Type: "a judgment has waited past its due time", Subject: subjNote, RaisedBy: []string{"R12"}},
}

// Judgments is table 2.2 by type: every judgment the machine and the verbs
// raise, and nothing else.
var Judgments = judgmentTable(judgmentRows)

// Notices is table 2.5 by type: every notice the machine and the verbs raise,
// and nothing else.
var Notices = noticeTable(noticeRows)

func judgmentTable(rows []JudgmentType) map[string]JudgmentType {
	out := make(map[string]JudgmentType, len(rows))
	for _, r := range rows {
		out[r.Type] = r
	}
	return out
}

func noticeTable(rows []Notice) map[string]Notice {
	out := make(map[string]Notice, len(rows))
	for _, r := range rows {
		out[r.Type] = r
	}
	return out
}

// Printed is the decisions the inbox prints for the open judgment o in the
// snapshot: its row's decisions where their conditions hold, and for a stalled
// card the decisions its place allows first, less every decision DROPPING
// refuses (frozenDecision). A note that is no judgment of the table prints
// none.
//
// Follows dropcond in tla/SprintEvents.tla (errata 3, H8).
func Printed(s *Snapshot, o Open) []Decision { return printedIn(Judgments, s, o) }

// placeDecisions are the rows whose decisions follow the place of the card
// besides the row's own: the decisions before them in the printed list.
var placeDecisions = map[string]func(*Snapshot, string) []Decision{
	typeStalled: stalledDecisions,
}

func printedIn(table map[string]JudgmentType, s *Snapshot, o Open) []Decision {
	if o.Note.Kind != Judgment {
		return nil
	}
	row, ok := table[o.Note.Type]
	if !ok {
		return nil
	}
	subject := o.Subject()
	var out []Decision
	offer := func(ds []Decision) {
		for _, d := range ds {
			if frozenDecision(s, row, o, d) {
				continue
			}
			if d.Accepted != nil {
				if ok, _ := d.Accepted(s, o); !ok {
					continue
				}
			}
			out = append(out, d)
		}
	}
	if place := placeDecisions[row.Type]; place != nil {
		offer(place(s, subject))
	}
	offer(row.Decisions)
	// A primary that comes back a second time for the same cause is marked on
	// that cause's judgment, with "stop and look" added to its decisions (2.2,
	// as today, RepeatDecision): to look is card.
	if o.Note.Marked && !slices.ContainsFunc(out, func(d Decision) bool { return d.Verb == verbCard }) {
		out = append(out, decide(verbCard))
	}
	return out
}

// Answerable is the invariant of section 5: every decision printed for an open
// judgment is accepted by its verb's guard in the present state. A violation
// is a decision the inbox would print that its verb would refuse; the row or
// its condition is wrong, and the machine cannot be left offering it. It holds
// the decisions Printed prints, so a decision DROPPING refuses is left out of
// both.
//
// Follows dropcond in tla/SprintEvents.tla (errata 3, H8).
func Answerable(s *Snapshot, open []Open) []Violation { return answerableIn(Judgments, s, open) }

func answerableIn(table map[string]JudgmentType, s *Snapshot, open []Open) []Violation {
	var out []Violation
	for name, tbl := range map[string]*Table{Work: s.Work, Readers: s.Readers, Merge: s.Merge, Fleet: s.Fleet} {
		if tbl == nil {
			out = append(out, Violation{Rule: RuleAnswerable, Detail: "the snapshot holds no " + name + " table: its planners read all four"})
		}
	}
	if len(out) > 0 {
		slices.SortFunc(out, func(a, b Violation) int { return strings.Compare(a.Detail, b.Detail) })
		return out
	}
	// The planners read the open judgments from the snapshot: they read the
	// ones given.
	at := *s
	at.Open = open
	for _, o := range open {
		for _, d := range printedIn(table, &at, o) {
			guard, ok := verbGuards[d.Verb]
			if !ok {
				out = append(out, Violation{Rule: RuleAnswerable, Detail: fmt.Sprintf("%s on %s offers %q: %q has no guard", o.Note.Type, o.Subject(), d.String(), d.Verb)})
				continue
			}
			if ok, why := guard(&at, o, d); !ok {
				out = append(out, Violation{Rule: RuleAnswerable, Detail: fmt.Sprintf("%s on %s offers %q: %s", o.Note.Type, o.Subject(), d.String(), why)})
			}
		}
	}
	return out
}

// The DROPPING clause of Printed and Answerable (errata 3 to version 2.1, H8,
// as amended at 04:35): every verb that changes a card of a stream being
// dropped or removed is refused DROPPING (section 3), so no decision that
// makes one is printed while its stream is. Follows dropcond in
// tla/SprintEvents.tla (errata 3, H8), whose verbs are drop, rework, release,
// land, ack of a dropped, missing or refused judgment, and add n while n's
// stream is being dropped. This package has more verbs that change a card
// than the model: land is merge here (MergeStep), and accept, return, ask,
// rank, resume and add --before change a card of the stream they act on as
// well, so they are left out the same way (the errata's "every verb DROPPING
// refuses"). An ack changes a card only where it waives or clears: the two
// blocked judgments (it waives the need) and "the machine could not move a
// card" (it clears refused); the ack of any other judgment closes a note
// alone and is printed. The decisions of a verb in parts stopped before its
// end are that op's own (its parts are not refused), and a sprint-level
// decision (add when the sprint is done) names no stream.

// changesACard are the verbs whose decisions change a card of the stream they
// act on: DROPPING refuses each while that stream is being dropped.
var changesACard = map[string]bool{
	"release": true, "add": true, "drop": true, "rework": true, "ask": true,
	"accept": true, "return": true, "merge": true, "rank": true, "resume": true,
}

// ackChangesACard are the judgments whose ack changes their card: it waives a
// dropped or missing need, or clears the card's refused field.
var ackChangesACard = map[string]bool{NBlocked: true, NMissingNeed: true, typeCouldNotMove: true}

// frozenDecision says DROPPING refuses the decision d of the open judgment o
// of the row: its verb changes a card, and a stream it changes a card of is
// being dropped.
//
// Follows dropcond in tla/SprintEvents.tla (errata 3, H8).
func frozenDecision(s *Snapshot, row JudgmentType, o Open, d Decision) bool {
	if !changesACard[d.Verb] && !(d.Verb == "ack" && ackChangesACard[row.Type]) {
		return false
	}
	for _, stream := range decisionStreams(s, row, o, d) {
		if streamDropping(s, stream) {
			return true
		}
	}
	return false
}

// decisionStreams are the streams whose cards the decision d of o changes:
// for rank, the stream of the card a cross stop needs; for add <n>, the
// waiter's (Add creates n in it); for a stopped stream or a stream's
// lateness, the stream; for a judgment on a card, the stream of its primary.
// The op's own decisions and a sprint's name none. Reads the primaries named,
// by id (see the conditions' comment).
func decisionStreams(s *Snapshot, row JudgmentType, o Open, d Decision) []string {
	n := o.Note
	switch {
	case row.Subject == subjOp, n.SprintLevel:
		return nil
	case d.Verb == "rank":
		if c := placedPrimary(s, n.Other); c != nil {
			return []string{c.Row}
		}
		return streamList(n.OtherStream)
	case n.StreamLevel:
		return streamList(n.Stream)
	}
	if c := placedPrimary(s, primaryOfCard(o.Subject())); c != nil {
		return []string{c.Row}
	}
	return streamList(n.Stream)
}

// streamList is the stream as a list, none when it is "".
func streamList(stream string) []string {
	if stream == "" {
		return nil
	}
	return []string{stream}
}

// streamDropping says the stream is being dropped or removed ({p}dropping@e):
// a mark in the snapshot's Dropping, or in the dropping marks a read plan
// asked for. A snapshot loaded from a read plan that did not ask for the marks
// is refused as posDropping refuses it (posNotLoaded: Unloaded names the
// marks, and the caller refuses the plan, UnloadedErr): the read plan for the
// inbox (IT22) has to read the marks of the streams of the judgments it
// prints, as it reads the cards by id the conditions name.
//
// Follows dropcond in tla/SprintEvents.tla (errata 3, H8).
func streamDropping(s *Snapshot, stream string) bool {
	if s == nil || stream == "" {
		return false
	}
	if s.Dropping[stream] != "" {
		return true
	}
	return posDropping(s, stream)
}

// stalledDecisions are the decisions a stalled card's place allows that its
// verbs accept: today's held.decisions, as Decisions.
func stalledDecisions(s *Snapshot, subject string) []Decision {
	pr := placedPrimary(s, subject)
	if pr == nil || s.Readers == nil || s.Fleet == nil {
		return nil
	}
	var out []Decision
	for _, name := range (&held{s: s}).decisions(pr) {
		verb, arg, _ := strings.Cut(name, " ")
		switch {
		case name == "drop" || name == "wait" || name == "look at the card":
			// the row's own: card, drop and wait, last
		case name == "release":
			out = append(out, decide("release", "G", "--reason"))
		case verb == "fleet" && strings.HasPrefix(arg, "down "):
			out = append(out, decide("fleet down", strings.TrimPrefix(arg, "down ")))
		case name == "fleet up":
			out = append(out, decide("fleet up", "<m>"))
		case name == "rework":
			out = append(out, decide("rework", "--fix"))
		case name == "ask --another":
			// held.decisions lists it for any primary with a read, and ask
			// --another wants a reader that has not read the attempt
			out = append(out, decideWhen(anotherReaderFree, "ask", "--another"))
		case name == "accept" || name == "ask" || name == "return":
			out = append(out, decide(name))
		}
	}
	return out
}

// The conditions of the rows. Each reads the cards its comment names: a
// snapshot that answers Printed holds them.
//
// Some are read by id (Table.Card), which a snapshot loaded from a read plan
// does not guard: an id the plan did not read answers nil, the same as a card
// the store has no record of, and nothing records the missed read. They are the
// needs' records (needMissing, creatableNeeds, needCreatable, guardAdd), the
// read card of each reader at the attempt, a retired one included
// (freeReaders, anotherReaderFree), and the merge card of each card a
// stopped stream's note names (cardsReturnable). The read plan for the inbox
// (IT22) has to load every one of them by id, or Table.Card has to guard reads
// by id (state.go, IT05's). Their comments say "by id" where they read one.

// placedPrimary is the primary the subject names, placed on the table.
func placedPrimary(s *Snapshot, id string) *Card {
	if s == nil || s.Work == nil {
		return nil
	}
	return s.Work.Placed(id)
}

// needMissing: a need the waiter names has no record and was not waived (2.2,
// "ack (waives n, while n has no record)"). Reads the waiter and, by id, the
// record of each need it names (an id the read did not load looks like no
// record).
func needMissing(s *Snapshot, subject string) (bool, string) {
	w := placedPrimary(s, subject)
	if w == nil {
		return false, subject + " is not on the table"
	}
	waived := Split(w.F("waived"))
	for _, n := range missingNeeds(s, Split(w.F("needs"))) {
		if !contains(waived, n) {
			return true, ""
		}
	}
	return false, "every need " + subject + " names has a record"
}

// creatableNeeds are the needs the waiter names that have no record, were not
// waived, and have a name a card can be created under. Reads, by id, the
// record of each need the waiter names (see needMissing).
func creatableNeeds(s *Snapshot, w *Card) []string {
	waived := Split(w.F("waived"))
	var out []string
	for _, n := range missingNeeds(s, Split(w.F("needs"))) {
		if !contains(waived, n) && ValidID(n) {
			out = append(out, n)
		}
	}
	return out
}

// needCreatable: add <n> is accepted for a need that has no record, and not
// for one that has (EXISTS), or whose name no card can have (2.2, 3). Reads
// the waiter and, by id, the record of each need it names (see needMissing).
func needCreatable(s *Snapshot, subject string) (bool, string) {
	w := placedPrimary(s, subject)
	switch {
	case w == nil:
		return false, subject + " is not on the table"
	case len(creatableNeeds(s, w)) == 0:
		return false, subject + " names no need that has no record and a name a card can have"
	}
	return true, ""
}

// scoreBefore: add --before the sentinel is accepted while a score lies
// between it and the card before it; where none does, the add is refused and
// names rank (1.5.4, 3). Reads the stream's line of cards.
func scoreBefore(s *Snapshot, subject string) (bool, string) {
	c := placedPrimary(s, subject)
	if c == nil {
		return false, subject + " is not on the table"
	}
	if _, why := addScores(s, AddReq{Stream: c.Row, Before: c.ID}, 1); why != "" {
		return false, why
	}
	return true, ""
}

// atABound: rework is accepted in review at the attempts bound, and in ready
// at the redeal bound (2.2). Reads the primary and its work card.
func atABound(s *Snapshot, subject string) (bool, string) {
	c := placedPrimary(s, subject)
	switch {
	case c == nil:
		return false, subject + " is not on the table"
	case c.Col == Review && c.F("bound") != "":
		return true, ""
	case AtRedealBound(s, c) != nil:
		return true, ""
	}
	return false, subject + " is not at a bound: rework is accepted in review at the attempts bound and in ready at the redeal bound"
}

// heldItsOwnDeadline: the member that holds the work card has held it its own
// whole deadline (2.2, "only the member that held the card its own whole
// deadline"), and is up. Reads the work card and its member's control card.
func heldItsOwnDeadline(s *Snapshot, subject string) (bool, string) {
	if s == nil || s.Fleet == nil {
		return false, "no fleet in the snapshot"
	}
	c := s.Fleet.Placed(subject)
	if c == nil {
		return false, subject + " is held by no member"
	}
	// own is the stamp of the holder's own deal or take, "" for a card that
	// is withdrawn
	_, limit, _, own := WorkDeadline(c)
	if own == "" {
		return false, subject + " is held by no member"
	}
	if s.MemberCtl(c.Row).F("status") != Up {
		return false, "member " + c.Row + " is not up"
	}
	if d, ok := (TickReq{}).running(s.Now, c.F(own)); !ok || d <= limit {
		return false, "member " + c.Row + " has not held " + subject + " its own whole deadline"
	}
	return true, ""
}

// primaryOfCard is the primary a work card or a read card names, or the
// subject itself when it names none.
func primaryOfCard(subject string) string {
	if p, _, ok := ParseWorkCard(subject); ok {
		return p
	}
	if p, _, _, ok := ParseReadCard(subject); ok {
		return p
	}
	return subject
}

// freeReaders are the readers that have not read the primary at its attempt,
// not even a retired read (Ask's own choice). Reads the readers' rows and, by
// id, the read card of each reader at the attempt, a retired one included (an
// id the read did not load counts its reader as free).
func freeReaders(s *Snapshot, pr *Card) []string {
	attempt := pr.Int("attempt")
	var out []string
	for _, r := range s.Readers.Rows() {
		if s.Readers.Card(ReadCardID(pr.ID, attempt, r)) == nil {
			out = append(out, r)
		}
	}
	return out
}

// anotherReaderFree: ask --another is accepted for a primary in review whose
// work did not fail, already asked at its attempt, with a reader that has not
// read it and fewer than 15 read cards (2.2, 3). Reads the primary, the
// readers' rows and its read cards, the retired ones by id (freeReaders); the
// subject is the primary or one of its read cards.
func anotherReaderFree(s *Snapshot, subject string) (bool, string) {
	p := primaryOfCard(subject)
	pr := placedPrimary(s, p)
	switch {
	case pr == nil || pr.Col != Review:
		return false, p + " is not in review"
	case pr.F("result") == "failed":
		return false, p + " came back failed: its work is not read"
	case s.Readers == nil || len(readsAt(s, pr, pr.Int("attempt"))) == 0:
		return false, p + " is not asked yet at its attempt"
	case len(freeReaders(s, pr)) == 0:
		return false, "no reader is left that has not read " + p + " at its attempt"
	case max(len(s.Readers.Of(pr.ID)), len(Split(pr.F("rcards")))) >= maxReadCards:
		return false, p + " has the most read cards a primary may have"
	}
	return true, ""
}

// neverAsked: ask is accepted for a primary in review whose work did not fail
// and that has no read card at its attempt (2.2, "when never asked at its
// attempt"). Reads the primary and its read cards.
func neverAsked(s *Snapshot, subject string) (bool, string) {
	pr := placedPrimary(s, subject)
	switch {
	case pr == nil || pr.Col != Review:
		return false, subject + " is not in review"
	case pr.F("result") == "failed":
		return false, subject + " came back failed: its work is not read"
	case s.Readers == nil || len(readsAt(s, pr, pr.Int("attempt"))) > 0:
		return false, subject + " is asked at its attempt already"
	}
	return true, ""
}

// readsStand: accept is accepted for a primary in review while ok reads from
// two different readers stand at its head (2.2). Reads the primary and its
// read cards.
func readsStand(s *Snapshot, subject string) (bool, string) {
	pr := placedPrimary(s, subject)
	switch {
	case pr == nil || pr.Col != Review:
		return false, subject + " is not in review"
	case s.Readers == nil || len(okReaders(s, pr)) < 2:
		return false, "its reads do not stand at its head: accept wants ok from two different readers"
	}
	return true, ""
}

// somethingQueued: merge --stream is accepted while the stream is merging or
// waiting and has a card queued (2.2, 3). Reads the stream's control card and
// its queued cell. The subject is the stream, or its stream subject.
func somethingQueued(s *Snapshot, subject string) (bool, string) {
	st := strings.TrimPrefix(subject, "stream:")
	ctl := s.StreamCtl(st)
	switch {
	case ctl == nil:
		return false, "no stream " + st
	case ctl.F("state") == StreamStopped || ctl.F("state") == StreamLanded:
		return false, "stream " + st + " is " + ctl.F("state")
	case s.Merge.Count(st, Queued) == 0:
		return false, "nothing is queued in stream " + st
	}
	return true, ""
}

// namedCards are the primaries a decision of the judgment acts on that are on
// the work table: what cardsOf names, in its order, each once.
func namedCards(s *Snapshot, o Open) []*Card {
	var out []*Card
	seen := map[string]bool{}
	for _, id := range cardsOf(o) {
		if seen[id] {
			continue
		}
		seen[id] = true
		if c := placedPrimary(s, id); c != nil {
			out = append(out, c)
		}
	}
	return out
}

// cardsReturnable: return is accepted while a card the judgment names is in
// merging, queued or stuck in its stream or with no merge card, or is an
// orphan (in review with its merge card still queued or stuck): the stopped
// stream stays stopped and its judgment open after a return, so a card
// already returned is no card to return (rule 9). Reads the cards the note
// names and, by id, their merge cards (a merge card the read did not load
// looks like none, and the card is then taken as having no merge card).
func cardsReturnable(s *Snapshot, o Open) (bool, string) {
	for _, c := range namedCards(s, o) {
		var m *Card
		if s.Merge != nil {
			m = s.Merge.Placed(c.ID)
		}
		inQueue := m == nil || m.Col == Queued || m.Col == Stuck
		if (c.Col == Merging && inQueue) || (c.Col == Review && m != nil && (m.Col == Queued || m.Col == Stuck)) {
			return true, ""
		}
	}
	return false, "no card of " + o.Subject() + " is in merging: return takes a card from merging, and the stream stays stopped after it"
}

// cardsOpen: drop is accepted while a card the judgment names is open on the
// table (waiting, ready, working, review or merging): the stream stays
// stopped and its judgment open after a drop. Reads the cards the note names.
func cardsOpen(s *Snapshot, o Open) (bool, string) {
	for _, c := range namedCards(s, o) {
		if IsOpen(c.Col) {
			return true, ""
		}
	}
	return false, "no card of " + o.Subject() + " is open on the table: drop takes an open card"
}

// cardsReworkable: rework is accepted while a card the judgment names is in
// review, or in ready at the redeal bound; a card in merging is returned
// first (2.2 lists rework for a card a stream stopped on, and the verb refuses
// merging). The card is the one the note names, not one in the stream's
// stuck cell, which is empty once it is returned. Reads the cards the note
// names.
func cardsReworkable(s *Snapshot, o Open) (bool, string) {
	for _, c := range namedCards(s, o) {
		if c.Col == Review || (s.Fleet != nil && AtRedealBound(s, c) != nil) {
			return true, ""
		}
	}
	return false, "no card of " + o.Subject() + " is in review or at its redeal bound: rework refuses a card in merging, return it first"
}

// neededCardOpen: rank of the card a cross stop needs is accepted while that
// card is on the table and not landed (landed is final). Reads the needed card.
func neededCardOpen(s *Snapshot, o Open) (bool, string) {
	c := placedPrimary(s, o.Note.Other)
	if o.Note.Other == "" || c == nil {
		return false, "the card " + o.Subject() + " needs is not on the table"
	}
	if c.Col == Landed {
		return false, "the card " + o.Subject() + " needs has landed: landed is final"
	}
	return true, ""
}

// judgmentUnread is the condition of a decision whose row depends on state a
// Snapshot does not hold: it never holds, and says what would have to be read.
func judgmentUnread(what string) func(*Snapshot, string) (bool, string) {
	return func(*Snapshot, string) (bool, string) {
		return false, what + " is not in a snapshot: the decision is not printed"
	}
}

// A verb's guard says whether the verb's planner accepts the decision d of the
// open judgment o in the snapshot, which holds the open judgments given to
// Answerable.
type verbGuard func(s *Snapshot, o Open, d Decision) (bool, string)

// verbGuards are the guards of the verbs of section 3 that a decision names,
// by verb.
var verbGuards = map[string]verbGuard{
	"release":    guardRelease,
	"add":        guardAdd,
	"drop":       guardDrop,
	"rework":     guardRework,
	"ask":        guardAsk,
	"accept":     guardAccept,
	"return":     guardReturn,
	"resume":     guardResume,
	"merge":      guardMerge,
	"rank":       guardRank,
	"fleet up":   guardFleetUp,
	"fleet down": guardFleetDown,
	"ack":        guardAck,
	"wait":       guardWait,
	"remove":     guardRemove,
	// the op's own command: no snapshot holds its verb
	verbSameCommand: guardSameCommand,
	// no planner in this package: accepted in every state a judgment names
	"fleet beat": guardAlways,
	"reader add": guardAlways,
	"goal set":   guardAlways,
	"goal drop":  guardAlways,
	"start":      guardAlways,
	"stop":       guardAlways,
	"clear":      guardAlways,
	"card":       guardAlways,
	"log":        guardAlways,
	"where":      guardAlways,
}

func guardAlways(*Snapshot, Open, Decision) (bool, string) { return true, "" }

// guardSameCommand: the op's own verb is in no snapshot, so no planner can
// hold the decision that repeats it: it is accepted nowhere until one can.
func guardSameCommand(*Snapshot, Open, Decision) (bool, string) {
	return false, "the op's verb is not in a snapshot: the decision is not printed"
}

// accepts is the planner's answer: accepted when it refuses nothing.
func accepts(verb string, p Plan) (bool, string) {
	if len(p.Refused) > 0 {
		return false, verb + " refuses " + p.Refused[0].Key + ": " + p.Refused[0].Why
	}
	return true, ""
}

// acceptsSome is the planner's answer for a decision on one of the cards a
// judgment names (a suspect of a batch, a card of it): accepted when it takes
// at least one of them, since the decision is made on one and the stream stays
// stopped, and its judgment open, after it.
func acceptsSome(verb string, p Plan, cards []string) (bool, string) {
	if len(p.Refused) < len(cards) {
		return true, ""
	}
	return accepts(verb, p)
}

// distinct is the ids once each, in order.
func distinct(ids []string) []string {
	var out []string
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func hasArg(d Decision, arg string) bool { return contains(d.Args, arg) }

// cardsOf are the primaries a decision of o acts on: the cards a stopped
// stream names (the card it stopped on, the suspects, the cards of the
// batch), or the primary a subject is or belongs to.
func cardsOf(o Open) []string {
	n := o.Note
	switch {
	case n.StreamLevel && n.Card != "":
		return []string{n.Card}
	case n.StreamLevel && len(n.Suspects) > 0:
		return n.Suspects
	case n.StreamLevel:
		return n.Primaries
	case n.SprintLevel:
		return nil
	}
	return []string{primaryOfCard(o.Subject())}
}

// probeID is an id no card of the work table has, for a decision that adds a
// card whatever its name.
func probeID(s *Snapshot) string {
	id := probeIDBase
	for i := 1; s.Work.Card(id) != nil; i++ {
		id = fmt.Sprintf("%s-%d", probeIDBase, i)
	}
	return id
}

func guardRelease(s *Snapshot, o Open, _ Decision) (bool, string) {
	who := s.Coordinator
	return accepts("release", Release(s, ReleaseReq{IDs: []string{o.Subject()}, Reason: answerReason, Coordinator: who, Who: who}))
}

// guardAdd: add --before the sentinel, add the need that has no record, or add
// to the stream when the sprint is done.
func guardAdd(s *Snapshot, o Open, d Decision) (bool, string) {
	who := s.Coordinator
	switch {
	case hasArg(d, "--before"):
		c := placedPrimary(s, o.Subject())
		if c == nil {
			return false, o.Subject() + " is not on the table"
		}
		return accepts("add", Add(s, AddReq{Stream: c.Row, IDs: []string{probeID(s)}, Before: c.ID, Who: who}))
	case hasArg(d, "<n>"):
		w := placedPrimary(s, o.Subject())
		if w == nil {
			return false, o.Subject() + " is not on the table"
		}
		missing := creatableNeeds(s, w)
		if len(missing) == 0 {
			return false, o.Subject() + " names no need that has no record and a name a card can have"
		}
		return accepts("add", Add(s, AddReq{Stream: w.Row, IDs: missing[:1], Who: who}))
	}
	return accepts("add", Add(s, AddReq{Stream: probeIDBase, IDs: []string{probeID(s)}, Who: who}))
}

func guardDrop(s *Snapshot, o Open, _ Decision) (bool, string) {
	cards := distinct(cardsOf(o))
	if len(cards) == 0 {
		return false, "the judgment names no card to drop"
	}
	return acceptsSome("drop", Drop(s, DropReq{Sel: Sel{IDs: cards}, Reason: answerReason, Who: s.Coordinator}), cards)
}

func guardRework(s *Snapshot, o Open, _ Decision) (bool, string) {
	cards := distinct(cardsOf(o))
	if len(cards) == 0 {
		return false, "the judgment names no card to rework"
	}
	return acceptsSome("rework", Rework(s, ReworkReq{Sel: Sel{IDs: cards}, Fix: answerFix, Who: s.Coordinator}), cards)
}

func guardAsk(s *Snapshot, o Open, d Decision) (bool, string) {
	cards := cardsOf(o)
	if len(cards) == 0 {
		return false, "the judgment names no card to ask about"
	}
	return accepts("ask", Ask(s, AskReq{Sel: Sel{IDs: cards}, Another: hasArg(d, "--another"), Who: s.Coordinator}))
}

func guardAccept(s *Snapshot, o Open, _ Decision) (bool, string) {
	cards := cardsOf(o)
	if len(cards) == 0 {
		return false, "the judgment names no card to accept"
	}
	return accepts("accept", Accept(s, AcceptReq{Sel: Sel{IDs: cards}, Who: s.Coordinator}))
}

func guardReturn(s *Snapshot, o Open, _ Decision) (bool, string) {
	cards := distinct(cardsOf(o))
	if len(cards) == 0 {
		return false, "the judgment names no card to return"
	}
	return acceptsSome("return", Return(s, ReturnReq{Sel: Sel{IDs: cards}, Reason: answerReason, Who: s.Coordinator}), cards)
}

func guardResume(s *Snapshot, o Open, _ Decision) (bool, string) {
	return accepts("resume", Resume(s, ResumeReq{Stream: strings.TrimPrefix(o.Subject(), "stream:"), Did: answerDid, Who: s.Coordinator}))
}

func guardMerge(s *Snapshot, o Open, _ Decision) (bool, string) {
	return accepts("merge", MergeStep(s, MergeReq{Stream: strings.TrimPrefix(o.Subject(), "stream:"), Batch: answerBatch, Who: s.Coordinator}))
}

func guardRank(s *Snapshot, o Open, _ Decision) (bool, string) {
	if o.Note.Other == "" {
		return false, "the judgment names no card to rank"
	}
	return accepts("rank", Rank(s, RankReq{IDs: []string{o.Note.Other}, First: true, Who: s.Coordinator}))
}

// guardFleetUp: fleet up releases a member: the first that is not up, else the
// first, else one the fleet does not know (release adds it).
func guardFleetUp(s *Snapshot, _ Open, _ Decision) (bool, string) {
	member := probeIDBase
	rows := s.Fleet.Rows()
	if len(rows) > 0 {
		member = rows[0]
	}
	for _, m := range rows {
		if s.MemberCtl(m).F("status") != Up {
			member = m
			break
		}
	}
	return accepts("fleet up", FleetStep(s, FleetReq{Op: "release", Member: member, Who: s.Coordinator}))
}

// guardFleetDown: fleet down takes a member down: the one that holds the work
// card the judgment is on, or the one the decision names.
func guardFleetDown(s *Snapshot, o Open, d Decision) (bool, string) {
	member := ""
	if c := s.Fleet.Placed(o.Subject()); c != nil {
		member = c.Row
	} else if len(d.Args) > 0 && s.Fleet.HasRow(d.Args[0]) {
		member = d.Args[0]
	}
	if member == "" {
		return false, "no member holds " + o.Subject()
	}
	return accepts("fleet down", FleetStep(s, FleetReq{Op: "hold", Member: member, Who: s.Coordinator}))
}

func guardAck(s *Snapshot, o Open, _ Decision) (bool, string) {
	return accepts("ack", Ack(s, AckReq{Notes: []string{o.Note.ID}, Reason: answerReason, Who: s.Coordinator}))
}

// guardWait: the coordinator waits on the judgment, which Answerable is given
// as open.
func guardWait(s *Snapshot, _ Open, _ Decision) (bool, string) {
	if why := notCoordinator(s, s.Coordinator, "wait"); why != "" {
		return false, why
	}
	return true, ""
}

// guardRemove: remove is refused until layer 1's AL6 is in its contract (3).
func guardRemove(*Snapshot, Open, Decision) (bool, string) {
	return false, "remove is refused until layer 1's AL6 is in its contract"
}

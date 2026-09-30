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
//	two Decisions, since a Decision has one verb. The rows "stream stopped:
//	conflict on a card" and "stream stopped: stream branch red" offer rework
//	of a card that stands in merging, which rework refuses (return it first);
//	it is printed only for a card rework accepts, so while the stream is
//	stopped on it, never.
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
//	one verb: it is left out.
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
//	the redeal bound is the present MaxRedeals, and the attempts bound is the
//	card's field bound, which the design names (1.3.1).

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
	// Accepted is whether the decision is offered in the state s for the
	// subject the judgment is open on.
	Accepted func(s *Snapshot, subject string) (bool, string)
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

// decideWhen is a decision offered only where the condition holds.
func decideWhen(cond func(*Snapshot, string) (bool, string), verb string, args ...string) Decision {
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
		Decisions: []Decision{decide("resume", "--stream", "s", "--did"), decideWhen(stuckCardReworkable, "rework", "<card>"), decide("drop", "<card>")}},
	{Type: "stream stopped: stream branch red", Subject: subjStream, OwnerKey: keyNone, RaisedBy: []string{"merge"},
		Decisions: []Decision{decide("return", "<suspect>"), decide("resume", "--did"), decideWhen(stuckCardReworkable, "rework", "<suspect>")}},
	{Type: "stream stopped: needs a card of another stream first", Subject: subjStream, OwnerKey: keyFixed("cross"), RaisedBy: []string{"merge"},
		Decisions: []Decision{decide("rank", "<needed card>"), decide("card"), decide("return", "<card>"), decide("drop", "<card>"), decide("wait")}},
	{Type: "stream stopped: the merge queue rejected", Subject: subjStream, OwnerKey: keyNone, RaisedBy: []string{"merge"},
		Decisions: []Decision{decide("resume", "--did"), decide("return"), decide("drop")}},
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
		Decisions: []Decision{decide("card"), decide("clear", "--confirm", "<prefix>"),
			decideWhen(unread("the quarantine record"), "drop"), decide("wait")}},
	{Type: "the machine's step was refused", Subject: subjRuleKey, OwnerKey: keySelf, RaisedBy: []string{"the tick"}, Ack: true, TickKept: true,
		Decisions: []Decision{decide("log", "--since"), decide("ack"), decideWhen(unread("the machine's clock"), "stop"), decide("wait")}},
	{Type: "the sprint is done", Subject: subjSprint, OwnerKey: keyFixed("done"), RaisedBy: []string{"R15"},
		Decisions: []Decision{decide("clear"), decide("add")}},
	{Type: "a reminder could not be delivered", Subject: subjPerson, OwnerKey: keyOf("remind:"), RaisedBy: []string{"R14"}, Ack: true, TickKept: true,
		Decisions: []Decision{decide("goal set"), decide("goal drop"), decide("ack")}},
	{Type: "the machine is falling behind", Subject: subjSprint, OwnerKey: keyFixed("behind"), RaisedBy: []string{"R18"}, TickKept: true,
		Decisions: []Decision{decide("wait"), decideWhen(unread("the machine's clock"), "stop"), decide("where")}},
	{Type: "the machine is STOPPED and moves are due", Subject: subjSprint, OwnerKey: keyNone, RaisedBy: []string{"R17"}, TickKept: true,
		Decisions: []Decision{decide("start"), decide("wait", "--for", "d", "--reason")}},
	{Type: "a verb in parts stopped before its end", Subject: subjOp, OwnerKey: keyOf("late:cut:"), RaisedBy: []string{"R11"}, Ack: true, TickKept: true,
		Decisions: []Decision{decideWhen(unread("the op's verb"), "drop", "--abort", "--op", "<op>"),
			decideWhen(unread("the op's verb"), "remove", "--abort", "--op", "<op>"),
			decideWhen(unread("the op's verb"), "ack"), decide("wait")}},
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
// card the decisions its place allows first. A note that is no judgment of
// the table prints none.
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
			if d.Accepted != nil {
				if ok, _ := d.Accepted(s, subject); !ok {
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
	return out
}

// Answerable is the invariant of section 5: every decision printed for an open
// judgment is accepted by its verb's guard in the present state. A violation
// is a decision the inbox would print that its verb would refuse; the row or
// its condition is wrong, and the machine cannot be left offering it.
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

// placedPrimary is the primary the subject names, placed on the table.
func placedPrimary(s *Snapshot, id string) *Card {
	if s == nil || s.Work == nil {
		return nil
	}
	return s.Work.Placed(id)
}

// needMissing: a need the waiter names has no record and was not waived (2.2,
// "ack (waives n, while n has no record)"). Reads the waiter.
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
// waived, and have a name a card can be created under.
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
// the waiter.
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
// not even a retired read (Ask's own choice).
func freeReaders(s *Snapshot, pr *Card) []string {
	attempt := pr.Int("attempt")
	var out []string
	for _, r := range s.Readers.Rows {
		if s.Readers.Card(ReadCardID(pr.ID, attempt, r)) == nil {
			out = append(out, r)
		}
	}
	return out
}

// anotherReaderFree: ask --another is accepted for a primary in review whose
// work did not fail, already asked at its attempt, with a reader that has not
// read it and fewer than 15 read cards (2.2, 3). Reads the primary, the
// readers' rows and its read cards; the subject is the primary or one of its
// read cards.
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

// stuckCardReworkable: rework of the card a stopped stream stands on is
// accepted for a card in review or at the redeal bound; a card in merging is
// returned first (2.2 lists rework for it, and the verb refuses merging).
// Reads the stream's stuck cell and their primaries.
func stuckCardReworkable(s *Snapshot, subject string) (bool, string) {
	st := strings.TrimPrefix(subject, "stream:")
	if s.Merge != nil {
		for _, m := range s.Merge.Cell(st, Stuck) {
			if pr := placedPrimary(s, m.ID); pr != nil && (pr.Col == Review || AtRedealBound(s, pr) != nil) {
				return true, ""
			}
		}
	}
	return false, "the card stream " + st + " stopped on is in merging: rework refuses it, return it first"
}

// unread is the condition of a decision whose row depends on state a Snapshot
// does not hold: it never holds, and says what would have to be read.
func unread(what string) func(*Snapshot, string) (bool, string) {
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

// accepts is the planner's answer: accepted when it refuses nothing.
func accepts(verb string, p Plan) (bool, string) {
	if len(p.Refused) > 0 {
		return false, verb + " refuses " + p.Refused[0].Key + ": " + p.Refused[0].Why
	}
	return true, ""
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
	cards := cardsOf(o)
	if len(cards) == 0 {
		return false, "the judgment names no card to drop"
	}
	return accepts("drop", Drop(s, DropReq{Sel: Sel{IDs: cards}, Reason: answerReason, Who: s.Coordinator}))
}

func guardRework(s *Snapshot, o Open, _ Decision) (bool, string) {
	cards := cardsOf(o)
	if len(cards) == 0 {
		return false, "the judgment names no card to rework"
	}
	return accepts("rework", Rework(s, ReworkReq{Sel: Sel{IDs: cards}, Fix: answerFix, Who: s.Coordinator}))
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
	cards := cardsOf(o)
	if len(cards) == 0 {
		return false, "the judgment names no card to return"
	}
	return accepts("return", Return(s, ReturnReq{Sel: Sel{IDs: cards}, Reason: answerReason, Who: s.Coordinator}))
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
	if len(s.Fleet.Rows) > 0 {
		member = s.Fleet.Rows[0]
	}
	for _, m := range s.Fleet.Rows {
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

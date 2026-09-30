package sprint

// The shared types of the event-driven tick's upper layers (the upper design,
// version 2.1, section 8.0): what a rule plans, in the words every builder of
// the layers agrees on so that they build at the same time. There is no logic
// here, only shapes; the rules that fill them, the step builder that cuts
// them and the parts that apply them are other files.
//
// Not here: AgendaKey lives in events.go (IT01). ReadPlan and SprintQ, which a
// Rule names, are in readplan.go, and RegisterRule and RuleTable in rule.go.
//
// ReadAnswer and Answer are here because the errata to version 2.1 (E2 and E3)
// give them to IT05's plan_types.go. Their fields are the errata's. Two of the
// field types are the words of the tset package (tset.Decimal, tset.ReadAnswer),
// Layer 1's twin, which is not in this tree: Decimal and TsetAnswer below hold
// those words until it merges, and are the two types that change then.

// Now is the one reading of the clocks a tick plans against (1.2, 1.4.2): the
// store's TIME is frozen at the start of a call, so R and Wall are read once.
type Now struct {
	// R is running time in milliseconds: the wall time less the time the
	// machine has been STOPPED (R does not move while it is). The due set and
	// every deadline of a rule are in R.
	// Wall is the store's wall time in milliseconds: the cut set and the
	// STOPPED judgment's hold are in it.
	R, Wall int64
	// Running says the machine is RUNNING and not STOPPED.
	Running bool
}

// Intent is an effect that a rule cannot plan from its read and that Lua
// decides at apply, from the real before-state (1.3.3). The step builder
// carries it; the derive phase turns it into layer 1 entries.
type Intent struct {
	// Kind is the effect: waitfor, needmet, needgone or waive.
	Kind string
	// Card is the waiter.
	Card string
	// Need is the need that landed or was removed (needmet, needgone).
	Need string
	// Needs are the needs a card waits for (waitfor) or waives (waive).
	Needs []string
	// Waiters are the cards waiting for Need (needmet, needgone).
	Waiters []string
}

// XGuard is a guard on a sprint key that X checks at apply (1.3.5, 2.3): a
// plan that read the key as it was is refused XGUARD when it has moved.
type XGuard struct {
	// Kind is what is guarded: memberup, beatstale, due, hold, clock,
	// coordinator or stranger; and for R17's step the version of its dry
	// plans' inputs as read (revs, counter, ctl, beat: StopInputs, IT10).
	Kind string
	// Member is the fleet member the guard is about, when it is about one.
	Member string
	// Key is the key or entry guarded: a due set member, a hold, a field of a
	// sprint hash.
	Key string
	// Score is the score of the entry as the plan read it, where the guard is
	// on one.
	Score int64
}

// Quarantined is a card that a lower layer refused, named so that the next
// step the tick sends writes its quarantine (1.3.5). The mark is a sprint key
// written with no layer 1 entry on the card.
type Quarantined struct {
	// ID is the card, Stream its stream, Code the refusal's code and Rule the
	// rule whose plan or read met it.
	ID, Stream, Code, Rule string
	// Cells are the cells the refusal's detail names.
	Cells []string
}

// NoteReq is a request to J for a note (1.3.4): J turns the step's requests
// into notes in the pre stage, one per cause.
type NoteReq struct {
	// Op is what J is asked to do: open, close, update, hold, unhold, know
	// or request.
	Op string
	// Type is the note's type (2.2 for a judgment, 2.5 for a notice).
	Type string
	// Cause is the cause the one-per-cause rule keys on.
	Cause string
	// Subjects are the subjects the note is open on.
	Subjects []string
	// Text is the note's text.
	Text string
	// Decisions are the decisions the note offers, printed as verbs.
	Decisions []string
	// Until is a hold's R, or the review time of a note left open.
	Until int64
}

// RulePlan is what a rule plans on one tick: the plan for the tables, and
// everything besides that the step builder puts in the same requests (1.3.6).
type RulePlan struct {
	// Plan is what the rule does to the tables.
	Plan Plan
	// Intents are the effects Lua decides at apply (1.3.3).
	Intents []Intent
	// Guards are the sprint keys the plan is guarded on.
	Guards []XGuard
	// Notes are the notes the rule asks J for.
	Notes []NoteReq
	// Done are the agenda keys the plan removes in its step, and Requeue those
	// it puts back with their orders kept.
	Done    []AgendaKey
	Requeue []AgendaKey
	// Quarantine are the cards the rule found refused (1.3.5).
	Quarantine []Quarantined
	// HeldBack are the keys whose only work was in dropping streams (1.3.5):
	// they stay in the agenda, and the loop does not plan them again until a
	// dropping mark clears or a new line queues them.
	HeldBack []AgendaKey
	// Sprint are the writes to the sprint's own keys that a rule plans beside
	// its tables: R14's move of its due entry and claim on the goal record,
	// R18's re-arm, R17's clock fields and the park of 1.3.5 (TimeWrites, in
	// rules_time.go, IT10). The step that carries the plan carries them, and a
	// step that dropped them would remove the key and lose the work.
	Sprint TimeWrites
}

// ReadBounds are the limits one read of a tick is planned within (1.0, 1.4.2):
// the number of queries, the records they may return, the ids their ranges
// may name, and the bytes of the answer.
type ReadBounds struct{ Queries, Records, RangeIDs, Bytes int }

// Cost is what a query, or a read plan, may cost the store (1.0): the records
// it may return, the ids its ranges may name and the bytes of its answer.
type Cost struct{ Records, RangeIDs, Bytes int }

// Rule is one rule of the tick (2.3).
type Rule struct {
	// Name is the rule's name, the one the table of rule keys uses (2.1).
	Name string
	// Priority orders the rules, lowest first: it is the order of the tick's
	// round robin, so that every rule with keys gets a step before any gets a
	// second (1.4.2). Two rules of one priority are ordered by name.
	Priority int
	// MaxSteps is the most steps a tick gives the rule; 0 is no cap, and R19
	// is 1.
	MaxSteps int
	// Read is the plan of the read the keys need, and the keys it left for
	// later, cut to fit the read bounds; halvings is more than 0 after a BUDGET
	// or a LIMIT (1.3.5), and the read is then half its size (Halved).
	Read func(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey)
	// Plan is what the rule does on the snapshot the read loaded.
	Plan func(s *Snapshot, keys []AgendaKey, now Now) RulePlan
	// ReadFor is Read given the names the tick's own read found (TickShape), for a
	// rule whose read names a stream or a member that its keys do not (R6's
	// front(s) of every stream, 2.3 R6 "Read:"). A tick that has the shape
	// reads with ReadFor when it is set, and with Read otherwise.
	ReadFor func(keys []AgendaKey, sh TickShape, b ReadBounds, halvings int) (ReadPlan, []AgendaKey)
}

// TickShape is the names a tick's first read found, which a rule's read may name
// (open question 4): the streams, the work table's rows (1.3.1: a stream is a
// row of each table), in name order. Members is not read yet, and a read that
// is given none reads the whole fleet.
type TickShape struct{ Streams, Members []string }

// Decimal is an exact unsigned integer written as its decimal digits, the
// words of tset.Decimal: an epoch or a time that a JSON number could not carry
// exactly. The empty string is not given, and reads as 0 (Uint64, in
// partial.go).
type Decimal string

// The kinds of answer of a Layer 1 or Layer 2 query, the words of
// tset.ReadAnswer.Kind that a ReadPlan asks for. A range or an rcount named a
// key or cells in the plan; the ids of a table are one query for the table.
const (
	AnswerIDs    = "ids"
	AnswerRange  = "range"
	AnswerCount  = "count"
	AnswerRCount = "rcount"
	AnswerLines  = "lines"
)

// TsetAnswer is the answer of one Layer 1 or Layer 2 query (L1 7), the words of
// tset.ReadAnswer with the records and scores already decoded. Which of its
// fields an answer uses is its kind's:
//
//	ids     Records: one for each id the plan named for the table, in order, nil
//	        where the table has no record.
//	range   IDs and Scores in order, HasMore when members beyond them match, and
//	        Records, one for each id, when the query asked for them.
//	count   Counts: one for each cell.
//	rcount  Counts: one for each cell, and Sum, their sum.
//	lines   Lines: the log's lines, each with its stream id, which is its seq
//	        (L2 2).
//
// A record's Row and Col are empty when it is kept but not placed.
type TsetAnswer struct {
	// Kind is one of the Answer kinds; empty is the kind of the query it answers.
	Kind    string
	IDs     []string
	Scores  []float64
	HasMore bool
	Counts  []int
	Sum     int
	Records []*Card
	Lines   []LogLine
}

// LogLine is a line of the log: its stream id and its body.
type LogLine struct {
	ID   string
	Body []byte
}

// TableCard is a record with the table it belongs to.
type TableCard struct {
	Table string
	Card  *Card
}

// FrontAnswer is what `front(s)` returns of the stream's line (1.0): the first
// sentinel G and its score sigma, and the count of the stream's open cards
// before it.
type FrontAnswer struct {
	// Stream is the stream, and G the first sentinel of its sent:s index, empty
	// when it has none (then Sigma and NBefore mean nothing).
	Stream, G string
	// Sigma is G's score, and NBefore the count of the stream's five open cells
	// below it.
	Sigma   float64
	NBefore int
	// GQuarantined says G is quarantined: its record was not returned, and it
	// stays the first sentinel so that nothing behind it is released (1.0).
	GQuarantined bool
}

// CellCount is the count of the cards in one cell of a table.
type CellCount struct {
	Row, Col string
	N        int
}

// Answer is the answer of one SprintQ (1.0's table of queries), of the kind of
// its query. It holds what a partial snapshot is loaded from; the other results
// of the queries (the heads of an index, the waiters of a need, a note's
// subjects) are for IT30 and the rules that read them, who add them beside
// these.
type Answer struct {
	// Kind is the query's kind; empty is the query's.
	Kind string
	// IDs are the ids the query's source named, in order, when the plan does
	// not (a head or a line: a list of ids names them itself).
	IDs []string
	// Records are the records the query returned, each with its table.
	Records []TableCard
	// Rows are the rows of the tables a `streams`, `fleet` or `readers` query
	// lists, in the tables' order: at most the query's Units (SprintQ.units).
	Rows []string
	// HasMore says the listing has more rows than it returned, cut at its Units.
	// Such a listing is not the table's rows: they stay unread (Table.Rows,
	// Snapshot.Streams and UpMembers refuse), and only a listing that says no
	// more loads them. Only a listing has it, with at least one row.
	HasMore bool
	// Counts are the counts of the cells of the members (or readers) a `fleet`
	// (`readers`) query lists, in the table the query is over.
	Counts []CellCount
	// Front is the answer of `front`.
	Front *FrontAnswer
	// Heads are the heads of a `front` query, one for each of the query's
	// Heads in its order; their records are in Records (rules_position_read.go).
	// An answer that lists none gives the heads' records only: it loads, and a
	// position rule's plan that reads a head of it is refused.
	Heads []HeadAnswer
	// Needs are the needs of a `waiters` query, one for each id its source
	// named, in order; the waiters' records are in Records. MoreIDs says a
	// source that is a line has ids beyond the window read.
	Needs   []NeedAnswer
	MoreIDs bool
	// Stuck are the first ids of the stuck cell of each stream a `streams` query
	// found stopped on a cross need, in the streams' order.
	Stuck []StuckAnswer
	// Keys are the sprint keys the query was asked to read (SprintQ.Keys), one
	// answer for each, in order.
	Keys []KeyAnswer
	// Time is the answer of the sprint-key reads of the time rules (jnote,
	// goal, cut, tick and dropping; TimeAnswer, in rules_time.go, IT10).
	Time *TimeAnswer
}

// ReadAnswer is the answer of one atomic read (the errata to version 2.1, E3),
// aligned with the ReadPlan that asked it: the times it was read at, the answer
// of each Layer 1 and Layer 2 query in the order ReadPlan.TsetSlots gives, and
// the answer of each SprintQ, one for one. It is what LoadPartial reads.
type ReadAnswer struct {
	// Epoch is the epoch read, ActiveEpoch the sprint's active epoch, and TimeMS
	// the store's time in milliseconds, read once for the whole call (1.0).
	Epoch, ActiveEpoch, TimeMS Decimal
	// Tset answers the plan's Layer 1 and Layer 2 queries, one for each slot of
	// ReadPlan.TsetSlots, in that order (IT05's own choice: the errata say only
	// that Tset is aligned with the plan's queries, and give no order).
	Tset []TsetAnswer
	// Sprint answers the plan's SprintQ, one for each.
	Sprint []Answer
}

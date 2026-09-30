package sprint

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// Ingest, the other half of the event-driven tick's third layer (the upper
// design, version 2, sections 2.1 and 2.2, and addendum 1): the events of a
// page of the log become the keys the agenda is to hold and the touched set.
// It is pure: the same events give the same keys, in any order, however they
// are cut into pages, and a line seen twice queues what it queued once. That
// is what lets a tick that fails between its two writes (A1: the keys first,
// the cursor last) read the same lines again and add the same keys again. A
// rule is level triggered, so a key queued for a line that changes nothing is
// a rule that finds nothing and removes its key; what matters here is that no
// line that owes work is passed over, and that the keys a line queues do not
// grow with the cards it names (E6, finding B3).
//
// Every mapping is a row of a table (lineRows for 2.1, closeRows for 2.2), so
// a change to the design is a changed row. A row is in its table only when
// the design says it and a line can say its condition; the rest is a question
// to the design, never a guess, and is listed here:
//
//	the reader row added (2.1): the design says a reader row queues askwait,
//	and also (1.6) that a rows entry writes no line; no line is its event.
//	a sentinel moved, re-scored or removed (2.1, A7): a move line carries the
//	fields it set, and only the line that creates a sentinel sets its kind,
//	so no other line of a sentinel says it is one (the lifecycle lands only
//	a sentinel from waiting, but no row of the design says to read that).
//	a work or read card past its deadline (2.2): its owner key is
//	late:<kind>:<card>, and the type of the judgment (NWorkLate, NReadLate)
//	does not say which of the kinds (untaken or unfinished, unbegun or
//	unreported) it closes.
//	a reminder that could not be delivered, and a verb in parts that stopped
//	before its end (2.2): their subjects are a person and an operation, and
//	the design does not say how a note line names either.
//	a line of the readers, merge or fleet tables whose cards are open after
//	it (2.1, the last row): the held rule (R16) judges primaries, and the row
//	does not say when a work, read or merge card is open; only the lines of
//	the work table are read for it.
//	a stall or an invariant of a stream, closed (2.2): the row says the
//	subject is the card, and a stream is not one; the row queues nothing for a
//	stream's subject.
//
// A closing line that names no subject, or whose list of them was cut, queues
// the key of its rule by line (rule@seq), and the rule reads the subjects
// itself: the design does not yet say that a close line names the subjects it
// closes. The ack step's plan holds a decided note that names none (the store
// replaces it with one that names the subjects it closes), and the Redis store
// cuts the list of a note to MaxListed (Note.Bound), keeping the total in its
// count.

// The rule of a key is the name before its first ':' or '@': ask:p17 and
// ask@48213 are keys of the rule ask.
const (
	ruleResolve = "resolve"
	ruleDeal    = "deal"
	ruleAsk     = "ask"
	ruleAccept  = "accept"
	ruleRework  = "rework"
	ruleNeeds   = "needs"
	ruleCross   = "cross"
	ruleDone    = "done"
	ruleHeld    = "held"
	ruleLevel   = "level"
	ruleAskwait = "askwait"
	ruleDown    = "down"
	ruleBehind  = "behind"
	ruleLate    = "late"
)

// The types of two judgments the design names (2.2) that no rule raises yet:
// the design's own words, until the rule that raises each says its type.
const (
	typeCouldNotMove  = "the machine could not move a card"
	typeFallingBehind = "the machine is falling behind"
)

// AgendaKey is a rule key and the seq of the first line of the page that
// queued it. The design gives the agenda's order as the seq that queued a key
// (1.1), and its ingest part scores each key of a page at the page's last seq:
// which is written is the write part's to say.
type AgendaKey struct {
	Key string
	Seq uint64
}

// Ingested is what a page of events turned into. Keys are every key the
// agenda is to be given, once each, by the seq of the line that queued it
// first and then by name. Touched is the keys of the held rule among them,
// in the same order: the cards the page changed or judged, as the held rule
// is to look at them. Both are bounded by the lines, never by the cards the
// lines name.
type Ingested struct {
	Keys    []AgendaKey
	Touched []string
}

// IngestChunk is the standard chunk size for paging events during ingest:
// matching StepChunk (2,000 members).
const IngestChunk = 2000

// MaxIngestBatch is the maximum batch size for backlog draining in one operation:
// bounded to stay strictly within Layer 1 command and argv limits.
const MaxIngestBatch = 10000

// Ingest is the keys the events queue, by the tables of 2.1 and 2.2.
func Ingest(events []Event) Ingested {
	if len(events) == 0 {
		return Ingested{}
	}
	capEst := min(len(events)*2, 65536)
	first := make(map[string]uint64, capEst)
	for _, e := range events {
		recordEventKeys(e, first)
	}
	in := Ingested{
		Keys: make([]AgendaKey, 0, len(first)),
	}
	for k, s := range first {
		in.Keys = append(in.Keys, AgendaKey{Key: k, Seq: s})
	}
	slices.SortFunc(in.Keys, func(a, b AgendaKey) int {
		if a.Seq != b.Seq {
			return cmp.Compare(a.Seq, b.Seq)
		}
		return strings.Compare(a.Key, b.Key)
	})
	for _, k := range in.Keys {
		if RuleOf(k.Key) == ruleHeld {
			in.Touched = append(in.Touched, k.Key)
		}
	}
	return in
}

// recordEventKeys evaluates event rules directly into the first-seen map,
// avoiding heap allocations of intermediate slices and closures.
func recordEventKeys(e Event, first map[string]uint64) {
	for _, r := range lineRows {
		if r.when(e) {
			for _, f := range r.keys {
				if k, ok := f(e); ok {
					if s, ok := first[k]; !ok || e.Seq < s {
						first[k] = e.Seq
					}
				}
			}
		}
	}
	if e.Closes {
		for _, r := range closeRows {
			if r.has(e.NoteType) {
				for _, f := range r.keys {
					if k, ok := f(e); ok {
						if s, ok := first[k]; !ok || e.Seq < s {
							first[k] = e.Seq
						}
					}
				}
			}
		}
	}
}

// DrainBacklog partitions a large backlog of events into bounded batches of
// at most batchSize (defaulting to MaxIngestBatch when batchSize <= 0, capped
// at MaxIngestBatch). This enables pipelined backlog draining without exceeding
// Layer 1 command or argv limits.
func DrainBacklog(events []Event, batchSize int) []Ingested {
	if len(events) == 0 {
		return nil
	}
	if batchSize <= 0 || batchSize > MaxIngestBatch {
		batchSize = MaxIngestBatch
	}
	batches := make([]Ingested, 0, (len(events)+batchSize-1)/batchSize)
	for i := 0; i < len(events); i += batchSize {
		end := min(i+batchSize, len(events))
		batches = append(batches, Ingest(events[i:end]))
	}
	return batches
}

// MergeIngested merges multiple Ingested results into one, preserving the
// earliest sequence order for duplicated keys and keeping touched keys unique.
func MergeIngested(results ...Ingested) Ingested {
	first := map[string]uint64{}
	for _, res := range results {
		for _, k := range res.Keys {
			if s, ok := first[k.Key]; !ok || k.Seq < s {
				first[k.Key] = k.Seq
			}
		}
	}
	in := Ingested{
		Keys: make([]AgendaKey, 0, len(first)),
	}
	for k, s := range first {
		in.Keys = append(in.Keys, AgendaKey{Key: k, Seq: s})
	}
	slices.SortFunc(in.Keys, func(a, b AgendaKey) int {
		if a.Seq != b.Seq {
			return cmp.Compare(a.Seq, b.Seq)
		}
		return strings.Compare(a.Key, b.Key)
	})
	for _, k := range in.Keys {
		if RuleOf(k.Key) == ruleHeld {
			in.Touched = append(in.Touched, k.Key)
		}
	}
	return in
}

// RuleOf is the rule a key belongs to: its name before the first ':' or '@'.
func RuleOf(key string) string {
	if i := strings.IndexAny(key, ":@"); i >= 0 {
		return key[:i]
	}
	return key
}

// keysOf is the keys one event queues: those of every row of both tables that
// holds for it. A key two rows queue is there twice; Ingest counts it once.
func keysOf(e Event) []string {
	var keys []string
	add := func(fs []keyFunc) {
		for _, f := range fs {
			if k, ok := f(e); ok {
				keys = append(keys, k)
			}
		}
	}
	for _, r := range lineRows {
		if r.when(e) {
			add(r.keys)
		}
	}
	if e.Closes {
		for _, r := range closeRows {
			if r.has(e.NoteType) {
				add(r.keys)
			}
		}
	}
	return keys
}

// keyFunc is one key of a row, from the event: false when the event does not
// name what the key is of.
type keyFunc func(Event) (string, bool)

// fixed is the key of a rule with no subject: the sprint's own.
func fixed(rule string) keyFunc {
	return func(Event) (string, bool) { return rule, true }
}

// ofStream is the key of a rule for the stream the event is in.
func ofStream(rule string) keyFunc {
	return func(e Event) (string, bool) {
		if e.Stream == "" {
			return "", false
		}
		return rule + ":" + e.Stream, true
	}
}

// ofMember is the key of a rule for the member whose row the line is in.
func ofMember(rule string) keyFunc {
	return func(e Event) (string, bool) {
		if row := e.placeRow(); row != "" {
			return rule + ":" + row, true
		}
		return "", false
	}
}

// ofPrimaries is the key of a rule for the cards of a move line: the primary
// when the line names one card, the line (rule@seq) when it names more, so
// that a bulk move queues one key however many cards it moves (E6). A one card
// line that does not say its primary names the line too, since no key could
// name the card without it.
func ofPrimaries(rule string) keyFunc {
	return func(e Event) (string, bool) {
		switch {
		case len(e.Cards) == 0:
			return "", false
		case len(e.Cards) == 1 && e.Primary != "":
			return rule + ":" + e.Primary, true
		}
		return ofLine(rule, e.Seq), true
	}
}

// ofSubjects is the key of a rule for the subjects of a note line, as
// ofPrimaries is for the cards of a move line: the subject when the line names
// one and lists every one it is on, the line (rule@seq) when it names more.
// A line that names no subject, or whose list of them was cut (Count is more
// than the subjects listed), gets the key of the line too, never none: the
// rule reads the subjects itself. The subject of a stream or of the sprint is
// no card, and the rows that use this key are of cards: they queue nothing for
// it.
func ofSubjects(rule string) keyFunc {
	return func(e Event) (string, bool) {
		switch {
		case len(e.Subjects) == 1 && !isCard(e.Subjects[0]):
			return "", false
		case len(e.Subjects) == 1 && e.Count <= 1:
			return rule + ":" + e.Subjects[0], true
		}
		return ofLine(rule, e.Seq), true
	}
}

// isCard says a subject is a card: the subject of a stream or of the sprint
// has a colon, and no primary or stream id has one.
func isCard(subject string) bool { return !strings.Contains(subject, ":") }

// ofLine is the key that names a line: the rule, '@' and the seq.
func ofLine(rule string, seq uint64) string { return rule + "@" + strconv.FormatUint(seq, 10) }

// keyRow is one row of the design's table 2.1: the line as the design says it,
// when an event is that line, and the keys it queues.
type keyRow struct {
	line string
	when func(Event) bool
	keys []keyFunc
}

// lineRows is section 2.1, row by row and in its order.
var lineRows = []keyRow{
	{"work: any change in stream s",
		func(e Event) bool { return e.Kind == LineMove && e.Table == Work },
		[]keyFunc{ofStream(ruleResolve)}},
	{"work: a sentinel created, moved, re-scored, landed or removed in s",
		func(e Event) bool { return e.Kind == LineMove && e.Table == Work && e.sentinel() },
		[]keyFunc{ofStream(ruleResolve), fixed(ruleDeal)}},
	{"work: cards enter ready",
		func(e Event) bool { return e.enters(Work, Ready) },
		[]keyFunc{fixed(ruleDeal)}},
	{"work: cards enter review with result ok",
		func(e Event) bool { return e.enters(Work, Review) && e.Set["result"] == "ok" },
		[]keyFunc{ofPrimaries(ruleAsk)}},
	{"work: cards enter review with result failed",
		func(e Event) bool { return e.enters(Work, Review) && e.Set["result"] == "failed" },
		[]keyFunc{ofPrimaries(ruleRework)}},
	{"work: cards land",
		func(e Event) bool { return e.enters(Work, Landed) },
		[]keyFunc{ofPrimaries(ruleNeeds), fixed(ruleCross), fixed(ruleDone)}},
	{"work: cards removed",
		func(e Event) bool { return e.Kind == LineMove && e.Table == Work && e.Removed },
		[]keyFunc{ofPrimaries(ruleNeeds), fixed(ruleDone), fixed(ruleDeal)}},
	{"readers: read cards enter ok",
		func(e Event) bool { return e.enters(Readers, OK) },
		[]keyFunc{ofPrimaries(ruleAccept)}},
	{"readers: read cards enter broken",
		func(e Event) bool { return e.enters(Readers, Broken) },
		[]keyFunc{ofPrimaries(ruleRework)}},
	{"fleet: cards leave a member's ready or working cell",
		func(e Event) bool { return e.leaves(Fleet, Ready, Working) },
		[]keyFunc{fixed(ruleDeal)}},
	{"fleet: a member's control card goes up",
		func(e Event) bool { return e.control() && e.Set["status"] == Up },
		[]keyFunc{fixed(ruleDeal), fixed(ruleLevel)}},
	{"fleet: a member's control card goes down or held",
		func(e Event) bool { return e.control() && (e.Set["status"] == Down || e.Set["held"] != "") },
		[]keyFunc{ofMember(ruleDown)}},
	{"machine started",
		func(e Event) bool { return e.Kind == Happened && e.NoteType == NMachineStarted },
		[]keyFunc{fixed(ruleDeal), fixed(ruleAskwait), fixed(ruleLevel), fixed(ruleDone)}},
	{"any line whose cards are open after it",
		func(e Event) bool { return e.openAfter() },
		[]keyFunc{ofPrimaries(ruleHeld)}},
}

// closeRow is one row of the design's table 2.2: a judgment type as the design
// names it and the keys the line that closes it queues, its owner keys. The
// types are those the notes write: several for a row the design words as
// several. A row of no keys says the design queues none.
type closeRow struct {
	line  string
	types []string
	keys  []keyFunc
}

func (r closeRow) has(typ string) bool {
	for _, t := range r.types {
		if t == typ {
			return true
		}
	}
	return false
}

// closeRows is section 2.2, row by row and in its order. A line closes a
// judgment when it is decided or acknowledged; a hold that runs out queues the
// same keys (R13), which is the tick's, not ingest's. A decided line may also
// answer a judgment that stays open (a stopped stream's, until it resumes),
// and the line does not say so: it queues the owner key as well, which a rule
// finds quiet. The waive of a blocked primary changes its open count in a work
// line, and that line queues resolve:s by the first row of 2.1.
var closeRows = []closeRow{
	{"sentinel reached", []string{NSentinelReached},
		[]keyFunc{ofStream(ruleResolve)}},
	{"a primary is blocked on something dropped, or missing", []string{NBlocked, NMissingNeed},
		[]keyFunc{ofSubjects(ruleHeld)}},
	{"cannot ask", []string{NCannotAsk},
		[]keyFunc{ofSubjects(ruleAsk)}},
	{"no fleet member is up", []string{NNoMember},
		[]keyFunc{fixed(ruleDeal)}},
	{"a card reached its bound", []string{NBound},
		[]keyFunc{ofSubjects(ruleHeld)}},
	{"a stream has had no merge step past its deadline", []string{NMergeLate},
		[]keyFunc{ofStream(ruleLate + ":mergeidle")}},
	{"stream stopped (conflict, red, rejected)", []string{NConflict, NRed, NRejected},
		[]keyFunc{ofStream(ruleResolve)}},
	{"stream stopped (cross)", []string{NCross},
		[]keyFunc{ofStream(ruleResolve), fixed(ruleCross)}},
	{"the machine could not move a card; an invariant is broken; stalled; stranded in review; reads exhausted",
		[]string{typeCouldNotMove, NInvariant, NStalled, NStranded, NReadsExhausted},
		[]keyFunc{ofSubjects(ruleHeld)}},
	{"the sprint is done", []string{NSprintDone},
		[]keyFunc{fixed(ruleDone)}},
	{"the machine is falling behind", []string{typeFallingBehind},
		[]keyFunc{fixed(ruleBehind)}},
	{"the machine is STOPPED and moves are due", []string{NStoppedWithDue}, nil},
}

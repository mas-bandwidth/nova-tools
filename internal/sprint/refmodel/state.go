// Package refmodel is a reference model of nova-sprint's sprint table, written
// from the TLA+ module tla/SprintTables.tla (its sprint-tables-model branch, at
// 4bf919875) and docs/SPEC-SPRINT.md, and not from the engine. It is small and plain: one
// struct for the abstract state, and one function per action of the model,
// named as the model names it, each returning the next state or a refusal.
// Where the model leaves a choice open (which member a card is dealt to, which
// readers are asked, which score a new card takes) the function takes the
// choice as an argument and refuses a choice the model does not allow, with a
// *ChoiceError.
//
// Functions marked "from the spec, not yet in the model" cover what the
// engine does today that tla/SprintTables.tla does not yet say: sentinels,
// release, the tick's parts, the machine, clear and ci green. They are built
// from the spec's text alone.
//
// The differential test in internal/sprint/store drives the engine and this
// model with the same actions and compares Abstract(engine) with the model's
// state after every step.
//
// The other half of the package is the reference for what today's scanning
// tick decides, and it is written from the engine, not from the spec: Decide
// (decide.go) is every move one tick makes on a whole snapshot of the four
// tables, one function per duty, each calling the engine's own planners, and
// Equal (moves.go) says two decisions are the same and names the first card
// that is not. A machine that decides from events and indexes instead of
// scanning compares its decisions with these at every quiet point.
package refmodel

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Work cells: a primary's state (SprintTables.tla WorkCells), and Off for a
// primary that left the table (the model's dropped set).
const (
	Waiting = "waiting"
	Ready   = "ready"
	Working = "working"
	Review  = "review"
	Merging = "merging"
	Landed  = "landed"
	Off     = "off"
)

// Fleet places of a work card (FleetCells), Withdrawn for a card withdrawn
// because no member was up (spec section 5: kept in withdrawn), and Gone for
// a card that left the table with its record kept (the model's gone set).
const (
	FReady     = "ready"
	FWorking   = "working"
	FDone      = "done"
	FWithdrawn = "withdrawn"
	Gone       = ""
)

// Read places of a read card (ReadCells), and Retired ("") for a read card
// retired with its record kept.
const (
	Asked   = "asked"
	Reading = "reading"
	OK      = "ok"
	Broken  = "broken"
	Retired = ""
)

// Merge places of a primary (MergeCells), and Returned for one sent back from
// merging (spec section 7: the hidden returned column).
const (
	Queued   = "queued"
	Merged   = "merged"
	Stuck    = "stuck"
	Returned = "returned"
)

// Stream states (the model's sstate) and the causes of a stop (spec section
// 7: conflict on a card, the branch red, a need of another stream's card, the
// merge queue rejected).
const (
	SWaiting  = "waiting"
	SMerging  = "merging"
	SStopped  = "stopped"
	SLanded   = "landed"
	CConflict = "conflict"
	CCross    = "cross"
	CRed      = "red"
	CRejected = "rejected"
)

// Member status, the machine's states, and the kinds of a primary.
const (
	Up           = "up"
	Down         = "down"
	Running      = "RUNNING"
	Stopped      = "STOPPED"
	KindPrimary  = "primary"
	KindSentinel = "sentinel"
)

// Width is every member's width, the engine's default (sprint.DefaultWidth,
// errata 3 amendment 9): the most work cards, ready and working together, the
// tick deals a member.
const Width = 64

// MaxRedeals is the spec's redeal bound (section 2): an attempt's work card
// is dealt again at most this many times after its member went down.
const MaxRedeals = 3

// Judgment types. The model's own (SprintTables.tla CardNoteTypes and
// StopNote) are failed, broken, reads, blocked, ci, skipped and the stopped
// notes; the rest are the spec's (section 8 and section 14).
const (
	JFailed    = "failed"           // work came back failed
	JBroken    = "broken"           // a reader found it broken
	JReads     = "reads"            // reads exhausted
	JStranded  = "stranded"         // stranded in review
	JAccept    = "accept"           // ready to accept
	JReturned  = "returned"         // returned to review
	JBlocked   = "blocked"          // a primary is blocked on something dropped
	JCI        = "ci"               // ci red
	JSkipped   = "skipped"          // repair skipped changes
	JConflict  = "stopped:conflict" // stream stopped: conflict on a card
	JRed       = "stopped:red"      // stream stopped: stream branch red
	JCross     = "stopped:cross"    // stream stopped: needs a card of another stream first
	JRejected  = "stopped:rejected" // stream stopped: the merge queue rejected
	JDone      = "done"             // the sprint is done
	JReached   = "reached"          // sentinel reached
	JNoMember  = "nomember"         // no fleet member is up (the tick's)
	JCannotAsk = "cannotask"        // cannot ask (the tick's)
	JBound     = "bound"            // a card reached its bound (the tick's)
)

// Subjects that are not a primary.
const SprintSubject = "sprint:done"

// StreamSubject is a stream's subject.
func StreamSubject(s string) string { return "stream:" + s }

// Primary is one primary's abstract record.
type Primary struct {
	Stream  string
	Kind    string   // primary or sentinel
	State   string   // a work cell, or Off
	Needs   []string // sorted
	Waived  []string // sorted: the dropped needs the coordinator waived
	Score   float64
	Attempt int      // the attempt of its current or next work card, from 1
	Head    int      // the attempt whose finished work is its head; 0 before
	Pair    []string // sorted: the readers kept on it (D2)
	Reached bool     // a sentinel whose needs have all landed or been waived
}

// WorkCard is one work card: <primary>.w<attempt>.
type WorkCard struct {
	Primary string
	Attempt int
	Member  string
	Place   string // FReady, FWorking, FDone, FWithdrawn, Gone
	Gen     int
	OK      string // "", "ok" or "failed"
	// Redeals is how many times the attempt was dealt again after its
	// member went down (spec section 2): at MaxRedeals the card stays
	// withdrawn and the bound's judgment names its primary.
	Redeals int
}

// ReadCard is one read card: <primary>.r<attempt>.<reader>.
type ReadCard struct {
	Primary string
	Attempt int
	Reader  string
	Place   string // Asked, Reading, OK, Broken, Retired
	Verdict string // "", "ok" or "broken"
}

// MergeCard is one primary's merge place, and a stuck card's cross-stream
// need (the model's need[p], D6).
type MergeCard struct {
	Place string // Queued, Merged, Stuck, Returned, or Gone
	Need  string
}

// Stream is one stream's merge state and the cause of its stop.
type Stream struct {
	State string
	Cause string
}

// Judgment is one open judgment on one subject: a primary, a stream
// (StreamSubject) or the sprint (SprintSubject).
type Judgment struct {
	Type    string
	Subject string
}

func (j Judgment) String() string { return j.Type + "|" + j.Subject }

// State is the abstract state of one sprint.
type State struct {
	Primaries map[string]Primary
	Work      map[string]WorkCard
	Reads     map[string]ReadCard
	Merge     map[string]MergeCard
	Streams   map[string]Stream
	Members   map[string]string // member -> Up or Down
	Order     []string          // the fleet's members in row order
	Readers   []string          // the readers in row order
	Open      map[Judgment]bool // the open judgments (D7)
	Acked     map[Judgment]bool // the tick's conditions acknowledged while they hold
	Machine   string
	Epoch     uint64
	Pending   string // the verb of the pending operation (D1), "" when none
	// Coordinator is the one actor who releases sentinels.
	Coordinator string
	// DealLast and AskLast are the rolling indexes of the deal and the ask
	// (errata 3, amendment 5; tla/SprintEvents.tla dcur and acur): each a
	// counter, as the table's property holds it (a decimal uint64 from 0 this
	// epoch, up by one with every placement and by one for every name passed
	// over, roundPast). The next member is the first up with room from
	// DealLast modulo the members in name order, wrapping; the next readers
	// the first able from AskLast modulo the readers.
	DealLast, AskLast string
	// StreamLast, AskStreamLast and AcceptStreamLast are the work table's
	// stream indexes of the deal, the ask and the accept (errata 3 amendment
	// 10; sprint.PropStreamIndex, PropAskStreamIndex, PropAcceptStreamIndex):
	// the stream of the last primary each took. Each takes the streams in
	// turn from the first past its own, in name order, wrapping, a stream with
	// nothing to take skipped.
	StreamLast, AskStreamLast, AcceptStreamLast string
}

// New is an empty sprint with its readers and members, every member down,
// the machine STOPPED.
func New(readers, members []string, coordinator string) State {
	s := State{
		Primaries: map[string]Primary{}, Work: map[string]WorkCard{}, Reads: map[string]ReadCard{},
		Merge: map[string]MergeCard{}, Streams: map[string]Stream{}, Members: map[string]string{},
		Open: map[Judgment]bool{}, Acked: map[Judgment]bool{}, Machine: Stopped, Coordinator: coordinator,
	}
	s.Readers = append(s.Readers, readers...)
	for _, m := range members {
		s.Order = append(s.Order, m)
		s.Members[m] = Down
	}
	return s
}

// Clone is a deep copy.
func (s State) Clone() State {
	c := s
	c.Primaries = make(map[string]Primary, len(s.Primaries))
	for k, v := range s.Primaries {
		v.Needs = append([]string(nil), v.Needs...)
		v.Waived = append([]string(nil), v.Waived...)
		v.Pair = append([]string(nil), v.Pair...)
		c.Primaries[k] = v
	}
	c.Work = make(map[string]WorkCard, len(s.Work))
	for k, v := range s.Work {
		c.Work[k] = v
	}
	c.Reads = make(map[string]ReadCard, len(s.Reads))
	for k, v := range s.Reads {
		c.Reads[k] = v
	}
	c.Merge = make(map[string]MergeCard, len(s.Merge))
	for k, v := range s.Merge {
		c.Merge[k] = v
	}
	c.Streams = make(map[string]Stream, len(s.Streams))
	for k, v := range s.Streams {
		c.Streams[k] = v
	}
	c.Members = make(map[string]string, len(s.Members))
	for k, v := range s.Members {
		c.Members[k] = v
	}
	c.Order = append([]string(nil), s.Order...)
	c.Readers = append([]string(nil), s.Readers...)
	c.Open = make(map[Judgment]bool, len(s.Open))
	for k := range s.Open {
		c.Open[k] = true
	}
	c.Acked = make(map[Judgment]bool, len(s.Acked))
	for k := range s.Acked {
		c.Acked[k] = true
	}
	return c
}

// Refusal is an action the model does not allow in the state: the action's
// guard is false.
type Refusal struct{ Why string }

func (r *Refusal) Error() string { return "refused: " + r.Why }

func refuse(format string, a ...any) error { return &Refusal{Why: fmt.Sprintf(format, a...)} }

// ChoiceError is a choice the caller made for an open choice of the model
// that the model does not allow (a card dealt to a member that is not the
// next round the fleet, a read asked of a reader that is not the next round
// the readers).
type ChoiceError struct{ Why string }

func (c *ChoiceError) Error() string { return "choice not allowed: " + c.Why }

func badChoice(format string, a ...any) error { return &ChoiceError{Why: fmt.Sprintf(format, a...)} }

// Identities (spec section 2).

// WC is a work card's id.
func WC(p string, a int) string { return fmt.Sprintf("%s.w%d", p, a) }

// RC is a read card's id.
func RC(p string, a int, r string) string { return fmt.Sprintf("%s.r%d.%s", p, a, r) }

// ------------------------------------------------------------------ views

// InWork is SprintTables.tla InWork(p, c).
func (s State) InWork(p, c string) bool {
	pr, ok := s.Primaries[p]
	return ok && pr.State == c
}

// Placedp is SprintTables.tla Placedp(p): admitted and not dropped.
func (s State) Placedp(p string) bool {
	pr, ok := s.Primaries[p]
	return ok && pr.State != Off
}

// NeedsMet is SprintTables.tla NeedsMet(p): every need has landed, or was
// dropped and waived.
func (s State) NeedsMet(p string) bool {
	pr := s.Primaries[p]
	for _, q := range pr.Needs {
		if !s.InWork(q, Landed) && !has(pr.Waived, q) {
			return false
		}
	}
	return len(s.PositionWaits(p)) == 0
}

// PositionWaits is what p waits for by its place in its stream's order
// (spec section 16): a sentinel, every primary before it on the table that
// has not landed; a waiting primary, the latest unlanded sentinel before it.
// Nothing of it is stored as a need.
func (s State) PositionWaits(p string) []string {
	pr, ok := s.Primaries[p]
	if !ok || pr.State == Landed || pr.State == Off {
		return nil
	}
	var out []string
	latest := ""
	for _, q := range s.StreamOrder(pr.Stream) {
		qp := s.Primaries[q]
		if q == p || qp.State == Landed || qp.State == Off || !(qp.Score < pr.Score) {
			continue
		}
		if pr.Kind == KindSentinel {
			out = append(out, q)
		} else if qp.Kind == KindSentinel {
			latest = q
		}
	}
	if pr.Kind != KindSentinel {
		if pr.State != Waiting || latest == "" {
			return nil
		}
		return []string{latest}
	}
	return out
}

// DroppedNeeds is the needs of p that were dropped and are not waived.
func (s State) DroppedNeeds(p string) []string {
	pr := s.Primaries[p]
	var out []string
	for _, q := range pr.Needs {
		if s.InWork(q, Off) && !has(pr.Waived, q) {
			out = append(out, q)
		}
	}
	return out
}

// Up is SprintTables.tla Up, in row order.
func (s State) Up() []string {
	var out []string
	for _, m := range s.Order {
		if s.Members[m] == Up {
			out = append(out, m)
		}
	}
	return out
}

// Held is the member's work cards held against its width: ready and working.
func (s State) Held(m string) int {
	n := 0
	for _, w := range s.Work {
		if w.Member == m && (w.Place == FReady || w.Place == FWorking) {
			n++
		}
	}
	return n
}

// RL is SprintTables.tla RL(m): the member's ready queue length.
func (s State) RL(m string) int {
	n := 0
	for _, w := range s.Work {
		if w.Member == m && w.Place == FReady {
			n++
		}
	}
	return n
}

// roundFrom is where a rolling index starts in order (sorted): its counter
// modulo the number of names (errata 3 amendment 5, the owner's form: a uint64
// from 0 that goes up with every placement, modulo the count); a value that is
// not a counter (a name) starts just past that name.
func roundFrom(order []string, value string) int {
	if len(order) == 0 {
		return 0
	}
	return int(roundCount(order, value) % uint64(len(order)))
}

// roundCount is the counter a rolling index's value holds over order: its
// decimal, 0 when it is empty, and for a name the place just past it.
func roundCount(order []string, value string) uint64 {
	if value == "" {
		return 0
	}
	if n, err := strconv.ParseUint(value, 10, 64); err == nil {
		return n
	}
	i := sort.SearchStrings(order, value)
	if i < len(order) && order[i] == value {
		i++
	}
	return uint64(i)
}

// roundPast is the counter value moved past name by a placement on it: up by
// one for the placement and by one for each name passed over from where the
// index starts to reach it (SprintEvents.tla Past, on the counter).
func roundPast(order []string, value, name string) string {
	c := roundCount(order, value)
	i := sort.SearchStrings(order, name)
	if len(order) > 0 && i < len(order) && order[i] == name {
		n := len(order)
		c += uint64((i-int(c%uint64(n))+n)%n) + 1
	}
	return strconv.FormatUint(c, 10)
}

// streamOrder is the streams in name order: the ones the sprint has and extra.
func (s State) streamOrder(extra ...string) []string {
	names := s.StreamNames()
	for _, st := range extra {
		if _, ok := s.Streams[st]; !ok && !has(names, st) {
			names = append(names, st)
		}
	}
	sort.Strings(names)
	return names
}

// NextMember is the deal's choice (errata 3, amendment 5; SprintEvents.tla
// RoundAssign): the first of the members, in name order from just past
// DealLast, wrapping, that is in set; "" when none is.
func (s State) NextMember(set []string) string {
	order := sorted(s.Order)
	at := roundFrom(order, s.DealLast)
	for i := range order {
		if m := order[(at+i)%len(order)]; has(set, m) {
			return m
		}
	}
	return ""
}

// PlaceOn is the member a card placed on the fleet goes to (errata 3
// amendment 5: every placement, first attempts and redeals and levelling
// alike, goes round the fleet and moves the index; the engine's round.next):
// the next member round the fleet among set holding fewer work cards, ready
// and working, than its Width (errata 3 amendment 9), avoid only when no
// other has room; with none having room, the next of set, avoid only when it
// is the only one. "" when set is empty.
func (s State) PlaceOn(set []string, avoid string) string {
	var room []string
	for _, m := range set {
		if s.Held(m) < Width {
			room = append(room, m)
		}
	}
	for _, from := range [][]string{room, set} {
		if m := s.NextMember(without(from, avoid)); m != "" {
			return m
		}
		if has(from, avoid) {
			return avoid
		}
	}
	return ""
}

// ReworkChoice is the member a rework of p deals its next attempt to
// (PlaceOn over the members up, avoiding the member of the attempt's work
// card); "" when no member is up.
func (s State) ReworkChoice(p string) string {
	pr := s.Primaries[p]
	return s.PlaceOn(s.Up(), s.Work[WC(p, pr.Attempt)].Member)
}

// without is xs less x.
func without(xs []string, x string) []string {
	var out []string
	for _, y := range xs {
		if y != x {
			out = append(out, y)
		}
	}
	return out
}

// NextReaders is the ask's choice of k readers for p at its attempt (errata 3,
// amendment 5; SprintEvents.tla RoundTwo): from just past AskLast, in name
// order, wrapping, the first reader without a read card at the attempt, then
// the first past it, and so on.
func (s State) NextReaders(p string, k int) []string {
	order := sorted(s.Readers)
	attempt := s.Primaries[p].Attempt
	at := roundFrom(order, s.AskLast)
	var out []string
	for len(out) < k && len(order) > 0 {
		pick := -1
		for i := range order {
			j := (at + i) % len(order)
			if _, made := s.Reads[RC(p, attempt, order[j])]; !made && !has(out, order[j]) {
				pick = j
				break
			}
		}
		if pick < 0 {
			break
		}
		out = append(out, order[pick])
		at = pick + 1
	}
	return out
}

// AskedLen is SprintTables.tla AskedLen(r).
func (s State) AskedLen(r string) int {
	n := 0
	for _, c := range s.Reads {
		if c.Reader == r && c.Place == Asked {
			n++
		}
	}
	return n
}

// OutOf is SprintTables.tla OutOf(p): p's read cards asked or reading.
func (s State) OutOf(p string) []string {
	var out []string
	for id, c := range s.Reads {
		if c.Primary == p && (c.Place == Asked || c.Place == Reading) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// LiveReadsOf is p's read cards on the table in any cell (SprintTables.tla
// LiveReads restricted to p).
func (s State) LiveReadsOf(p string) []string {
	var out []string
	for id, c := range s.Reads {
		if c.Primary == p && c.Place != Retired {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Unfinished is p's work cards on a member's ready or working cell
// (SprintTables.tla Unfinished restricted to p).
func (s State) UnfinishedOf(p string) []string {
	var out []string
	for id, w := range s.Work {
		if w.Primary == p && (w.Place == FReady || w.Place == FWorking) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// OkReaders is SprintTables.tla OkReaders(p): the readers with an ok read
// card at p's head.
func (s State) OkReaders(p string) []string {
	pr := s.Primaries[p]
	var out []string
	for _, r := range s.Readers {
		if c, ok := s.Reads[RC(p, pr.Head, r)]; ok && pr.Head > 0 && c.Place == OK {
			out = append(out, r)
		}
	}
	return out
}

// Acceptable is SprintTables.tla Acceptable(p) (Broken = "none").
func (s State) Acceptable(p string) bool { return len(s.OkReaders(p)) >= 2 }

// Failed is SprintTables.tla Failed(p).
func (s State) Failed(p string) bool {
	return s.Work[WC(p, s.Primaries[p].Attempt)].OK == "failed"
}

// AskedNow is SprintTables.tla AskedNow(p): a read card of p's attempt was
// made (placed or retired).
func (s State) AskedNow(p string) bool {
	a := s.Primaries[p].Attempt
	for _, c := range s.Reads {
		if c.Primary == p && c.Attempt == a {
			return true
		}
	}
	return false
}

// OpenOn says a judgment is open on the subject.
func (s State) OpenOn(subject string) bool {
	for j := range s.Open {
		if j.Subject == subject {
			return true
		}
	}
	return false
}

// StreamOrder is the stream's primaries on the table, in work order (score,
// then id).
func (s State) StreamOrder(stream string) []string {
	var out []string
	for id, p := range s.Primaries {
		if p.Stream == stream && p.State != Off {
			out = append(out, id)
		}
	}
	s.sortByScore(out)
	return out
}

func (s State) sortByScore(ids []string) {
	sort.Slice(ids, func(i, j int) bool {
		a, b := s.Primaries[ids[i]], s.Primaries[ids[j]]
		if a.Score != b.Score {
			return a.Score < b.Score
		}
		return ids[i] < ids[j]
	})
}

// streamTurns is the ids in stream turns from a stream index, last (errata 3
// amendment 10; sprint.streamTurns): one of each stream in turn, the streams
// from the first past last in name order, wrapping, a stream with none
// skipped at no cost of a turn; within a stream by score.
func (s State) streamTurns(ids []string, last string) []string {
	by := map[string][]string{}
	for _, id := range ids {
		st := s.Primaries[id].Stream
		by[st] = append(by[st], id)
	}
	names := s.StreamNames()
	for st := range by {
		if _, ok := s.Streams[st]; !ok {
			names = append(names, st)
		}
	}
	sort.Strings(names)
	at := roundFrom(names, last)
	var streams []string
	for i := range names {
		streams = append(streams, names[(at+i)%len(names)])
	}
	for _, st := range streams {
		s.sortByScore(by[st])
	}
	out := make([]string, 0, len(ids))
	for turn := 0; len(out) < len(ids); turn++ {
		for _, st := range streams {
			if turn < len(by[st]) {
				out = append(out, by[st][turn])
			}
		}
	}
	return out
}

// MergeCell is the stream's primaries in a merge place, in work order.
func (s State) MergeCell(stream, place string) []string {
	var out []string
	for id, m := range s.Merge {
		if m.Place == place && s.Primaries[id].Stream == stream {
			out = append(out, id)
		}
	}
	s.sortByScore(out)
	return out
}

// Streams is the streams in name order.
func (s State) StreamNames() []string {
	var out []string
	for k := range s.Streams {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ------------------------------------------------------------------ helpers

func has(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func addSorted(xs []string, add ...string) []string {
	out := append([]string(nil), xs...)
	for _, a := range add {
		if !has(out, a) {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

func (s *State) setPrimary(p string, f func(*Primary)) {
	pr := s.Primaries[p]
	f(&pr)
	s.Primaries[p] = pr
}

func (s *State) open(t, subject string) { s.Open[Judgment{t, subject}] = true }

// closeOn closes every open judgment on the subject whose type is in types
// (every type when types is empty).
func (s *State) closeOn(subject string, types ...string) {
	for j := range s.Open {
		if j.Subject == subject && (len(types) == 0 || has(types, j.Type)) {
			delete(s.Open, j)
		}
	}
}

// cardJudgments is the model's card note types a primary can carry, with the
// spec's.
var stopTypes = []string{JConflict, JRed, JCross, JRejected}

// Keys lists a map's keys, sorted.
func Keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Join is a sorted list as one comma string, for printing.
func Join(xs []string) string { return strings.Join(xs, ",") }

// AtBound is the primary's withdrawn work card when it is at its redeal
// bound, "" when it is not.
func (s State) AtBound(p string) string {
	pr, ok := s.Primaries[p]
	if !ok || pr.State != Ready {
		return ""
	}
	id := WC(p, pr.Attempt)
	if w, ok := s.Work[id]; ok && w.Place == FWithdrawn && w.Redeals >= MaxRedeals {
		return id
	}
	return ""
}

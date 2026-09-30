package sprint

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The add in parts (the upper design, version 2.1, 1.5.4 "Add in parts", U2 of
// 1.3.1, and section 3's add rows; item IT19): the pure half. Part 1 reads the
// counter {p}next@e and reserves the whole op, its integer scores, its
// generated ids and gates of every stream named, and for a stream that has no
// rows its rows, its control card and streams + 1 (Reserve); every part,
// the first included, creates its slice of the op from the reservation alone
// (AddPart), and never touches the counter. A concurrent add allocates above
// the reservation, so the op's cards are contiguous and their number is the one
// asked; a rank or an insertion never chooses an integer, so it cannot take a
// reserved score (U2).
//
// Where the design is silent, or the stack's shapes do not reach, the narrower
// reading is taken and listed here; the pull request repeats them.
//
//	The ready guard. 1.5.4 guards a card created in ready with
//	S.zguard(sent:s, rcount, -inf, max score admitted, atmost 0). Layer 1's
//	zguard is a helper of its Lua (S.zguard), not an entry, so the guard rides
//	to X as IT08's SetGuard of kind zguard in an XGuard of kind setguard, which
//	IT19 adds to X (Go and Lua): RANGECOUNT, a race, when a sentinel of s was
//	placed at or below the highest score the part admits to ready since the
//	read.
//	The ids. A generated id is <stream>-<n> and a gate <stream>-gate-<n>, the
//	present build's names (AddIDs), numbered from id:<s> and gate:<s> of the
//	counter (1.3.1), each 1 when the counter has none. An id the table holds
//	(a named id added before, or a generated id a named add took) is refused
//	EXISTS from the part's read, naming the ids, before the step (the verbs'
//	addTaken). Part 1 of a --count add learns its ids from its own read, so
//	its first step finds such an id, and its retry reads the ids and refuses.
//	The first score. The counter's score is the next global score; a sprint
//	whose counter has none starts at 1.
//	The order of an op over several streams: each stream's slice whole, in the
//	order the flag names them, so the scores are contiguous over the op and in
//	line within each stream.
//	madeclose (errata 3, H3). The add that creates n closes "blocked on
//	something missing: n" on n's waiters in its own step, for the waiters its
//	part read (the head of wait:n, AddMadeLimit a need, AddMadeNotes needs a
//	part); R4's made, which the create line queues, closes any others on the
//	next tick. Part 1 of a --count add cannot read the waiters of ids it has
//	not reserved yet, so its slice's are left to R4's made. This partial form
//	is the accepted one: the judgments it leaves open last one tick, and
//	IT06's row stops printing "add n" once n exists.
//	The part's size is the chunk in changed members (1.0): a new stream's
//	control card is one, so part 1 creates that many fewer cards.

// Bounds of an add, each with the section that gives it.
const (
	// AddChunk is a part's size in changed members: StepChunk (1.0).
	AddChunk = 2000
	// AddNeedsMax is the most needs a card names (3: "add refuses more than 64
	// needs on a card").
	AddNeedsMax = 64
	// AddWalkMax is the most records the add's needs walk reads (3: "at most
	// 2,000 records, refused past it naming the chain's head").
	AddWalkMax = 2000
	// AddMadeLimit is the waiters of one created need the part reads to close
	// their "missing" judgments (H3), and AddMadeNotes the needs a part closes
	// them for: a step's notes are at most 100 (1.0), and the read's records at
	// most 10,000 (L1 7) for as many as 2,000 ids.
	AddMadeLimit = 2
	AddMadeNotes = 64
)

// AddStreams is the streams an add names: --stream s[,s...] as the flag
// writes them.
func AddStreams(r AddReq) []string { return Split(r.Stream) }

// Next is {p}next@e as a read gave it (1.3.1): the next global score, the
// stream set's version, and each stream's next generated id and gate number.
// Read is every field the read asked, as it stood ("" for a field the hash
// does not hold): what a change of the counter guards (COUNTER).
type Next struct {
	Score, Streams uint64
	IDs, Gates     map[string]uint64
	Read           map[string]string
}

// The fields of {p}next@e (1.3.1).
const (
	NextScore   = "score"
	NextStreams = "streams"
)

// NextID and NextGate are the counter's fields of a stream's next generated id
// and gate number.
func NextID(stream string) string   { return "id:" + stream }
func NextGate(stream string) string { return "gate:" + stream }

// NextFields is what an add over these streams reads of the counter.
func NextFields(streams []string) []string {
	out := []string{NextScore, NextStreams}
	for _, s := range streams {
		out = append(out, NextID(s), NextGate(s))
	}
	return out
}

// ParseNext is the counter from the fields asked and those the read found.
// A value that is not a whole number is an error: the counter is exact.
func ParseNext(asked []string, got map[string]string) (Next, error) {
	n := Next{IDs: map[string]uint64{}, Gates: map[string]uint64{}, Read: map[string]string{}}
	for _, f := range asked {
		v := got[f]
		n.Read[f] = v
		if v == "" {
			continue
		}
		x, err := strconv.ParseUint(v, 10, 64)
		if err != nil || strconv.FormatUint(x, 10) != v {
			return Next{}, fmt.Errorf("the counter's %s is %q, not a whole number", f, v)
		}
		switch {
		case f == NextScore:
			n.Score = x
		case f == NextStreams:
			n.Streams = x
		case strings.HasPrefix(f, "id:"):
			n.IDs[strings.TrimPrefix(f, "id:")] = x
		case strings.HasPrefix(f, "gate:"):
			n.Gates[strings.TrimPrefix(f, "gate:")] = x
		}
	}
	return n, nil
}

// ReservedStream is one stream's slice of an add over several: its first
// generated id's and gate's numbers, how many cards and gates it has, and
// where its first item lies in the op's sequence.
type ReservedStream struct {
	Stream    string `json:"s"`
	FirstID   uint64 `json:"i,omitempty"`
	FirstGate uint64 `json:"g,omitempty"`
	Cards     int    `json:"c"`
	Gates     int    `json:"n,omitempty"`
	At        int    `json:"a"`
}

// Reservation is what part 1 of an add reserves (1.5.4), the continuation every
// later part plans from: the first integer score, the op's items (cards and
// gates, Total), each stream's slice, the streams it opens, and the counter's
// change. An insertion reserves no integer: its scores lie between Lo and Hi
// (the anchor and its neighbour, or the next integer when the anchor has none
// after it), none an integer (U2). R is the running clock of part 1's read (a
// new stream's due_idle), and Chunk the part's size, which the caller sets for
// each part (the driver halves it after a LIMIT).
type Reservation struct {
	Base      uint64            `json:"b,omitempty"`
	Total     int               `json:"t"`
	Streams   []ReservedStream  `json:"ss"`
	New       []string          `json:"new,omitempty"`
	Counter   map[string]string `json:"-"`
	Insert    bool              `json:"ins,omitempty"`
	Lo        float64           `json:"lo,omitempty"`
	Hi        float64           `json:"hi,omitempty"`
	Anchor    string            `json:"an,omitempty"`
	Neighbour string            `json:"nb,omitempty"`
	R         int64             `json:"r,omitempty"`
	Chunk     int               `json:"-"`
}

// gatesOf is how many gates a stream of cards has with a gate after every
// every cards: none after the last unless last (3, --sentinel-last).
func gatesOf(cards, every int, last bool) int {
	if every <= 0 || cards <= 0 {
		return 0
	}
	if last {
		return cards / every
	}
	return (cards - 1) / every
}

// Reserve is what part 1 reserves from the counter as read (1.5.4): the
// integer scores score .. score + n + gates - 1, the ids id:<s> .. and the
// gates gate:<s> .. of every stream named, and the counter raised past them,
// each field guarded by its value as read. An insertion reserves no integer
// (U2): its scores are the insertion's, and it leaves the counter alone. The
// streams the add opens are marked by Open, once the read says which have no
// rows.
func Reserve(n Next, r AddReq) Reservation {
	res := Reservation{Counter: map[string]string{}}
	base := n.Score
	if base == 0 {
		base = 1
	}
	streams := AddStreams(r)
	switch {
	case r.Before != "" || r.After != "":
		res.Insert = true
		if len(streams) > 0 {
			res.Streams = []ReservedStream{{Stream: streams[0], Cards: len(r.IDs)}}
		}
		res.Total = len(r.IDs)
		res.Anchor = r.Before + r.After
		return res
	case r.Count > 0:
		at := 0
		for _, s := range streams {
			fid, fgate := max(n.IDs[s], 1), max(n.Gates[s], 1)
			g := gatesOf(r.Count, r.Every, r.Last)
			res.Streams = append(res.Streams, ReservedStream{Stream: s, FirstID: fid, FirstGate: fgate, Cards: r.Count, Gates: g, At: at})
			at += r.Count + g
			res.Counter[NextID(s)] = strconv.FormatUint(fid+uint64(r.Count), 10)
			if g > 0 {
				res.Counter[NextGate(s)] = strconv.FormatUint(fgate+uint64(g), 10)
			}
		}
		res.Total = at
	default:
		if len(streams) > 0 {
			res.Streams = []ReservedStream{{Stream: streams[0], Cards: len(r.IDs)}}
		}
		res.Total = len(r.IDs)
	}
	res.Base = base
	res.Counter[NextScore] = strconv.FormatUint(base+uint64(res.Total), 10)
	return res
}

// Open marks the streams the add creates (those with no rows as read): each
// gets its rows, its control card and streams + 1 in part 1 (3, add), the
// counter's stream-set version guarded as read.
func (res *Reservation) Open(n Next, fresh []string) {
	res.New = append([]string(nil), fresh...)
	if len(fresh) > 0 {
		if res.Counter == nil {
			res.Counter = map[string]string{}
		}
		res.Counter[NextStreams] = strconv.FormatUint(n.Streams+uint64(len(fresh)), 10)
	}
}

// PartSize is how many items the part that starts at offset k creates: the
// chunk, less the control cards of the streams part 1 opens (each a changed
// member, 1.0), and never past the op's end.
func (res Reservation) PartSize(k int) int {
	c := res.Chunk
	if c <= 0 || c > AddChunk {
		c = AddChunk
	}
	if k == 0 {
		c -= len(res.New)
	}
	return max(0, min(c, res.Total-k))
}

// AddItem is item j of the op: a card or a gate, its stream, its id and its
// score.
type AddItem struct {
	ID     string
	Stream string
	Score  float64
	Gate   bool
}

// Item is item j of the op (0-based), computed from the reservation alone: a
// --count add's stream is the one whose slice holds j, and within it a gate
// follows every Every cards; a named add's id is the j-th named; an
// insertion's score is InsertScores' j-th. scores is the insertion's scores
// (nil otherwise).
func (res Reservation) Item(r AddReq, j int, scores []float64) AddItem {
	if res.Insert {
		return AddItem{ID: r.IDs[j], Stream: res.Streams[0].Stream, Score: scores[j]}
	}
	score := float64(res.Base + uint64(j))
	if r.Count <= 0 {
		return AddItem{ID: r.IDs[j], Stream: res.Streams[0].Stream, Score: score}
	}
	for _, rs := range res.Streams {
		if j < rs.At || j >= rs.At+rs.Cards+rs.Gates {
			continue
		}
		l := j - rs.At
		if r.Every <= 0 {
			return AddItem{ID: fmt.Sprintf("%s-%d", rs.Stream, rs.FirstID+uint64(l)), Stream: rs.Stream, Score: score}
		}
		block, off := l/(r.Every+1), l%(r.Every+1)
		if off < r.Every {
			n := uint64(block*r.Every + off)
			return AddItem{ID: fmt.Sprintf("%s-%d", rs.Stream, rs.FirstID+n), Stream: rs.Stream, Score: score}
		}
		return AddItem{ID: gatePrefix(rs.Stream) + strconv.FormatUint(rs.FirstGate+uint64(block), 10), Stream: rs.Stream, Score: score, Gate: true}
	}
	return AddItem{}
}

// ErrNoRoom is an insertion or a rank with no score between its neighbours
// that is not an integer: refused before any write, naming rank (1.5.4).
var ErrNoRoom = errors.New("no score lies between")

// InsertScores are n scores evenly between lo and hi, strictly between them
// and increasing, none an integer (U2: an integer value is skipped, taking the
// midpoint of it and the value below it). ErrNoRoom when the floats between
// them cannot hold n such scores.
func InsertScores(lo, hi float64, n int) ([]float64, error) {
	out := make([]float64, n)
	prev := lo
	for j := 0; j < n; j++ {
		v := lo + (hi-lo)*float64(j+1)/float64(n+1)
		if v == math.Trunc(v) {
			v = prev + (v-prev)/2
		}
		if !(v > prev && v < hi) || v == math.Trunc(v) || math.IsInf(v, 0) || math.IsNaN(v) {
			return nil, fmt.Errorf("%w %s and %s for %d cards", ErrNoRoom, fmtScore(lo), fmtScore(hi), n)
		}
		out[j] = v
		prev = v
	}
	return out, nil
}

// CheckAdd refuses an add whose request cannot be planned, before anything is
// read: the caller's fault (REQUEST), each naming what to fix.
func CheckAdd(r AddReq) error {
	streams := AddStreams(r)
	if len(streams) == 0 {
		return errors.New("add names no stream: --stream s[,s...]")
	}
	seen := map[string]bool{}
	for _, s := range streams {
		if !ValidID(s) || strings.HasPrefix(s, "ctl-") {
			return fmt.Errorf("stream %q wants letters, digits, _ and -", s)
		}
		if seen[s] {
			return fmt.Errorf("stream %s is named twice", s)
		}
		seen[s] = true
	}
	insert := r.Before != "" || r.After != ""
	switch {
	case r.Before != "" && r.After != "":
		return errors.New("--before and --after are one or the other")
	case r.Count < 0:
		return errors.New("--count is a number of cards, at least 1")
	case r.Count > 0 && len(r.IDs) > 0:
		return errors.New("--count generates the ids; named ids and --count are one or the other")
	case r.Count == 0 && len(r.IDs) == 0:
		return errors.New("add names no card: ids, or --count n")
	case len(streams) > 1 && r.Count == 0:
		return errors.New("several streams go with --count; named ids go to one stream")
	case r.Every < 0:
		return errors.New("--sentinel-every is a number of cards, at least 1")
	case (r.Every > 0 || r.Last) && r.Count == 0:
		return errors.New("--sentinel-every and --sentinel-last go with --count")
	case r.Last && r.Every == 0:
		return errors.New("--sentinel-last goes with --sentinel-every")
	case insert && r.Count > 0:
		return errors.New("--before and --after place named ids; --count adds at the end of the stream")
	case r.Sentinel && len(r.IDs) != 1:
		return errors.New("a sentinel is added one at a time: add --stream s --sentinel <id>")
	case len(r.Needs) > AddNeedsMax:
		return fmt.Errorf("a card names at most %d needs; this add names %d", AddNeedsMax, len(r.Needs))
	}
	ids := map[string]bool{}
	for _, id := range r.IDs {
		if !ValidID(id) || strings.HasPrefix(id, "ctl-") {
			return fmt.Errorf("id %q wants letters, digits, _ and -, and does not start with ctl-", id)
		}
		if ids[id] {
			return fmt.Errorf("id %s is named twice", id)
		}
		ids[id] = true
	}
	needs := map[string]bool{}
	for _, n := range r.Needs {
		if !ValidID(n) {
			return fmt.Errorf("need %q is not a card's id", n)
		}
		if needs[n] {
			return fmt.Errorf("need %s is named twice", n)
		}
		if ids[n] {
			return fmt.Errorf("the needs would make a cycle: %s needs %s; nothing was changed", n, n)
		}
		needs[n] = true
	}
	if a := r.Before + r.After; insert && ids[a] {
		return fmt.Errorf("the anchor %s is one of the ids added", a)
	}
	return nil
}

// needOpen says a need counts in its waiter's open (1.3.3: open on the table,
// no record, or removed; a landed need does not), as the part read it.
func needOpen(s *Snapshot, stored string) bool {
	c := s.Work.Card(stored)
	return c == nil || !c.Placed() || c.Col != Landed
}

// AddPart is the part of the add at offset k of the op (1.5.4): the slice
// k .. k + PartSize(k) - 1, each card created in waiting or ready (a card that
// names needs, a gate, a sentinel, and a card behind an unlanded sentinel of
// its stream, an earlier gate of this op or an older one read as σ, go to
// waiting; the others to ready), with a waitfor intent for each card with
// needs (1.3.3), which carries its needs and its open as the read found them;
// and for k = 0, the rows and control card of each stream it opens. It reads
// the snapshot's front(s) of each stream of the slice, and the record of each
// need; anything else it would read is refused by the snapshot (1.5.2). The
// card ids and the needs are the stored ids at the snapshot's epoch
// (StoredID). The plan's units are one a card, the control cards first.
func AddPart(s *Snapshot, r AddReq, res Reservation, k int) (Plan, []Intent, error) {
	var p Plan
	p.on(s)
	size := res.PartSize(k)
	if k < 0 || k > res.Total || (size == 0 && !(k == 0 && res.Total == 0)) {
		return p, nil, fmt.Errorf("add: part at %d of an op of %d plans nothing", k, res.Total)
	}
	var scores []float64
	if res.Insert {
		var err error
		if scores, err = InsertScores(res.Lo, res.Hi, res.Total); err != nil {
			return p, nil, err
		}
	}
	if k == 0 {
		for _, st := range res.New {
			for _, t := range []string{Work, Merge} {
				p.Rows = append(p.Rows, RowAdd{t, st})
			}
			ctl := map[string]string{"kind": "stream", "state": StreamWaiting}
			if res.R > 0 {
				ctl[fieldDueIdle] = strconv.FormatInt(res.R+ruleIdleSpan.Milliseconds(), 10)
			}
			p.Units = append(p.Units, Unit{Key: CtlID(st), Stream: st,
				Changes: []Change{change(Merge, createEntry(StoredID(CtlID(st), s.Epoch), st, Ctl, 0, ctl))},
				Moved:   "stream " + st + " opened"})
		}
	}
	var needs []string
	open := 0
	for _, n := range r.Needs {
		stored := StoredID(n, s.Epoch)
		needs = append(needs, stored)
		if needOpen(s, stored) {
			open++
		}
	}
	var intents []Intent
	gateSeen := map[string]bool{}
	for j := k; j < k+size; j++ {
		it := res.Item(r, j, scores)
		id := StoredID(it.ID, s.Epoch)
		col, kind := Ready, "primary"
		why := ""
		switch {
		case it.Gate || r.Sentinel:
			col, kind, why = Waiting, Sentinel, "a sentinel"
		case len(needs) > 0:
			col, why = Waiting, "it names needs"
		case gateSeen[it.Stream]:
			col, why = Waiting, "behind a gate of this add"
		default:
			if st := FirstSentinel(s, it.Stream); st != nil && st.Score < it.Score {
				col, why = Waiting, "behind sentinel "+CardID(st.ID)
			}
		}
		if it.Gate {
			gateSeen[it.Stream] = true
		}
		fields := map[string]string{"kind": kind, "stream": it.Stream, "attempt": "0"}
		if r.Brief != "" && !it.Gate {
			fields["brief"] = r.Brief
		}
		// a named sentinel names its needs as any card does (the model's NeedsOf
		// and AddDest; R3's reach reads its open); a gate of a --count add names
		// none, its cards carry them
		if len(needs) > 0 && !it.Gate {
			fields["needs"] = strings.Join(needs, ",")
			fields["open"] = strconv.Itoa(open)
			intents = append(intents, Intent{Kind: "waitfor", Card: id, Needs: append([]string(nil), needs...)})
		}
		moved := fmt.Sprintf("%s -> %s stream=%s score=%s", it.ID, col, it.Stream, fmtScore(it.Score))
		if why != "" {
			moved += " (" + why + ")"
		}
		p.Units = append(p.Units, Unit{Key: id, Stream: it.Stream,
			Changes: []Change{change(Work, createEntry(id, it.Stream, col, it.Score, fields))}, Moved: moved})
	}
	if reads := s.Unloaded(); len(reads) > 0 {
		return Plan{}, nil, &UnloadedError{Reads: reads}
	}
	return p, intents, nil
}

// AddGuards are the set guards of a part's plan (1.5.4): for each stream the
// plan creates a card of in ready, S.zguard(sent:s, rcount, -inf, the highest
// score it admits to ready, atmost 0), so no sentinel was placed before a card
// admitted to ready since the read; for an insertion, the rcount of the
// stream's six cells in (Lo, Hi) equal to the cards the op created before this
// part (1.5.4: "0 at part 1"), so no card came between the anchor and its
// neighbour. The first is a zguard, which rides to X; the second Layer 1's
// rcount entry.
func AddGuards(p Plan, res Reservation, k int) []SetGuard {
	var out []SetGuard
	high := map[string]float64{}
	var order []string
	for _, u := range p.Units {
		for _, c := range u.Changes {
			cr := c.Entry.Create
			if c.Table != Work || cr == nil || cr.Col != Ready {
				continue
			}
			if v, ok := high[cr.Row]; !ok || cr.Score > v {
				if !ok {
					order = append(order, cr.Row)
				}
				high[cr.Row] = cr.Score
			}
		}
	}
	for _, st := range order {
		out = append(out, SetGuard{Kind: GuardZGuard, Key: IndexSent + ":" + st, Min: "-inf", Max: fmtScore(high[st]), AtMost: posInt(0)})
	}
	if res.Insert {
		cells := make([]string, 0, len(States))
		for _, c := range States {
			cells = append(cells, res.Streams[0].Stream+":"+string(c))
		}
		out = append(out, SetGuard{Kind: GuardRCount, Table: Work, Cells: cells,
			Min: "(" + fmtScore(res.Lo), Max: "(" + fmtScore(res.Hi), AtLeast: posInt(k), AtMost: posInt(k)})
	}
	return out
}

// AddMadeCloses are the closes of "blocked on something missing: n" for the
// waiters the part's read found of each id it creates that has a score in
// {p}missing@e (errata 3, H3 madeclose): one close a need, its waiters the
// subjects, at most AddMadeNotes needs. It reads the part's `waiters` query of
// Missing kind (AddMadeQ), for the ids the part creates (made: a read may ask
// more); a snapshot whose read asked none gives none.
func AddMadeCloses(s *Snapshot, made map[string]bool) []NoteReq {
	if s == nil || s.Partial == nil {
		return nil
	}
	var out []NoteReq
	for i, q := range s.Partial.Plan.Sprint {
		if q.Kind != QueryWaiters || !q.Missing || i >= len(s.Partial.Answer.Sprint) {
			continue
		}
		for _, n := range s.Partial.Answer.Sprint[i].Needs {
			if !n.Missing || len(n.Waiters) == 0 || !made[n.ID] || len(out) >= AddMadeNotes {
				continue
			}
			out = append(out, NoteReq{Op: "close", Type: NMissingNeed, Cause: n.ID, Subjects: append([]string(nil), n.Waiters...),
				Text: CardID(n.ID) + " was added: its waiters wait for it to land"})
		}
	}
	return out
}

// AddReachedCloses are the closes of "sentinel reached" a part makes (errata
// 3, H13 rankclose in the model's form, amendment 2; SprintEvents.tla
// ReachedPassed): a part that places a card of a stream before its first
// sentinel closes that sentinel's reached judgment in its own step, since the
// sentinel is no longer reached. Only an insertion places before a sentinel:
// an add's integer scores lie above every placed card (U2). It reads front(s)
// of the part's stream; a close of a judgment that is not open changes
// nothing.
func AddReachedCloses(s *Snapshot, r AddReq, res Reservation, k int) []NoteReq {
	if !res.Insert {
		return nil
	}
	scores, err := InsertScores(res.Lo, res.Hi, res.Total)
	if err != nil {
		return nil
	}
	st := res.Streams[0].Stream
	g := FirstSentinel(s, st)
	if g == nil {
		return nil
	}
	for j := k; j < k+res.PartSize(k); j++ {
		if scores[j] < g.Score {
			return []NoteReq{{Op: "close", Type: NSentinelReached, Cause: ReachedCause, Subjects: []string{g.ID},
				Text: fmt.Sprintf("%s was placed before it", r.IDs[j])}}
		}
	}
	return nil
}

// AddMadeQ is the part's read of the waiters of the ids it creates that have a
// score in {p}missing@e (H3): the head of wait:n, AddMadeLimit a need.
func AddMadeQ(ids []string) SprintQ {
	return SprintQ{Kind: QueryWaiters, Source: IDSource{Kind: SourceIDs, IDs: ids}, Fields: []string{"kind"},
		Limit: AddMadeLimit, Missing: true}
}

// AddCycle is the add's early check of a needs cycle (3: "checked over the
// whole named set and every existing card that names a new id ... before any
// part applies"), from the needchain the part-1 read walked from the named
// needs: a card the walk reached that names one of the new ids closes a cycle
// through it, and a walk cut at its bound is refused naming the bound and the
// chain's head. chain is the walk's cards with the needs each names; makes
// says a stored id is one the add creates (Reservation.Makes). "" is no cycle.
func AddCycle(chain []ChainCard, cut bool, head string, makes func(stored string) bool) string {
	for _, c := range chain {
		for _, n := range c.Needs {
			if makes(n) {
				return fmt.Sprintf("the needs would make a cycle: %s needs %s, which this add creates with those needs; nothing was changed", CardID(c.ID), CardID(n))
			}
		}
	}
	if cut {
		return fmt.Sprintf("the needs walk reached its bound of %d records from %s, the chain's head, before it ended (1.3.3); nothing was changed", AddWalkMax, CardID(head))
	}
	return ""
}

// Makes says the op creates the card with this id (plain, as the request and
// the counter name it): a named id, or a generated one in a stream's reserved
// range (a gate names no needs, so no cycle runs through one).
func (res Reservation) Makes(r AddReq, id string) bool {
	if r.Count <= 0 {
		for _, x := range r.IDs {
			if x == id {
				return true
			}
		}
		return false
	}
	for _, rs := range res.Streams {
		n, err := strconv.ParseUint(strings.TrimPrefix(id, rs.Stream+"-"), 10, 64)
		if err == nil && strings.HasPrefix(id, rs.Stream+"-") && n >= rs.FirstID && n < rs.FirstID+uint64(rs.Cards) {
			return true
		}
	}
	return false
}

// ChainCard is one card a needchain walk read, with the needs it names.
type ChainCard struct {
	ID    string
	Needs []string
}

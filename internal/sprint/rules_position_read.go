package sprint

import (
	"slices"
	"strconv"
)

// The read seam of the position rules (the upper design, version 2.1, IT08): what
// the composite queries `front`, `waiters` and `streams` answer beyond what a
// partial snapshot's tables hold, how a snapshot keeps it, and the reads through
// which a plan takes it. IT05 owns the partial snapshot and says the other
// results of a query are for the rules that read them (Answer); the types here
// are those results, and the fields that carry them are Answer's and SprintQ's.
//
// A plan reads them only through the accessors below (posFrontOf, posHeadOf,
// posKeyViewOf, posStuckOf, posDropping, posJudgedG, posJudgedSprint,
// posNextStreams, posStreamsRead). Each is refused, like a cell that was not
// loaded, when the plan's read did not ask for what it reads: the read is put in
// the snapshot's log (a panic in a test build, Snapshot.Unloaded in a release
// build), and the accessor answers as if there were nothing, so a key is never
// planned on an answer its read did not ask for.

// The sprint keys a composite query may also read (SprintQ.Keys; the errata to
// version 2.1 add the sprint-key reads to the seam of E6).
const (
	// KeyJOpenG is jopen of the first sentinel of the stream a `front` query
	// is about: the judgments open, and held, on it (2.3 R3).
	KeyJOpenG = "jopen:G"
	// KeyJOpenSprint is jopen:sprint (2.3 R15).
	KeyJOpenSprint = "jopen:sprint"
	// KeyDropping is {p}dropping@e as far as the query reaches: for `front`, the
	// stream; for `waiters`, the streams of the waiters returned; for
	// `streams`, every stream it lists (1.3.5).
	KeyDropping = "dropping"
	// KeyNextStreams is {p}next@e.streams, the version of the stream set (2.3
	// R15).
	KeyNextStreams = "next.streams"
)

// sprintKeyBytes is the bytes a sprint key's answer is estimated to carry: a few
// names, since the judgments open on one subject and the streams being dropped
// are few (1.3.5). The design gives none; an over-estimate cuts a read smaller
// and never refuses it.
const sprintKeyBytes = 512

// sprintKeysCost is what the sprint keys and the counts a query also reads may
// cost the store, from its arguments alone (QueryCost adds it to the query's).
func sprintKeysCost(q SprintQ) Cost {
	c := Cost{Bytes: len(q.Keys) * sprintKeyBytes}
	if q.Kind == QueryStreams && len(q.Counts) > 0 {
		c.Bytes += unitsOr(q.Units, MaxStreams) * len(q.Counts) * CountBytes
	}
	return c
}

// HeadAnswer is one head of a `front` query: the index it is the head of
// (HeadQ.Index), its first ids in order (their records are in the answer's
// Records), and whether the index has members beyond them.
type HeadAnswer struct {
	Index string
	IDs   []string
	More  bool
}

// NeedAnswer is the answer of `waiters` for one id n of its source: n's place
// in the work table ("" when it has none), whether it has a score in
// {p}missing@e, and the head of wait:n after the query's WaiterOffset, up to its
// Limit, whose records are in the answer's Records. More says wait:n has members
// beyond the head. A query that reads only the ids in {p}missing@e (Missing)
// leaves Waiters empty for the others.
type NeedAnswer struct {
	ID      string
	Place   string
	Missing bool
	Waiters []string
	More    bool
}

// StuckAnswer is the first ids of a stream's stuck cell, by score, that a
// `streams` query read for a stream stopped on a cross need; More says the cell
// has more.
type StuckAnswer struct {
	Stream string
	IDs    []string
	More   bool
}

// KeyAnswer is the answer of one sprint key of a query's Keys, in the same order:
// for a jopen key the subject whose jopen it is and the types of the judgments
// open on it and of those held; for KeyDropping the streams among those the
// query reaches that are being dropped; for KeyNextStreams the version.
type KeyAnswer struct {
	Key     string
	Subject string
	Open    []string
	Held    []string
	Streams []string
	N       uint64
}

// posLoaded is what a snapshot loaded from a read plan holds of the position
// rules' queries, built by loadPosition as the answers are loaded.
type posLoaded struct {
	// front is the place in the plan's Sprint of the `front` query of each
	// stream, waiters the place of the `waiters` query of each agenda key (by its
	// text), and stuck the stuck cell each stream's `streams` answer gave.
	front   map[string]int
	waiters map[string]int
	stuck   map[string]StuckAnswer
	// streams says a `streams` query was answered.
	streams bool
	// jopen are the judgments open on the subjects a jopen key read, dropping the
	// streams being dropped among those any query reached, and askedDropping says
	// some query read the marks at all. next is {p}next@e.streams when read.
	jopen         map[string]KeyAnswer
	dropping      map[string]bool
	askedDropping bool
	next          uint64
	hasNext       bool
}

func (p *Partial) position() *posLoaded {
	if p.pos == nil {
		p.pos = &posLoaded{front: map[string]int{}, waiters: map[string]int{}, stuck: map[string]StuckAnswer{},
			jopen: map[string]KeyAnswer{}, dropping: map[string]bool{}}
	}
	return p.pos
}

// posQueryKey is the agenda key a `waiters` query reads for: the text of the
// key the rule built it from, so that a plan finds the answer of its key.
func posQueryKey(q SprintQ) string {
	rule := ruleNeeds
	if q.Missing {
		rule = posMadeRule
	}
	if q.Source.Kind == SourceLine {
		return posLineKey(rule, q.Source.Seq, max(q.Source.Offset, 0), 0).Key
	}
	if len(q.Source.IDs) != 1 {
		return ""
	}
	return posNeedKey(rule, q.Source.IDs[0], q.WaiterOffset, 0).Key
}

// loadPosition loads what the answer of query i holds for the position rules and
// checks that it answers the query: heads and needs aligned with what was asked,
// every id of a head or a waiters' head with its record, and every sprint key
// asked answered and none besides. An answer that does not is ErrMisaligned, and
// nothing is planned on it (1.5.1).
func (p *Partial) loadPosition(s *Snapshot, i int, q SprintQ, a Answer) error {
	pos := p.position()
	if err := pos.loadKeys(i, q, a); err != nil {
		return err
	}
	hasRecord := func(id string) bool { return s.Work.Card(id) != nil }
	switch q.Kind {
	case QueryFront:
		if len(a.Heads) != len(q.Heads) {
			return misaligned("composite query %d asked %d heads of %q, %d answered", i, len(q.Heads), q.Stream, len(a.Heads))
		}
		for j, h := range a.Heads {
			if h.Index != q.Heads[j].Index || len(h.IDs) > q.Heads[j].Limit {
				return misaligned("composite query %d: head %d is %q with %d ids, asked %q up to %d", i, j, h.Index, len(h.IDs), q.Heads[j].Index, q.Heads[j].Limit)
			}
			for _, id := range h.IDs {
				if !hasRecord(id) {
					return misaligned("composite query %d: %q heads %s of %s and its record is not in the answer", i, id, h.Index, q.Stream)
				}
			}
		}
		pos.front[q.Stream] = i
	case QueryWaiters:
		ids := q.Source.IDs
		if q.Source.Kind == SourceIDs {
			if len(a.IDs) != 0 && !slices.Equal(a.IDs, ids) {
				return misaligned("composite query %d names ids, and its answer names others", i)
			}
		} else {
			ids = a.IDs
			if most, _ := q.Source.size(); len(ids) > most {
				return misaligned("composite query %d: its source names at most %d ids, the answer names %d", i, most, len(ids))
			}
		}
		if len(a.Needs) != len(ids) {
			return misaligned("composite query %d names %d ids, %d needs answered", i, len(ids), len(a.Needs))
		}
		for j, n := range a.Needs {
			if n.ID != ids[j] || len(n.Waiters) > q.Limit {
				return misaligned("composite query %d: need %d is %q with %d waiters, the id is %q and the head is %d", i, j, n.ID, len(n.Waiters), ids[j], q.Limit)
			}
			for _, w := range n.Waiters {
				if !hasRecord(w) {
					return misaligned("composite query %d: %q waits for %s and its record is not in the answer", i, w, n.ID)
				}
			}
		}
		if key := posQueryKey(q); key != "" {
			pos.waiters[key] = i
		}
	case QueryStreams:
		pos.streams = true
		listed := map[string]bool{}
		for _, r := range a.Rows {
			listed[r] = true
		}
		for _, st := range a.Stuck {
			if !listed[st.Stream] || len(st.IDs) > q.Limit {
				return misaligned("composite query %d: %d stuck ids of %q, asked up to %d of a stream it lists", i, len(st.IDs), st.Stream, q.Limit)
			}
			pos.stuck[st.Stream] = st
		}
		if want := len(a.Rows) * len(q.Counts); len(q.Counts) > 0 && len(a.Counts) != want {
			return misaligned("composite query %d asked the counts of %d columns of %d streams, %d answered", i, len(q.Counts), len(a.Rows), len(a.Counts))
		}
		if len(q.Counts) == 0 && len(a.Counts) > 0 {
			return misaligned("composite query %d gives counts and asked for none", i)
		}
	}
	return nil
}

// loadKeys keeps the sprint keys a query answered.
func (pos *posLoaded) loadKeys(i int, q SprintQ, a Answer) error {
	if len(a.Keys) != len(q.Keys) {
		return misaligned("composite query %d asked %d sprint keys, %d answered", i, len(q.Keys), len(a.Keys))
	}
	for j, k := range a.Keys {
		if k.Key != q.Keys[j] {
			return misaligned("composite query %d: sprint key %d is %q, asked %q", i, j, k.Key, q.Keys[j])
		}
		switch k.Key {
		case KeyJOpenG:
			if q.Kind != QueryFront || a.Front == nil || k.Subject != a.Front.G {
				return misaligned("composite query %d: %s names %q, and the first sentinel is not that", i, k.Key, k.Subject)
			}
			if k.Subject != "" {
				pos.jopen[k.Subject] = k
			}
		case KeyJOpenSprint:
			if k.Subject != SprintSubject {
				return misaligned("composite query %d: %s names %q", i, k.Key, k.Subject)
			}
			pos.jopen[k.Subject] = k
		case KeyDropping:
			pos.askedDropping = true
			for _, st := range k.Streams {
				pos.dropping[st] = true
			}
		case KeyNextStreams:
			pos.next, pos.hasNext = k.N, true
		default:
			return misaligned("composite query %d asks the sprint key %q, which no query reads", i, k.Key)
		}
	}
	return nil
}

// The accessors. A snapshot built whole has none of this (its planners read
// cells): they answer as if the read had not asked, without a note.

// posOf is the position rules' answers a snapshot holds, nil for a snapshot
// that was not loaded from a plan.
func posOf(s *Snapshot) *posLoaded {
	if s == nil || s.Partial == nil {
		return nil
	}
	return s.Partial.position()
}

// posNotLoaded says a plan read what its read did not ask for.
func posNotLoaded(s *Snapshot, what string) {
	if s != nil && s.Partial != nil {
		s.Partial.log.note("the planner asked for " + what + " its plan did not load")
	}
}

// posFront is what a plan reads of front(s), less its heads (posHeadOf): the
// first sentinel (nil when the stream has none), the count of the stream's open
// cards before it, and whether it is quarantined.
type posFront struct {
	G            *Card
	NBefore      int
	GQuarantined bool
}

// posFrontOf is front(s) as the snapshot holds it, false when the read did not
// ask it: a key with no answer is not planned, since a partial reply is never
// planned on (1.5.1).
func posFrontOf(s *Snapshot, stream string) (posFront, bool) {
	pos := posOf(s)
	if pos == nil {
		return posFront{}, false
	}
	if _, ok := pos.front[stream]; !ok {
		posNotLoaded(s, "front of "+stream+", which")
		return posFront{}, false
	}
	f := posFront{G: FirstSentinel(s, stream), GQuarantined: s.Partial.Fronts[stream].GQuarantined}
	if f.G != nil {
		f.NBefore = OpenBefore(s, stream, f.G.Score)
	}
	return f, true
}

// posHeadOf is the head of front(s) at the index, with its records in order and
// whether the index has more beyond it. A head the read did not ask for is
// refused.
func posHeadOf(s *Snapshot, stream, index string) (cards []*Card, more bool) {
	pos := posOf(s)
	if pos == nil {
		return nil, false
	}
	i, ok := pos.front[stream]
	if !ok {
		posNotLoaded(s, "front of "+stream+", which")
		return nil, false
	}
	for _, h := range s.Partial.Answer.Sprint[i].Heads {
		if h.Index != index {
			continue
		}
		for _, id := range h.IDs {
			cards = append(cards, s.Work.Card(id))
		}
		return cards, h.More
	}
	posNotLoaded(s, "the head "+index+" of "+stream+", which")
	return nil, false
}

// posJudgedG says a judgment of the type is open, or held by the coordinator,
// on the stream's first sentinel (jopen:G, read with front(s)).
func posJudgedG(s *Snapshot, stream, typ string) bool {
	pos := posOf(s)
	if pos == nil {
		return false
	}
	i, ok := pos.front[stream]
	if !ok || !slices.Contains(s.Partial.Plan.Sprint[i].Keys, KeyJOpenG) {
		posNotLoaded(s, "jopen of the first sentinel of "+stream+", which")
		return false
	}
	g := s.Partial.Fronts[stream].G
	return g != "" && posJudgedOn(pos.jopen[g], typ)
}

// posJudgedSprint says a judgment of the type is open, or held, on the sprint
// (jopen:sprint).
func posJudgedSprint(s *Snapshot, typ string) bool {
	pos := posOf(s)
	if pos == nil {
		return false
	}
	k, ok := pos.jopen[SprintSubject]
	if !ok {
		posNotLoaded(s, KeyJOpenSprint+", which")
		return false
	}
	return posJudgedOn(k, typ)
}

func posJudgedOn(k KeyAnswer, typ string) bool {
	return slices.Contains(k.Open, typ) || slices.Contains(k.Held, typ)
}

// posDropping says the stream is being dropped or removed ({p}dropping@e). A
// plan whose read did not ask for the marks is refused.
func posDropping(s *Snapshot, stream string) bool {
	pos := posOf(s)
	if pos == nil {
		return false
	}
	if !pos.askedDropping {
		posNotLoaded(s, "the dropping marks, which")
		return false
	}
	return pos.dropping[stream]
}

// posNextStreams is {p}next@e.streams as the read saw it, the version of the
// stream set; false when the read did not ask it.
func posNextStreams(s *Snapshot) (uint64, bool) {
	pos := posOf(s)
	if pos == nil {
		return 0, false
	}
	if !pos.hasNext {
		posNotLoaded(s, KeyNextStreams+", which")
		return 0, false
	}
	return pos.next, true
}

// posStreamsRead says a `streams` query was answered: a snapshot with no answer
// says nothing of the streams, and no key is planned on it.
func posStreamsRead(s *Snapshot) bool {
	pos := posOf(s)
	if pos == nil {
		return false
	}
	if !pos.streams {
		posNotLoaded(s, "the streams, which")
		return false
	}
	return true
}

// posStuckOf is the ids of a stream's stuck cell a `streams` query read; false
// when it read none for the stream.
func posStuckOf(s *Snapshot, stream string) (StuckAnswer, bool) {
	pos := posOf(s)
	if pos == nil {
		return StuckAnswer{}, false
	}
	st, ok := pos.stuck[stream]
	return st, ok
}

// posNeed is the answer of waiters for one need of a key, with the waiters'
// records; Index is the need's place among the ids of its key's line (the
// window's first id is the key's offset).
type posNeed struct {
	Index   int
	Need    string
	Place   string
	Missing bool
	Waiters []*Card
	More    bool
}

// posKeyView is the answer of waiters for one key: the needs of the window it
// read, in order, and whether the line has more ids beyond it.
type posKeyView struct {
	Needs   []posNeed
	MoreIDs bool
}

// posKeyViewOf is the answer of the waiters query of an agenda key; false when
// the read did not ask it.
func posKeyViewOf(s *Snapshot, k AgendaKey) (posKeyView, bool) {
	pos := posOf(s)
	if pos == nil {
		return posKeyView{}, false
	}
	i, ok := pos.waiters[k.Key]
	if !ok {
		posNotLoaded(s, "the waiters of "+strconv.Quote(k.Key)+", which")
		return posKeyView{}, false
	}
	p, _ := posSplitKey(k)
	a := s.Partial.Answer.Sprint[i]
	v := posKeyView{MoreIDs: a.MoreIDs}
	for j, n := range a.Needs {
		nv := posNeed{Index: p.offset + j, Need: n.ID, Place: n.Place, Missing: n.Missing, More: n.More}
		for _, w := range n.Waiters {
			nv.Waiters = append(nv.Waiters, s.Work.Card(w))
		}
		v.Needs = append(v.Needs, nv)
	}
	return v, true
}

package sprintfn

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// The answers of the sprint's queries (upper design 1.0; errata 1 E3; item
// IT30). IT05's sprint.Answer holds only what a partial snapshot loads from
// (records with their tables, the listing rows and counts, the front's
// sentinel); the rest of each query's result (a waiter's place in
// {p}missing@e, the heads of wait:n, the needs a chain reached, a note's
// subjects with their jopen) has no field there, and plan_types.go says those
// are IT30's to add beside them. That file is IT05's and is not edited: each
// result below is IT30's own type, holds everything a query returns, and
// projects onto sprint.Answer (Project) for what IT05's loader reads. The Lua
// kinds return the same objects, key for key (the JSON tags), so a result
// decodes from either (DecodeResult).

// Record is a record as Layer 1's checked read answers it, which every answer
// of a sprint query carries unchanged (S.read_record's object): id, exists,
// epoch, revision, place, score, and each field asked with its presence.
type Record = tset.MemberRecord

// QueryResult is the complete answer of one sprint query or sprint-key read.
type QueryResult interface {
	// ResultKind is the kind of the query that returned it.
	ResultKind() string
	// Project is what IT05's sprint.Answer holds of it, for the query q asked.
	// A sprint-key read has nothing there: its projection is the empty answer
	// of its kind.
	Project(q sprint.SprintQ) sprint.Answer
}

// QueryCharge is what a query read, counted as the store's checked reads
// count (L1 6, 7): a record once for every occurrence read, range ids as they
// are returned, each cell or key probe, and each line fetched by seq.
type QueryCharge struct {
	Records  int
	RangeIDs int
	// RowIDs are the rows a listing read, in RangeIDs as well: S.read_range_head
	// charges every id it returns, and Layer 1's own rows query charges none, so
	// IT05's QueryCost, which omits them, is compared without them.
	RowIDs int
	Probes int
	Lines  int
	// Work counts the steps of the twin's own logic over ids that are no store
	// read: one for each id examined in a quarantine set or a left-out set. A
	// store has no such charge; a test holds it to a multiple of the input, so
	// that no step of the logic grows with the square of it.
	Work int
}

// NeedLink is one need of a card: its record (absent when the need has none,
// which is how a card waits on something missing) and whether the card is in
// the need's wait:n set.
type NeedLink struct {
	ID     string `json:"id"`
	Record Record `json:"record"`
	InWait bool   `json:"in_wait"`
}

// JOpenCount is how many judgments are open or held on a subject ({p}jopen:<subject>@e's HLEN). The
// fields themselves are read by the jopen kind and by jnote, which name them:
// Layer 1's checked probes enumerate no hash.
type JOpenCount struct {
	Count int `json:"count"`
}

// DueEntry is one of a card's entries in the due set.
type DueEntry struct {
	Key   string `json:"key"`
	Score string `json:"score"`
}

// IndexEntry is one index the card is a member of, and its score there.
type IndexEntry struct {
	Key   string `json:"key"`
	Score string `json:"score"`
}

// Follows are what `related` (and a head of `front`) reached from one record,
// for the follows asked (1.0's table). A follow that found nothing is empty.
type Follows struct {
	Work      []Record     `json:"work,omitempty"`
	Withdrawn []Record     `json:"withdrawn,omitempty"`
	RCards    []Record     `json:"rcards,omitempty"`
	Merge     []Record     `json:"merge,omitempty"`
	Control   []Record     `json:"control,omitempty"`
	Needs     []NeedLink   `json:"needs,omitempty"`
	Member    []Record     `json:"member,omitempty"`
	JOpen     *JOpenCount  `json:"jopen,omitempty"`
	Due       []DueEntry   `json:"due,omitempty"`
	Index     []IndexEntry `json:"index,omitempty"`
}

// RelatedItem is one id read: its record and what its follows reached.
type RelatedItem struct {
	ID      string   `json:"id"`
	Record  Record   `json:"record"`
	Follows *Follows `json:"follows,omitempty"`
}

// RelatedResult is the answer of `related`.
type RelatedResult struct {
	Kind string `json:"kind"`
	// IDs are the ids read, in the source's order, quarantined ids left out;
	// LeftOut are those.
	IDs     []string      `json:"ids"`
	LeftOut []string      `json:"left_out"`
	Items   []RelatedItem `json:"items"`
}

// HeadResult is one head of `front`: the ids read in the index's order with
// their scores, whether the index had more, and the records of the ids with
// their follows.
type HeadResult struct {
	Index   string        `json:"index"`
	IDs     []string      `json:"ids"`
	Scores  []string      `json:"scores"`
	HasMore bool          `json:"has_more"`
	Items   []RelatedItem `json:"items"`
}

// FrontResult is the answer of `front(s)`: G and sigma (the first of sent:s;
// G is "" and Sigma "" when s has no sentinel), n_before (the count of s's
// five open cells below sigma), G's record (none when G is quarantined, which
// stays the first sentinel so nothing behind it is released, or absent), and
// the heads asked.
type FrontResult struct {
	Kind         string       `json:"kind"`
	Stream       string       `json:"stream"`
	G            string       `json:"g"`
	Sigma        string       `json:"sigma"`
	NBefore      int          `json:"n_before"`
	GQuarantined bool         `json:"g_quarantined"`
	GRecord      *Record      `json:"g_record"`
	Heads        []HeadResult `json:"heads"`
	LeftOut      []string     `json:"left_out"`
}

// WaiterRef is one waiter of a need: its record.
type WaiterRef struct {
	ID     string `json:"id"`
	Record Record `json:"record"`
}

// WaitHead is the head of wait:n up to the query's limit.
type WaitHead struct {
	IDs     []string    `json:"ids"`
	HasMore bool        `json:"has_more"`
	LeftOut []string    `json:"left_out"`
	Items   []WaiterRef `json:"items"`
}

// WaiterItem is one id n of a `waiters` source: its record (whose place is
// absent when n has none), its score in {p}missing@e (nil when it has none),
// and the head of wait:n.
type WaiterItem struct {
	ID      string   `json:"id"`
	Record  Record   `json:"record"`
	Missing *string  `json:"missing"`
	Wait    WaitHead `json:"wait"`
}

// WaitersResult is the answer of `waiters`.
type WaitersResult struct {
	Kind    string       `json:"kind"`
	IDs     []string     `json:"ids"`
	LeftOut []string     `json:"left_out"`
	Items   []WaiterItem `json:"items"`
	// MoreIDs says a source that is a line has ids beyond the window read
	// (sprint.Answer.MoreIDs). The twin reports it and the wire does not carry it.
	MoreIDs bool `json:"-"`
}

// StuckIDs are the first ids of a stopped stream's stuck cell.
type StuckIDs struct {
	IDs     []string `json:"ids"`
	HasMore bool     `json:"has_more"`
	LeftOut []string `json:"left_out"`
}

// StreamItem is one stream: its control card (nil when it has none), and, for
// a stream stopped on a cross need, the need card's record and the stuck ids;
// both nil otherwise.
type StreamItem struct {
	Stream  string    `json:"stream"`
	Control *Record   `json:"control"`
	Need    *Record   `json:"need"`
	Stuck   *StuckIDs `json:"stuck"`
}

// StreamsResult is the answer of `streams`: the rows of the work table, up to
// the query's units, and HasMore when there were more. LeftOut are the control
// cards and need cards that were quarantined and so not read.
type StreamsResult struct {
	Kind    string       `json:"kind"`
	Rows    []string     `json:"rows"`
	HasMore bool         `json:"has_more"`
	LeftOut []string     `json:"left_out"`
	Items   []StreamItem `json:"items"`
}

// CellN is the count of the cards in one cell of a row.
type CellN struct {
	Col string `json:"col"`
	N   int    `json:"n"`
}

// ListingItem is one member's (reader's) row: its control card (nil when it
// has none) and the counts of its cells, every set column but the control's.
type ListingItem struct {
	Row     string  `json:"row"`
	Control *Record `json:"control"`
	Counts  []CellN `json:"counts"`
}

// ListingResult is the answer of `fleet` and of `readers`. LeftOut are the
// control cards that were quarantined and so not read.
type ListingResult struct {
	Kind    string        `json:"kind"`
	Rows    []string      `json:"rows"`
	HasMore bool          `json:"has_more"`
	LeftOut []string      `json:"left_out"`
	Items   []ListingItem `json:"items"`
}

// ChainItem is one card `needchain` read: its record, the needs it names, and
// whether it was one of the ids asked (Start) and not a need reached.
type ChainItem struct {
	ID     string   `json:"id"`
	Record Record   `json:"record"`
	Needs  []string `json:"needs"`
	Start  bool     `json:"start"`
}

// NeedchainResult is the answer of `needchain`: the cards read in the order
// they were reached, and Cut when the walk stopped at its limit with cards
// still to read.
type NeedchainResult struct {
	Kind    string      `json:"kind"`
	Cut     bool        `json:"cut"`
	Items   []ChainItem `json:"items"`
	LeftOut []string    `json:"left_out"`
}

// SubjectOpen is one subject of a note: how many judgments are open or held on
// it, and what its jopen holds for the note's own type and cause (the note id,
// "h<note id>" for a hold, nil when none).
type SubjectOpen struct {
	ID    string  `json:"id"`
	Count int     `json:"count"`
	Own   *string `json:"own"`
}

// NoteItem is one note read from its line: its seq, type, cause and subjects
// (quarantined subjects left out, LeftOut of them).
type NoteItem struct {
	Note     string        `json:"note"`
	Seq      string        `json:"seq"`
	Type     string        `json:"type"`
	Cause    string        `json:"cause"`
	Subjects []SubjectOpen `json:"subjects"`
	LeftOut  int           `json:"left_out"`
}

// JnoteResult is the answer of `jnote`.
type JnoteResult struct {
	Kind  string     `json:"kind"`
	IDs   []string   `json:"ids"`
	Items []NoteItem `json:"items"`
}

// ResultKind is the kind of the query that returned the answer.
func (r RelatedResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r FrontResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r WaitersResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r StreamsResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r ListingResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r NeedchainResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r JnoteResult) ResultKind() string { return r.Kind }

// cardOf is a record as IT05's Card: its place and score as numbers, its
// fields the present ones. A record that is kept and not placed has no row or
// column (the loader's words for it).
func cardOf(r Record) *sprint.Card {
	c := &sprint.Card{ID: r.ID, Fields: map[string]string{}}
	if r.Place != nil {
		c.Row, c.Col = r.Place.Row, r.Place.Col
	}
	if r.Score != "" {
		c.Score, _ = strconv.ParseFloat(r.Score, 64)
	}
	if r.Revision != "" {
		c.Rev, _ = strconv.ParseUint(string(r.Revision), 10, 64)
	}
	for name, v := range r.Fields {
		if v.Present {
			c.Fields[name] = v.Value
		}
	}
	return c
}

// tableCards are the existing records of a list, each with its table: a
// record that does not exist is left out, as the loader refuses one without
// a card.
func tableCards(table string, recs ...Record) []sprint.TableCard {
	var out []sprint.TableCard
	for _, r := range recs {
		if r.Exists {
			out = append(out, sprint.TableCard{Table: table, Card: cardOf(r)})
		}
	}
	return out
}

// followCards are the cards of a record's follows, in 1.0's order of the
// follows (sprint.Follows), each in the table it is read from.
func (f *Follows) followCards() []sprint.TableCard {
	if f == nil {
		return nil
	}
	var out []sprint.TableCard
	out = append(out, tableCards(sprint.Fleet, f.Work...)...)
	out = append(out, tableCards(sprint.Fleet, f.Withdrawn...)...)
	out = append(out, tableCards(sprint.Readers, f.RCards...)...)
	out = append(out, tableCards(sprint.Merge, f.Merge...)...)
	out = append(out, tableCards(sprint.Merge, f.Control...)...)
	for _, n := range f.Needs {
		out = append(out, tableCards(sprint.Work, n.Record)...)
	}
	out = append(out, tableCards(sprint.Fleet, f.Member...)...)
	return out
}

// itemCards is an item's primary and its follows' cards.
func itemCards(table string, it RelatedItem) []sprint.TableCard {
	return append(tableCards(table, it.Record), it.Follows.followCards()...)
}

// sourceNamed is the ids a head or a line source named, which the answer says
// when the plan does not (sprint.Answer.IDs): nil for a list of ids.
func sourceNamed(src sprint.IDSource, ids []string) []string {
	if src.Kind == sprint.SourceIDs {
		return nil
	}
	return append([]string(nil), ids...)
}

// Project is `related` as IT05's Answer: the ids a head or a line named, and
// every record read, each primary followed by its follows' records.
func (r RelatedResult) Project(q sprint.SprintQ) sprint.Answer {
	a := sprint.Answer{Kind: r.Kind, IDs: sourceNamed(q.Source, r.IDs)}
	for _, it := range r.Items {
		a.Records = append(a.Records, itemCards(q.Table, it)...)
	}
	return a
}

// Project is `front` as IT05's Answer: the sentinel's line and G's record, every
// head (its index, its ids read and whether the index had more; the records are
// in Records) and every head record with its follows. Heads are one for each
// head asked, in order (IT08's alignment, loadPosition).
func (r FrontResult) Project(q sprint.SprintQ) sprint.Answer {
	f := sprint.FrontAnswer{Stream: r.Stream, G: r.G, NBefore: r.NBefore, GQuarantined: r.GQuarantined}
	f.Sigma, _ = strconv.ParseFloat(r.Sigma, 64)
	a := sprint.Answer{Kind: r.Kind, Front: &f}
	if r.GRecord != nil {
		a.Records = append(a.Records, tableCards(sprint.Work, *r.GRecord)...)
	}
	for _, h := range r.Heads {
		a.Heads = append(a.Heads, sprint.HeadAnswer{Index: h.Index, IDs: append([]string(nil), h.IDs...), More: h.HasMore})
		for _, it := range h.Items {
			a.Records = append(a.Records, itemCards(sprint.Work, it)...)
		}
	}
	return a
}

// Project is `waiters` as IT05's Answer: each id's record and its waiters', and
// one NeedAnswer for each id of the source, in order (IT08's alignment,
// loadPosition): the id's place (its column, "" when it has no record), whether
// it has a score in {p}missing@e, the head of wait:n read (after the query's
// WaiterAfter, up to its Limit) and whether wait:n has more beyond it. An id of
// a list that the query left out (quarantined) is answered with no place and no
// waiters, since the list names it and the answer must answer every id it
// names. MoreIDs is the twin's: a line has ids beyond the window.
func (r WaitersResult) Project(q sprint.SprintQ) sprint.Answer {
	a := sprint.Answer{Kind: r.Kind, IDs: sourceNamed(q.Source, r.IDs), MoreIDs: r.MoreIDs}
	byID := make(map[string]WaiterItem, len(r.Items))
	for _, it := range r.Items {
		byID[it.ID] = it
		a.Records = append(a.Records, tableCards(sprint.Work, it.Record)...)
		for _, w := range it.Wait.Items {
			a.Records = append(a.Records, tableCards(sprint.Work, w.Record)...)
		}
	}
	ids := r.IDs
	if q.Source.Kind == sprint.SourceIDs {
		ids = q.Source.IDs
	}
	for _, id := range ids {
		n := sprint.NeedAnswer{ID: id}
		if it, ok := byID[id]; ok {
			if it.Record.Exists && it.Record.Place != nil {
				n.Place = it.Record.Place.Col
			}
			n.Missing = it.Missing != nil
			n.Waiters = append([]string(nil), it.Wait.IDs...)
			n.More = it.Wait.HasMore
		}
		a.Needs = append(a.Needs, n)
	}
	return a
}

// Project is `streams` as IT05's Answer: the rows, whether there were more, the
// control cards and need cards read, and the stuck ids of each stream stopped on
// a cross need (IT08 reads them, loadPosition). The counts and the sprint keys a
// query may ask beside are not read by the twin.
func (r StreamsResult) Project(q sprint.SprintQ) sprint.Answer {
	a := sprint.Answer{Kind: r.Kind, Rows: append([]string(nil), r.Rows...), HasMore: r.HasMore}
	for _, it := range r.Items {
		if it.Control != nil {
			a.Records = append(a.Records, tableCards(sprint.Merge, *it.Control)...)
		}
		if it.Need != nil {
			a.Records = append(a.Records, tableCards(sprint.Work, *it.Need)...)
		}
		if it.Stuck != nil {
			a.Stuck = append(a.Stuck, sprint.StuckAnswer{Stream: it.Stream, IDs: append([]string(nil), it.Stuck.IDs...), More: it.Stuck.HasMore})
		}
	}
	return a
}

// Project is `fleet` or `readers` as IT05's Answer: the rows, whether there
// were more, every cell's count, and the control cards.
func (r ListingResult) Project(q sprint.SprintQ) sprint.Answer {
	table := sprint.Fleet
	if r.Kind == sprint.QueryReaders {
		table = sprint.Readers
	}
	a := sprint.Answer{Kind: r.Kind, Rows: append([]string(nil), r.Rows...), HasMore: r.HasMore}
	for _, it := range r.Items {
		for _, c := range it.Counts {
			a.Counts = append(a.Counts, sprint.CellCount{Row: it.Row, Col: c.Col, N: c.N})
		}
		if it.Control != nil {
			a.Records = append(a.Records, tableCards(table, *it.Control)...)
		}
	}
	return a
}

// Project is `needchain` as IT05's Answer: every card the walk read.
func (r NeedchainResult) Project(q sprint.SprintQ) sprint.Answer {
	a := sprint.Answer{Kind: r.Kind, IDs: sourceNamed(q.Source, startIDs(r))}
	for _, it := range r.Items {
		a.Records = append(a.Records, tableCards(sprint.Work, it.Record)...)
	}
	return a
}

// startIDs are the ids of a chain's source, in the order they were read.
func startIDs(r NeedchainResult) []string {
	var out []string
	for _, it := range r.Items {
		if it.Start {
			out = append(out, it.ID)
		}
	}
	return out
}

// Project is `jnote` as IT05's Answer: the notes a head named. A note's
// subjects, type and cause are not records; IT05's Answer has no field for
// them.
func (r JnoteResult) Project(q sprint.SprintQ) sprint.Answer {
	return sprint.Answer{Kind: r.Kind, IDs: sourceNamed(q.Source, r.IDs)}
}

// DecodeResult reads the answer a query of a kind returned, from a twin or a
// store, into its type. A kind that is not a sprint kind is an error.
func DecodeResult(kind string, raw json.RawMessage) (QueryResult, error) {
	var out QueryResult
	switch kind {
	case sprint.QueryRelated:
		out = &RelatedResult{}
	case sprint.QueryFront:
		out = &FrontResult{}
	case sprint.QueryWaiters:
		out = &WaitersResult{}
	case sprint.QueryStreams:
		out = &StreamsResult{}
	case sprint.QueryFleet, sprint.QueryReaders:
		out = &ListingResult{}
	case sprint.QueryNeedchain:
		out = &NeedchainResult{}
	case sprint.QueryJnote:
		out = &JnoteResult{}
	case KeyClock:
		out = &ClockResult{}
	case KeyLease:
		out = &LeaseResult{}
	case KeyTick:
		out = &TickResult{}
	case KeyHeartbeat:
		out = &HeartbeatResult{}
	case KeyDropping:
		out = &DroppingResult{}
	case KeyParked:
		out = &ParkedResult{}
	case KeyMissing:
		out = &MissingResult{}
	case KeyJOpen:
		out = &JOpenResult{}
	case KeyDueCount:
		out = &DueCountResult{}
	default:
		return nil, fmt.Errorf("sprintfn: %q is not a sprint query kind", kind)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("sprintfn: a %s answer: %w", kind, err)
	}
	if out.ResultKind() != kind {
		return nil, fmt.Errorf("sprintfn: the answer is of the kind %q, asked %q", out.ResultKind(), kind)
	}
	return deref(out), nil
}

// deref turns the pointer a decode filled into the value type every result is
// returned as.
func deref(r QueryResult) QueryResult {
	switch v := r.(type) {
	case *RelatedResult:
		return *v
	case *FrontResult:
		return *v
	case *WaitersResult:
		return *v
	case *StreamsResult:
		return *v
	case *ListingResult:
		return *v
	case *NeedchainResult:
		return *v
	case *JnoteResult:
		return *v
	case *ClockResult:
		return *v
	case *LeaseResult:
		return *v
	case *TickResult:
		return *v
	case *HeartbeatResult:
		return *v
	case *DroppingResult:
		return *v
	case *ParkedResult:
		return *v
	case *MissingResult:
		return *v
	case *JOpenResult:
		return *v
	case *DueCountResult:
		return *v
	}
	return r
}

// The sprint-key reads' answers (the errata's addendum). Times and counters
// are exact decimal strings; a field the store holds no value for is nil.

// ClockFields are the five fields of {p}clock (1.2).
type ClockFields struct {
	StoppedMS      *string `json:"stopped_ms"`
	StoppedSinceMS *string `json:"stopped_since_ms"`
	StopholdMS     *string `json:"stophold_ms"`
	DueSinceMS     *string `json:"due_since_ms"`
	StopraisedMS   *string `json:"stopraised_ms"`
}

// ClockResult is the clock and R: R(t) = t - stopped_ms - (stopped_since_ms ==
// "" ? 0 : t - stopped_since_ms), computed once from the call's time (1.2).
type ClockResult struct {
	Kind   string      `json:"kind"`
	WallMS string      `json:"wall_ms"`
	R      string      `json:"r"`
	Clock  ClockFields `json:"clock"`
}

// LeaseResult is {p}lease (1.1) with the call's time, so a caller can say it
// is free or expired (until_ms at or before now_ms).
type LeaseResult struct {
	Kind    string  `json:"kind"`
	NowMS   string  `json:"now_ms"`
	Owner   *string `json:"owner"`
	Name    *string `json:"name"`
	UntilMS *string `json:"until_ms"`
	Gen     *string `json:"gen"`
}

// TickResult is {p}tick@e (1.1): the last seq ingested and the backlog when
// `behind` was armed.
type TickResult struct {
	Kind    string  `json:"kind"`
	Cur     *string `json:"cur"`
	BehindN *string `json:"behind_n"`
}

// HeartbeatResult is the fields of {p}heartbeat (1.4.1) that are set, of the
// fixed list HeartbeatReadFields.
type HeartbeatResult struct {
	Kind   string            `json:"kind"`
	Fields map[string]string `json:"fields"`
}

// DroppingResult is the marks of the streams asked (stream -> op) and the
// number of marks the hash holds.
type DroppingResult struct {
	Kind  string            `json:"kind"`
	Count int               `json:"count"`
	Marks map[string]string `json:"marks"`
}

// ParkedResult is the parked notes of the keys asked and the number of keys
// the hash holds.
type ParkedResult struct {
	Kind  string            `json:"kind"`
	Count int               `json:"count"`
	Notes map[string]string `json:"notes"`
}

// MissingResult is the score in {p}missing@e of each id asked, aligned with
// the ids (nil for one not in it).
type MissingResult struct {
	Kind   string    `json:"kind"`
	Scores []*string `json:"scores"`
}

// JOpenItem is one subject's jopen: the number of fields the hash holds and
// the value of each name asked (nil for one it does not hold).
type JOpenItem struct {
	ID     string             `json:"id"`
	Count  int                `json:"count"`
	Fields map[string]*string `json:"fields"`
}

// JOpenResult is the answer of the jopen kind.
type JOpenResult struct {
	Kind  string      `json:"kind"`
	Items []JOpenItem `json:"items"`
}

// DueCountResult is the number of due entries at or below R, the count the
// machine line shows (1.2).
type DueCountResult struct {
	Kind string `json:"kind"`
	R    string `json:"r"`
	Due  int    `json:"due"`
}

// ResultKind is the kind of the query that returned the answer.
func (r ClockResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r LeaseResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r TickResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r HeartbeatResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r DroppingResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r ParkedResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r MissingResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r JOpenResult) ResultKind() string { return r.Kind }

// ResultKind is the kind of the query that returned the answer.
func (r DueCountResult) ResultKind() string { return r.Kind }

// Project is the empty answer of the kind: a sprint-key read has no field in
// sprint.Answer.
func (r ClockResult) Project(sprint.SprintQ) sprint.Answer { return sprint.Answer{Kind: r.Kind} }

// Project is the empty answer of the kind: a sprint-key read has no field in
// sprint.Answer.
func (r LeaseResult) Project(sprint.SprintQ) sprint.Answer { return sprint.Answer{Kind: r.Kind} }

// Project is the empty answer of the kind: a sprint-key read has no field in
// sprint.Answer.
func (r TickResult) Project(sprint.SprintQ) sprint.Answer { return sprint.Answer{Kind: r.Kind} }

// Project is the empty answer of the kind: a sprint-key read has no field in
// sprint.Answer.
func (r HeartbeatResult) Project(sprint.SprintQ) sprint.Answer { return sprint.Answer{Kind: r.Kind} }

// Project is the empty answer of the kind: a sprint-key read has no field in
// sprint.Answer.
func (r DroppingResult) Project(sprint.SprintQ) sprint.Answer { return sprint.Answer{Kind: r.Kind} }

// Project is the empty answer of the kind: a sprint-key read has no field in
// sprint.Answer.
func (r ParkedResult) Project(sprint.SprintQ) sprint.Answer { return sprint.Answer{Kind: r.Kind} }

// Project is the empty answer of the kind: a sprint-key read has no field in
// sprint.Answer.
func (r MissingResult) Project(sprint.SprintQ) sprint.Answer { return sprint.Answer{Kind: r.Kind} }

// Project is the empty answer of the kind: a sprint-key read has no field in
// sprint.Answer.
func (r JOpenResult) Project(sprint.SprintQ) sprint.Answer { return sprint.Answer{Kind: r.Kind} }

// Project is the empty answer of the kind: a sprint-key read has no field in
// sprint.Answer.
func (r DueCountResult) Project(sprint.SprintQ) sprint.Answer { return sprint.Answer{Kind: r.Kind} }

// HeartbeatReadFields are the fields of {p}heartbeat that the heartbeat read asks
// for (1.4.1), in the order the design lists them, then the two a loop that
// does not hold the lease writes.
var HeartbeatReadFields = []string{"tick_at", "ticks", "error", "failures", "backlog", "agenda", "heldq", "due_now",
	"owner", "gen", "swept", "looked_at", "rules", "idle_loop", "idle_at"}

// ClockFieldNames are the fields of {p}clock (1.2), in the order ClockFields
// has them.
var ClockFieldNames = []string{"stopped_ms", "stopped_since_ms", "stophold_ms", "due_since_ms", "stopraised_ms"}

// LeaseFieldNames are the fields of {p}lease (1.1).
var LeaseFieldNames = []string{"owner", "name", "until_ms", "gen"}

// TickFieldNames are the fields of {p}tick@e (1.1) the tick read returns.
var TickFieldNames = []string{"cur", "behind_n"}

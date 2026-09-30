package sprint

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// removeWorld is a snapshot built whole: streams of n primaries each, spread
// over the six work columns, with the cards a primary in each column holds in
// the other tables. want is what a remove of a stream must name, by table, as
// the builder made it (not as Remove finds it).
type removeWorld struct {
	s    *Snapshot
	want map[string]map[string][]string // stream -> table -> card ids
}

func newRemoveWorld(streams []string, n int) *removeWorld {
	s := &Snapshot{Now: t0, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	s.Fleet.SetRows([]string{"m1", "m2"})
	s.Readers.SetRows([]string{"r1", "r2"})
	s.Work.SetRows(append([]string(nil), streams...))
	s.Merge.SetRows(append([]string(nil), streams...))
	w := &removeWorld{s: s, want: map[string]map[string][]string{}}
	put := func(st, table, id, row, col string, f map[string]string) {
		s.T(table).Put(&Card{ID: id, Row: row, Col: col, Score: float64(len(w.want[st][table])), Rev: 1, Fields: f})
		w.want[st][table] = append(w.want[st][table], id)
	}
	for _, st := range streams {
		w.want[st] = map[string][]string{}
		put(st, Merge, CtlID(st), st, Ctl, map[string]string{"state": StreamMerging})
		for i := 0; i < n; i++ {
			pr := fmt.Sprintf("%s-%d", st, i+1)
			col := States[i%len(States)]
			put(st, Work, pr, st, col, map[string]string{"kind": "primary"})
			switch col {
			case Working:
				put(st, Fleet, WorkCardID(pr, 1), "m1", Working, map[string]string{"kind": "work", "primary": pr, "stream": st})
			case Ready:
				put(st, Fleet, WorkCardID(pr, 1), "m2", Ready, map[string]string{"kind": "work", "primary": pr, "stream": st})
			case Review:
				for _, r := range []string{"r1", "r2"} {
					put(st, Readers, ReadCardID(pr, 1, r), r, Reading, map[string]string{"kind": "read", "primary": pr, "stream": st, "reader": r})
				}
				put(st, Fleet, WorkCardID(pr, 1), "m1", DoneOK, map[string]string{"kind": "work", "primary": pr, "stream": st})
			case Merging, Landed:
				put(st, Merge, pr, st, Queued, map[string]string{"kind": "merge", "primary": pr, "stream": st})
			}
		}
	}
	return w
}

// planned is the (table, id) of every removal in the plan, and fails on a
// duplicate or an entry that is not a removal.
func planned(t *testing.T, p Plan) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, u := range p.Units {
		for _, c := range u.Changes {
			if !c.Entry.Remove || c.Entry.Create != nil || c.Entry.Move != nil {
				t.Fatalf("%s %s is not a plain removal: %+v", c.Table, c.Entry.ID, c.Entry)
			}
			k := c.Table + "/" + c.Entry.ID
			if seen[k] {
				t.Fatalf("%s planned twice", k)
			}
			seen[k] = true
			out[c.Table] = append(out[c.Table], c.Entry.ID)
		}
	}
	for _, ids := range out {
		sort.Strings(ids)
	}
	return out
}

func TestRemoveNamesEveryCardOfTheStreamsAndNothingOfTheThird(t *testing.T) {
	t.Parallel()
	w := newRemoveWorld([]string{"s1", "s2", "s3"}, 10)
	// A finished work card of a primary dropped earlier has no primary on the
	// table any more: it goes with its stream, by the stream it names.
	w.s.Fleet.Put(&Card{ID: "gone.w1", Row: "m2", Col: DoneFailed, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "gone", "stream": "s2"}})
	w.want["s2"][Fleet] = append(w.want["s2"][Fleet], "gone.w1")
	p, err := Remove(w.s, []string{"s1", "s2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Refused) != 0 {
		t.Fatalf("refused: %+v", p.Refused)
	}
	want := map[string][]string{}
	for _, st := range []string{"s1", "s2"} {
		for table, ids := range w.want[st] {
			want[table] = append(want[table], ids...)
		}
	}
	for _, ids := range want {
		sort.Strings(ids)
	}
	got := planned(t, p)
	for _, table := range []string{Work, Readers, Merge, Fleet} {
		if strings.Join(got[table], " ") != strings.Join(want[table], " ") {
			t.Errorf("%s:\n got  %v\n want %v", table, got[table], want[table])
		}
		if len(want[table]) == 0 {
			t.Errorf("the world holds no %s cards to remove", table)
		}
	}
	for _, table := range []string{Work, Readers, Merge, Fleet} {
		for _, id := range got[table] {
			if strings.HasPrefix(id, "s3") || strings.HasPrefix(id, "ctl-s3") {
				t.Errorf("%s %s of the third stream is in the plan", table, id)
			}
		}
	}
	// The work cards were in every column, the reader and fleet cards in more
	// than one.
	cols := map[string]bool{}
	for _, u := range p.Units {
		for _, c := range u.Changes {
			if c.Table == Work {
				cols[c.Entry.Expect.Place.Col] = true
			}
		}
	}
	if len(cols) != len(States) {
		t.Errorf("work cards removed from %d columns, want %d", len(cols), len(States))
	}
	// The rows: both streams' rows in work and merge, in the last part only.
	var rows []string
	for i, u := range p.Units {
		if len(u.RowDels) > 0 && i != len(p.Units)-1 {
			t.Errorf("part %d of %d holds row deletions", i+1, len(p.Units))
		}
		for _, r := range u.RowDels {
			rows = append(rows, r.Table+"/"+r.Row)
		}
	}
	sort.Strings(rows)
	if strings.Join(rows, " ") != "merge/s1 merge/s2 work/s1 work/s2" {
		t.Errorf("rows deleted: %v", rows)
	}
	// A primary and every card it had are in one part.
	primaryParts(t, p)
	// It is a planner: nothing of the snapshot changed.
	if w.s.Work.Card("s1-1") == nil || len(w.s.Work.Rows()) != 3 || len(w.s.Merge.Rows()) != 3 {
		t.Errorf("the plan changed the snapshot")
	}
}

// primaryOfEntry is the primary a removed card belongs to: a work card, a read
// card, or the primary's own card (a merge card has its id).
func primaryOfEntry(id string) string {
	if pr, _, ok := ParseWorkCard(id); ok {
		return pr
	}
	if i := strings.Index(id, ".r"); i > 0 {
		return id[:i]
	}
	return id
}

// primaryParts maps each primary of the plan to the parts (1-based) that hold
// its cards; ctl cards of a stream are left out.
func primaryParts(t *testing.T, p Plan) map[string]map[int]bool {
	t.Helper()
	out := map[string]map[int]bool{}
	for i, u := range p.Units {
		for _, c := range u.Changes {
			pr := primaryOfEntry(c.Entry.ID)
			if strings.HasPrefix(pr, "ctl-") {
				continue
			}
			if out[pr] == nil {
				out[pr] = map[int]bool{}
			}
			out[pr][i+1] = true
		}
	}
	return out
}

func TestRemoveRefusesWhatItCannotPlanAndPlansNothing(t *testing.T) {
	t.Parallel()
	w := newRemoveWorld([]string{"s1", "s2"}, 6)
	for _, tc := range []struct {
		name    string
		streams []string
		err     string
	}{
		{"a missing stream", []string{"s1", "nope"}, "no such stream: nope; nothing was removed"},
		{"only a missing stream", []string{"nope"}, "no such stream: nope; nothing was removed"},
		{"two missing", []string{"a", "s1", "b"}, "no such stream: a, b; nothing was removed"},
		{"an empty list", nil, "no stream named; nothing was removed"},
	} {
		p, err := Remove(w.s, tc.streams)
		if err == nil || err.Error() != tc.err {
			t.Errorf("%s: error %v, want %q", tc.name, err, tc.err)
		}
		if len(p.Units) != 0 || len(p.Rows) != 0 || len(p.Refused) != 0 {
			t.Errorf("%s: the plan is not empty: %+v", tc.name, p)
		}
	}
	// A stream named twice is planned once.
	twice, err := Remove(w.s, []string{"s1", "s1"})
	once, err2 := Remove(w.s, []string{"s1"})
	if err != nil || err2 != nil || len(planned(t, twice)[Work]) != len(planned(t, once)[Work]) || len(planned(t, once)[Work]) != 6 {
		t.Errorf("a stream named twice: %v %v", err, err2)
	}
	// A snapshot loaded from a read plan is not the whole tables.
	part := *w.s
	part.Partial = &Partial{}
	if p, err := Remove(&part, []string{"s1"}); err == nil || len(p.Units) != 0 {
		t.Errorf("a partial snapshot was planned: %v", err)
	}
}

func TestRemoveOfAnEmptyStreamIsItsRowsAlone(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Now: t0, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	s.Work.SetRows([]string{"e"})
	p, err := Remove(s, []string{"e"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Units) != 1 || len(p.Units[0].Changes) != 0 || len(p.Units[0].RowDels) != 1 || p.Units[0].RowDels[0] != (RowDel{Work, "e"}) {
		t.Errorf("plan: %+v", p)
	}
}

func TestRemoveCutsPartsOfAtMostTwoThousandEntries(t *testing.T) {
	t.Parallel()
	w := newRemoveWorld([]string{"s1", "s2"}, 1200)
	p, err := Remove(w.s, []string{"s1", "s2"})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for i, u := range p.Units {
		if len(u.Changes) > RemoveChunk {
			t.Errorf("part %d holds %d entries", i+1, len(u.Changes))
		}
		total += len(u.Changes)
	}
	want := 0
	for _, st := range []string{"s1", "s2"} {
		for _, ids := range w.want[st] {
			want += len(ids)
		}
	}
	if total != want || len(p.Units) < 2 {
		t.Errorf("%d entries in %d parts, want %d entries in several", total, len(p.Units), want)
	}
	// Where the cuts fall, no primary spans two parts.
	for pr, parts := range primaryParts(t, p) {
		if len(parts) != 1 {
			t.Errorf("%s spans parts %v", pr, parts)
		}
	}
}

// bigPrimary puts a primary of the stream in the work table with k fleet work
// cards (k+1 entries in all), scored so.
func bigPrimary(s *Snapshot, st, pr string, score float64, k int) {
	s.Work.Put(&Card{ID: pr, Row: st, Col: Working, Score: score, Rev: 1, Fields: map[string]string{"kind": "primary"}})
	for i := 1; i <= k; i++ {
		s.Fleet.Put(&Card{ID: WorkCardID(pr, i), Row: "m1", Col: Working, Score: float64(i), Rev: 1,
			Fields: map[string]string{"kind": "work", "primary": pr, "stream": st}})
	}
}

func bareRemoveSnapshot(streams ...string) *Snapshot {
	s := &Snapshot{Now: t0, Work: NewTable(Work), Readers: NewTable(Readers), Merge: NewTable(Merge), Fleet: NewTable(Fleet)}
	s.Fleet.SetRows([]string{"m1"})
	s.Work.SetRows(append([]string(nil), streams...))
	s.Merge.SetRows(append([]string(nil), streams...))
	return s
}

func partSizes(p Plan) []int {
	var out []int
	for _, u := range p.Units {
		out = append(out, len(u.Changes))
	}
	return out
}

// A primary of 1,990 entries followed by one of 21 does not squeeze in: the
// parts are 1990 and 21, and no primary is split.
func TestRemoveKeepsAPrimaryWholeWhenTheNextOneDoesNotFit(t *testing.T) {
	t.Parallel()
	s := bareRemoveSnapshot("a")
	bigPrimary(s, "a", "a-1", 1, 1989)
	bigPrimary(s, "a", "a-2", 2, 20)
	p, err := Remove(s, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(partSizes(p)); got != "[1990 21]" {
		t.Fatalf("parts %s, want [1990 21]", got)
	}
	for pr, parts := range primaryParts(t, p) {
		if len(parts) != 1 {
			t.Errorf("%s spans parts %v", pr, parts)
		}
	}
}

// A primary of 2,001 entries is split, not refused: 2,000 and 1, the
// primary's own work card last, so an abort between the parts leaves a
// primary without children and never children without a primary.
func TestRemoveSplitsAPrimaryOfMoreThanAPartAndItsOwnCardIsLast(t *testing.T) {
	t.Parallel()
	s := bareRemoveSnapshot("a")
	bigPrimary(s, "a", "a-1", 1, 2000)
	p, err := Remove(s, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Refused) != 0 {
		t.Fatalf("refused: %+v", p.Refused)
	}
	if got := fmt.Sprint(partSizes(p)); got != "[2000 1]" {
		t.Fatalf("parts %s, want [2000 1]", got)
	}
	if c := p.Units[1].Changes[0]; c.Table != Work || c.Entry.ID != "a-1" {
		t.Errorf("the last part holds %s %s, want the primary's own work card", c.Table, c.Entry.ID)
	}
	if got := planned(t, p); len(got[Fleet]) != 2000 || len(got[Work]) != 1 {
		t.Errorf("planned %d fleet and %d work cards", len(got[Fleet]), len(got[Work]))
	}
}

// A card goes by its primary when it carries no stream field, and when its
// stream names a stream outside the batch but its primary is inside (B1:
// every card of each primary).
func TestRemoveTakesACardByItsPrimaryWhateverItsStreamField(t *testing.T) {
	t.Parallel()
	w := newRemoveWorld([]string{"s1", "s2", "s3"}, 3)
	w.s.Fleet.Put(&Card{ID: WorkCardID("s1-1", 9), Row: "m2", Col: DoneOK, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "s1-1"}})
	w.s.Fleet.Put(&Card{ID: WorkCardID("s1-2", 9), Row: "m2", Col: DoneOK, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "s1-2", "stream": "s3"}})
	// A card of the third stream's own primary is not touched.
	w.s.Fleet.Put(&Card{ID: WorkCardID("s3-1", 9), Row: "m2", Col: DoneOK, Rev: 1, Fields: map[string]string{"kind": "work", "primary": "s3-1"}})
	p, err := Remove(w.s, []string{"s1"})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(planned(t, p)[Fleet], " ")
	for _, id := range []string{WorkCardID("s1-1", 9), WorkCardID("s1-2", 9)} {
		if !strings.Contains(got, id) {
			t.Errorf("%s is not removed: %s", id, got)
		}
	}
	if strings.Contains(got, WorkCardID("s3-1", 9)) {
		t.Errorf("a card of the third stream is removed: %s", got)
	}
}

func openOn(w *removeWorld, id, subject string, streamLevel bool, stream string) Open {
	n := Note{ID: id, Kind: Judgment, Type: NStranded, Stream: stream, StreamLevel: streamLevel, At: t0}
	o := Open{Key: OpenKey(id, subject), Note: n}
	w.s.Open = append(w.s.Open, o)
	return o
}

// A remove closes the judgments of what it removes, as drop does: a per-card
// judgment on a removed card on the unit that removes it, a stream-level one
// of a removed stream on the stream's last part; the third stream's stay open.
func TestRemoveClosesTheJudgmentsOfWhatItRemoves(t *testing.T) {
	t.Parallel()
	w := newRemoveWorld([]string{"s1", "s2", "s3"}, 4)
	card := openOn(w, "n-card", "s1-2", false, "s1")
	stream := openOn(w, "n-stream", StreamSubject("s2"), true, "s2")
	openOn(w, "n-third-card", "s3-2", false, "s3")
	openOn(w, "n-third-stream", StreamSubject("s3"), true, "s3")
	p, err := Remove(w.s, []string{"s1", "s2"})
	if err != nil {
		t.Fatal(err)
	}
	closes := map[string]string{} // note id -> unit key
	for _, u := range p.Units {
		for _, o := range u.Closes {
			if _, dup := closes[o.Note.ID]; dup {
				t.Errorf("%s closed twice", o.Note.ID)
			}
			closes[o.Note.ID] = u.Key
		}
	}
	if len(closes) != 2 {
		t.Errorf("closes %v, want the card's and the stream's judgments only", closes)
	}
	// The card's judgment is on the unit that removes s1-2.
	for _, u := range p.Units {
		removes := false
		for _, c := range u.Changes {
			removes = removes || c.Table == Work && c.Entry.ID == "s1-2"
		}
		if removes != (closes[card.Note.ID] == u.Key) {
			t.Errorf("unit %s removes s1-2: %v, but holds its judgment: %v", u.Key, removes, closes[card.Note.ID] == u.Key)
		}
	}
	// The stream's is on the last part of s2.
	last := ""
	for _, u := range p.Units {
		if u.Stream == "s2" {
			last = u.Key
		}
	}
	if closes[stream.Note.ID] != last || last == "" {
		t.Errorf("the stream judgment closes on %q, want the last part of s2, %q", closes[stream.Note.ID], last)
	}
}

// TestRemoveOf30000CardsIsPlannedAndMeasured plans a remove of 30,000 cards and
// logs how long it took. The bound, 100 ms, is measured by BenchmarkRemove30000
// (go test -bench Remove30000 ./internal/sprint): the repository's class tests
// refuse a wall-clock assertion in a unit test, and a benchmark costs a second.
func TestRemoveOf30000CardsIsPlannedAndMeasured(t *testing.T) {
	t.Parallel()
	w := newRemoveWorld([]string{"s1", "s2", "s3"}, 10000)
	start := time.Now()
	p, err := Remove(w.s, []string{"s1", "s2", "s3"})
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, u := range p.Units {
		if len(u.Changes) > RemoveChunk {
			t.Fatalf("a part of %d entries", len(u.Changes))
		}
		n += len(u.Changes)
	}
	if got := planned(t, p); len(got[Work]) != 30000 {
		t.Fatalf("%d work cards planned, want 30000", len(got[Work]))
	}
	t.Logf("Remove of 30,000 cards (%d entries in %d parts) took %s", n, len(p.Units), took)
}

func BenchmarkRemove30000(b *testing.B) {
	w := newRemoveWorld([]string{"s1", "s2", "s3"}, 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Remove(w.s, []string{"s1", "s2", "s3"}); err != nil {
			b.Fatal(err)
		}
	}
}

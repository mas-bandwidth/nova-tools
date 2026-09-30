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
	part := map[string]int{}
	for i, u := range p.Units {
		for _, c := range u.Changes {
			pr := c.Entry.ID
			if p, _, ok := ParseWorkCard(pr); ok {
				pr = p
			} else if i := strings.Index(pr, ".r"); i > 0 {
				pr = pr[:i]
			}
			if j, ok := part[pr]; ok && j != i {
				t.Errorf("%s is split over parts %d and %d", pr, j, i)
			}
			part[pr] = i
		}
	}
	// It is a planner: nothing of the snapshot changed.
	if w.s.Work.Card("s1-1") == nil || len(w.s.Work.Rows()) != 3 || len(w.s.Merge.Rows()) != 3 {
		t.Errorf("the plan changed the snapshot")
	}
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

package sprint

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What a stats tidy keeps (stats_tidy.go, TidyKept), on a world built by hand: the live
// work's cards, the recent finishes by running time, the rest rule's sample and each
// provider's newest ok finish; everything else on the rows named leaves its done cell.

// putDone puts a finished work card of a primary off the work table (history only) on row.
func putDone(w *world, row, id, col string, finished time.Time, model, route, wall string) {
	ok := "yes"
	if col == DoneFailed {
		ok = "no"
	}
	f := map[string]string{"kind": "work", PrimaryField: strings.TrimSuffix(id, ".w1"), "attempt": "1", "ok": ok,
		"finished": finished.UTC().Format(time.RFC3339), FieldModel: model}
	if route != "" {
		f[FieldRoute] = route
	}
	if wall != "" {
		f[FieldUsage] = "wall=" + wall
	}
	if !w.s.Fleet.HasRow(row) {
		w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), row))
	}
	w.s.Fleet.Put(&Card{ID: id, Row: row, Col: col, Rev: 1, Fields: f})
}

// movedIDs is every card a tidy's rows took off.
func movedIDs(rows []TidyRow) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		for _, c := range r.Moved {
			out[c.ID] = c.Cell
		}
	}
	return out
}

func TestATidyKeepsTheRecentAndTheRulesSamplesAndTakesTheRest(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	now := w.s.Now
	m, amy := "keep-m1", FriendRow("amy")
	// fourteen old ok finishes on prov-a, and the oldest of all a failed one
	for i := range 14 {
		putDone(w, m, fmt.Sprintf("m-%02d.w1", i), DoneOK, now.Add(-10*time.Hour+time.Duration(i)*time.Minute), "prov-a/x", "", "")
	}
	putDone(w, m, "m-failed.w1", DoneFailed, now.Add(-20*time.Hour), "prov-a/x", "", "")
	// provider prov-b's one ok finish, old: its newest
	putDone(w, m, "m-provb.w1", DoneOK, now.Add(-19*time.Hour), "prov-b/y", "", "")
	// twelve old finishes on the friend's row; the oldest holds route r2's only ended take
	for i := range 12 {
		route := ""
		if i == 0 {
			route = "r2"
		}
		putDone(w, amy, fmt.Sprintf("a-%02d.w1", i), DoneOK, now.Add(-12*time.Hour+time.Duration(i)*time.Minute), "prov-a/x", route, "")
	}
	// recent: 20 minutes ago by the clock; and 45 minutes ago, 20 of them STOPPED
	putDone(w, m, "m-recent.w1", DoneOK, now.Add(-20*time.Minute), "prov-a/x", "", "")
	putDone(w, m, "m-stopped.w1", DoneOK, now.Add(-45*time.Minute), "prov-a/x", "", "")
	stopped := func(from, to time.Time) time.Duration { return 20 * time.Minute }

	kept := TidyKept(w.s, stopped)
	window := "finished within " + TidyKeepWindow(w.s).String()
	assert.Equal(t, 30*time.Minute, TidyKeepWindow(w.s), "the largest of the idle, overload and view windows")
	assert.Contains(t, kept["m-recent.w1"], window, "a recently finished card is kept")
	assert.Contains(t, kept["m-stopped.w1"], window, "45m by the clock is 25m of running time: kept")
	assert.NotContains(t, TidyKept(w.s, nil)["m-stopped.w1"], window, "45m of running time is outside the window")
	assert.Contains(t, kept["m-provb.w1"], "newest ok finish of provider prov-b")
	assert.Contains(t, kept["a-00.w1"], "route r2's newest")
	assert.Contains(t, kept["m-13.w1"], "row's newest 10")

	rows, p := TidyDone(w.s, []string{TidyFleet, TidyFriends}, stopped)
	moved := movedIDs(rows)
	assert.Equal(t, "failed", moved["m-failed.w1"], "a failed card is tidied, and says its cell")
	for i := range 6 {
		assert.Equal(t, "ok", moved[fmt.Sprintf("m-%02d.w1", i)], "the row's older ok finishes are tidied")
	}
	assert.NotContains(t, moved, "m-06.w1", "the newest ten of the row (with the two recent) stay")
	assert.Equal(t, "ok", moved["a-01.w1"], "the friend's row is tidied")
	assert.NotContains(t, moved, "a-00.w1", "the route's sample stays")
	assert.Len(t, moved, 7+1)
	w.must(p)
	assert.Len(t, w.s.Fleet.Cell(m, DoneOK), 17-6)
	assert.Empty(t, w.s.Fleet.Cell(m, DoneFailed))
	assert.Len(t, w.s.Fleet.Cell(amy, DoneOK), 11)
	require.NotNil(t, w.s.Fleet.Card("m-failed.w1"), "its record is kept")
	assert.False(t, w.s.Fleet.Card("m-failed.w1").Placed())

	// --fleet alone leaves the friend's row
	w2 := newWorld(t, "reader-a")
	for i := range 12 {
		putDone(w2, amy, fmt.Sprintf("b-%02d.w1", i), DoneOK, now.Add(-12*time.Hour+time.Duration(i)*time.Minute), "prov-a/x", "", "")
	}
	rows, _ = TidyDone(w2.s, []string{TidyFleet}, nil)
	assert.Empty(t, rows)
}

// A machine whose median run wall is 20 minutes, on a 30-minute route, keeps its
// 60-minute deadline through a tidy: the tidy carries the median (PropCarriedMedian) while
// the live sample is smaller than the carried count, and drops it once it is as large.
func TestATidyKeepsTheDeadlineItsMedianGave(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a")
	now := w.s.Now
	m := "carry-m1"
	// forty old ok attempts of 20 minutes, then the ten newest of 100 s
	for i := range 40 {
		putDone(w, m, fmt.Sprintf("c-%02d.w1", i), DoneOK, now.Add(-30*time.Hour+time.Duration(i)*time.Minute), "prov-a/x", "", "1200s")
	}
	for i := 40; i < 50; i++ {
		putDone(w, m, fmt.Sprintf("c-%02d.w1", i), DoneOK, now.Add(-10*time.Hour+time.Duration(i)*time.Minute), "prov-a/x", "", "100s")
	}
	median, n := MemberMedianWall(w.s, m)
	require.Equal(t, 1200.0, median)
	require.Equal(t, 50, n)
	require.Equal(t, 3600, w.s.memberDeadline(m, 1800), "three times 20 minutes, over the route's 30")

	rows, p := TidyDone(w.s, []string{TidyFleet}, nil)
	require.Len(t, movedIDs(rows), 40)
	w.must(p)
	v, ok := w.s.Fleet.Prop(PropCarriedMedian(m))
	require.True(t, ok)
	assert.Equal(t, "1200 50", v)
	median, n = MemberMedianWall(w.s, m)
	assert.Equal(t, 1200.0, median, "the live ten say 100 s; the carried median stands")
	assert.Equal(t, 50, n)
	assert.Equal(t, 3600, w.s.memberDeadline(m, 1800), "the deadline is unchanged by the tidy")

	// forty more 100 s attempts: the live sample is as large as the carried, which drops
	for i := 50; i < 90; i++ {
		putDone(w, m, fmt.Sprintf("c-%02d.w1", i), DoneOK, now.Add(-time.Hour+time.Duration(i-50)*time.Second), "prov-a/x", "", "100s")
	}
	w.s.Fleet.cells = nil
	median, n = MemberMedianWall(w.s, m)
	assert.Equal(t, 100.0, median, "the live sample is the carried count: the live median")
	assert.Equal(t, 50, n)
	assert.Equal(t, 1800, w.s.memberDeadline(m, 1800))

	// a friend's median is carried the same way
	amy := FriendRow("carry-amy")
	for i := range 12 {
		c := fmt.Sprintf("f-%02d.w1", i)
		putDone(w, amy, c, DoneOK, now.Add(-10*time.Hour+time.Duration(i)*time.Minute), "prov-a/x", "", "")
		card := w.s.Fleet.Card(c)
		card.Fields["taken"] = now.Add(-11 * time.Hour).UTC().Format(time.RFC3339)
		card.Fields[FieldReported] = now.Add(-11*time.Hour + time.Duration(600+i)*time.Second).UTC().Format(time.RFC3339)
	}
	w.s.Fleet.cells = nil
	fm, fn := FriendMedianWall(w.s, "carry-amy")
	require.Equal(t, 12, fn)
	_, p = TidyDone(w.s, []string{TidyFriends}, nil)
	w.must(p)
	gm, gn := FriendMedianWall(w.s, "carry-amy")
	assert.Equal(t, fm, gm, "her median is carried through the tidy")
	assert.Equal(t, 12, gn)
}

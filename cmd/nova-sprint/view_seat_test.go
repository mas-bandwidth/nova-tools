package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintdash"
)

// The seat view (view_seat.go; docs/SPEC-SPRINT.md, "Role views", view seat): the
// coordinator's model read from the dashboard's own snapshot, with no socket: the dashboard
// is a sprintdash.Server over the twin, answered in this process through the GET seam.

// seatDashboard serves ta's sprint as the dashboard does, reading it once an hour so the
// snapshot holds still, and answers view seat's GET from it; asked collects the URLs asked.
func seatDashboard(ta *testApp) (*sprintdash.Server, *[]string) {
	srv := &sprintdash.Server{Read: func() ([]byte, error) { return ta.a.whereJSON("", false) }, Now: ta.a.now, Every: time.Hour}
	var asked []string
	ta.a.outside.httpGet = func(_ context.Context, url string) (int, []byte, error) {
		asked = append(asked, url)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, strings.TrimPrefix(url, "http://"+DashboardListenDefault), nil))
		return w.Code, w.Body.Bytes(), nil
	}
	return srv, &asked
}

// view seat is the dashboard's own snapshot: its fetchedAt, every friend's and machine's row,
// the live streams' counts and the ready pool are the snapshot's, read from the dashboard it
// names; a change to the store does not reach it until the dashboard reads again; a snapshot
// older than 30s is refused, naming the dashboard.
func TestViewSeatIsTheDashboardsOwnSnapshot(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy", "bob")
	ta.ok("friend sync")
	ta.ok("add --stream s1 --count 4")
	ta.ok("add --stream s2 --count 2")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("tick")
	srv, asked := seatDashboard(ta)

	var v seatModel
	ta.json("view seat", &v)
	require.Equal(t, []string{"http://" + DashboardListenDefault + "/api/sprint?release=all"}, *asked, "one read of the dashboard, every stream")
	assert.Equal(t, "seat", v.View)
	assert.Equal(t, DashboardListenDefault, v.Server)

	var snap seatSnapshot
	require.NoError(t, json.Unmarshal(srv.SnapshotOf(sprintdash.AllReleases), &snap))
	require.True(t, snap.OK)
	assert.True(t, snap.FetchedAt.Equal(v.FetchedAt), "fetchedAt is the snapshot's: %s %s", snap.FetchedAt, v.FetchedAt)
	assert.Equal(t, snap.Data.Ready, v.Ready, "the ready pool")
	assert.Equal(t, snap.Data.Width, v.Width)
	assert.Equal(t, snap.Data.Summary, v.Sum)

	rows := map[string]seatRow{}
	for _, r := range v.Rows {
		rows[r.K] = r
	}
	want := 0
	for table, prefix := range map[string]string{sprint.Friends: "f:", sprint.Fleet: "m:"} {
		for name, cells := range snap.Data.Tables[table] {
			want++
			r, ok := rows[prefix+name]
			if assert.True(t, ok, "every %s row: %s", table, name) {
				assert.Equal(t, cellText(cells[sprint.Status]), r.St, name)
				assert.Equal(t, cellInt(cells[string(sprint.Ready)]), r.R, name)
				assert.Equal(t, cellInt(cells[string(sprint.Working)]), r.W, name)
				assert.Equal(t, cellInt(cells[sprint.FieldWidth]), r.Wd, name)
			}
		}
	}
	assert.Len(t, v.Rows, want, "every row and no other: %+v", v.Rows)
	assert.Contains(t, rows, "f:amy")
	assert.Contains(t, rows, "m:m1")
	assert.Equal(t, "never", rows["f:amy"].Beat, "a friend who has never beaten")

	var streams []string
	for _, s := range v.Streams {
		streams = append(streams, s.S)
		cells := snap.Data.Tables[sprint.Work][s.S]
		assert.Equal(t, cellInt(cells[string(sprint.Waiting)]), s.Wait, s.S)
		assert.Equal(t, cellInt(cells[string(sprint.Ready)]), s.Ready, s.S)
		assert.Equal(t, cellInt(cells[string(sprint.Working)]), s.Work, s.S)
		assert.Equal(t, map[string]int{"s1": 4, "s2": 2}[s.S], s.Wait+s.Ready+s.Work+s.Review+s.Merge+s.Landed, "every card of %s in a column", s.S)
	}
	assert.ElementsMatch(t, []string{"s1", "s2"}, streams)

	// ten seconds on, the store moves; the seat does not, until the dashboard reads it again
	ta.later(10 * time.Second)
	ta.ok("add --stream s3 --count 2")
	var again seatModel
	ta.json("view seat", &again)
	assert.True(t, again.FetchedAt.Equal(v.FetchedAt), "the snapshot's time, never the reader's clock")
	assert.Equal(t, "10s", again.Age)
	for _, s := range again.Streams {
		assert.NotEqual(t, "s3", s.S, "the seat reads the snapshot and nothing else")
	}
	text := ta.ok("view seat")
	assert.True(t, strings.HasPrefix(text, "VIEW seat server="+DashboardListenDefault+" fetchedAt="), text)
	assert.Contains(t, text, "\nROW f:amy ")
	assert.Contains(t, text, "\nSTREAM s1 ")

	// 31 seconds after its read, with no read since, the snapshot is refused, naming the dashboard
	ta.later(21 * time.Second)
	code, out, errs := ta.do("view seat --json")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errs, "nova-sprint view seat REFUSED: the dashboard at "+DashboardListenDefault+" serves a snapshot 31s old")
	assert.Contains(t, errs, "older than 30s")
	assert.Contains(t, errs, "; run: nova-sprint seat check")

	// --dashboard names another; one that does not answer is exit 2, named
	ta.a.outside.httpGet = func(context.Context, string) (int, []byte, error) { return 0, nil, errors.New("connection refused") }
	code, _, errs = ta.do("view seat --dashboard 127.0.0.1:7999")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "the dashboard at 127.0.0.1:7999 did not answer: connection refused")
}

// A dashboard with no good read is refused, its error named; a fresh one is read.
func TestViewSeatRefusesADashboardWithNoGoodRead(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 23, 0, 0, 0, time.UTC)
	why := "where exited 2"
	_, refused := seatOf(seatSnapshot{OK: false, FetchedAt: &now, Error: &why}, "d:1", now)
	assert.Contains(t, refused, "the dashboard at d:1 serves no good snapshot (its last read failed: where exited 2)")
	_, refused = seatOf(seatSnapshot{OK: true}, "d:1", now)
	assert.Contains(t, refused, "it has made no good read yet")
	at := now.Add(-30 * time.Second)
	v, refused := seatOf(seatSnapshot{OK: true, FetchedAt: &at}, "d:1", now)
	assert.Empty(t, refused, "30s is still fresh")
	assert.Equal(t, "30s", v.Age)
}

// The things out of place, each with the command that answers it, ranked by the cards they
// hold up, at most five, nout counting them all.
func TestViewSeatNamesWhatIsOutOfPlace(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 5, 23, 0, 0, 0, time.UTC)
	row := func(st string, r, w int) map[string]any {
		return map[string]any{sprint.Status: st, string(sprint.Ready): strconv.Itoa(r), string(sprint.Working): strconv.Itoa(w), sprint.FieldWidth: "1"}
	}
	work := func(wait, ready int) map[string]any {
		return map[string]any{string(sprint.Waiting): strconv.Itoa(wait), string(sprint.Ready): strconv.Itoa(ready), string(sprint.Landed): "1"}
	}
	snap := seatSnapshot{OK: true, FetchedAt: &at, Data: whereView{Ready: 2, Held: 3,
		Tables: map[string]map[string]map[string]any{
			sprint.Friends: {"amy": row("up", 0, 0), "bob": row("up", 4, 1), "cat": row("up", 0, 1), "dan": row("down", 0, 0)},
			sprint.Fleet:   {"m1": row("up", 1, 1)},
			sprint.Work:    {"s1": work(5, 2), "s2": work(0, 0), "s3": work(2, 0)},
			sprint.Readers: {"reader-a": {sprint.Asked: "3", sprint.Reading: "0", sprint.FieldWidth: "4"}, "reader-b": {sprint.Asked: "1", sprint.Reading: "1"}},
		},
		Friends: []store.FriendRow{{Name: "amy", Beat: at.Add(-time.Minute)}, {Name: "bob", Beat: at.Add(-time.Minute)}, {Name: "cat", Beat: at.Add(-20 * time.Minute)}},
		Streams: []sprint.StreamClock{{Stream: "s3", State: sprint.StreamStopped, Reason: "conflict"}, {Stream: "s1", State: "running"}},
		Cards: []dealtCard{
			{ID: "s1-1.w1", Member: "friend.bob", State: "working", Deadline: at.Add(-5 * time.Minute)},
			{ID: "s1-2.w1", Member: "m1", State: "working", Deadline: at.Add(-time.Minute)},
			{ID: "s1-3.w1", Member: "m1", State: "ready", Deadline: at.Add(time.Minute)},
		},
		Judgments: []judgmentRef{{ID: "n1", Kind: "failed", Card: "s1-1"}, {ID: "n1", Kind: "failed", Card: "s1-2"}, {ID: "n2", Kind: "stale", Card: "s1-3"}},
		Critical:  []sprint.CriticalCard{{ID: "s1-1", Behind: 4, State: "working"}},
		Holds:     []sprint.HoldView{{Kind: "stream", Name: "s1", Reason: "wave two"}},
	}}
	v, refused := seatOf(snap, "d:1", at.Add(time.Second))
	require.Empty(t, refused)

	assert.Equal(t, []seatStream{{S: "s3", St: sprint.StreamStopped, Wait: 2, Landed: 1}, {S: "s1", St: "running", Wait: 5, Ready: 2, Landed: 1}}, v.Streams,
		"the live streams in the snapshot's order; s2 is wholly landed")
	assert.Equal(t, map[string]int{"failed": 1, "stale": 1}, v.J, "a judgment once, by kind")
	assert.Equal(t, []seatGate{{G: "card:s1-1", N: 4, S: "working"}, {G: "hold:stream:s1", N: 7, S: "wave two"}, {G: "held", N: 3, S: "behind a sentinel not released, or admitted held"}}, v.Gates)
	assert.Equal(t, "20m", rowOf(v, "f:cat").Beat)
	assert.Equal(t, "never", rowOf(v, "f:dan").Beat)
	assert.Empty(t, rowOf(v, "m:m1").Beat, "the snapshot carries no machine's beat")

	type line struct{ W, Next string }
	var got []line
	for _, o := range v.Out {
		got = append(got, line{o.W, o.Next})
	}
	assert.Equal(t, []line{
		{outFriendIdle, "nova-sprint friend take bob --all-unstarted --reason 'amy is up at 0'"},
		{outReader, "nova-sprint queue --as reader-a"},
		{outStopped, "nova-sprint resume --stream s3"},
		{outLate, "nova-sprint friend take bob s1-1.w1 --reason '5m past its deadline'"},
		{outLate, "nova-sprint log --card s1-2.w1"},
	}, got, "ranked by the cards held up, then by kind, then by key: %+v", v.Out)
	assert.Equal(t, 6, v.NOut, "a stale beat ranks after a late card of as many cards: it is the sixth")
	assert.Equal(t, "amy is up at 0 while bob holds 4 unstarted", v.Out[0].S)
	assert.Equal(t, "stream s3 is stopped (conflict) with 2 cards not landed", v.Out[2].S)

	// a reader at width 0 is brought up; an idle friend with no holder is answered by the deal
	snap.Data.Tables[sprint.Readers]["reader-a"][sprint.FieldWidth] = "0"
	snap.Data.Tables[sprint.Friends]["bob"] = row("up", 0, 1)
	v, _ = seatOf(snap, "d:1", at)
	for _, o := range v.Out {
		switch o.W {
		case outReader:
			assert.Equal(t, "nova-sprint reader up reader-a", o.Next)
		case outFriendIdle:
			assert.Equal(t, "nova-sprint friend sync", o.Next)
			assert.Equal(t, "amy is up at 0 while 2 cards are ready", o.S)
		}
	}
}

func rowOf(v seatModel, k string) seatRow {
	for _, r := range v.Rows {
		if r.K == k {
			return r
		}
	}
	return seatRow{}
}


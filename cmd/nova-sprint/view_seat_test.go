package main

import (
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
	"github.com/mas-bandwidth/nova-tools/internal/sprintdash"
)

// roundTrip is a transport that is a function: a dashboard's handler served with no socket.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// served is the transport that answers every request from h, and the URLs asked.
func served(h http.Handler, asked *[]string) roundTrip {
	return func(r *http.Request) (*http.Response, error) {
		*asked = append(*asked, r.URL.String())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Result(), nil
	}
}

// view seat is the dashboard's own snapshot (view_seat.go): one GET of /api/sprint, every
// friend's and machine's row and every live stream's counts as the snapshot carries them, a
// change of the store after the snapshot unseen until the dashboard reads again, and a
// snapshot older than 30s refused naming the dashboard.
func TestViewSeatIsTheDashboardsOwnSnapshot(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy", "bob")
	ta.ok("add --stream s2 --count 3")
	ta.ok("tick")
	srv := &sprintdash.Server{Read: func() ([]byte, error) { return ta.a.whereJSON("", false) }, Now: ta.a.now, Every: time.Hour}
	var asked []string
	ta.a.transport = served(srv, &asked)
	const server = "http://127.0.0.1:7390/api/sprint?release=all"

	var v modelView
	ta.json("view seat --dashboard 127.0.0.1:7390", &v)
	require.Equal(t, []string{server}, asked, "one GET of the snapshot, every stream")
	assert.Equal(t, "seat", v.View)
	assert.Equal(t, server, v.Server)

	// the snapshot as the page reads it, decoded apart from the verb
	var snap struct {
		FetchedAt time.Time `json:"fetchedAt"`
		Data      struct {
			Summary string                                  `json:"summary"`
			Ready   int64                                   `json:"ready"`
			Tables  map[string]map[string]map[string]string `json:"tables"`
			Friends []struct {
				Name    string    `json:"name"`
				Status  string    `json:"status"`
				Ready   int       `json:"ready"`
				Working int       `json:"working"`
				Width   int       `json:"width"`
				Beat    time.Time `json:"beat"`
			} `json:"friends"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(srv.SnapshotOf(sprintdash.AllReleases), &snap))
	assert.True(t, v.FetchedAt.Equal(snap.FetchedAt), "fetchedAt is the snapshot's: %s %s", v.FetchedAt, snap.FetchedAt)
	assert.Equal(t, snap.Data.Summary, v.Sum)
	assert.Equal(t, snap.Data.Ready, v.Ready)
	require.Len(t, v.Friends, len(snap.Data.Friends))
	for i, f := range snap.Data.Friends {
		assert.Equal(t, modelRow{N: f.Name, St: f.Status, R: f.Ready, W: f.Working, Wd: f.Width, Beat: ageWord(snap.FetchedAt.Sub(f.Beat))}, v.Friends[i], "friend %s", f.Name)
	}
	for _, f := range v.Friends {
		if f.N == "amy" {
			assert.GreaterOrEqual(t, f.W, 1, "amy's card is working on her row")
		}
	}
	require.Len(t, v.Fleet, len(snap.Data.Tables[sprint.Fleet]))
	for _, m := range v.Fleet {
		cells := snap.Data.Tables[sprint.Fleet][m.N]
		assert.Equal(t, cells["status"], m.St, "machine %s", m.N)
		assert.Equal(t, cells["ready"], strconv.Itoa(m.R), "machine %s", m.N)
		assert.Equal(t, cells["working"], strconv.Itoa(m.W), "machine %s", m.N)
		assert.Equal(t, cells["width"], strconv.Itoa(m.Wd), "machine %s", m.N)
	}
	require.Len(t, v.Streams, 2, "s1 and s2 are live: %+v", v.Streams)
	for _, s := range v.Streams {
		cells := snap.Data.Tables[sprint.Work][s.N]
		for col, n := range map[string]int{sprint.Waiting: s.Wait, sprint.Ready: s.Ready, sprint.Working: s.Work, sprint.Review: s.Review, sprint.Merging: s.Merge, sprint.Landed: s.Landed} {
			assert.Equal(t, cells[col], strconv.Itoa(n), "stream %s %s", s.N, col)
		}
	}
	assert.LessOrEqual(t, len(v.Out), modelOutMax)
	for _, o := range v.Out {
		assert.True(t, strings.HasPrefix(o.Next, "nova-sprint "), "every thing out of place carries its verb: %+v", o)
	}

	// the store moves; the seat's model is the dashboard's, which has not read again
	ta.ok("friend down bob --reason 'away for the night'")
	var bobNow string
	for _, r := range ta.coordView("--all").Rows {
		if r.K == "f:bob" {
			bobNow = r.St
		}
	}
	require.Equal(t, sprint.Held, bobNow, "the store holds bob")
	ta.json("view seat --dashboard 127.0.0.1:7390", &v)
	for _, f := range v.Friends {
		if f.N == "bob" {
			assert.Equal(t, sprint.Up, f.St, "the seat reads the snapshot and nothing else")
		}
	}
	text := ta.ok("view seat --dashboard http://127.0.0.1:7390/")
	assert.True(t, strings.HasPrefix(text, "VIEW seat fetchedAt="+snap.FetchedAt.UTC().Format(time.RFC3339)+" age=0s server="+server+"\n"), "%s", text)
	assert.Contains(t, text, "\nfriend amy ")

	// 31s on, the snapshot is an old model: refused, naming the dashboard
	ta.a.sleep(modelMaxAge + time.Second)
	code, out, errs := ta.do("view seat --dashboard 127.0.0.1:7390 --json")
	assert.Equal(t, 2, code, "%s%s", out, errs)
	assert.Empty(t, out)
	assert.Contains(t, errs, "nova-sprint view seat REFUSED: the dashboard at "+server+" served a snapshot 31s old")
	assert.Contains(t, errs, "older than 30s")

	// a dashboard that does not answer is named too
	ta.a.transport = roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") })
	code, _, errs = ta.do("view seat")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "the dashboard at "+server+" did not answer")
	code, _, errs = ta.do("view seat --dashboard ftp://somewhere.invalid")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--dashboard wants the dashboard's address:port or its http(s) URL")
	assert.Contains(t, readVerb([]string{"view", "seat"}).unserved(), "view seat is not run by the server", "the server never runs it: it reads the dashboard where it is typed")
}

// The five things out of place, each one line with its verb, in their order: a stream
// stopped, a card past its deadline, an up friend at 0 with cards ready elsewhere, a reader
// behind, a friend whose beat is stale; nout counts every one.
func TestViewSeatNamesWhatIsOutOfPlace(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 5, 23, 0, 0, 0, time.UTC)
	data := map[string]any{
		"summary": "LANDING 1/9", "ready": 2, "width": 8,
		"tables": map[string]any{
			sprint.Work:    map[string]any{"s1": map[string]string{"ready": "2", "working": "1", "landed": "1"}, "s2": map[string]string{"waiting": "4", "landed": "1"}, "s3": map[string]string{"landed": "3"}},
			sprint.Merge:   map[string]any{"s2": map[string]string{"state": "stopped"}},
			sprint.Readers: map[string]any{"reader-a": map[string]string{"asked": "3", "reading": "0"}, "reader-b": map[string]string{"asked": "1", "reading": "1"}},
			sprint.Fleet:   map[string]any{"m1": map[string]string{"status": "up", "ready": "0", "working": "1", "width": "4", "load": "12.0%"}},
		},
		"friends": []map[string]any{
			{"name": "amy", "status": "up", "width": 2, "beat": at.Add(-5 * time.Second)},
			{"name": "bob", "status": "down", "ready": 1, "width": 2, "beat": at.Add(-20 * time.Minute)},
			{"name": "cat", "status": "up", "width": 3, "beat": at.Add(-2 * time.Second)},
		},
		"cards":     []map[string]any{{"id": "s1-1.w1", "primary": "s1-1", "member": "m1", "state": "working", "deadline": at.Add(-10 * time.Minute)}},
		"judgments": []map[string]string{{"id": "n1", "kind": "work failed", "card": "s1-1"}, {"id": "n2", "kind": "work failed", "card": "s1-2"}},
		"critical":  []map[string]any{{"id": "s2-stop", "behind": 4, "state": "waiting"}},
	}
	body, err := json.Marshal(map[string]any{"ok": true, "data": data, "fetchedAt": at})
	require.NoError(t, err)
	v, err := modelOf(body, "http://dash.test/api/sprint?release=all", at.Add(3*time.Second))
	require.NoError(t, err)
	assert.Equal(t, "3s", v.Age)
	assert.Equal(t, map[string]int{"work failed": 2}, v.J)
	assert.Equal(t, []modelGate{{G: "s2-stop", K: "card", B: 4, St: "waiting"}}, v.Gates)
	assert.Equal(t, []string{"s1", "s2"}, []string{v.Streams[0].N, v.Streams[1].N}, "s3 has landed: no live stream")
	assert.Equal(t, modelRow{N: "m1", St: "up", W: 1, Wd: 4, Load: "12.0%"}, v.Fleet[0])
	assert.Equal(t, 6, v.NOut, "two idle friends, one of each other kind")
	var kinds, nexts []string
	for _, o := range v.Out {
		kinds, nexts = append(kinds, o.K), append(nexts, o.Next)
	}
	assert.Equal(t, []string{outStopped, outLate, outIdle, outIdle, outReader}, kinds, "%+v", v.Out)
	assert.Equal(t, []string{
		"nova-sprint resume --stream s2 --did '<what you fixed>'",
		"nova-sprint log --card s1-1 --since 1h",
		"nova-sprint friend level",
		"nova-sprint friend level",
		"nova-sprint queue --as reader-a",
	}, nexts)
	assert.Equal(t, "s1-1.w1 on m1 is 10m past its deadline (working)", v.Out[1].S)

	// one fewer idle friend: the stale beat is the fifth
	data["ready"] = 0
	body, err = json.Marshal(map[string]any{"ok": true, "data": data, "fetchedAt": at})
	require.NoError(t, err)
	v, err = modelOf(body, "http://dash.test/api/sprint?release=all", at)
	require.NoError(t, err)
	require.Len(t, v.Out, 4, "%+v", v.Out)
	assert.Equal(t, outBeat, v.Out[3].K)
	assert.Equal(t, "nova-sprint friend take bob --all-unstarted --reason 'bob has not beaten for 20m'", v.Out[3].Next)

	// a snapshot of no sprint is refused, naming the dashboard
	_, err = modelOf([]byte(`{"ok":false,"data":null,"fetchedAt":null,"error":"where exited 1"}`), "http://dash.test/api/sprint?release=all", at)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the dashboard at http://dash.test/api/sprint?release=all served no snapshot of the sprint (where exited 1)")
}

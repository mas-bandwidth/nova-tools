package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
)

// The tick's snapshot (serve.go, whereSnapshots; docs/SPEC-SPRINT.md section 14, The
// server, "The tick's snapshot"). The owner, 2026-10-07 6:55 PM ET: "It's a requirement
// that it updates at 1s." That evening where --json through the server took 4.6 to 8.5 s
// (89 to 112 store trips) while the tick had the same display ready in 29 ms. The server
// keeps its last tick's where --json document and answers where --json from it at once.
// These tests step the server in one process on the in-memory store: the clock is the
// test's, the HTTP is httptest's recorder, and no socket is opened.

// snapRig is a serving app over the in-memory store with an archived stream (s1, landed and
// archived by the tick), a stream on the table (s2) with its cards dealt, and the server's
// snapshots kept (publishWhere, as run --listen keeps them); its binary a file of the test's.
func snapRig(t *testing.T) (*testApp, *whereSnapshots) {
	t.Helper()
	ta := headlineTwoStreams(t)
	ta.ok("tick")
	exe := filepath.Join(t.TempDir(), "nova-sprint")
	require.NoError(t, os.WriteFile(exe, []byte("the build the server started with"), 0o755))
	ta.a.executable = func() (string, error) { return exe, nil }
	ta.a.serveAddr = "mem:0"
	return ta, ta.a.publishWhere()
}

// publish is one tick's end on the server, and its document read.
func (s *whereSnapshots) publish(a *app, n int) {
	s.ticked(a, n)
	<-s.settled()
}

// snapshotAge is the snapshot field of a where --json answered from the snapshot.
func snapshotAge(t *testing.T, doc string) snapshotMeta {
	t.Helper()
	var v whereView
	require.NoError(t, json.Unmarshal([]byte(doc), &v), doc)
	require.NotNil(t, v.Snapshot, "answered from the snapshot: %s", doc)
	return *v.Snapshot
}

// A tick of the run loop publishes the document: the build starts at the tick's end, beside
// the line, and the document is the tick's, at the time it ended.
func TestATickPublishesTheWhereSnapshot(t *testing.T) {
	t.Parallel()
	ta, s := snapRig(t)
	require.Nil(t, s.doc, "no tick yet: no document")
	st, _, code := ta.a.machineVerb("run", nil, &bytes.Buffer{})
	require.NotNil(t, st, "run: %d", code)
	var out, errb startBuffer
	began := ta.a.now()
	ta.a.runLoop(context.Background(), st, 20, 1, &out, &errb)
	<-s.settled()
	require.NotNil(t, s.doc, "the tick published a document:\n%s%s", out.String(), errb.String())
	assert.Equal(t, 1, s.doc.tick)
	assert.False(t, s.doc.at.Before(began), "the document's time is the tick's end: %s, the tick began %s", s.doc.at, began)
	assert.False(t, s.doc.view.At.Before(s.doc.at), "read once the tick ended")
	assert.NotEmpty(t, s.doc.view.Cards, "the fullest document: --cards")
	assert.NotEmpty(t, s.doc.view.Rows, "the fullest document: --rows")
	assert.Contains(t, s.doc.view.Tables["work"], "s1", "the fullest document: --archived")
	assert.Empty(t, s.failed)
}

// A where --json sent to the server is answered from the document with its age, with no
// store trip; within two ticks it is served, past them it is refused with the exact line,
// and --fresh reads the store whatever the document's age. A server whose binary was
// replaced under it (a switch under way) refuses too.
func TestAForwardedWhereJSONIsTheSnapshotUntilTwoTicksOld(t *testing.T) {
	t.Parallel()
	ta, s := snapRig(t)
	none := serveOne(ta.a, true, "where", "--json")
	assert.Equal(t, 1, none.Code)
	assert.Equal(t, "nova-sprint where: snapshot stale: none yet, no tick has ended since the server began; run where --json --fresh\n", none.Stderr)

	s.publish(ta.a, 1)
	at := ta.a.now()
	ta.a.sleep(time.Second)
	ta.m.Calls = map[string]int{}
	res := serveOne(ta.a, true, "where", "--json", "--cards", "--actor", "coordinator")
	calls := ta.m.Calls
	ta.m.Calls = map[string]int{}
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.Empty(t, calls, "answered from memory: no store trip")
	meta := snapshotAge(t, res.Stdout)
	assert.True(t, at.Equal(meta.At), "at is the tick's end: %s", meta.At)
	assert.Equal(t, int64(1000), meta.AgeMS)
	assert.Equal(t, 2, ta.a.served.reads, "counted with the reads, the refusal too")

	// past two ticks: refused, never served silently
	ta.a.sleep(2 * time.Second)
	stale := serveOne(ta.a, true, "where", "--json")
	assert.Equal(t, 1, stale.Code)
	assert.Empty(t, stale.Stdout)
	assert.Equal(t, "nova-sprint where: snapshot stale: 3s; run where --json --fresh\n", stale.Stderr)

	// --fresh reads the store
	fresh := serveOne(ta.a, true, "where", "--json", "--fresh")
	require.Equal(t, 0, fresh.Code, fresh.Stderr)
	var v whereView
	require.NoError(t, json.Unmarshal([]byte(fresh.Stdout), &v))
	assert.Nil(t, v.Snapshot, "a read of the store carries no snapshot field")
	assert.Equal(t, ta.a.now(), v.At)
	// so does a flag the document does not hold
	stalled := serveOne(ta.a, true, "where", "--json", "--stale", "1m")
	require.Equal(t, 0, stalled.Code, stalled.Stderr)
	assert.NotContains(t, stalled.Stdout, `"snapshot"`)

	// the next tick: served again
	s.publish(ta.a, 2)
	res = serveOne(ta.a, true, "where", "--json")
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.Equal(t, int64(0), snapshotAge(t, res.Stdout).AgeMS)

	// the two ticks are the last tick's period: ticks 4 s apart serve a document 7 s old
	ta.a.sleep(4 * time.Second)
	s.publish(ta.a, 3)
	ta.a.sleep(7 * time.Second)
	res = serveOne(ta.a, true, "where", "--json")
	require.Equal(t, 0, res.Code, res.Stderr)
	assert.Equal(t, int64(7000), snapshotAge(t, res.Stdout).AgeMS)

	// a switch under way: the binary replaced under the server
	s.publish(ta.a, 4)
	exe, err := ta.a.executable()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(exe, []byte("the build installed under it, longer"), 0o755))
	switching := serveOne(ta.a, true, "where", "--json")
	assert.Equal(t, 1, switching.Code)
	assert.Equal(t, "nova-sprint where: snapshot stale: 0s, the server's binary was replaced (a switch under way); run where --json --fresh\n", switching.Stderr)
	// --fresh still reads the store
	require.Equal(t, 0, serveOne(ta.a, true, "where", "--json", "--fresh").Code)
	// and --fresh without --json is refused: where without --json always reads the store
	alone := serveOne(ta.a, true, "where", "--fresh")
	assert.Equal(t, 2, alone.Code)
	assert.Contains(t, alone.Stderr, "--fresh is a read of the JSON view: give --json with it")
}

// GET /api/sprint answers the full document (--cards --rows --archived) with its age, and
// the same refusal as a 503: a page that needs the document never spawns a process.
func TestGetAPISprintIsTheSnapshot(t *testing.T) {
	t.Parallel()
	ta, s := snapRig(t)
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		localHandler{ta.a}.ServeHTTP(w, httptest.NewRequest(http.MethodGet, sprintPath, nil))
		return w
	}
	w := get()
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "snapshot stale: none yet, no tick has ended since the server began; run where --json --fresh\n", w.Body.String())

	s.publish(ta.a, 1)
	ta.a.sleep(500 * time.Millisecond)
	w = get()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	var v whereView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	require.NotNil(t, v.Snapshot)
	assert.Equal(t, int64(500), v.Snapshot.AgeMS)
	assert.NotEmpty(t, v.Cards)
	assert.NotEmpty(t, v.Rows)
	assert.Contains(t, v.Tables["work"], "s1", "the archived stream's row: the dashboard's document")
	// the fleet's listener serves it too, and a POST is not the way to read it
	w = httptest.NewRecorder()
	ta.a.ServeHTTP(w, httptest.NewRequest(http.MethodGet, sprintPath, nil))
	assert.Equal(t, http.StatusOK, w.Code)
	w = httptest.NewRecorder()
	ta.a.ServeHTTP(w, httptest.NewRequest(http.MethodPost, sprintPath, nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)

	ta.a.sleep(2 * store.TickEvery)
	w = get()
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "snapshot stale: 2.5s; run where --json --fresh\n", w.Body.String())
}

// The golden: each where --json the snapshot answers (every mix of --cards, --rows and
// --archived) is, but its snapshot field, byte for byte the document a read of the store
// gives for the same rows at the same time.
func TestTheSnapshotIsTheFreshReadOfTheSameRows(t *testing.T) {
	t.Parallel()
	ta, s := snapRig(t)
	s.publish(ta.a, 1)
	require.NotNil(t, s.doc.view.Archived, "the fixture has an archived stream")
	for _, flags := range [][]string{
		nil, {"--cards"}, {"--rows"}, {"--archived"}, {"--cards", "--rows"}, {"--cards", "--archived"},
		{"--rows", "--archived"}, {"--cards", "--rows", "--archived"}, {"--all", "--cards"},
	} {
		argv := append([]string{"where", "--json"}, flags...)
		snap := serveOne(ta.a, true, argv...)
		require.Equal(t, 0, snap.Code, "%v: %s", flags, snap.Stderr)
		fresh := serveOne(ta.a, true, append(argv, "--fresh")...)
		require.Equal(t, 0, fresh.Code, "%v: %s", flags, fresh.Stderr)
		cut := strings.LastIndex(snap.Stdout, `,"snapshot":`)
		require.Positive(t, cut, "%v: %s", flags, snap.Stdout)
		assert.Equal(t, strings.TrimSuffix(fresh.Stdout, "}\n"), snap.Stdout[:cut], "%v: the snapshot is the fresh read", flags)
		assert.Equal(t, `,"snapshot":{"at":"`+s.doc.at.Format(time.RFC3339Nano)+`","age_ms":0}}`+"\n", snap.Stdout[cut:], "%v", flags)
	}
}

// The forward path whole: a where --json typed on the coordinator's machine
// (NOVA_SPRINT_SERVER set) goes through the client's forward, the server's HTTP handler as on
// the wire, and is answered from the snapshot with no store trip, where the store's read took
// 4.6 to 8.5 s on the live sprint. The wall is not a unit test's to bound (docs/STANDARD.md
// section 8: assert the event, not the clock): no store trip is the event.
func TestAForwardedWhereJSONAnswersFromTheSnapshotWhole(t *testing.T) {
	t.Parallel()
	ta, s := snapRig(t)
	s.publish(ta.a, 1)
	env := map[string]string{ServerEnv: "127.0.0.1:7391", "NOVA_SPRINT_ACTOR": "coordinator"}
	client := newApp(func(k string) string { return env[k] })
	client.forward = func(_ context.Context, _ string, verbs ...[]string) ([]sprintwire.Result, error) {
		body, err := json.Marshal(sprintwire.Request{Verbs: verbs})
		if err != nil {
			return nil, err
		}
		w := httptest.NewRecorder()
		localHandler{ta.a}.ServeHTTP(w, httptest.NewRequest(http.MethodPost, sprintwire.Path, bytes.NewReader(body)))
		var res sprintwire.Response
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			return nil, err
		}
		return res.Results, nil
	}
	ta.m.Calls = map[string]int{}
	var out, errb bytes.Buffer
	code := client.run([]string{"where", "--json", "--cards", "--rows", "--archived"}, &out, &errb)
	calls := ta.m.Calls
	ta.m.Calls = map[string]int{}
	require.Equal(t, 0, code, errb.String())
	assert.Empty(t, calls, "answered from memory: no store trip")
	assert.Equal(t, int64(0), snapshotAge(t, out.String()).AgeMS)
	var v whereView
	require.NoError(t, json.Unmarshal(out.Bytes(), &v))
	assert.NotEmpty(t, v.Cards)
	assert.NotEmpty(t, v.Rows)
	assert.Contains(t, out.String(), `"landedSeries"`, "the document carries the landings series, as where --json does")
}

package sprintdash

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// health is /healthz's code and body on the page's handler and on the pull routes'.
func (r *rig) health() (int, string) {
	r.t.Helper()
	w, p := httptest.NewRecorder(), httptest.NewRecorder()
	r.s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	r.s.Pull().ServeHTTP(p, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(r.t, w.Code, p.Code, "the pull routes' /healthz is the page's")
	assert.Equal(r.t, w.Body.String(), p.Body.String())
	return w.Code, w.Body.String()
}

// The freshness check (docs/SPEC-SPRINT-DASHBOARD.md, Serving and publishing): each tick
// reads, whoever is looking; data older than 2 s for 30 s raises the alarm once (a line
// on Log, /healthz 503, stale in /api/sprint); the first fresh read clears it, one line.
func TestTheFreshnessAlarmIsOlderThanTwoSecondsForThirty(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	for range 3 {
		r.advance(time.Second)
		r.s.Tick()
	}
	assert.Equal(t, 3, r.reads, "a read a tick with no page open")
	code, body := r.health()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ok\n", body)

	r.next = func() ([]byte, error) { return nil, errors.New("the sprint's server does not answer") }
	good := r.now // the last good read's time
	for r.now.Sub(good) < 2*time.Second+30*time.Second-time.Second {
		r.advance(time.Second)
		r.s.Tick()
	}
	assert.NotContains(t, r.log.String(), "ALARM", "older than 2 s for 29 s: no alarm yet")
	assert.Equal(t, false, r.api()["stale"])
	r.advance(time.Second)
	r.s.Tick()
	assert.Equal(t, 1, strings.Count(r.log.String(), "ALARM stale: the served data is 32s old, older than 2s for 30s"), r.log.String())
	code, body = r.health()
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Contains(t, body, "stale: the served data is older than 2s since 2026-10-02T19:00:05Z")
	assert.Equal(t, true, r.api()["stale"])
	for range 10 {
		r.advance(time.Second)
		r.s.Tick()
	}
	assert.Equal(t, 1, strings.Count(r.log.String(), "ALARM"), "once an episode")
	assert.EqualValues(t, 0, r.api()["data"].(map[string]any)["landed"], "the page holds the last good copy")

	r.next = func() ([]byte, error) { return where(1), nil }
	r.advance(time.Second)
	r.s.Tick()
	assert.Equal(t, 1, strings.Count(r.log.String(), "FRESH again"), r.log.String())
	code, _ = r.health()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, false, r.api()["stale"])
}

// Before any good read the data's age runs from the first tick: a dashboard whose sprint
// never answers raises the alarm too.
func TestTheFreshnessAlarmBeforeAnyRead(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.next = func() ([]byte, error) { return nil, errors.New("no sprint here yet") }
	for range 32 {
		r.s.Tick()
		r.advance(time.Second)
	}
	assert.NotContains(t, r.log.String(), "ALARM")
	r.s.Tick()
	assert.Contains(t, r.log.String(), "ALARM stale")
}

// A puller (--pull <url>) serves the upstream dashboard's copy as the upstream serves it:
// the same data, the same time and the same throughput, so the two pages agree; an
// upstream that holds a failed read is a failed read here, and the puller's freshness
// check sees the upstream's age, not its own read's.
func TestThePullerServesTheUpstreamsCopy(t *testing.T) {
	t.Parallel()
	up := newRig(t)
	n := 0
	up.next = func() ([]byte, error) { n++; return where(n), nil }
	for range 11 * 60 {
		up.advance(time.Second)
		up.s.Tick()
	}
	var mu sync.Mutex // the upstream's clock and script, moved here and read by its handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		up.s.ServeHTTP(w, req)
	}))
	t.Cleanup(ts.Close)

	p := newRig(t)
	p.now = up.now.Add(300 * time.Millisecond)
	p.s.From = &Upstream{URL: ts.URL + "/api/sprint"}
	p.s.Tick()
	assert.Zero(t, p.reads, "a puller never reads the sprint")
	mu.Lock()
	theirs := up.api()
	mu.Unlock()
	mine := p.api()
	for _, k := range []string{"ok", "data", "fetchedAt", "throughput", "throughputMinutes"} {
		assert.Equal(t, theirs[k], mine[k], k)
	}
	require.NotNil(t, mine["throughput"], "the upstream's throughput, not ten minutes of the puller's own")

	// the upstream's reads fail: it holds its copy, and so does the puller, which goes stale
	mu.Lock()
	up.next = func() ([]byte, error) { return nil, errors.New("the sprint's server does not answer") }
	mu.Unlock()
	for range 33 {
		mu.Lock()
		up.advance(time.Second)
		up.s.Tick()
		mu.Unlock()
		p.advance(time.Second)
		p.s.Tick()
	}
	assert.Contains(t, p.log.String(), "the upstream dashboard holds its last good copy: the sprint")
	assert.Contains(t, p.log.String(), "ALARM stale")
	assert.Equal(t, theirs["data"], p.api()["data"])

	// an upstream that does not answer as a dashboard
	bad := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(bad.Close)
	q := newRig(t)
	q.s.From = &Upstream{URL: bad.URL + "/api/sprint"}
	q.s.Tick()
	assert.Contains(t, q.log.String(), "the upstream dashboard answered 404 Not Found")
	var v snapshot
	w := httptest.NewRecorder()
	q.s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sprint", nil))
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
	assert.False(t, v.OK)
}

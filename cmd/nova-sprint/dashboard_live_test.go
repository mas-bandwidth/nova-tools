package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// The owner, 2026-10-04: "I need to be able to always trust the dashboard"; "Golang
// nova-tools and nova-sprint verbs only" (docs/SPEC-SPRINT-DASHBOARD.md, Serving and
// publishing). The dashboard reads the live server (NOVA_SPRINT_SERVER), in this process,
// once a second whether or not a page is open; a page between two ticks is answered from
// the copy, which is the server's own where --json --cards; and when the server stops
// answering, the page holds its copy and the freshness check raises one alarm once the
// data is older than 2 s for 30 s, and clears it at the first fresh read. A fake server
// (the rig) and a fake clock: no socket and no real time.
func TestTheDashboardServesTheLiveServersDataOncePerSecond(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	now := time.Date(2026, 10, 4, 21, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	r := newServerRig(t)
	r.a.now = clock
	r.boss("nova-sprint init --readers reader-a --members m1,m2")
	r.boss("nova-sprint add --stream s1 --count 3")

	var sent [][]string
	c, _ := clientOf(t, r, "boss", &sent)
	c.now = clock
	ask, down := c.forward, false
	c.forward = func(ctx context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
		if down {
			return nil, errors.New("dial tcp 127.0.0.1:6390: connect: connection refused")
		}
		return ask(ctx, addr, verbs...)
	}
	var log bytes.Buffer
	srv := c.dashboardServer("", false, "", time.Second, "", &log)
	second := func() {
		mu.Lock()
		now = now.Add(time.Second)
		mu.Unlock()
		srv.Tick()
	}
	reads := func() int {
		return len(slices.DeleteFunc(slices.Clone(sent), func(v []string) bool { return v[0] != "where" }))
	}
	get := func(path string) (int, []byte) {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Code, w.Body.Bytes()
	}
	api := func() (data string, stale bool) {
		code, body := get("/api/sprint")
		require.Equal(t, http.StatusOK, code)
		var v struct {
			Data  json.RawMessage `json:"data"`
			Stale bool            `json:"stale"`
		}
		require.NoError(t, json.Unmarshal(body, &v))
		return string(v.Data), v.Stale
	}

	// a read a second with no page open, each sent to the live server as where --json
	// --cards --rows (the rows place the critical path and are not served)
	for range 5 {
		second()
	}
	assert.Equal(t, 5, reads(), "one read a second, whoever is looking")
	assert.Equal(t, []string{"where", "--actor", "boss", "--json", "--cards", "--rows", "--archived"}, sent[0])
	data, stale := api()
	assert.JSONEq(t, r.boss("nova-sprint where --json --cards"), data, "the page's data is the live server's own")
	assert.False(t, stale)
	for range 3 {
		api()
	}
	assert.Equal(t, 5, reads(), "pages between two ticks are answered from the copy")

	// the server moves; a second later the page has it
	r.boss("nova-sprint add --stream s2 --count 2")
	second()
	data, _ = api()
	assert.JSONEq(t, r.boss("nova-sprint where --json --cards"), data)
	assert.Contains(t, data, `"all":5`)
	good := data

	// the server stops answering: the page holds the last good copy, and the freshness
	// check raises its alarm only once the data has been older than 2 s for 30 s
	down = true
	for range 31 {
		second()
	}
	assert.NotContains(t, log.String(), "ALARM", "older than 2 s for 29 s: no alarm yet")
	code, _ := get("/healthz")
	assert.Equal(t, http.StatusOK, code)
	second()
	assert.Equal(t, 1, strings.Count(log.String(), "ALARM stale: the served data is 32s old, older than 2s for 30s"), log.String())
	assert.Equal(t, 1, strings.Count(log.String(), "read failed: where exited 2: nova-sprint where: dial tcp 127.0.0.1:6390"), "one line a new failure")
	data, stale = api()
	assert.Equal(t, good, data, "the page holds the last good copy")
	assert.True(t, stale)
	code, body := get("/healthz")
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Contains(t, string(body), "stale: the served data is older than 2s")
	for range 5 {
		second()
	}
	assert.Equal(t, 1, strings.Count(log.String(), "ALARM"), "once an episode")

	// the server answers again: the next second is fresh, and the alarm clears
	down = false
	second()
	assert.Equal(t, 1, strings.Count(log.String(), "FRESH again"), log.String())
	data, stale = api()
	assert.JSONEq(t, r.boss("nova-sprint where --json --cards"), data)
	assert.False(t, stale)
	code, _ = get("/healthz")
	assert.Equal(t, http.StatusOK, code)
}

// --pull <url> is a puller of another dashboard: its /api/sprint, whichever form the URL
// takes; anything but an http or https URL with no credential is refused.
func TestDashboardPullOfAURLIsAPuller(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"127.0.0.1:7395":                           "",
		"none":                                     "",
		"http://127.0.0.1:7390":                    "http://127.0.0.1:7390/api/sprint",
		"https://dash.example.test:7390/":          "https://dash.example.test:7390/api/sprint",
		"http://127.0.0.1:7390/api/sprint":         "http://127.0.0.1:7390/api/sprint",
		"http://localhost:7390/sprint/api/sprint/": "http://localhost:7390/sprint/api/sprint",
	} {
		got, err := dashboardUpstream(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	ta := newTestApp(t)
	for _, url := range []string{"ftp://localhost:7390", "http://", "http://u:p@localhost:7390", "http://localhost:7390/?x=1"} {
		code, out, errs := ta.do("dashboard --pull " + url)
		assert.Equal(t, 2, code, "%s: %s%s", url, out, errs)
		assert.Contains(t, errs, "a puller wants the http:// or https:// URL of another dashboard", url)
		assert.NotContains(t, errs, "u:p@", "a credential is never echoed")
		assert.Empty(t, out, url)
	}
}

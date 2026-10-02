package sprintdash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rig is a server on a hand-moved clock whose reads answer from a script: no socket and
// no real time.
type rig struct {
	t     *testing.T
	s     *Server
	now   time.Time
	reads int
	next  func() ([]byte, error)
	log   bytes.Buffer
}

func newRig(t *testing.T) *rig {
	r := &rig{t: t, now: time.Date(2026, 10, 2, 19, 0, 0, 0, time.UTC)}
	r.next = func() ([]byte, error) { return where(0), nil }
	r.s = &Server{
		Now:   func() time.Time { return r.now },
		Every: time.Second,
		Read: func(context.Context) ([]byte, error) {
			r.reads++
			return r.next()
		},
		Log: &r.log,
	}
	return r
}

// where is a where --json object with landed cards.
func where(landed int) []byte {
	return []byte(fmt.Sprintf(`{"at":"2026-10-02T19:00:00Z","landed":%d,"all":100,"summary":"x","tables":{"work":{}}}`, landed))
}

func (r *rig) advance(d time.Duration) { r.now = r.now.Add(d) }

// api is /api/sprint's answer, decoded.
func (r *rig) api() map[string]any {
	r.t.Helper()
	w := httptest.NewRecorder()
	r.s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sprint", nil))
	require.Equal(r.t, http.StatusOK, w.Code)
	var v map[string]any
	require.NoError(r.t, json.Unmarshal(w.Body.Bytes(), &v))
	return v
}

// The server reads the sprint at most once per Every, however often pages ask.
func TestDashboardReadsAtMostOncePerEvery(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.api()
	r.api()
	assert.Equal(t, 1, r.reads, "two asks in the same instant: one read")
	r.advance(999 * time.Millisecond)
	r.api()
	assert.Equal(t, 1, r.reads, "an ask under Every after the last read began: the cached copy")
	r.advance(time.Millisecond)
	r.api()
	assert.Equal(t, 2, r.reads, "an ask Every after the last read began: a read")
}

// While a read runs, every other ask is answered from the cached copy at once.
func TestDashboardAnswersFromTheCacheWhileAReadRuns(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.api()
	r.advance(time.Second)
	entered, release := make(chan struct{}), make(chan struct{})
	r.next = func() ([]byte, error) {
		close(entered)
		<-release
		return where(5), nil
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.s.Refresh(context.Background())
	}()
	<-entered
	v := r.api() // the read above is still running
	assert.InDelta(t, 0, v["data"].(map[string]any)["landed"], 0, "the ask during a read is the copy before it")
	close(release)
	wg.Wait()
	assert.Equal(t, 2, r.reads)
	assert.InDelta(t, 5, r.api()["data"].(map[string]any)["landed"], 0)
}

// A failed read holds the last good copy and says nothing on the page; the log takes
// one line per new failure.
func TestDashboardHoldsSilentlyOnAFailedRead(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.next = func() ([]byte, error) { return where(7), nil }
	good := r.api()
	require.Equal(t, true, good["ok"])

	for i, fail := range []func() ([]byte, error){
		func() ([]byte, error) { return nil, errors.New("where exited 2") },
		func() ([]byte, error) { return nil, errors.New("where exited 2") },
		func() ([]byte, error) { return []byte("not json"), nil },
		func() ([]byte, error) { return []byte(`{"landed":3}`), nil },
	} {
		r.advance(time.Second)
		r.next = fail
		v := r.api()
		assert.Equal(t, false, v["ok"], "read %d", i)
		assert.Equal(t, good["data"], v["data"], "read %d: the data held is the last good read's", i)
		assert.Equal(t, good["fetchedAt"], v["fetchedAt"], "read %d", i)
		assert.NotEmpty(t, v["error"], "read %d", i)
	}
	assert.Equal(t, 3, strings.Count(r.log.String(), "read failed:"), "one log line per new failure:\n%s", r.log.String())
	assert.Contains(t, r.log.String(), "read failed: where JSON has no tables")

	r.advance(time.Second)
	r.next = func() ([]byte, error) { return where(9), nil }
	v := r.api()
	assert.Equal(t, true, v["ok"])
	assert.Nil(t, v["error"])
	assert.InDelta(t, 9, v["data"].(map[string]any)["landed"], 0)
}

// Throughput is the cards landed per hour over the last hour, null until ten minutes of
// samples; a drop in landed (a cleared sprint) starts the samples again.
func TestDashboardThroughput(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	landed := 100
	r.next = func() ([]byte, error) { return where(landed), nil }
	v := r.api()
	assert.Nil(t, v["throughput"])
	assert.InDelta(t, 0, v["throughputMinutes"], 0)

	r.advance(9 * time.Minute)
	landed = 109
	v = r.api()
	assert.Nil(t, v["throughput"], "nine minutes of samples: not yet")
	assert.InDelta(t, 9, v["throughputMinutes"], 0)

	r.advance(time.Minute)
	landed = 110
	v = r.api()
	assert.InDelta(t, 60, v["throughput"], 0, "10 cards in 10 minutes: 60 an hour")

	for i := 0; i < 6; i++ { // an hour and ten minutes in: the first sample has left the window
		r.advance(10 * time.Minute)
		landed += 20
		r.api()
	}
	v = r.api() // the same instant: the cached copy
	assert.InDelta(t, 120, v["throughput"], 0, "the last hour: 120 cards")
	assert.InDelta(t, 60, v["throughputMinutes"], 0)

	r.advance(time.Second)
	landed = 3 // cleared
	v = r.api()
	assert.Nil(t, v["throughput"], "a cleared sprint starts the samples again")
	assert.InDelta(t, 0, v["throughputMinutes"], 0)
}

// The read-time summary is one line a minute.
func TestDashboardLogsAReadSummaryAMinute(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	for i := 0; i < 60; i++ {
		r.api()
		r.advance(time.Second)
	}
	assert.Empty(t, r.log.String())
	r.api()
	assert.Equal(t, 1, strings.Count(r.log.String(), "reads=61 failed=0"), r.log.String())
}

// Every answer is no-store; the page's files are embedded; the script link carries the
// build number, and /api/sprint carries the same one.
func TestDashboardServesThePageNoStore(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, "no-store, max-age=0", w.Header().Get("Cache-Control"), path)
		return w
	}
	for path, ctype := range map[string]string{
		"/": "text/html", "/index.html": "text/html", "/app.js": "text/javascript",
		"/nunito-800.woff2": "font/woff2", "/api/sprint": "application/json", "/healthz": "text/plain",
	} {
		w := get(path)
		assert.Equal(t, http.StatusOK, w.Code, path)
		assert.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), ctype), "%s: %s", path, w.Header().Get("Content-Type"))
	}
	assert.Equal(t, http.StatusNotFound, get("/server.py").Code)
	assert.Equal(t, http.StatusNotFound, get("/logo").Code, "no --logo: no logo")
	page := get("/").Body.String()
	assert.Contains(t, page, `src="app.js?v=`+r.s.Build()+`"`)
	assert.NotContains(t, page, `id="logo"`, "no --logo: the slot renders nothing")
	assert.NotContains(t, page, `rel="icon"`)
	assert.Equal(t, r.s.Build(), r.api()["build"])
}

// --logo names the image served as the logo and the favicon; a new file changes the
// build number, so an open page reloads itself.
func TestDashboardServesTheLogoAndItsBuild(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	logo := filepath.Join(t.TempDir(), "logo.webp")
	webp := []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	require.NoError(t, os.WriteFile(logo, webp, 0o600))
	r.s.Logo = logo
	before := r.s.Build()

	w := httptest.NewRecorder()
	r.s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/logo", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/webp", w.Header().Get("Content-Type"))
	assert.Equal(t, webp, w.Body.Bytes())

	w = httptest.NewRecorder()
	r.s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Contains(t, w.Body.String(), `<img id="logo" class="logo-tile" src="/logo?v=`+before+`"`)
	assert.Contains(t, w.Body.String(), `<link rel="icon" href="/logo?v=`+before+`">`)

	require.NoError(t, os.WriteFile(logo, append(webp, 'x'), 0o600))
	assert.NotEqual(t, before, r.s.Build(), "a new logo file is a new build number")
	r.s.Version = "v9"
	assert.NotEqual(t, before, r.s.Build())
}

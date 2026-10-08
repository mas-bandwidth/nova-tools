package sprintdash

import (
	"context"
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

// The verb serves what the stopgap dashboard server served (docs/STOPGAPS.md,
// sprint-dashboard): one poller whose reads run back to back with Every as their floor,
// pages that read the copy only, a failed or timed-out read that holds the last good copy
// marked ok false with a short reason and never the reader's own text, the last read's
// time and length, the licence and the logo routes. The throughput, the summary line and
// the puller are held by TestDashboardThroughput, TestDashboardLogsAReadSummaryAMinute
// and TestThePullerServesTheUpstreamsCopy. A hand-moved clock; no socket.
func TestDashboardServesWhatServerPyServedFromOnePoller(t *testing.T) {
	t.Parallel()

	t.Run("one poller reads back to back with Every as the floor", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		r.s.Tick()
		r.s.Tick()
		assert.Equal(t, 1, r.reads, "a second tick in the same instant: under the floor, no read")
		r.advance(time.Second)
		r.s.Tick()
		assert.Equal(t, 2, r.reads, "a tick Every after the last read began: a read")
		r.next = func() ([]byte, error) { r.advance(3 * time.Second); return where(1), nil }
		r.advance(time.Second)
		r.s.Tick()
		r.next = func() ([]byte, error) { return where(2), nil }
		r.s.Tick()
		assert.Equal(t, 4, r.reads, "a read longer than Every: the next starts as it returns")
	})

	t.Run("while the poller runs a page reads the copy only", func(t *testing.T) {
		t.Parallel()
		var mu sync.Mutex
		now, reads := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC), 0
		s := &Server{Every: time.Second,
			Now:  func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
			Read: func() ([]byte, error) { mu.Lock(); defer mu.Unlock(); reads++; return where(reads), nil }}
		count := func() int { mu.Lock(); defer mu.Unlock(); return reads }
		tick := make(chan time.Time)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { s.Run(ctx, tick); close(done) }()
		tick <- now
		require.Eventually(t, func() bool { return count() == 1 }, 5*time.Second, time.Millisecond)
		for range 20 {
			mu.Lock()
			now = now.Add(time.Minute)
			mu.Unlock()
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sprint", nil))
			require.Equal(t, http.StatusOK, w.Code)
			w = httptest.NewRecorder()
			s.Pull().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/team", nil))
			require.Equal(t, http.StatusOK, w.Code)
		}
		assert.Equal(t, 1, count(), "forty asks twenty minutes apart: the poller's one read")
		cancel()
		<-done
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sprint", nil))
		assert.Equal(t, 2, count(), "no poller: an ask past Every reads")
	})

	t.Run("a failed read holds the copy, marked, and serves none of the reader's text", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		good := r.api()
		r.advance(time.Second)
		r.next = func() ([]byte, error) {
			return nil, &ReadError{Why: "where exited 2", Detail: "SECRETS loaded 3; dial tcp 127.0.0.1:6390: refused"}
		}
		v := r.api()
		assert.Equal(t, false, v["ok"])
		assert.Equal(t, "where exited 2", v["error"])
		assert.Equal(t, good["data"], v["data"])
		assert.Equal(t, good["fetchedAt"], v["fetchedAt"])
		assert.NotEqual(t, good["attemptAt"], v["attemptAt"], "the attempt is the failed read's")
		assert.NotContains(t, string(r.s.Snapshot()), "SECRETS")
		assert.NotContains(t, string(r.s.Snapshot()), "6390")
		assert.Contains(t, r.log.String(), "read failed: where exited 2: SECRETS loaded 3; dial tcp 127.0.0.1:6390: refused", "the detail goes to the log")
	})

	t.Run("a read past the timeout is marked failed and the next waits for it", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		expired := make(chan time.Time, 1)
		var asked time.Duration
		r.s.readTimer = func(d time.Duration) (<-chan time.Time, func()) { asked = d; return expired, func() {} }
		entered, release := make(chan struct{}), make(chan struct{})
		r.next = func() ([]byte, error) { close(entered); <-release; return where(4), nil }
		ticked := make(chan struct{})
		go func() { r.s.Tick(); close(ticked) }()
		<-entered
		expired <- r.now
		require.Eventually(t, func() bool { return strings.Contains(string(r.s.Snapshot()), "timed out") }, 5*time.Second, time.Millisecond)
		assert.Equal(t, ReadTimeoutDefault, asked)
		v := r.api()
		assert.Equal(t, false, v["ok"])
		assert.Equal(t, "the read timed out after 1m0s", v["error"])
		assert.Equal(t, 1, r.reads, "the timed-out read still runs: no second read overlaps it")
		close(release)
		<-ticked
		v = r.api()
		assert.Equal(t, true, v["ok"], "the late read's copy is served when it ends")
		assert.InDelta(t, 4, v["data"].(map[string]any)["landed"], 0)
	})

	t.Run("the snapshot carries the last attempt, its length, the floor and the build", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		start := r.now
		r.next = func() ([]byte, error) { r.advance(250 * time.Millisecond); return where(0), nil }
		v := r.api()
		assert.Equal(t, start.Add(250*time.Millisecond).Format(time.RFC3339Nano), v["attemptAt"])
		assert.InDelta(t, 0.25, v["readSeconds"], 0)
		assert.InDelta(t, 1, v["minInterval"], 0)
		assert.Equal(t, r.s.Build(), v["build"])
	})

	get := func(s *Server, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	rasters := []string{"/favicon.png", "/logo-icon.png", "/logo-tile-192.png", "/logo-tile-384.png", "/logo.webp", "/logo.png"}

	t.Run("the licence, and an svg logo drawn inline and served as the favicon", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		w := get(r.s, "/OFL.txt")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, file("OFL.txt"), w.Body.Bytes())
		assert.Equal(t, http.StatusNotFound, get(r.s, "/favicon.svg").Code, "no logo: no favicon")

		r.s.Logo = filepath.Join(t.TempDir(), "logo.svg")
		require.NoError(t, os.WriteFile(r.s.Logo, []byte(`<?xml version="1.0"?><svg xmlns="`+svgNS+`" viewBox="0 0 24 24"><path d="M1 1h22"/></svg>`), 0o600))
		page := get(r.s, "/").Body.String()
		assert.Contains(t, page, `<svg id="logo" viewBox="0 0 24 24" width="32" height="32" fill="currentColor" aria-hidden="true"><path d="M1 1h22"/></svg>`)
		assert.Contains(t, page, `<link rel="icon" type="image/svg+xml" href="/favicon.svg?v=`+r.s.Build()+`">`)
		w = get(r.s, "/favicon.svg")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "image/svg+xml", w.Header().Get("Content-Type"))
		assert.Equal(t, `<svg xmlns="`+svgNS+`" viewBox="0 0 24 24" fill="currentColor">`+faviconStyle+`<path d="M1 1h22"/></svg>`, w.Body.String())
		for _, path := range rasters {
			assert.Equal(t, http.StatusNotFound, get(r.s, path).Code, "%s: an svg logo has no raster", path)
		}
	})

	t.Run("a raster logo answers every raster logo route", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		for _, path := range rasters {
			assert.Equal(t, http.StatusNotFound, get(r.s, path).Code, "%s: no logo", path)
		}
		r.s.Logo = filepath.Join(t.TempDir(), "logo.png")
		png := []byte("\x89PNG\r\n\x1a\nrest")
		require.NoError(t, os.WriteFile(r.s.Logo, png, 0o600))
		for _, path := range rasters {
			w := get(r.s, path)
			assert.Equal(t, http.StatusOK, w.Code, path)
			assert.Equal(t, "image/png", w.Header().Get("Content-Type"), path)
			assert.Equal(t, png, w.Body.Bytes(), path)
		}
		assert.Equal(t, http.StatusNotFound, get(r.s, "/favicon.svg").Code, "a raster logo has no svg favicon")
	})

	t.Run("a raster logo's magic bytes name its type, not its extension", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		r.s.Logo = filepath.Join(t.TempDir(), "logo.png")
		webp := []byte("RIFF\x00\x00\x00\x00WEBPVP")
		require.NoError(t, os.WriteFile(r.s.Logo, webp, 0o600))
		for _, path := range []string{"/logo", "/logo.png", "/favicon.png"} {
			w := get(r.s, path)
			assert.Equal(t, http.StatusOK, w.Code, path)
			assert.Equal(t, "image/webp", w.Header().Get("Content-Type"), "%s: a webp file named .png is served as webp", path)
			assert.Equal(t, webp, w.Body.Bytes(), path)
		}
	})
}

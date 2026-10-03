package sprintdash

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// streamRig is a server behind httptest's listener, on a hand-moved clock and a hand-fed
// ticker, whose reads answer from body (the read count n in it).
type streamRig struct {
	t    *testing.T
	s    *Server
	tick chan time.Time
	mu   sync.Mutex
	now  time.Time
	n    int
	body func(n int) []byte
}

func newStreamRig(t *testing.T, body func(n int) []byte) *streamRig {
	r := &streamRig{t: t, tick: make(chan time.Time), now: time.Date(2026, 10, 3, 15, 20, 0, 0, time.UTC), body: body}
	r.s = &Server{Every: time.Second,
		Now: func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now },
		Read: func() ([]byte, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			b := r.body(r.n)
			r.n++
			return b, nil
		}}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.s.Run(ctx, r.tick)
	return r
}

func (r *streamRig) reads() int { r.mu.Lock(); defer r.mu.Unlock(); return r.n }

// second moves the clock a second and ticks.
func (r *streamRig) second() {
	r.mu.Lock()
	r.now = r.now.Add(time.Second)
	r.mu.Unlock()
	r.tick <- r.now
}

// open opens a stream at path and is its lines, one a channel item.
func (r *streamRig) open(h http.Handler, path string) (*http.Response, <-chan string) {
	r.t.Helper()
	ts := httptest.NewServer(h)
	r.t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	r.t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+path, nil)
	require.NoError(r.t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(r.t, err)
	r.t.Cleanup(func() { _ = resp.Body.Close() })
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	return resp, lines
}

// next is the stream's next event (its name and its data) or keepalive comment.
func next(t *testing.T, lines <-chan string) (name, data string) {
	t.Helper()
	for l := range lines {
		switch {
		case l == "" && (name != "" || data != ""):
			return name, data
		case strings.HasPrefix(l, ": "):
			return "", l
		case strings.HasPrefix(l, "event: "):
			name = strings.TrimPrefix(l, "event: ")
		case strings.HasPrefix(l, "data: "):
			data += strings.TrimPrefix(l, "data: ")
		}
	}
	t.Fatal("the stream ended")
	return "", ""
}

// /events sends the current copy at once, then each new copy as the ticker's refresh
// reads it: no poll, so no answer's latency on top of the second. Nobody connected, the
// ticker reads nothing.
func TestEventsSendEachNewCopyAsTheRefreshReadsIt(t *testing.T) {
	t.Parallel()
	r := newStreamRig(t, func(n int) []byte { return where(n) })
	r.tick <- r.now
	assert.Equal(t, 0, r.reads(), "no stream: a tick reads nothing")

	resp, lines := r.open(r.s, "/events")
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-store, max-age=0", resp.Header.Get("Cache-Control"))
	landed := func() float64 {
		name, data := next(t, lines)
		require.Equal(t, "sprint", name)
		var v struct {
			Data struct {
				Landed float64 `json:"landed"`
			} `json:"data"`
			Build string `json:"build"`
		}
		require.NoError(t, json.Unmarshal([]byte(data), &v), data)
		assert.NotEmpty(t, v.Build, "the same JSON as /api/sprint")
		return v.Data.Landed
	}
	assert.InDelta(t, 0, landed(), 0, "the first event at once: the copy read at the connect")
	for want := 1; want <= 3; want++ {
		r.second()
		assert.InDelta(t, float64(want), landed(), 0, "a tick a second on: a new read, a new event")
	}
	assert.Equal(t, 4, r.reads())
}

// /events/friend/<name> streams one friend's view, the JSON /api/friend/<name> answers;
// an unknown name is a 404 of one line, never a stream.
func TestEventsOfAFriendStreamHerView(t *testing.T) {
	t.Parallel()
	r := newStreamRig(t, func(int) []byte { return fixture(t) })
	resp, lines := r.open(r.s.Pull(), "/events/friend/amy")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	name, data := next(t, lines)
	assert.Equal(t, "sprint", name)
	var v PullView
	require.NoError(t, json.Unmarshal([]byte(data), &v), data)
	assert.Equal(t, "amy", v.Name)
	assert.Len(t, v.Cards, 2)
	r.second()
	_, data = next(t, lines)
	require.NoError(t, json.Unmarshal([]byte(data), &v), data)
	assert.Equal(t, "amy", v.Name, "the next copy, her view again")

	w := httptest.NewRecorder()
	r.s.Pull().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events/friend/zed", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "no friend named \"zed\" on the friends table\n", w.Body.String())
}

// An idle stream says it is alive every Keepalive (15 s unless set) with a comment a
// client ignores; the keepalive's ticker here is the test's.
func TestEventsKeepAnIdleStreamAlive(t *testing.T) {
	t.Parallel()
	r := newStreamRig(t, func(n int) []byte { return where(n) })
	keep := make(chan time.Time, 1)
	r.s.keepaliveTick = func(every time.Duration) (<-chan time.Time, func()) {
		assert.Equal(t, KeepaliveDefault, every)
		keep <- time.Time{} // one tick waiting when the stream starts
		return keep, func() {}
	}
	_, lines := r.open(r.s.Pull(), "/events")
	name, _ := next(t, lines)
	assert.Equal(t, "sprint", name)
	_, comment := next(t, lines)
	assert.Equal(t, ": keepalive", comment)
}

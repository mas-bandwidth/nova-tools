//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncBuffer is a buffer the dashboard writes from its goroutines and the test reads.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// The verb end to end on real sockets: two listeners share one server, each serves the
// page and the sprint as where --json prints it, and an interrupt stops it at exit 0.
func TestDashboardServesThePageOnEveryListener(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1,m2")
	ta.ok("add --stream s1 --count 3")
	want := ta.ok("where --json")
	ta.a.now, ta.a.sleep = time.Now, time.Sleep // real sockets, real time
	ctx, cancel := context.WithCancel(context.Background())
	ta.a.notify = func(context.Context) (context.Context, context.CancelFunc) { return ctx, func() {} }

	var out, errb syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- ta.a.run([]string{"dashboard", "--listen", "127.0.0.1:0,localhost:0", "--every", "100ms"}, &out, &errb)
	}()
	re := regexp.MustCompile(`DASHBOARD listening on (http://\S+/)`)
	var addrs []string
	require.Eventually(t, func() bool {
		addrs = nil
		for _, m := range re.FindAllStringSubmatch(out.String(), -1) {
			addrs = append(addrs, m[1])
		}
		return len(addrs) == 2
	}, 10*time.Second, 10*time.Millisecond, "the listening lines: %s%s", out.String(), errb.String())

	get := func(url string) (*http.Response, []byte) {
		resp, err := http.Get(url)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp, body
	}
	var builds []string
	for _, base := range addrs {
		resp, page := get(base)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "no-store, max-age=0", resp.Header.Get("Cache-Control"))
		assert.Contains(t, string(page), `<span class="wordmark">nova-sprint</span>`)
		resp, body := get(base + "api/sprint")
		assert.Equal(t, "no-store, max-age=0", resp.Header.Get("Cache-Control"))
		var v struct {
			OK    bool            `json:"ok"`
			Data  json.RawMessage `json:"data"`
			Build string          `json:"build"`
		}
		require.NoError(t, json.Unmarshal(body, &v), string(body))
		assert.True(t, v.OK, string(body))
		var got, exp map[string]any
		require.NoError(t, json.Unmarshal(v.Data, &got))
		require.NoError(t, json.Unmarshal([]byte(want), &exp))
		delete(got, "at") // the clock is real here
		delete(exp, "at")
		assert.Equal(t, exp["tables"], got["tables"])
		assert.Equal(t, exp["landed"], got["landed"])
		builds = append(builds, v.Build)
		_, ok := get(base + "healthz")
		assert.Equal(t, "ok\n", string(ok))
	}
	assert.Equal(t, builds[0], builds[1], "one server behind every listener")

	cancel()
	select {
	case code := <-done:
		assert.Equal(t, 0, code, "%s%s", out.String(), errb.String())
	case <-time.After(10 * time.Second):
		t.Fatal("the dashboard did not stop at the interrupt")
	}
	assert.Contains(t, out.String(), "DASHBOARD STOP interrupted")
}

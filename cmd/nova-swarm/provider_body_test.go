//go:build functional

package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// The card does not dial the provider. The fake harness posts to the baseURL
// in the job config, which the run points at the proxy. These tests are that
// path: one accepted-then-silent request, and one body that resumes inside
// the gap.

const bodyCard = "FAKE-LAUNCHES\nFAKE-PROVIDER-READ\n"

type bodyResult struct {
	res   nativeRunResult
	code  int
	err   string
	proxy *swarm.ProviderProxy
}

func runBodyCard(t *testing.T, upstream string, silence time.Duration, after func(time.Duration) <-chan time.Time) bodyResult {
	t.Helper()
	return runProviderCard(t, upstream, silence, 0, after)
}

// runProviderCard is runBodyCard with the proxy's header wait as well. Zero
// headerWait means ProviderHeaderTimeout.
func runProviderCard(t *testing.T, upstream string, silence, headerWait time.Duration, after func(time.Duration) <-chan time.Time) bodyResult {
	t.Helper()
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	raw := fmt.Sprintf("{\"provider\":{\"fake\":{\"options\":{\"baseURL\":%q}}}}\n", upstream)
	require.NoError(t, os.WriteFile(cfgPath, []byte(raw), 0o644))
	var proxy *swarm.ProviderProxy
	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "bodycard",
		card: []byte(bodyCard), slotDir: slot, root: root,
		configFile: cfgPath, deadline: 30 * time.Second, noWall: true,
		bodySilence: silence, bodyAfter: after, headerWait: headerWait,
		onProxy: func(p *swarm.ProviderProxy) { proxy = p },
	}, &errOut)
	return bodyResult{res: res, code: code, err: errOut.String(), proxy: proxy}
}

func discardReq(r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
}

type recordClock struct {
	mu    sync.Mutex
	armed []time.Duration
}

func (c *recordClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.armed = append(c.armed, d)
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- time.Unix(0, 1)
	return ch
}

func (c *recordClock) first() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.armed) == 0 {
		return 0
	}
	return c.armed[0]
}

func jobLaunches(t *testing.T, job string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(job, "launches"))
	require.NoError(t, err)
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func assertDialedProxy(t *testing.T, job string, proxy *swarm.ProviderProxy) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(job, "provider-url"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("the harness recorded no provider url")
	}
	for _, line := range lines {
		require.Equal(t, proxy.HarnessURL(), line, "harness dialed %q, want the proxy %s", line, proxy.HarnessURL())
	}
}

// TestProviderBodySilenceArmsFortyFiveSecondsAndDoesNotRelaunch: the card path
// arms ProviderBodySilence when no shorter gap is set. The clock fires as soon
// as it is armed, so this does not wait 45s. One upstream request, one card
// launch, unknown, and the persisted mark.
func TestProviderBodySilenceArmsFortyFiveSecondsAndDoesNotRelaunch(t *testing.T) {
	t.Parallel()

	var upstream atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		discardReq(r)
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer up.Close()

	clock := &recordClock{}
	got := runBodyCard(t, up.URL, 0, clock.After)
	require.NotNil(t, got.proxy, "no proxy\n%s", got.err)
	require.Equal(t, swarm.ProviderBodySilence, clock.first(), "the card armed %s, want %s", clock.first(), swarm.ProviderBodySilence)
	if got.code != 0 || !got.res.lost || got.res.end != swarm.EndUnknown {
		t.Fatalf("code=%d lost=%v end=%s\n%s", got.code, got.res.lost, got.res.end, got.err)
	}
	verdict, why := nativeVerdictWhy(got.res)
	if verdict != "INCOMPLETE" || why != "unknown-acceptance" {
		t.Fatalf("verdict %s why %s", verdict, why)
	}
	n := jobLaunches(t, got.res.job)
	require.Equal(t, 1, n, "card launches=%d, want 1", n)
	if got.proxy.Requests() != 1 || upstream.Load() != 1 {
		t.Fatalf("requests=%d upstream=%d, want 1 and 1", got.proxy.Requests(), upstream.Load())
	}
	assertDialedProxy(t, got.res.job, got.proxy)
	mark, err := os.ReadFile(filepath.Join(got.res.job, "provider-acceptance"))
	require.NoError(t, err)
	require.Equal(t, oneline.Escape("unknown\n"), string(mark), "provider-acceptance = %q", mark)
	_, err = os.Stat(filepath.Join(got.res.job, "RESULT.md"))
	require.Error(t, err, "a lost response published a result")
	t.Logf("CANARY requests=%d silence_ms=%d armed=%s verdict=%s why=%s",
		got.proxy.Requests(), got.proxy.SilenceWall().Milliseconds(), clock.first(), verdict, why)
}

// TestProviderBodySilenceAbortsTheCardOnce is the same card with a real timer.
// The gap is shorter than production so the suite can finish; the production
// gap is the test above. elapsed is the measured body gap, not the card's idle.
func TestProviderBodySilenceAbortsTheCardOnce(t *testing.T) {
	t.Parallel()

	var upstream atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		discardReq(r)
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer up.Close()

	started := time.Now()
	got := runBodyCard(t, up.URL, 2*time.Second, nil)
	runFor := time.Since(started)
	require.NotNil(t, got.proxy, "no proxy\n%s", got.err)
	require.Equal(t, 2*time.Second, got.proxy.Silence(), "silence %s, want 2s", got.proxy.Silence())
	if got.code != 0 || !got.res.lost || got.res.end != swarm.EndUnknown {
		t.Fatalf("code=%d lost=%v end=%s\n%s", got.code, got.res.lost, got.res.end, got.err)
	}
	verdict, why := nativeVerdictWhy(got.res)
	if verdict != "INCOMPLETE" || why != "unknown-acceptance" {
		t.Fatalf("verdict %s why %s", verdict, why)
	}
	n := jobLaunches(t, got.res.job)
	require.Equal(t, 1, n, "card launches=%d, want 1", n)
	if got.proxy.Requests() != 1 || upstream.Load() != 1 {
		t.Fatalf("requests=%d upstream=%d, want 1 and 1", got.proxy.Requests(), upstream.Load())
	}
	assertDialedProxy(t, got.res.job, got.proxy)
	mark, err := os.ReadFile(filepath.Join(got.res.job, "provider-acceptance"))
	require.NoError(t, err)
	require.Equal(t, oneline.Escape("unknown\n"), string(mark), "provider-acceptance = %q", mark)
	_, err = os.Stat(filepath.Join(got.res.job, "RESULT.md"))
	require.Error(t, err, "a lost response published a result")
	require.Positive(t, got.proxy.SilenceWall(), "the real timer did not measure a body gap")
	t.Logf("CANARY requests=%d silence_ms=%d run_ms=%d verdict=%s why=%s",
		got.proxy.Requests(), got.proxy.SilenceWall().Milliseconds(), runFor.Milliseconds(), verdict, why)
}

// TestProviderBodyThatResumesInsideTheDeadlineIsNotUnknown: headers, a pause
// shorter than the gap, then the body. The card completes. No acceptance file.
func TestProviderBodyThatResumesInsideTheDeadlineIsNotUnknown(t *testing.T) {
	t.Parallel()

	var upstream atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		discardReq(r)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer up.Close()

	got := runBodyCard(t, up.URL, 2*time.Second, nil)
	require.NotNil(t, got.proxy, "no proxy\n%s", got.err)
	if got.code != 0 || got.res.lost || got.res.rc != 0 {
		t.Fatalf("code=%d lost=%v rc=%d\n%s", got.code, got.res.lost, got.res.rc, got.err)
	}
	verdict, why := nativeVerdictWhy(got.res)
	if verdict != "OK" || why != "" {
		t.Fatalf("verdict %s why %s, want OK", verdict, why)
	}
	n := jobLaunches(t, got.res.job)
	require.Equal(t, 1, n, "card launches=%d, want 1", n)
	if got.proxy.Requests() != 1 || upstream.Load() != 1 {
		t.Fatalf("requests=%d upstream=%d, want 1 and 1", got.proxy.Requests(), upstream.Load())
	}
	require.False(t, got.proxy.Lost(), "a body inside the deadline was marked unknown")
	_, err := os.Stat(filepath.Join(got.res.job, "provider-acceptance"))
	require.True(t, os.IsNotExist(err), "provider-acceptance exists: %v", err)
	_, err = os.Stat(filepath.Join(got.res.job, "RESULT.md"))
	require.NoError(t, err, "the body inside the deadline did not publish")
	assertDialedProxy(t, got.res.job, got.proxy)
}

// TestProviderNoHeadersEndsAtTheHeaderWaitAsUnknown is HOLD
// 5782441006: the upstream accepts the POST and never sends response headers.
// The proxy's own header wait ends it, the card is UNKNOWN (not failed), one
// upstream request, one launch, the persisted mark, no result. The body gap is
// set far above the header wait so only the header wait can end this card.
func TestProviderNoHeadersEndsAtTheHeaderWaitAsUnknown(t *testing.T) {
	t.Parallel()

	var upstream atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		discardReq(r)
		<-r.Context().Done()
	}))
	defer up.Close()

	started := time.Now()
	got := runProviderCard(t, up.URL, 25*time.Second, time.Second, nil)
	runFor := time.Since(started)
	require.NotNil(t, got.proxy, "no proxy\n%s", got.err)
	require.Equal(t, time.Second, got.proxy.HeaderWait(), "header wait %s, want 1s", got.proxy.HeaderWait())
	if got.code != 0 || !got.res.lost || got.res.end != swarm.EndUnknown {
		t.Fatalf("code=%d lost=%v end=%s\n%s", got.code, got.res.lost, got.res.end, got.err)
	}
	verdict, why := nativeVerdictWhy(got.res)
	if verdict != "INCOMPLETE" || why != "unknown-acceptance" {
		t.Fatalf("verdict %s why %s", verdict, why)
	}
	n := jobLaunches(t, got.res.job)
	require.Equal(t, 1, n, "card launches=%d, want 1", n)
	if got.proxy.Requests() != 1 || upstream.Load() != 1 {
		t.Fatalf("requests=%d upstream=%d, want 1 and 1", got.proxy.Requests(), upstream.Load())
	}
	assertDialedProxy(t, got.res.job, got.proxy)
	mark, err := os.ReadFile(filepath.Join(got.res.job, "provider-acceptance"))
	require.NoError(t, err)
	require.Equal(t, oneline.Escape("unknown\n"), string(mark), "provider-acceptance = %q", mark)
	_, err = os.Stat(filepath.Join(got.res.job, "RESULT.md"))
	require.Error(t, err, "a response with no headers published a result")
	require.GreaterOrEqual(t, got.proxy.HeaderWall(), time.Second, "header wall %s, want at least the 1s wait", got.proxy.HeaderWall())
	require.Zero(t, got.proxy.SilenceWall(), "the body gap fired (%s) with no headers", got.proxy.SilenceWall())
	t.Logf("CANARY requests=%d header_ms=%d run_ms=%d verdict=%s why=%s",
		got.proxy.Requests(), got.proxy.HeaderWall().Milliseconds(), runFor.Milliseconds(), verdict, why)
}

// TestProviderDelayedHeadersInsideTheWaitAreNotUnknown is the pair's normal
// case: headers come late but inside the wait, then the body streams. The
// card completes. No acceptance file, nothing lost.
func TestProviderDelayedHeadersInsideTheWaitAreNotUnknown(t *testing.T) {
	t.Parallel()

	var upstream atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.Add(1)
		discardReq(r)
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		for _, chunk := range []string{"o", "k", "\n"} {
			_, _ = w.Write([]byte(chunk))
			if f != nil {
				f.Flush()
			}
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer up.Close()

	got := runProviderCard(t, up.URL, 2*time.Second, 2*time.Second, nil)
	require.NotNil(t, got.proxy, "no proxy\n%s", got.err)
	if got.code != 0 || got.res.lost || got.res.rc != 0 {
		t.Fatalf("code=%d lost=%v rc=%d\n%s", got.code, got.res.lost, got.res.rc, got.err)
	}
	verdict, why := nativeVerdictWhy(got.res)
	if verdict != "OK" || why != "" {
		t.Fatalf("verdict %s why %s, want OK", verdict, why)
	}
	n := jobLaunches(t, got.res.job)
	require.Equal(t, 1, n, "card launches=%d, want 1", n)
	if got.proxy.Requests() != 1 || upstream.Load() != 1 {
		t.Fatalf("requests=%d upstream=%d, want 1 and 1", got.proxy.Requests(), upstream.Load())
	}
	if got.proxy.Lost() || got.proxy.HeaderWall() != 0 {
		t.Fatalf("delayed headers inside the wait were marked unknown (header wall %s)", got.proxy.HeaderWall())
	}
	_, err := os.Stat(filepath.Join(got.res.job, "provider-acceptance"))
	require.True(t, os.IsNotExist(err), "provider-acceptance exists: %v", err)
	_, err = os.Stat(filepath.Join(got.res.job, "RESULT.md"))
	require.NoError(t, err, "the delayed-header card did not publish")
	assertDialedProxy(t, got.res.job, got.proxy)
}

//go:build functional

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Libraries considered: httptest supplies the real HTTP shell, FarLink the
// bounded local latency seam, and an ordinary RoundTripper drops one reply.
// No production server or test-only implementation of the protocol is copied.
type serverGateTransport struct {
	base  http.RoundTripper
	drop  atomic.Bool
	calls atomic.Int64
}

func (tr *serverGateTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.calls.Add(1)
	res, err := tr.base.RoundTrip(r)
	if err == nil && tr.drop.Swap(false) {
		_ = res.Body.Close() // ignored: deliberately lose an answer after execution
		return nil, io.ErrUnexpectedEOF
	}
	return res, err
}

func TestServerGateAtOneHundredMillisecondsWithMixedWorkers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, fn.Load(ctx, admin))
	env := func(k string) string {
		return map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "boss"}[k]
	}
	a := newApp(env)
	t.Cleanup(a.close)
	do := func(args ...string) {
		t.Helper()
		var out, errb bytes.Buffer
		require.Equal(t, 0, a.run(args, &out, &errb), "%v: %s %s", args, out.String(), errb.String())
	}
	do("init", "--members", "m1:2,m2:2,m3:2,m4:2,m5:2,m6:2", "--readers", "reader-a,reader-b")
	do("add", "--stream", "s1", "--count", "48")
	for i := 1; i <= 6; i++ {
		m := "m" + strconv.Itoa(i)
		do("fleet", "beat", m, "--load", "0")
		do("fleet", "up", m)
	}
	do("start")
	do("tick")
	a.serveAddr = addr
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	link := testredis.FarLink(t, strings.TrimPrefix(srv.URL, "http://"), 100*time.Millisecond)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	fault := &serverGateTransport{base: transport}
	remote := sprintwire.Client{Addr: link.Addr(), HTTP: &http.Client{Transport: fault, Timeout: 30 * time.Second}}
	worker := &sprintwire.Worker{Send: remote.Do, Budget: 30 * time.Second}
	st, _, code := a.machineVerb("run", nil, &bytes.Buffer{})
	require.NotNil(t, st, "run exit=%d", code)
	var ticks atomic.Int64
	a.ticked = func(int, time.Time, string) { ticks.Add(1) }
	loopCtx, cancel := context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go func() { defer close(loopDone); a.runLoop(loopCtx, st, 20, 0, io.Discard, io.Discard) }()
	t.Cleanup(func() { cancel(); <-loopDone })

	// Four workers use HTTP beside the server; the fifth retains the old path.
	// They execute real take/finish verbs, rather than a fake contention counter.
	var wg sync.WaitGroup
	errors := make(chan error, 5)
	start := make(chan struct{})
	for i := 2; i <= 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			who := "m" + strconv.Itoa(i)
			local := newApp(env)
			defer local.close()
			near := &sprintwire.Worker{Send: sprintwire.Client{Addr: strings.TrimPrefix(srv.URL, "http://")}.Do}
			run := func(args ...string) (int, []byte) {
				if i != 6 {
					return near.Run(args...)
				}
				var out, errb bytes.Buffer
				code := local.run(args, &out, &errb)
				if code != 0 {
					return code, append(out.Bytes(), errb.Bytes()...)
				}
				return code, out.Bytes()
			}
			<-start
			code, raw := run("take", "--as", who, "--limit", "1", "--epoch", "0", "--json")
			var got output
			if code != 0 {
				errors <- fmt.Errorf("%s take: %d %s", who, code, raw)
				return
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				errors <- err
				return
			}
			if len(got.Packets) != 1 {
				errors <- fmt.Errorf("%s take returned %d packets", who, len(got.Packets))
				return
			}
			p := got.Packets[0]
			code, raw = run("finish", "--as", who, p.Card+"@"+strconv.Itoa(p.Gen), "--epoch", "0", "--json")
			if code != 0 {
				errors <- fmt.Errorf("%s finish: %d %s", who, code, raw)
				return
			}
			errors <- nil
		}()
	}
	t.Cleanup(wg.Wait)
	close(start)

	call := func(args ...string) output {
		t.Helper()
		before, writes, began := fault.calls.Load(), link.Writes(), time.Now()
		code, raw := worker.Run(args...)
		require.Equal(t, 0, code, "%v: %s", args, raw)
		var out output
		require.NoError(t, json.Unmarshal(raw, &out))
		t.Logf("HTTP %s requests=%d network_writes=%d wall=%s replay=%t", args[0], fault.calls.Load()-before, link.Writes()-writes, time.Since(began), out.Replay)
		return out
	}
	before := fault.calls.Load()
	take := call("take", "--as", "m1", "--limit", "2", "--epoch", "0", "--json")
	require.Len(t, take.Packets, 2)
	assert.Equal(t, int64(1), fault.calls.Load()-before)
	first, second := take.Packets[0], take.Packets[1]
	before = fault.calls.Load()
	finish := call("finish", "--as", "m1", first.Card+"@"+strconv.Itoa(first.Gen), "--epoch", "0", "--json")
	require.NotEmpty(t, finish.Moved)
	assert.Equal(t, int64(1), fault.calls.Load()-before, "ordinary finish is one HTTP exchange")
	acknowledgedAt := ticks.Load()
	monitor := &store.Store{B: &store.Redis{C: admin, Now: time.Now}, Actor: "boss"}
	var appliedAt int64
	require.Eventually(t, func() bool {
		cards, err := monitor.ReadCells(ctx, sprint.Work, "s1", sprint.Review)
		if err != nil {
			return false
		}
		for _, c := range cards {
			if c.ID == first.Primary {
				appliedAt = ticks.Load()
				return true
			}
		}
		return false
	}, 10*time.Second, 10*time.Millisecond, "finish must reach the work table")
	assert.LessOrEqual(t, appliedAt-acknowledgedAt, int64(2), "applied within two completed ticks after acknowledgment")

	before = fault.calls.Load()
	fault.drop.Store(true)
	replay := call("finish", "--as", "m1", second.Card+"@"+strconv.Itoa(second.Gen), "--epoch", "0", "--json")
	assert.Equal(t, int64(2), fault.calls.Load()-before, "one lost answer and one retry")
	assert.True(t, replay.Replay, "retry returns the original committed finish")
	wg.Wait()
	for range 5 {
		require.NoError(t, <-errors)
	}
	assert.Positive(t, ticks.Load(), "the real coordinator loop ticked")
	assert.GreaterOrEqual(t, link.Shortest(), 100*time.Millisecond)
	t.Logf("HTTP GATE ticks=%d finish_applied_after_ticks=%d other_workers=5 old_path=1", ticks.Load(), appliedAt-acknowledgedAt)
}

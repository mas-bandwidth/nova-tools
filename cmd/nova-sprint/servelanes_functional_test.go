//go:build functional && slow

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
)

// The server under the load of 2026-10-04 2:13 PM, on a scratch Redis made far (each
// exchange delayed, as the loaded Studio's store answered), over real HTTP: the run loop
// ticking, eight friends beating twice a second each (the daemon's beat and the beat
// loop's), two dashboards reading `where --json --cards` once a second each, four
// members polling their queue once a second, and a slow step injected that holds the
// line 1.5 s of every 2 s (a land or a tick at its worst). Each caller gives up at 10 s,
// as `friend beat` does. With the lanes, every beat and every read is answered within a
// second; the run with the lanes off is the same load with every verb on the line.
// Libraries considered: httptest and the repo's own far link; nothing new.
func TestServerAnswersUnderTheLoadOfTheAfternoon(t *testing.T) {
	t.Parallel()
	for _, lanes := range []bool{true, false} {
		t.Run(map[bool]string{true: "lanes", false: "every-verb-on-the-line"}[lanes], func(t *testing.T) {
			got := afternoonLoad(t, lanes, 20*time.Second)
			for _, class := range []string{"slow-step", "beat", "read", "queue"} {
				t.Logf("%-5s %s", class, got[class])
			}
			if lanes {
				assert.Less(t, got["beat"].max, time.Second, "a beat is answered within a second")
				assert.Less(t, got["read"].max, time.Second, "a read is answered within a second")
				assert.Zero(t, got["beat"].failed+got["read"].failed)
			}
		})
	}
}

type latencies struct {
	all    []time.Duration
	max    time.Duration
	failed int
}

func (l latencies) String() string {
	if len(l.all) == 0 {
		return fmt.Sprintf("n=0 failed=%d", l.failed)
	}
	s := slices.Clone(l.all)
	slices.Sort(s)
	q := func(p float64) time.Duration { return s[min(len(s)-1, int(p*float64(len(s))))].Round(time.Millisecond) }
	return fmt.Sprintf("n=%d p50=%s p90=%s p99=%s max=%s failed=%d", len(s), q(0.5), q(0.9), q(0.99), l.max.Round(time.Millisecond), l.failed)
}

func afternoonLoad(t *testing.T, lanes bool, span time.Duration) map[string]latencies {
	ctx := context.Background()
	near := testutil.Start(t)
	admin := redis.NewClient(&redis.Options{Addr: near})
	t.Cleanup(func() { _ = admin.Close() })
	require.NoError(t, fn.Load(ctx, admin))
	addr := testredis.Far(t, near, 3*time.Millisecond)
	env := func(k string) string {
		return map[string]string{"NOVA_SPRINT_REDIS": addr, "NOVA_SPRINT_ACTOR": "boss"}[k]
	}
	a := newApp(env)
	t.Cleanup(a.close)
	friends := []string{"amy", "bea", "cat", "dot", "eve", "fay", "gus", "hal"}
	a.friends = friendRows(friends...)
	do := func(args ...string) {
		t.Helper()
		var out, errb bytes.Buffer
		require.Equal(t, 0, a.run(args, &out, &errb), "%v: %s %s", args, out.String(), errb.String())
	}
	do("init", "--members", "m1:4,m2:4,m3:4,m4:4", "--readers", "reader-a,reader-b")
	do("friend", "sync", "--root", t.TempDir())
	do("add", "--stream", "s1", "--count", "200")
	for i := 1; i <= 4; i++ {
		do("fleet", "beat", "m"+strconv.Itoa(i), "--load", "0")
		do("fleet", "up", "m"+strconv.Itoa(i))
	}
	do("start")
	a.serveAddr = addr
	if lanes {
		require.NotNil(t, a.lanesFor(ctx), "made as listen makes them")
	} else {
		a.lanes = &serveLanes{off: true}
	}
	fleet, local := httptest.NewServer(a), httptest.NewServer(localHandler{a})
	t.Cleanup(fleet.Close)
	t.Cleanup(local.Close)
	st, _, code := a.machineVerb("run", nil, &bytes.Buffer{})
	require.NotNil(t, st, "run exit=%d", code)
	var mu sync.Mutex
	got := map[string]latencies{}
	loopCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); a.runLoop(loopCtx, st, 20, 0, io.Discard, io.Discard) }()
	// the slow step: real store work holding the line, ten whole reads of the sprint a
	// turn, again as soon as the line comes back to it (the line is first in, first out,
	// so every verb waiting gets its turn between two)
	wg.Add(1)
	go func() {
		defer wg.Done()
		heavy := []string{"where", "--json", "--cards", "--redis", addr}
		for loopCtx.Err() == nil {
			a.serial.Lock()
			began := time.Now()
			for range 10 {
				a.run(heavy, io.Discard, io.Discard)
			}
			mu.Lock()
			l := got["slow-step"]
			l.all = append(l.all, time.Since(began))
			l.max = max(l.max, time.Since(began))
			got["slow-step"] = l
			mu.Unlock()
			a.serial.Unlock()
		}
	}()
	client := func(srv *httptest.Server) sprintwire.Client {
		return sprintwire.Client{Addr: strings.TrimPrefix(srv.URL, "http://"), HTTP: &http.Client{Timeout: 10 * time.Second}}
	}
	caller := func(class string, c sprintwire.Client, every time.Duration, argv ...string) {
		defer wg.Done()
		for loopCtx.Err() == nil {
			began := time.Now()
			res, err := c.Do(loopCtx, argv)
			took := time.Since(began)
			mu.Lock()
			l := got[class]
			if err != nil || len(res) != 1 || res[0].Code != 0 {
				if loopCtx.Err() == nil {
					l.failed++
				}
			} else {
				l.all = append(l.all, took)
				l.max = max(l.max, took)
			}
			got[class] = l
			mu.Unlock()
			time.Sleep(max(every-took, 0))
		}
	}
	for _, f := range friends {
		wg.Add(2)
		go caller("beat", client(fleet), time.Second, "friend", "beat", f)
		go caller("beat", client(fleet), time.Second, "friend", "beat", f)
	}
	for range 2 {
		wg.Add(1)
		go caller("read", client(local), time.Second, "where", "--json", "--cards")
	}
	for i := 1; i <= 4; i++ {
		wg.Add(1)
		go caller("queue", client(fleet), time.Second, "queue", "--as", "m"+strconv.Itoa(i))
	}
	time.Sleep(span)
	stop()
	wg.Wait()
	return got
}

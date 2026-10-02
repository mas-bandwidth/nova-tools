package main

// spill_test.go holds the spill/recall slice of docs/SPEC-REDIS.md (nova-tools
// #2279, behaviours 14, 16, 17 and 27 of "Tests this spec demands") against the
// PRODUCTION path: every test drives run(), the same function main() calls,
// over a miniredis fake standing in for the instance. The clock is injected
// through deps.now, so expiry is proven by moving a fake clock, never by
// waiting on the wall.

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock is the controlled clock: it reads t and only moves when a test
// moves it.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// harness is one fake instance plus the deps run() is given. run() opens the
// fake through redisconn at the --addr it is given, as main() does.
type harness struct {
	mr    *miniredis.Miniredis
	clock *fakeClock
	d     deps
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	mr := miniredis.RunT(t)
	clock := &fakeClock{t: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
	h := &harness{mr: mr, clock: clock}
	h.d = deps{
		now:    clock.now,
		getenv: func(string) string { return "" },
	}
	return h
}

// run drives run() with --addr pointed at the fake instance.
func (h *harness) run(args ...string) (int, string, string) {
	full := append([]string{args[0], "--addr", h.mr.Addr()}, args[1:]...)
	return h.runBare(full...)
}

// runBare drives run() with exactly the arguments given: no --addr is added,
// so a test can prove what a missing or empty address does.
func (h *harness) runBare(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, h.d)
	return code, out.String(), errb.String()
}

// TestAddrRefusedWhenMissingOrEmpty: an address that is missing, empty, blank
// or lacks a host or a port is refused (exit 2) BEFORE the store is opened,
// for spill and for recall. The Redis client would otherwise fill an empty
// address in as localhost:6379, which is a guess the tool refuses to make.
func TestAddrRefusedWhenMissingOrEmpty(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	// A regression must fail here, never reach a real host: connect reads the
	// environment before it opens anything, so the first read stops the test
	// (t.Fatalf ends this goroutine) before redisconn.Open can dial.
	h.d.getenv = func(k string) string {
		require.FailNowf(t, "", "a refused address reached connect (it read %s); the refusal comes before the store is opened", k)
		return ""
	}
	verbs := map[string][]string{
		"spill":  {"--owner", "rowan", "--name", "note", "--ttl", "1h", "--value", "hi"},
		"recall": {"--owner", "rowan", "--name", "note"},
	}
	addrs := []struct {
		label string
		flag  []string
		want  string
	}{
		{"missing", nil, "--addr is required"},
		{"empty", []string{"--addr", ""}, "--addr"},
		{"blank", []string{"--addr", "   "}, "--addr"},
		{"no host", []string{"--addr", ":6379"}, "--addr"},
		{"no port", []string{"--addr", "127.0.0.1"}, "--addr"},
		{"empty port", []string{"--addr", "127.0.0.1:"}, "--addr"},
		{"bad port", []string{"--addr", "127.0.0.1:redis"}, "--addr"},
	}
	for verb, rest := range verbs {
		for _, a := range addrs {
			args := append(append([]string{verb}, a.flag...), rest...)
			code, stdout, stderr := h.runBare(args...)
			assert.Equal(t, 2, code, "%s with %s --addr exits %d, want 2; stdout=%q stderr=%q", verb, a.label, code, stdout, stderr)
			if assert.Contains(t, stderr, a.want, "%s with %s --addr must name --addr and the remedy; stderr=%q", verb, a.label, stderr) {
				assert.Contains(t, stderr, "run: nova-redis help", "%s with %s --addr must name --addr and the remedy; stderr=%q", verb, a.label, stderr)
			}
		}
	}
	{
		n := h.mr.TotalConnectionCount()
		assert.Zero(t, n, "a refused address opened %d connections to the fake; the refusal comes before the dial", n)
	}
	{
		keys := h.mr.Keys()
		assert.Len(t, keys, 0, "a refused address stored %v; a refusal writes nothing", keys)
	}
}

func TestSpillRefusedWithoutOwner(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	code, stdout, stderr := h.run("spill", "--name", "note", "--ttl", "1h", "--value", "hi")
	require.Equal(t, 2, code, "spill with no --owner exits %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
	if assert.Contains(t, stderr, "--owner is required", "spill with no --owner must name the remedy; stderr=%q", stderr) {
		assert.Contains(t, stderr, "run: nova-redis help", "spill with no --owner must name the remedy; stderr=%q", stderr)
	}
	// An empty owner is no owner: it would store a key of the form ":note".
	code, _, stderr = h.run("spill", "--owner", "", "--name", "note", "--ttl", "1h", "--value", "hi")
	require.Equal(t, 2, code, "spill with an empty --owner exits %d, want 2; stderr=%q", code, stderr)
	{
		keys := h.mr.Keys()
		assert.Len(t, keys, 0, "a refused spill stored %v; a refusal writes nothing", keys)
	}
}

func TestSpillRefusedWithoutTTL(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	code, stdout, stderr := h.run("spill", "--owner", "rowan", "--name", "note", "--value", "hi")
	require.Equal(t, 2, code, "spill with no --ttl exits %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
	if assert.Contains(t, stderr, "--ttl is required", "spill with no --ttl must name the remedy; stderr=%q", stderr) {
		assert.Contains(t, stderr, "run: nova-redis help", "spill with no --ttl must name the remedy; stderr=%q", stderr)
	}
	for _, ttl := range []string{"0s", "-5m"} {
		code, _, stderr = h.run("spill", "--owner", "rowan", "--name", "note", "--ttl", ttl, "--value", "hi")
		assert.Equal(t, 2, code, "spill with --ttl %s exits %d, want 2 (an unbounded key is a bug); stderr=%q", ttl, code, stderr)
	}
	{
		keys := h.mr.Keys()
		assert.Len(t, keys, 0, "a refused spill stored %v; an unbounded key is refused, not stored", keys)
	}
}

func TestRecallRefusesAnExpiredKey(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	{
		code, _, stderr := h.run("spill", "--owner", "rowan", "--name", "note", "--ttl", "1h", "--value", "hi")
		require.Zero(t, code, "spill exits %d; stderr=%q", code, stderr)
	}
	code, stdout, stderr := h.run("recall", "--owner", "rowan", "--name", "note")
	require.Zero(t, code, "recall inside the TTL exits %d stdout=%q stderr=%q, want 0 and value=hi", code, stdout, stderr)
	require.Contains(t, stdout, "value=hi", "recall inside the TTL exits %d stdout=%q stderr=%q, want 0 and value=hi", code, stdout, stderr)

	// Move the controlled clock past the TTL. The fake instance has NOT aged
	// the key out (it keeps its own clock), so only recall's own expiry check
	// stands between the caller and a stale value.
	h.clock.t = h.clock.t.Add(2 * time.Hour)
	code, stdout, stderr = h.run("recall", "--owner", "rowan", "--name", "note")
	require.Equal(t, 1, code, "recall past the TTL exits %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	assert.NotContains(t, stdout, "value=hi", "recall past the TTL printed the stale value: %q", stdout)
	assert.Contains(t, stdout+stderr, "EXPIRED", "recall past the TTL must say EXPIRED; stdout=%q stderr=%q", stdout, stderr)

	// And once the instance itself ages the key out, recall misses (exit 1).
	h.mr.FastForward(2 * time.Hour)
	{
		code, _, _ := h.run("recall", "--owner", "rowan", "--name", "note")
		assert.Equal(t, 1, code, "recall of a key the instance expired exits %d, want 1", code)
	}
}

func TestEveryEphemeralKeyCarriesOwnerAndTTL(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	attempts := []struct {
		args []string
		want int
	}{
		{[]string{"spill", "--owner", "rowan", "--name", "a", "--ttl", "1h", "--value", "1"}, 0},
		{[]string{"spill", "--owner", "stella", "--name", "b", "--ttl", "90m", "--value", "2"}, 0},
		{[]string{"spill", "--owner", "rowan", "--name", "c", "--value", "3"}, 2},
		{[]string{"spill", "--name", "d", "--ttl", "1h", "--value", "4"}, 2},
		{[]string{"spill", "--owner", "rowan", "--name", "e", "--ttl", "0s", "--value", "5"}, 2},
		{[]string{"spill", "--owner", "ro:wan", "--name", "f", "--ttl", "1h", "--value", "6"}, 2},
	}
	for _, a := range attempts {
		{
			code, stdout, stderr := h.run(a.args...)
			assert.Equal(t, a.want, code, "%v exits %d, want %d; stdout=%q stderr=%q", a.args, code, a.want, stdout, stderr)
		}
	}
	keys := h.mr.Keys()
	require.Len(t, keys, 2, "stored keys %v, want exactly the two bounded, owned spills", keys)
	for _, k := range keys {
		owner, name, ok := strings.Cut(k, ":")
		if assert.True(t, ok, "key %q is not <owner>:<name>", k) {
			if assert.NotEmpty(t, owner, "key %q is not <owner>:<name>", k) {
				assert.NotEmpty(t, name, "key %q is not <owner>:<name>", k)
			}
		}
		{
			ttl := h.mr.TTL(k)
			assert.Greater(t, ttl, time.Duration(0), "key %q carries no TTL (%v); an unbounded key is a bug", k, ttl)
		}
	}

	// The package seam, below the CLI: a caller that skips the flag parser
	// still cannot write a key without an owner or a TTL.
	ctx := context.Background()
	conn, err := redisconn.Open(ctx, redisconn.Options{Addr: h.mr.Addr()}, nil)
	require.NoError(t, err, err)
	t.Cleanup(func() { _ = conn.Close() })
	s := &scratch{rdb: conn.Client(), now: h.clock.now}
	{
		_, err := s.spill(ctx, "", "g", "7", time.Hour)
		assert.Error(t, err, "scratch.spill with no owner returned no error")
	}
	{
		_, err := s.spill(ctx, "rowan", "h", "8", 0)
		assert.Error(t, err, "scratch.spill with no TTL returned no error")
	}
	{
		n := len(h.mr.Keys())
		assert.Equal(t, 2, n, "the package seam stored a key it should have refused: %v", h.mr.Keys())
	}
}

// TestSpillAndRecallAreOneRoundTripEach: each verb, run whole through run(),
// makes one round trip for its work (spill one transaction, recall one
// pipeline) after the connect's one exchange (HELLO, which redisconn.Open
// guarantees is the only one). The trips are counted on the wire by a proxy in
// front of the fake, so a command added anywhere in the verb (a PING in
// cmdSpill, a read before the pipeline) shows as a third trip.
func TestSpillAndRecallAreOneRoundTripEach(t *testing.T) {
	t.Parallel()

	const handshake = 1
	h := newHarness(t)
	verbs := [][]string{
		{"spill", "--owner", "rowan", "--name", "note", "--ttl", "1h", "--value", "hi"},
		{"recall", "--owner", "rowan", "--name", "note"},
	}
	for _, v := range verbs {
		addr, trips := tripProxy(t, h.mr.Addr())
		args := append([]string{v[0], "--addr", addr}, v[1:]...)
		{
			code, stdout, stderr := h.runBare(args...)
			require.Zero(t, code, "%s exits %d; stdout=%q stderr=%q", v[0], code, stdout, stderr)
		}
		{
			n := trips()
			assert.Equal(t, int64(handshake+1), n, "%s made %d round trips, want %d: the handshake and one batch", v[0], n, handshake+1)
		}
	}
}

// tripProxy listens on loopback in front of target and counts the round trips
// its clients make: bytes from a client that open the connection or follow a
// reply begin one trip, however many writes carry them.
func tripProxy(t *testing.T, target string) (addr string, trips func() int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, err)
	var n atomic.Int64
	var mu sync.Mutex
	var open []net.Conn
	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range open {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	pipe := func(from, to net.Conn, onData func()) {
		defer wg.Done()
		defer func() { _ = from.Close(); _ = to.Close() }()
		buf := make([]byte, 32<<10)
		for {
			k, err := from.Read(buf)
			if k > 0 {
				onData()
				if _, werr := to.Write(buf[:k]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			mu.Lock()
			open = append(open, c, up)
			mu.Unlock()
			var sending atomic.Bool
			wg.Add(2)
			go pipe(c, up, func() {
				if sending.CompareAndSwap(false, true) {
					n.Add(1)
				}
			})
			go pipe(up, c, func() { sending.Store(false) })
		}
	}()
	return ln.Addr().String(), n.Load
}

// The store flag is --addr or --redis, the name every other nova tool's store
// flag has, and it takes the absolute path of a Unix socket as redisconn
// does (USE defect 6: a socket was refused as "not <host:port>", and --redis
// was an unknown flag).
func TestTheStoreIsAddrOrRedisAndASocketPathIsAnAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	code, _, errs := h.run("spill", "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", "v")
	require.Equal(t, 0, code, errs)
	code, out, errs := h.runBare("recall", "--redis", h.mr.Addr(), "--owner", "ada", "--name", "note")
	assert.Equal(t, 0, code, "recall --redis: %s%s", out, errs)
	assert.Contains(t, out, "RECALL OK key=ada:note", "recall --redis")
	sock := "/nova-redis-test-absent/redis.sock" // dialled, never created: nothing listens there
	code, out, errs = h.runBare("recall", "--addr", sock, "--owner", "ada", "--name", "note")
	assert.Equal(t, 2, code, "recall over an absent socket: %s%s", out, errs)
	assert.NotContains(t, out+errs, "is not <host:port>", "a socket path is an address")
	assert.Contains(t, out+errs, "RECALL FAIL", "a socket path is dialled")
	code, out, errs = h.runBare("recall", "--addr", "redis.sock", "--owner", "ada", "--name", "note")
	assert.Equal(t, 2, code, "a relative socket path: %s%s", out, errs)
	assert.Contains(t, out+errs, "or the absolute path of a Unix socket", "a relative path is refused naming both shapes")
	code, out, errs = h.runBare("recall", "--addr", h.mr.Addr(), "--redis", sock, "--owner", "ada", "--name", "note")
	assert.Equal(t, 2, code, "--addr beside --redis: %s%s", out, errs)
	assert.Contains(t, out+errs, "--addr and --redis name the same store", "--addr beside --redis is refused")
}

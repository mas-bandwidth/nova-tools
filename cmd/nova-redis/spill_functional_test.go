//go:build functional

package main

// spill_functional_test.go holds the spill and recall tests that run whole
// through run() against a miniredis fake dialled on loopback: real sockets are
// the functional tier (docs/STANDARD.md section 8). The logic each one pins
// keeps a unit test without a socket in spill_test.go
// (TestScratchKeepsEveryKeyOwnedAndBoundedWithoutASocket, over pipeStore, and
// TestSpillRefusesAStoreFamilyOwnerBeforeTheDial, over the trap harness).

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newStoreHarness is the harness whose fake the verbs dial: the functional
// tier's, so its cleanup does not refuse a connection.
func newStoreHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.dialled = true
	return h
}

func TestRecallRefusesAnExpiredKey(t *testing.T) {
	t.Parallel()

	h := newStoreHarness(t)
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

	h := newStoreHarness(t)
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
	h := newStoreHarness(t)
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

// TestSpillRefusesAStoreFamilyKey: an owner whose <owner>: prefix reaches a
// store key family (redisacl.Families) is refused at exit 2 before the store is
// dialled, and writes nothing. Without it --owner sprint --name epoch writes
// sprint:epoch, a sprint-family key, and --owner bench --name MACHINE:beat
// writes a machine's beat key; the HSET clobbers the key's type and the
// PEXPIRE puts a TTL on a store key, which SPEC-REDIS rule 2 forbids, so fleet
// state ages out (security#65 finding 2). The refusal names the family; scratch
// lives outside the store's families. recall is unchanged: it reads such a key
// back and refuses it as UNBOUNDED, guarding the boundary from the other side.
func TestSpillRefusesAStoreFamilyKey(t *testing.T) {
	t.Parallel()

	h := newStoreHarness(t)
	cases := []struct {
		owner, name, family string
	}{
		{"sprint", "epoch", "sprint"},
		{"bench", "MACHINE:beat", "beats"},
		{"machine", "m", "machines"},
		{"table", "work", "tables"},
		{"view", "sprint", "views"},
		{"friend", "f:beat", "friends"},
		{"fleet", "store", "fleet"},
		{"loop", "member-a", "loops"},
		{"route", "pro-a", "routes"},
		{"config", "decl", "config"},
		{"tokens", "ledger:2026-09-30", "tokens"},
		{"ev", "github", "events"},
	}
	for _, tc := range cases {
		t.Run(tc.owner, func(t *testing.T) {
			code, stdout, stderr := h.run("spill", "--owner", tc.owner, "--name", tc.name, "--ttl", "1h", "--value", "hi")
			assert.Equal(t, 2, code, "spill --owner %s exits %d, want 2; stdout=%q stderr=%q", tc.owner, code, stdout, stderr)
			assert.Contains(t, stderr, tc.family, "the refusal for --owner %s must name the %s store family; stderr=%q", tc.owner, tc.family, stderr)
			assert.Contains(t, stderr, "scratch lives outside the store's families", "the refusal for --owner %s must say scratch lives outside the store's families; stderr=%q", tc.owner, stderr)
		})
	}
	{
		keys := h.mr.Keys()
		assert.Len(t, keys, 0, "a refused spill stored %v; a refusal writes nothing", keys)
	}

	// An owner no family claims is still scratch.
	code, stdout, stderr := h.run("spill", "--owner", "rowan", "--name", "note", "--ttl", "1h", "--value", "hi")
	require.Zero(t, code, "spill --owner rowan exits %d; stdout=%q stderr=%q", code, stdout, stderr)
	assert.Contains(t, h.mr.Keys(), "rowan:note", "a scratch owner must still spill: %v", h.mr.Keys())

	// The package seam below the CLI refuses too, so a caller that skips the
	// flag parser cannot write a store-family key.
	ctx := context.Background()
	conn, err := redisconn.Open(ctx, redisconn.Options{Addr: h.mr.Addr()}, nil)
	require.NoError(t, err, err)
	t.Cleanup(func() { _ = conn.Close() })
	s := &scratch{rdb: conn.Client(), now: h.clock.now}
	{
		_, err := s.spill(ctx, "sprint", "epoch", "hi", time.Hour)
		assert.Error(t, err, "scratch.spill wrote a store-family key")
	}

	// recall is unchanged and guards the boundary from the other side: a
	// store-family key another tool wrote carries no TTL, so recall refuses it
	// as UNBOUNDED rather than reading it as scratch.
	{
		require.NoError(t, conn.Client().HSet(ctx, "sprint:epoch", "beat", "x").Err())
		code, stdout, stderr := h.run("recall", "--owner", "sprint", "--name", "epoch")
		assert.Equal(t, 1, code, "recall of an unbounded store key exits %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
		assert.Contains(t, stdout+stderr, "UNBOUNDED", "recall of an unbounded store key must say UNBOUNDED; stdout=%q stderr=%q", stdout, stderr)
	}
}

// TestEveryStoreVerbPrintsOneJSONObjectWithJSON is the half of
// TestEveryVerbPrintsOneJSONObjectWithJSON that dials: a recall that finds the
// value exactly, a recall of a missing key, and a store that does not answer,
// each one object on stdout.
func TestEveryStoreVerbPrintsOneJSONObjectWithJSON(t *testing.T) {
	t.Parallel()
	h := newStoreHarness(t)
	stored := "a b=c\\x20 d" // a space, an '=' and a backslash: the line escapes them, JSON does not
	code, _, errs := h.run("spill", "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", stored)
	require.Equal(t, 0, code, errs)
	checkJSONRows(t, h, []jsonRow{
		{"recall carries the value exactly", []string{"recall", "--addr", h.mr.Addr(), "--owner", "ada", "--name", "note", "--json"}, 0, "ok", "", "value", stored, 0},
		{"recall of a missing key says no", []string{"recall", "--json", "--addr", h.mr.Addr(), "--owner", "ada", "--name", "gone"}, 1, "failed", "MISSING", "key", "ada:gone", 0},
		{"a store that does not answer", []string{"spill", "--json", "--addr", unanswered, "--owner", "a", "--name", "b", "--ttl", "1m", "--value", "c"}, 2, "refused", "", "class", "unreachable", 1},
	})
}

// TestStatusGrammarOnAStore is the half of TestStatusGrammar that dials:
// spill's and recall's store that does not answer, and a recall that finds
// its key on the fake.
func TestStatusGrammarOnAStore(t *testing.T) {
	t.Parallel()
	checkStatusRows(t, []statusRow{
		{
			name:  "spill refused when the store does not answer",
			args:  []string{"spill", "--addr", unanswered, "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", "hi"},
			token: "SPILL", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
		{
			name: "recall ok",
			setup: func(t *testing.T) ([]string, func(...string) (int, string, string)) {
				t.Helper()
				h := newStoreHarness(t)
				code, _, stderr := h.run("spill", "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", "hi")
				require.Equal(t, 0, code, "setup spill: exit %d stderr %s", code, stderr)
				return []string{"recall", "--owner", "ada", "--name", "note"}, h.run
			},
			token: "RECALL", word: "OK", exit: 0,
		},
		{
			name:  "recall refused when the store does not answer",
			args:  []string{"recall", "--addr", unanswered, "--owner", "ada", "--name", "note"},
			token: "RECALL", word: "REFUSED", marker: "run: nova-redis help", exit: 2,
		},
	})
}

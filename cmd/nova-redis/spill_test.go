package main

// spill_test.go holds the spill/recall slice of docs/SPEC-REDIS.md (nova-tools
// #2279, behaviours 14, 16, 17 and 27 of "Tests this spec demands") that runs
// without a socket: every refusal drives run(), the same function main()
// calls, against a miniredis fake that is a trap (the cleanup fails a test
// that dialled it), and the store logic (scratch) runs over pipeStore, a
// miniredis reached through net.Pipe. The verbs run whole against a dialled
// fake in spill_functional_test.go. The clock is injected through deps.now,
// so expiry is proven by moving a fake clock, never by waiting on the wall.

import (
	"bytes"
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
	"github.com/redis/go-redis/v9"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock is the controlled clock: it reads t and only moves when a test
// moves it.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// harness is one fake instance plus the deps run() is given. run() opens the
// fake through redisconn at the --addr it is given, as main() does. In the
// unit tier the fake is a trap: nothing may dial it (dialled is false), and
// the cleanup fails the test that did. The functional tier's harness
// (newStoreHarness) is dialled.
type harness struct {
	mr      *miniredis.Miniredis
	clock   *fakeClock
	d       deps
	dialled bool
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
	t.Cleanup(func() {
		if !h.dialled {
			assert.Zero(t, mr.TotalConnectionCount(), "a unit test dialled the fake over TCP; the store logic runs over pipeStore and a verb run whole on a store is the functional tier")
		}
	})
	return h
}

// pipeStore is a miniredis fake reached in memory: the client's dialer hands
// it one end of a net.Pipe and the fake serves the other (ServeConn), so no
// TCP connection is made. miniredis binds a loopback listener when it starts;
// the cleanup fails the test when anything dialled it.
func pipeStore(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	var piped atomic.Int64
	client := redis.NewClient(&redis.Options{
		Addr:            "127.0.0.1:0", // never dialled (Dialer is the transport); an IP so nothing resolves it
		DisableIdentity: true,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			near, far := net.Pipe()
			piped.Add(1)
			mr.Server().ServeConn(far)
			return near, nil
		},
	})
	t.Cleanup(func() {
		_ = client.Close()
		assert.Equal(t, piped.Load(), int64(mr.TotalConnectionCount()), "the fake took a TCP connection beside its pipes")
	})
	return mr, client
}

// run drives run() with --redis pointed at the fake instance.
func (h *harness) run(args ...string) (int, string, string) {
	full := append([]string{args[0], "--redis", h.mr.Addr()}, args[1:]...)
	return h.runBare(full...)
}

// runBare drives run() with exactly the arguments given: no address is added,
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

// TestAddrAcceptsAUnixSocketPath: --redis (and its old spelling --addr) takes a
// Unix socket, the address shape nova-table's first-run recipe makes
// (redis-server --port 0 --unixsocket "$d/redis.sock"): an absolute path, bare
// or with redis-cli's unix: prefix. A socket names no host and no port, so
// there is nothing to guess, and redisconn dials an absolute path as a Unix
// socket already. It is accepted everywhere the address is read: the flag and
// login checks (validAddr), the dry run of spill that dials nothing, and the
// address handed to redisconn (login.options). A host:port address goes through
// unchanged.
func TestAddrAcceptsAUnixSocketPath(t *testing.T) {
	t.Parallel()

	sock := filepath.Join(t.TempDir(), "redis.sock")
	cases := []struct {
		name string
		addr string
		dial string
	}{
		{"absolute path", sock, sock},
		{"unix: prefix", "unix:" + sock, sock},
		{"host:port unchanged", "127.0.0.1:6379", "127.0.0.1:6379"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, validAddr("redis", tc.addr), "--redis %q is refused before anything is dialled", tc.addr)

			var out, errb bytes.Buffer
			d := deps{
				now:    func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) },
				getenv: func(string) string { return "" },
			}
			code := run([]string{"spill", "--dry-run", "--redis", tc.addr, "--owner", "ada", "--name", "note", "--ttl", "10m", "--value", "hi"}, &out, &errb, d)
			assert.Zero(t, code, "stdout=%q stderr=%q; a socket address passes the checks and dials nothing", out.String(), errb.String())
			assert.Contains(t, out.String(), "SPILL OK")
			assert.Contains(t, out.String(), "store="+tc.addr)

			l := login{addr: coverStr(tc.addr), user: coverStr(""), passwordEnv: coverStr(PasswordEnv), givenFn: func(string) bool { return true }}
			assert.Equal(t, tc.dial, l.options(deps{getenv: func(string) string { return "" }}).Addr, "the address redisconn dials")
		})
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

// TestScratchKeepsEveryKeyOwnedAndBoundedWithoutASocket is the store logic of
// the functional spill and recall tests (TestEveryEphemeralKeyCarriesOwnerAndTTL,
// TestRecallRefusesAnExpiredKey, TestSpillRefusesAStoreFamilyKey,
// TestSpillAndRecallAreOneRoundTripEach), run on scratch over pipeStore: a
// spill writes <owner>:<name> with its value, its expiry and a TTL in one
// transaction; the seam refuses a key with no owner, no name, no TTL or a
// store family's owner, and writes nothing; recall reads the value back
// inside the TTL, refuses it as expired once the injected clock passes it
// (the instance has not aged it out), as missing once the instance has, and
// as unbounded when the key carries no TTL; each verb is one round trip.
func TestScratchKeepsEveryKeyOwnedAndBoundedWithoutASocket(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mr, client := pipeStore(t)
	trips := redisconn.CountTrips(client)
	clock := &fakeClock{t: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
	s := &scratch{rdb: client, now: clock.now}

	refusals := []struct {
		name, owner, key string
		ttl              time.Duration
	}{
		{"no owner", "", "g", time.Hour},
		{"an owner with a colon", "ro:wan", "g", time.Hour},
		{"no name", "ada", "", time.Hour},
		{"no TTL", "ada", "h", 0},
		{"a negative TTL", "ada", "h", -5 * time.Minute},
		{"a store family's owner", "sprint", "epoch", time.Hour},
	}
	for _, r := range refusals {
		_, err := s.spill(ctx, r.owner, r.key, "v", r.ttl)
		assert.Error(t, err, "%s: scratch.spill wrote what it must refuse", r.name)
	}
	assert.Empty(t, mr.Keys(), "a refused spill stored a key")
	assert.Zero(t, trips.N(), "a refused spill reached the store")

	key, err := s.spill(ctx, "ada", "note", "hi", time.Hour)
	require.NoError(t, err)
	assert.Equal(t, "ada:note", key)
	assert.Equal(t, int64(1), trips.N(), "spill is one transaction, one round trip")
	assert.Equal(t, time.Hour, mr.TTL(key), "every spilled key carries its TTL")
	assert.Equal(t, "hi", mr.HGet(key, fieldValue))
	assert.Equal(t, "1790168400000", mr.HGet(key, fieldExpires), "the expiry is the spiller's clock plus the TTL, in milliseconds")

	v, err := s.recall(ctx, "ada", "note")
	require.NoError(t, err)
	assert.Equal(t, "hi", v)
	assert.Equal(t, int64(2), trips.N(), "recall is one pipeline, one round trip")

	clock.t = clock.t.Add(2 * time.Hour)
	_, err = s.recall(ctx, "ada", "note")
	assert.ErrorIs(t, err, errExpired, "past the TTL by the clock, before the instance ages the key out")
	mr.FastForward(2 * time.Hour)
	_, err = s.recall(ctx, "ada", "note")
	assert.ErrorIs(t, err, errMissing, "a key the instance aged out")

	mr.HSet("sprint:epoch", "beat", "x")
	_, err = s.recall(ctx, "sprint", "epoch")
	assert.ErrorIs(t, err, errUnbounded, "a store-family key carries no TTL; recall refuses it rather than read it as scratch")
}

// TestSpillRefusesAStoreFamilyOwnerBeforeTheDial is the refusal half of the
// functional TestSpillRefusesAStoreFamilyKey, through run(): an owner whose
// <owner>: prefix reaches a store key family (redisacl.Families) is refused at
// exit 2, naming the family, before the store is dialled (the harness's trap
// holds that), so nothing is written.
func TestSpillRefusesAStoreFamilyOwnerBeforeTheDial(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
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
			t.Parallel()
			code, stdout, stderr := h.run("spill", "--owner", tc.owner, "--name", tc.name, "--ttl", "1h", "--value", "hi")
			assert.Equal(t, 2, code, "spill --owner %s exits %d, want 2; stdout=%q stderr=%q", tc.owner, code, stdout, stderr)
			assert.Contains(t, stderr, tc.family, "the refusal for --owner %s must name the %s store family", tc.owner, tc.family)
			assert.Contains(t, stderr, "scratch lives outside the store's families", "the refusal for --owner %s", tc.owner)
			assert.True(t, strings.HasPrefix(stderr, "SPILL REFUSED"), "the refusal leads with SPILL REFUSED: %q", stderr)
		})
	}
}

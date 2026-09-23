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
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// fakeClock is the controlled clock: it reads t and only moves when a test
// moves it.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// harness is one fake instance plus the deps run() is given.
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
		now: clock.now,
		dial: func(addr, password string) redis.Cmdable {
			c := redis.NewClient(&redis.Options{Addr: addr, Password: password})
			t.Cleanup(func() { _ = c.Close() })
			return c
		},
		getenv: func(string) string { return "" },
	}
	return h
}

func (h *harness) run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	full := append([]string{args[0], "--addr", h.mr.Addr()}, args[1:]...)
	code := run(full, &out, &errb, h.d)
	return code, out.String(), errb.String()
}

func TestSpillRefusedWithoutOwner(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("spill", "--name", "note", "--ttl", "1h", "--value", "hi")
	if code != 2 {
		t.Fatalf("spill with no --owner exits %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "--owner is required") || !strings.Contains(stderr, "run: nova-redis help") {
		t.Errorf("spill with no --owner must name the remedy; stderr=%q", stderr)
	}
	// An empty owner is no owner: it would store a key of the form ":note".
	code, _, stderr = h.run("spill", "--owner", "", "--name", "note", "--ttl", "1h", "--value", "hi")
	if code != 2 {
		t.Fatalf("spill with an empty --owner exits %d, want 2; stderr=%q", code, stderr)
	}
	if keys := h.mr.Keys(); len(keys) != 0 {
		t.Errorf("a refused spill stored %v; a refusal writes nothing", keys)
	}
}

func TestSpillRefusedWithoutTTL(t *testing.T) {
	h := newHarness(t)
	code, stdout, stderr := h.run("spill", "--owner", "rowan", "--name", "note", "--value", "hi")
	if code != 2 {
		t.Fatalf("spill with no --ttl exits %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "--ttl is required") || !strings.Contains(stderr, "run: nova-redis help") {
		t.Errorf("spill with no --ttl must name the remedy; stderr=%q", stderr)
	}
	for _, ttl := range []string{"0s", "-5m"} {
		code, _, stderr = h.run("spill", "--owner", "rowan", "--name", "note", "--ttl", ttl, "--value", "hi")
		if code != 2 {
			t.Errorf("spill with --ttl %s exits %d, want 2 (an unbounded key is a bug); stderr=%q", ttl, code, stderr)
		}
	}
	if keys := h.mr.Keys(); len(keys) != 0 {
		t.Errorf("a refused spill stored %v; an unbounded key is refused, not stored", keys)
	}
}

func TestRecallRefusesAnExpiredKey(t *testing.T) {
	h := newHarness(t)
	if code, _, stderr := h.run("spill", "--owner", "rowan", "--name", "note", "--ttl", "1h", "--value", "hi"); code != 0 {
		t.Fatalf("spill exits %d; stderr=%q", code, stderr)
	}
	code, stdout, stderr := h.run("recall", "--owner", "rowan", "--name", "note")
	if code != 0 || !strings.Contains(stdout, "value=hi") {
		t.Fatalf("recall inside the TTL exits %d stdout=%q stderr=%q, want 0 and value=hi", code, stdout, stderr)
	}

	// Move the controlled clock past the TTL. The fake instance has NOT aged
	// the key out (it keeps its own clock), so only recall's own expiry check
	// stands between the caller and a stale value.
	h.clock.t = h.clock.t.Add(2 * time.Hour)
	code, stdout, stderr = h.run("recall", "--owner", "rowan", "--name", "note")
	if code != 1 {
		t.Fatalf("recall past the TTL exits %d, want 1; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout, "value=hi") {
		t.Errorf("recall past the TTL printed the stale value: %q", stdout)
	}
	if !strings.Contains(stdout+stderr, "EXPIRED") {
		t.Errorf("recall past the TTL must say EXPIRED; stdout=%q stderr=%q", stdout, stderr)
	}

	// And once the instance itself ages the key out, recall misses (exit 1).
	h.mr.FastForward(2 * time.Hour)
	if code, _, _ := h.run("recall", "--owner", "rowan", "--name", "note"); code != 1 {
		t.Errorf("recall of a key the instance expired exits %d, want 1", code)
	}
}

func TestEveryEphemeralKeyCarriesOwnerAndTTL(t *testing.T) {
	h := newHarness(t)
	attempts := [][]string{
		{"spill", "--owner", "rowan", "--name", "a", "--ttl", "1h", "--value", "1"},
		{"spill", "--owner", "stella", "--name", "b", "--ttl", "90m", "--value", "2"},
		{"spill", "--owner", "rowan", "--name", "c", "--value", "3"},
		{"spill", "--name", "d", "--ttl", "1h", "--value", "4"},
		{"spill", "--owner", "rowan", "--name", "e", "--ttl", "0s", "--value", "5"},
		{"spill", "--owner", "ro:wan", "--name", "f", "--ttl", "1h", "--value", "6"},
	}
	for _, a := range attempts {
		h.run(a...)
	}
	keys := h.mr.Keys()
	if len(keys) != 2 {
		t.Fatalf("stored keys %v, want exactly the two bounded, owned spills", keys)
	}
	for _, k := range keys {
		owner, name, ok := strings.Cut(k, ":")
		if !ok || owner == "" || name == "" {
			t.Errorf("key %q is not <owner>:<name>", k)
		}
		if ttl := h.mr.TTL(k); ttl <= 0 {
			t.Errorf("key %q carries no TTL (%v); an unbounded key is a bug", k, ttl)
		}
	}

	// The package seam, below the CLI: a caller that skips the flag parser
	// still cannot write a key without an owner or a TTL.
	s := &scratch{rdb: h.d.dial(h.mr.Addr(), ""), now: h.clock.now}
	ctx := context.Background()
	if _, err := s.spill(ctx, "", "g", "7", time.Hour); err == nil {
		t.Error("scratch.spill with no owner returned no error")
	}
	if _, err := s.spill(ctx, "rowan", "h", "8", 0); err == nil {
		t.Error("scratch.spill with no TTL returned no error")
	}
	if n := len(h.mr.Keys()); n != 2 {
		t.Errorf("the package seam stored a key it should have refused: %v", h.mr.Keys())
	}
}

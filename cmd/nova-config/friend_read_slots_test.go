package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nova-config friend set <name> --read-slots <n> is the same field as
// --read_slots. Add stores 2 when the flag is omitted. 0 is kept. A negative
// and a disagreement of the two flags are refused and change nothing.
func TestAFriendsReadSlotsRoundTripThroughSet(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "rowan"
	step := func(want int, args ...string) (string, string) {
		t.Helper()
		code, out, errs := h.run(t, args...)
		require.Equal(t, want, code, "%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errs)
		return out, errs
	}
	step(0, "friend", "add", "amy", "--slots", "2", "--tiers", "flash")
	out, _ := step(0, "friend", "show", "amy")
	assert.Contains(t, out, "read_slots=2")
	out, _ = step(0, "friend", "set", "amy", "--read-slots", "4")
	assert.Contains(t, out, "changed=read_slots")
	out, _ = step(0, "friend", "show", "amy")
	assert.Contains(t, out, "read_slots=4")
	step(0, "friend", "set", "amy", "--read_slots", "0")
	out, _ = step(0, "friend", "show", "amy")
	assert.Contains(t, out, "read_slots=0")
	_, errs := step(2, "friend", "set", "amy", "--read-slots", "3", "--read_slots", "4")
	assert.Contains(t, errs, "disagree")
	out, _ = step(0, "friend", "show", "amy")
	assert.Contains(t, out, "read_slots=0", "a disagreement changed nothing: %s", out)
	_, errs = step(2, "friend", "set", "amy", "--read-slots", "-1")
	assert.Contains(t, errs, "non-negative")
	out, _ = step(0, "friend", "show", "amy")
	assert.Contains(t, out, "read_slots=0")
	assert.NotContains(t, strings.TrimSpace(out), "read_slots=-1")
}

// nova-config friend set <name> --read-wait <seconds> is the same field as
// --read_wait. Add stores 600, ten minutes, when the flag is omitted. 0, a
// negative and a disagreement of the two flags are refused and change nothing.
func TestAFriendsReadWaitRoundTripsThroughSet(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.env["NOVA_FRIEND"] = "rowan"
	step := func(want int, args ...string) (string, string) {
		t.Helper()
		code, out, errs := h.run(t, args...)
		require.Equal(t, want, code, "%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errs)
		return out, errs
	}
	step(0, "friend", "add", "amy", "--slots", "2", "--tiers", "flash")
	out, _ := step(0, "friend", "show", "amy")
	assert.Contains(t, out, " read_wait=600 ")
	out, _ = step(0, "friend", "set", "amy", "--read-wait", "900")
	assert.Contains(t, out, "changed=read_wait")
	out, _ = step(0, "friend", "show", "amy")
	assert.Contains(t, out, " read_wait=900 ")
	step(0, "friend", "set", "amy", "--read_wait", "300")
	out, _ = step(0, "friend", "show", "amy")
	assert.Contains(t, out, " read_wait=300 ")
	_, errs := step(2, "friend", "set", "amy", "--read-wait", "60", "--read_wait", "120")
	assert.Contains(t, errs, "disagree")
	_, errs = step(1, "friend", "set", "amy", "--read-wait", "0")
	assert.Contains(t, errs, "at least 1")
	code, _, _ := h.run(t, "friend", "set", "amy", "--read-wait", "-5")
	assert.NotZero(t, code, "a negative read wait is refused")
	out, _ = step(0, "friend", "show", "amy")
	assert.Contains(t, out, " read_wait=300 ", "a refusal changed nothing: %s", out)
}

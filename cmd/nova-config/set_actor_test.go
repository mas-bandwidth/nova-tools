package main

// set_actor_test.go pins the set verb's actor resolution: set takes actor the
// way apply does (flag, env, login record), refusing only when none resolves
// and naming the three sources.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetTakesActorLikeApply: set with --actor works; set with no actor anywhere
// refuses naming the three sources.
func TestSetTakesActorLikeApply(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.dir = t.TempDir()
	code, _, _ := h.run(t, "migrate", "--file", "try.json")
	require.Equal(t, 0, code)

	// set with --actor works
	code, _, errs := h.run(t, "machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4", "--file", "try.json", "--actor", "a1")
	require.Equal(t, 0, code, errs)

	code, out, errs := h.run(t, "machine", "set", "m1", "--width", "6", "--actor", "a1", "--file", "try.json")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "CONFIG SET kind=machine name=m1 rev=2 changed=width")

	// set with no actor anywhere refuses naming the three sources
	code, out, errs = h.run(t, "machine", "set", "m1", "--width", "7", "--file", "try.json")
	require.Equal(t, 2, code, "%q %q", out, errs)
	assert.Empty(t, out)
	assert.Contains(t, errs, "--actor is required")
	assert.Contains(t, errs, "NOVA_FRIEND")
	assert.Contains(t, errs, "seat login's recorded actor")
}

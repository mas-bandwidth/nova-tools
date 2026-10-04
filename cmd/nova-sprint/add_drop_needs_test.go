package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAddRefusesAnUnknownNeedAndDropListsDependants tests two rules:
//  1. add refuses a need that names a dropped card (or any card not waiting, ready,
//     working, review, merging, landed, or a sentinel, or one of this add)
//  2. drop refuses a card that other waiting cards need unless --cascade is given
func TestAddRefusesAnUnknownNeedAndDropListsDependants(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")

	// Add two cards to s1
	ta.ok("add --stream s1 --count 2")
	// s1-1 and s1-2 are ready

	// Drop s1-1
	ta.ok("drop s1-1 --reason obsolete")
	// s1-1 is now dropped (off the table)

	// Try to add a card that needs the dropped s1-1 - should be refused
	code, out, errs := ta.do("add --stream s2 b --needs s1-1")
	require.Equal(t, 1, code, "add with dropped need should be refused: exit=%d out=%s err=%s", code, out, errs)
	require.Contains(t, out+errs, "s1-1", "refusal should name s1-1")
	require.Contains(t, out+errs, "dropped", "refusal should mention dropped")

	// Try to add a card that needs a ghost (non-existent) card - should be refused
	code, out, errs = ta.do("add --stream s2 b --needs ghost")
	require.Equal(t, 1, code, "add with ghost need should be refused: exit=%d out=%s err=%s", code, out, errs)
	require.Contains(t, out+errs, "ghost", "refusal should name ghost")

	// Add a card that needs s1-2 (which is ready)
	ta.ok("add --stream s2 b --needs s1-2")
	// Add another card that needs b
	ta.ok("add --stream s2 c --needs b")

	// Try to drop s1-2 (which b needs) without --cascade - should be refused
	code, out, errs = ta.do("drop s1-2 --reason obsolete")
	require.Equal(t, 1, code, "drop of needed card without cascade should be refused: exit=%d out=%s err=%s", code, out, errs)
	require.Contains(t, out+errs, "b", "refusal should name dependant b")
	require.Contains(t, out+errs, "needed by", "refusal should mention needed by")

	// Verify s1-2 is still ready (not dropped)
	require.Contains(t, ta.ok("card --fields s1-2"), "ready", "s1-2 should still be ready")

	// Drop s1-2 with --cascade - should succeed and drop s1-2, b, and c
	out = ta.ok("drop s1-2 --reason obsolete --cascade")
	require.Contains(t, out, "s1-2", "cascade drop output should name s1-2")
	require.Contains(t, out, "b", "cascade drop output should name b")
	require.Contains(t, out, "c", "cascade drop output should name c")

	// Verify c shows outcome dropped
	require.Contains(t, ta.ok("card --fields c"), "dropped", "card c should show outcome dropped")

	ta.clean()
}

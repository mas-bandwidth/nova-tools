package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

// Teardown leaves the store's keys as they were before init: the tables'
// residue, every member record, the fence, the notifications, the cursor and
// the callers' results are gone, and a table of someone else's is untouched.
// Init and the same work then run again on the same names.
func TestTeardownLeavesTheKeysAsBeforeInit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	m := NewMem()
	h.m, h.st.B = m, m
	cols, err := ntable.ParseColumns("a,b")
	require.NoError(t, err)
	require.NoError(t, m.Create(h.ctx, ntable.Table{Name: "elsewhere", Columns: cols}))
	before := m.Keys(h.st.Names)

	work := func() {
		t.Helper()
		require.NoError(t, h.st.Init(h.ctx))
		require.NoError(t, m.RowsAdd(h.ctx, "t-readers", []string{"reader-a", "reader-b", "reader-c"}))
		h.beat()
		h.setup(3)
		h.through("s1-1")
		step := DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: []string{"s1-2"}}})
		step.CallerOp = "caller-1"
		h.must(step)
		require.NoError(t, m.SetCursor(h.ctx, "1-0"))
		_, _, _, err := h.st.SyncFriends(h.ctx, []FriendSpec{{Name: "friend-a", Width: 1}, {Name: "friend-b", Width: 1}})
		require.NoError(t, err)
		_, err = h.st.FriendBeat(h.ctx, "friend-a")
		require.NoError(t, err)
		require.NoError(t, h.st.SetFriendHeld(h.ctx, "friend-b", true, "c"))
	}
	work()
	during := m.Keys(h.st.Names)
	for _, want := range []string{"table:t-work:changes", "table:t-work:identity", "t-sprint:w:s1-2", "t-sprint:done", "t-sprint:fencegen", "t-sprint:cursor", "t-sprint:inbox", "view:t-sprint", "t-sprint:friends", "t-sprint:friend-beat:friend-a"} {
		require.True(t, slices.Contains(during, want), "the work made no key %s: %v", want, during)
	}
	_, err = h.st.Teardown(h.ctx)
	require.NoError(t, err)
	if after := m.Keys(h.st.Names); !slices.Equal(after, before) {
		require.Fail(t, fmt.Sprintf("after teardown:\n%s\nbefore init:\n%s", strings.Join(after, "\n"), strings.Join(before, "\n")))
	}
	work()
	_, err = h.st.Teardown(h.ctx)
	require.NoError(t, err)
	after := m.Keys(h.st.Names)
	require.True(t, slices.Equal(after, before), "after the second teardown: %v", after)
}

// The keys teardown deletes are named exactly from the prefix, the table
// names and the ids: nothing is a pattern.
func TestTeardownKeysAreExactNames(t *testing.T) {
	t.Parallel()
	keys := TeardownKeys(sprint.Names{Prefix: "p-"}, map[string][]string{sprint.Work: {"s1-1"}}, Epochs{})
	for _, want := range []string{"table:p-work:revision", "table:p-work:definition", "table:p-work:changes", "table:p-work:ops",
		"table:p-fleet:changes", "p-sprint:w:s1-1", "p-sprint:fence", "p-sprint:fencegen", "p-sprint:inbox", "p-sprint:cursor", "p-sprint:done"} {
		require.True(t, slices.Contains(keys, want), "no %s in %v", want, keys)
	}
	for _, k := range keys {
		require.False(t, strings.ContainsAny(k, "*?["), "a pattern: %s", k)
	}
}

// After clears, the definition snapshot of every epoch after the first is
// named, the active one's too.
func TestTeardownNamesEveryEpochsDefinition(t *testing.T) {
	t.Parallel()
	keys := TeardownKeys(sprint.Names{Prefix: "p-"}, nil, Epochs{Last: 2})
	for _, want := range []string{"table:p-work:1:definition", "table:p-work:2:definition", "p-sprint:epoch", "p-sprint:inbox@2", "p-sprint:fence@1"} {
		require.True(t, slices.Contains(keys, want), "no %s in %v", want, keys)
	}
}

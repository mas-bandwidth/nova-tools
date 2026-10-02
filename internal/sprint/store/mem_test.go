package store

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Mem is the store the unit tier runs on, so what it reports to a test
// is what the test can prove: each of these pins one thing it reports.

// Tails are the last stream ids of the log and of the inbox, "" for a stream
// with no line.
func TestTheMemsTailsAreTheLastIDsOfTheLogAndTheInbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	if lg, in, err := m.Tails(ctx); err != nil || lg != "" || in != "" {
		require.Fail(t, fmt.Sprintf("an empty store: log %q, inbox %q, %v", lg, in, err))
	}
	h := newHarness(t)
	h.setup(1)
	lg, in, err := h.m.Tails(ctx)
	lines, ids, lerr := h.m.LogSince(ctx, "", 1000)
	_, nids, nerr := h.m.NotesSince(ctx, "", 1000)
	if err != nil || lerr != nil || nerr != nil || len(ids) == 0 || len(nids) == 0 {
		require.Fail(t, fmt.Sprintf("a store with a step: %v %v %v, %d lines, %d notes", err, lerr, nerr, len(lines), len(nids)))
	}
	require.Equal(t, ids[len(ids)-1], lg, "tails log %q inbox %q, want %q and %q", lg, in, ids[len(ids)-1], nids[len(nids)-1])
	require.Equal(t, nids[len(nids)-1], in, "tails log %q inbox %q, want %q and %q", lg, in, ids[len(ids)-1], nids[len(nids)-1])
}

// Touched counts the store calls that name an epoch, each epoch on its own:
// a write at an epoch, even one refused as ahead of the active epoch, and a
// search of every earlier epoch for a caller's operation.
func TestTheMemCountsTheCallsThatNameAnEpoch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newHarness(t)
	h.setup(1)
	s := h.snap()
	c := s.Work.Card("s1-1")
	man := ntable.BatchManifest{Schema: 1, Table: "t-work", Epoch: "7", ExpectedTableRevision: "0", OperationID: "e7-1", Actor: "tester",
		Members: []ntable.BatchMemberEntry{{ID: c.ID, Set: map[string]string{"probe": "1"},
			Expect: &ntable.MemberExpect{Revision: fmt.Sprint(c.Rev), Place: &ntable.PlaceExpect{Row: c.Row, Col: c.Col}}}}}
	_, err := h.m.Apply(ctx, man)
	require.Equal(t, "EPOCHAHEAD", refusalCode(err), "a write at an epoch ahead of the active one: %v", err)
	n := h.m.Touched(7)
	assert.Equal(t, 1, n, "a refused write at epoch 7 touched it %d times, want 1", n)
	if _, ok, err := h.m.DoneBefore(ctx, "no-such-op", 3); ok || err != nil {
		require.Fail(t, fmt.Sprintf("an operation nobody ran: %v %v", ok, err))
	}
	for e, want := range map[uint64]int{3: 0, 2: 1, 1: 1} {
		t.Run(fmt.Sprint("epoch ", e), func(t *testing.T) {
			n := h.m.Touched(e)
			assert.Equal(t, want, n, "a search before epoch 3 touched epoch %d %d times, want %d", e, n, want)
		})
	}
}

// A table dropped keeps what the table layer's drop keeps of it: its member
// records and its keys, until a teardown deletes them.
func TestTheMemKeepsTheResidueOfADroppedTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h := newHarness(t)
	h.setup(2)
	held, err := h.m.RecordIDs(ctx, "t-work")
	require.NoError(t, err, "the table holds no record: %v %v", held, err)
	require.NotEmpty(t, held, "the table holds no record: %v %v", held, err)
	require.NoError(t, h.m.DropTable(ctx, "t-work"))
	if left, err := h.m.RecordIDs(ctx, "t-work"); err != nil || !slices.Equal(left, held) {
		assert.Fail(t, fmt.Sprintf("after the drop the records are %v (%v), want the %v it held", left, err, held))
	}
	keys := h.m.Keys(h.st.Names)
	assert.True(t, slices.Contains(keys, "table:t-work:definition"), "after the drop the keys are %v, want its definition kept", keys)
}

// addRows names every changed row once, in order, whatever order the parts
// changed them in.
func TestAddRowsKeepsEachTablesRowsSortedAndOnce(t *testing.T) {
	t.Parallel()
	r := TickResult{Tables: newTables()}
	r.addRows(map[string][]string{sprint.Work: {"s2", "s1"}})
	r.addRows(map[string][]string{sprint.Work: {"s3", "s1"}, sprint.Fleet: {"m2", "m1"}})
	got := map[string][]string{}
	for _, tb := range r.Tables {
		got[tb.Table] = tb.Rows
	}
	if !slices.Equal(got[sprint.Work], []string{"s1", "s2", "s3"}) || !slices.Equal(got[sprint.Fleet], []string{"m1", "m2"}) || len(got[sprint.Merge]) != 0 {
		require.Fail(t, fmt.Sprintf("rows %v", got))
	}
}

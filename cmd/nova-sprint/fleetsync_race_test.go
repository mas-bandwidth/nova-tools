package main

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
)

// seamed is the test's store with a seam between the sync's reading of the
// fleet rows and their delete: the first row delete of the fleet table,
// conditional or not, runs between first; the seam's own writes go to the
// store underneath, never through the seam. failKeys fails that many key
// deletes first, as a store that did not answer.
type seamed struct {
	*store.Mem
	once    sync.Once
	between func()
}

func (s *seamed) RowsDelIf(ctx context.Context, table string, guards []store.RowGuard) ([]string, error) {
	if s.between != nil {
		s.once.Do(s.between)
	}
	return s.Mem.RowsDelIf(ctx, table, guards)
}

func (s *seamed) RowsDel(ctx context.Context, table string, rows []string) error {
	if s.between != nil {
		s.once.Do(s.between)
	}
	return s.Mem.RowsDel(ctx, table, rows)
}

// TestARemovedMemberRejoinedBeforeTheDeleteKeepsItsRowAndCards: the sync takes
// m2's control card off and reads the fleet's rows to delete; between that read
// and the delete, fleet up places m2's control card again and releases it, and a
// deal gives m2 a card. The delete is conditional at its commit on the control
// card still on no cell at the revision read, so it deletes nothing: m2's row and
// its card stay. A delete of the row as read would take the card off the table.
func TestARemovedMemberRejoinedBeforeTheDeleteKeepsItsRowAndCards(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	ta.ok("add --stream s1 --count 1 --one")
	ctx := context.Background()
	raw := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now, Actor: "tester"}
	seam := &seamed{Mem: ta.m, between: func() {
		// fleet up m2, as another coordinator command between the read and the delete
		_, err := raw.RejoinMembers(ctx, []string{"m2"})
		require.NoError(t, err)
		_, err = raw.Run(ctx, store.FleetStep(sprint.FleetReq{Op: "release", Member: "m2", Fresh: true, Who: "tester"}))
		require.NoError(t, err)
		_, err = raw.Run(ctx, store.FleetStep(sprint.FleetReq{Op: "hold", Member: "m1", Who: "tester"}))
		require.NoError(t, err)
		res, err := raw.Run(ctx, dealStep(sprint.DealReq{}))
		require.NoError(t, err)
		require.Empty(t, res.Refused, "the deal gives m2 the card")
	}}
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return seam, nil }
	inv.remove("m2")
	out := ta.ok("fleet sync")
	assert.Contains(t, out, "m2 removed", "the step took m2's control card off")
	rows := ta.fleetRows()
	require.NotNil(t, rows["m2"], "m2 was placed again before the delete: its row stays: %v", rows)
	assert.Equal(t, "1", rows["m2"]["ready"], "and its card: %v", rows)
	snap, err := raw.Load(ctx, []string{sprint.Fleet, sprint.Work}, nil)
	require.NoError(t, err)
	wc := snap.Fleet.Card("s1-1.w1")
	require.NotNil(t, wc)
	assert.Equal(t, "m2", wc.Row, "the card dealt to m2 is on the table")
}

// keysSeamed is the test's store with a seam before the cleanup's beat delete.
type keysSeamed struct {
	*store.Mem
	once    sync.Once
	between func()
}

func (s *keysSeamed) KeysDelIf(ctx context.Context, table string, guards []store.RowGuard) ([]string, error) {
	s.once.Do(s.between)
	return s.Mem.KeysDelIf(ctx, table, guards)
}

// TestARejoinBeforeTheBeatDeleteKeepsTheFreshBeat: the cleanup deletes m2's row,
// then reads that it owes m2's beat; before its delete, fleet up places m2 again
// and m2 beats. The beat delete is conditional at its commit (the row gone and
// the control card still off the table), so the rejoined member's fresh beat
// stays, and so does its row.
func TestARejoinBeforeTheBeatDeleteKeepsTheFreshBeat(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	ta.live = []string{"m1"}
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	ctx := context.Background()
	raw := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now, Actor: "tester"}
	seam := &keysSeamed{Mem: ta.m, between: func() {
		_, err := raw.RejoinMembers(ctx, []string{"m2"})
		require.NoError(t, err)
		zero := 0.0
		_, err = raw.Beat(ctx, "m2", &zero, hostload.Source{NCPU: 8})
		require.NoError(t, err)
	}}
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return seam, nil }
	inv.remove("m2")
	ta.ok("fleet sync")
	beats, err := raw.Beats(ctx, []string{"m2"})
	require.NoError(t, err)
	assert.Contains(t, beats, "m2", "the rejoined member's fresh beat is never deleted")
	assert.NotNil(t, ta.fleetRows()["m2"], "and its row is back")
}

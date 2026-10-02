package main

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// seamed is the test's store with a seam between the sync's reading of the
// fleet rows and their delete: the first row delete of the fleet table,
// conditional or not, runs between first; the seam's own writes go to the
// store underneath, never through the seam. failKeys fails that many key
// deletes first, as a store that did not answer.
type seamed struct {
	*store.Mem
	once     sync.Once
	between  func()
	failKeys int
}

func (s *seamed) RowsDelIf(ctx context.Context, table string, guards []store.RowGuard) ([]string, error) {
	if s.between != nil {
		s.once.Do(s.between)
	}
	return s.Mem.RowsDelIf(ctx, table, guards)
}

func (s *seamed) DeleteKeys(ctx context.Context, keys []string) (int, error) {
	if s.failKeys > 0 {
		s.failKeys--
		return 0, errors.New("the store did not answer")
	}
	return s.Mem.DeleteKeys(ctx, keys)
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
	ta.ok("add --stream s1 --count 1")
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
		res, err := raw.Run(ctx, store.DealStep(sprint.DealReq{}))
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

// TestAFailedCleanupIsFinishedByTheNextSync: the delete of a removed member's
// beat record fails once; the row is deleted only after its keys, so it stays,
// the sync says it could not finish, and the next sync finds the row (its control
// card off the table) as drift and finishes the cleanup.
func TestAFailedCleanupIsFinishedByTheNextSync(t *testing.T) {
	t.Parallel()
	ta, inv := syncApp(t)
	inv.set("m1", 4)
	inv.set("m2", 4)
	ta.ok("fleet sync")
	seam := &seamed{Mem: ta.m, failKeys: 1}
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return seam, nil }
	inv.remove("m2")
	code, out, errs := ta.do("fleet sync")
	assert.Equal(t, 2, code, "the cleanup did not finish: %s%s", out, errs)
	assert.Contains(t, errs, "run: nova-sprint fleet sync")
	require.NotNil(t, ta.fleetRows()["m2"], "the row stays while its keys are not deleted")
	code, check, _ := ta.do("fleet sync --check")
	assert.Equal(t, 2, code, "the row left behind is drift: %s", check)
	assert.Contains(t, check, "DRIFT remove m2")
	ta.ok("fleet sync")
	assert.Nil(t, ta.fleetRows()["m2"], "the next sync finished the cleanup")
	code, check, _ = ta.do("fleet sync --check")
	assert.Equal(t, 0, code, check)
}

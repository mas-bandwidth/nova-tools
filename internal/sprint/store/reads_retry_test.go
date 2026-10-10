package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
)

// movingStore is the in-memory store with a writer outside the fence (a
// display cell) that writes the fleet table as each of the next n read sets
// is sent: the table moves under the read, as the tick's display writes move
// it under a worker's queue read.
type movingStore struct {
	*Mem
	left *int
}

func (m movingStore) AtEpoch(epoch uint64, old bool) Backend {
	return movingStore{Mem: m.Mem.AtEpoch(epoch, old).(*Mem), left: m.left}
}

func (m movingStore) ReadSet(ctx context.Context, table string, ids []string) (ntable.ReadSetResult, error) {
	if *m.left > 0 && strings.HasSuffix(table, sprint.Fleet) {
		*m.left--
		if err := m.RowSet(ctx, table, "m1", map[string]string{sprint.Load: strconv.Itoa(*m.left)}); err != nil {
			return ntable.ReadSetResult{}, err
		}
	}
	return m.Mem.ReadSet(ctx, table, ids)
}

// A row's cells are read again when the table moved while they were read (a
// worker's queue read: queue --as): a table that moves once is read on the
// second try; one that moves at every one of the LoadTries reads is refused,
// naming the table, and never handed back as a partial read.
func TestReadCellsReadsAgainWhenTheTableMoves(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.machine() // m1 and m2 are dealt their cards
	for _, c := range []struct {
		moves int
		ok    bool
	}{{1, true}, {LoadTries + 1, false}} {
		left := c.moves
		st := &Store{B: movingStore{Mem: h.m, left: &left}, Names: h.st.Names, Actor: "m1", Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
		cards, err := st.ReadCells(h.ctx, sprint.Fleet, "m1", sprint.Ready, sprint.Working)
		switch {
		case c.ok && (err != nil || len(cards) == 0):
			require.FailNow(t, fmt.Sprintf("a table that moved once: %v, %d cards", err, len(cards)))
		case !c.ok && (err == nil || !strings.Contains(err.Error(), "kept changing") || !strings.Contains(err.Error(), sprint.Fleet)):
			require.FailNow(t, fmt.Sprintf("a table that moved at every read: %v", err))
		}
		t.Logf("moved %d times: %d cards, %v", c.moves, len(cards), err)
	}
}

// grantlessStore is the in-memory store with a user not granted the read of
// the tables' change streams.
type grantlessStore struct{ *Mem }

func (g grantlessStore) AtEpoch(epoch uint64, old bool) Backend {
	return grantlessStore{g.Mem.AtEpoch(epoch, old).(*Mem)}
}

func (g grantlessStore) TableChanges(context.Context, string, uint64, uint64) ([]string, bool, error) {
	return nil, false, &GrantError{Command: "XREVRANGE", Key: "table:fleet:changes", Cause: errors.New("NOPERM")}
}

// A read the store refuses for want of a grant is said once, a NOTE naming
// the grant, and the table is read whole instead: the tick goes on.
func TestAMissingGrantIsSaidOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setup(4)
	h.startMachine()
	h.st.B = grantlessStore{h.m}
	h.machine()
	// the world is another process: its writes are the tick's to catch up
	world := &Store{B: h.m, Names: h.st.Names, Actor: "m1", Now: h.st.Now, NewID: h.st.NewID, Sleep: h.st.Sleep}
	said := 0
	for i := 0; i < 3; i++ {
		_, err := world.Run(h.ctx, TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{Limit: 100}, Who: "m1"}))
		require.NoError(t, err)
		res := h.machine()
		for _, n := range res.Said {
			if strings.Contains(n, "grant +xrevrange") {
				said++
			}
		}
		if i == 0 {
			require.NotZero(t, res.Cost().Reads, "tick %d: a table that could not be caught up was not read whole: %s", i+1, res.TimesLine())
		}
	}
	require.EqualValues(t, 1, said, "the missing grant was said %d times over three ticks, want once", said)
}

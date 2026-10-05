package ci

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// workReads counts the reads of the work table's set columns, and of the log.
type workReads struct {
	*store.Mem
	work int
	log  int
}

func (w *workReads) CellIDs(ctx context.Context, shapes []ntable.Table) (map[string][]string, error) {
	for _, s := range shapes {
		if s.Name != sprint.Work {
			continue
		}
		sets := 0
		for _, col := range s.Columns {
			if col.HasSet() && col.Projection != ntable.Text {
				sets++
			}
		}
		if sets >= 2 {
			w.work++
		}
	}
	return w.Mem.CellIDs(ctx, shapes)
}

func (w *workReads) LogSince(ctx context.Context, after string, max int) ([]sprint.Line, []string, error) {
	w.log++
	return w.Mem.LogSince(ctx, after, max)
}

// `nova-sprint card` reads the work table whole once (CardHeld) and does not
// read the epoch log: the story comes from the log's card index. The
// behaviour is also pinned in cmd/nova-sprint and internal/sprint/store.
func TestMissed08NovaSprintCardReadsTheWorkTableIsDone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := &workReads{Mem: store.NewMem()}
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	st := &store.Store{B: m, Names: sprint.Names{}, Actor: "coordinator",
		Now:   func() time.Time { return now },
		Sleep: func(time.Duration) {}, Rand: func(int64) int64 { return 0 }}
	require.NoError(t, st.Init(ctx))
	require.NoError(t, m.RowsAdd(ctx, "readers", []string{"reader-a", "reader-b"}))
	require.NoError(t, m.SetCoordinator(ctx, "coordinator"))
	res, err := st.Run(ctx, store.AddStep(sprint.AddReq{Stream: "s1", Count: 3}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)

	m.work, m.log = 0, 0
	info, _, tbl, err := st.CardHeld(ctx, "s1-1", true)
	require.NoError(t, err)
	assert.NotNil(t, info.Primary)
	assert.NotNil(t, tbl)
	assert.Equal(t, 1, m.work, "whole work-table reads for one card")
	assert.Zero(t, m.log, "epoch-log reads for one card")
}

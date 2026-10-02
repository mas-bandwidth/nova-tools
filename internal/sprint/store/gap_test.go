package store

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/require"
)

func TestOpRecordTables(t *testing.T) {
	t.Parallel()
	op := OpRecord{
		Manifests: []ntable.BatchManifest{
			{Table: "work"},
			{Table: "fleet"},
		},
	}
	require.Equal(t, []string{"work", "fleet"}, op.Tables())
}

func TestRetryWaitReachingExactBudget(t *testing.T) {
	t.Parallel()
	st := &Store{
		Rand:  func(int64) int64 { return int64(RetryBudget) },
		Sleep: func(time.Duration) {},
	}
	r := &retry{st: st, b: &budget{}}
	require.True(t, r.wait(), "exact budget wait should be accepted")
	require.Equal(t, RetryBudget, r.slept())
}

func TestStoreJitterZero(t *testing.T) {
	t.Parallel()
	st := &Store{}
	require.Zero(t, st.jitter(0))
}

func TestTakeLockSkipsAcquireWhenFencePending(t *testing.T) {
	t.Parallel()
	m := NewMem()
	ctx := context.Background()
	op := OpRecord{ID: "op-pending", Verb: "test"}
	ok, err := m.Acquire(ctx, 0, op)
	require.NoError(t, err)
	require.True(t, ok)

	callsBefore := m.Calls["acquire"]

	st := &Store{B: m}
	lock, err := st.takeLock(ctx, Step{Verb: "tick"}, "fam")
	require.NoError(t, err)
	require.Nil(t, lock)
	require.Equal(t, callsBefore, m.Calls["acquire"], "takeLock should not call Acquire when fence already holds a pending operation")
}

func TestClearedErrorEqualEpochNotUnknown(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	err := &ClearedError{Held: 1, Now: 1, At: t0}
	require.NotContains(t, err.Error(), "unknown to this sprint")
	require.Contains(t, err.Error(), "the sprint was cleared at")
}

func TestLoadPinnedDetectsClearedEpoch(t *testing.T) {
	t.Parallel()
	m := NewMem()
	ctx := context.Background()
	st := &Store{B: m}
	require.NoError(t, st.Init(ctx))
	st, err := st.pin(ctx)
	require.NoError(t, err)

	ok, err := m.AdvanceEpoch(ctx, 0, time.Now())
	require.NoError(t, err)
	require.True(t, ok)

	_, err = st.Load(ctx, []string{sprint.Work}, nil)
	require.ErrorIs(t, err, errCleared)
}

type missingReadSetBackend struct {
	Backend
}

func (m *missingReadSetBackend) ReadSet(_ context.Context, _ string, _ []string) (ntable.ReadSetResult, error) {
	return ntable.ReadSetResult{Revision: 1, Missing: []string{"card-1"}}, nil
}

func TestReadIntoSingleMissingPlacedMember(t *testing.T) {
	t.Parallel()
	st := &Store{
		B: &missingReadSetBackend{},
	}
	tbl := sprint.NewTable("work")
	tbl.Revision = 1
	err := st.readInto(context.Background(), tbl, []string{"card-1"}, true)
	var moved *movedError
	require.ErrorAs(t, err, &moved, "a single missing placed member must trigger movedError")
}

func TestWriteEpochStaleCode(t *testing.T) {
	t.Parallel()
	m := NewMem()
	ctx := context.Background()
	st := &Store{B: m}
	require.NoError(t, st.Init(ctx))

	ok, err := m.AdvanceEpoch(ctx, 0, time.Now())
	require.NoError(t, err)
	require.True(t, ok)

	staleMem := m.AtEpoch(0, false)
	err = staleMem.RowsAdd(ctx, st.Names.Table(sprint.Work), []string{"new-row"})
	require.Equal(t, "STALE", refusalCode(err), "stale epoch write must return STALE, got: %v", err)
}

func TestMemSnapshotAndRestoreRoundTrip(t *testing.T) {
	t.Parallel()
	m1 := NewMem()
	ctx := context.Background()
	st := &Store{B: m1}
	require.NoError(t, st.Init(ctx))

	doc, err := m1.Snapshot()
	require.NoError(t, err)

	m2 := NewMem()
	require.NoError(t, m2.Restore(doc))

	doc2, err := m2.Snapshot()
	require.NoError(t, err)
	require.Equal(t, doc, doc2, "restored store must produce identical snapshot")
}

func TestRulesPathRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMem()
	st := &Store{B: m, Names: sprint.Names{Prefix: "test-"}}

	path, err := st.RulesPath(ctx)
	require.NoError(t, err)
	require.Empty(t, path)

	require.NoError(t, st.SetRulesPath(ctx, "child-rules.txt"))
	path, err = st.RulesPath(ctx)
	require.NoError(t, err)
	require.Equal(t, "child-rules.txt", path)

	require.NoError(t, st.SetRulesPath(ctx, ""))
	path, err = st.RulesPath(ctx)
	require.NoError(t, err)
	require.Empty(t, path)
}

func TestTimesLineFormat(t *testing.T) {
	t.Parallel()
	res := TickResult{
		Took: 150 * time.Millisecond,
		Times: []PartTime{
			{
				Table: "work",
				Name:  "fetch work",
				Took:  50 * time.Millisecond,
				Trips: 2,
				Reads: 1,
				Rows:  10,
			},
			{
				Name:  "clean",
				Took:  100 * time.Millisecond,
				Trips: 1,
				Reads: 0,
				Rows:  0,
			},
		},
	}
	line := res.TimesLine()
	require.Equal(t, "TIMES 150ms trips=3 reads=1 rows=10 stale=0 mismatch=0: work/fetch-work=50ms/2t/1r/10n clean=100ms/1t/0r/0n", line)
}

func TestFinishStepPricesFlag(t *testing.T) {
	t.Parallel()
	sNoUsage := FinishStep(sprint.FinishReq{As: "m1"})
	require.False(t, sNoUsage.Prices)

	sWithUsage := FinishStep(sprint.FinishReq{As: "m1", Usage: "tokens=100"})
	require.True(t, sWithUsage.Prices)
}

func TestChangeIDsBatchEventAccount(t *testing.T) {
	t.Parallel()
	_, err := changeIDs(changeEvent{verb: "apply"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "a batch event without its account")

	ids, err := changeIDs(changeEvent{verb: "other", members: `[{"id":"m1"}]`})
	require.NoError(t, err)
	require.Equal(t, []string{"m1"}, ids)

	ids, err = changeIDs(changeEvent{verb: "apply", batchDelta: `{"members":[{"id":"m2"}]}`})
	require.NoError(t, err)
	require.Equal(t, []string{"m2"}, ids)
}

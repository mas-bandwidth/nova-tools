//go:build functional

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// liveStore is a Store over a Redis of the test's own with the table layer's
// functions loaded.
func liveStore(t *testing.T) (*Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	require.NoError(t, fn.Load(ctx, c))
	names := sprint.Names{Prefix: "f-"}
	st := &Store{B: &Redis{C: c, Names: names, Now: time.Now}, Names: names, Actor: "functional"}
	require.NoError(t, st.Init(ctx))
	require.NoError(t, st.B.RowsAdd(ctx, names.Table(sprint.Readers), []string{"reader-a", "reader-b", "reader-c"}))
	return st, c
}

// The life of a stream on the real table layer, check clean at each stage,
// and the display cells and the inbox as they are written.
func TestRedisTheLifeOfAStream(t *testing.T) {
	t.Parallel()
	st, _ := liveStore(t)
	h := &harness{t: t, st: st, ctx: context.Background(), now: time.Now(), live: []string{"m1", "m2"}}
	h.setup(5)
	h.through("s1-1", "s1-2", "s1-3", "s1-4", "s1-5")
	h.clean("accepted")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 3, Conflict: "s1-2"}))
	h.clean("stopped")
	h.must(ResumeStep(sprint.ResumeReq{Stream: "s1", Did: "rebased"}))
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 10}))
	h.clean("landed")
	s := h.snap()
	require.Equal(t, string(sprint.StreamLanded), s.StreamCtl("s1").F("state"), "stream %s", s.StreamCtl("s1").F("state"))
	v, err := st.Inbox(h.ctx, time.Hour, time.Hour, 1000)
	require.NoError(t, err, "inbox: %+v %v", v, err)
	require.NotEmpty(t, v.Groups, "inbox: %+v %v", v, err)
}

// A verb over 300 cards is one invocation on the real store, in manifests
// under the 128 bound.
func TestRedisALargeSet(t *testing.T) {
	t.Parallel()
	st, _ := liveStore(t)
	h := &harness{t: t, st: st, ctx: context.Background(), now: time.Now(), live: []string{"m1", "m2"}}
	h.setup(300)
	res := h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 300}}))
	require.Len(t, res.Moved, 300, "moved %d", len(res.Moved))
	h.clean("300 started")
}

// A lost reply on the work table leaves the operation in the fence; the next
// verb finishes it, and the table layer's replay applies nothing twice.
func TestRedisAPendingOperationIsFinishedByTheNextVerb(t *testing.T) {
	t.Parallel()
	st, c := liveStore(t)
	h := &harness{t: t, st: st, ctx: context.Background(), now: time.Now(), live: []string{"m1", "m2"}}
	h.setup(1)
	lost := &lostOnce{Backend: st.B, table: st.Names.Table(sprint.Work)}
	cut := *st
	cut.B = lost
	_, err := cut.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 1}}))
	require.ErrorIs(t, err, ErrUnknown, "start with a lost reply: %v", err)
	f, _ := st.B.ReadFence(h.ctx)
	require.NotNil(t, f.Pending, "no pending operation")
	res := h.must(TakeStep(sprint.TakeReq{As: "m1"}))
	require.Len(t, res.Repaired, 1, "not finished first: %+v", res)
	h.clean("finished")
	_ = c
}

// lostOnce applies every manifest of one table and loses the reply.
type lostOnce struct {
	Backend
	table string
}

func (l *lostOnce) Apply(ctx context.Context, m ntable.BatchManifest) (ntable.Receipt, error) {
	if m.Table == l.table {
		if _, err := l.Backend.Apply(ctx, m); err != nil {
			return ntable.Receipt{}, err
		}
		return ntable.Receipt{}, errors.New("connection reset after the write")
	}
	return l.Backend.Apply(ctx, m)
}

// The owner of an operation and a writer finishing it for the owner release
// it at the same time: one commit, no error to either, the notifications once.
func TestRedisTwoWritersReleaseOneOperation(t *testing.T) {
	t.Parallel()
	st, _ := liveStore(t)
	ctx := context.Background()
	f, err := st.B.ReadFence(ctx)
	require.NoError(t, err)
	op := OpRecord{ID: "op-1", Verb: "test", At: time.Now(), Notes: []sprint.Note{{ID: "op-1.1", Kind: sprint.Judgment, Type: sprint.NWorkFailed,
		Stream: "s1", Primaries: []string{"p1"}, Count: 1, At: time.Now()}}}
	ok, err := st.B.Acquire(ctx, f.Gen, op)
	require.NoError(t, err, "acquire: %v %v", ok, err)
	require.True(t, ok, "acquire: %v %v", ok, err)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- st.B.Release(ctx, op, true) }()
	}
	for i := 0; i < 2; i++ {
		err := <-errs
		require.NoError(t, err, "release: %v", err)
	}
	notes, _, err := st.B.NotesSince(ctx, "", 100)
	require.NoError(t, err, "notifications: %d %v", len(notes), err)
	require.Len(t, notes, 1, "notifications: %d %v", len(notes), err)
}

// clear on the real table layer: the epoch advances, the rows are restored at
// it, the old epoch reads as it was, a writer holding it is refused, the same
// ids land again, and teardown leaves no key of any epoch.
func TestRedisClear(t *testing.T) {
	t.Parallel()
	st, c := liveStore(t)
	h := &harness{t: t, st: st, ctx: context.Background(), now: time.Now(), live: []string{"m1", "m2"}}
	h.setup(3)
	h.through("s1-1", "s1-2")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1", Batch: 1}))
	res, err := st.Clear(h.ctx)
	require.NoError(t, err, "clear: %+v %v", res, err)
	require.Equal(t, uint64(1), res.To, "clear: %+v %v", res, err)
	h.clean("cleared")
	old, err := st.At(0).Load(h.ctx, All, nil)
	require.NoError(t, err, "the old epoch: %v", err)
	require.Equal(t, sprint.Landed, old.StateOf("s1-1"), "the old epoch: %v", err)
	held := uint64(0)
	step := MergeStep(sprint.MergeReq{Stream: "s1"})
	step.Epoch = &held
	r, err := st.Run(h.ctx, step)
	require.NoError(t, err, "a merge holding the old epoch: %+v %v", r, err)
	require.Len(t, r.Refused, 1, "a merge holding the old epoch: %+v %v", r, err)
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
	h.through("s1-1", "s1-2")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1"}))
	h.clean("landed again")
	_, err = st.Teardown(h.ctx)
	require.NoError(t, err)
	keys, err := c.Keys(h.ctx, "*f-*").Result()
	require.NoError(t, err, "keys left: %v %v", keys, err)
	require.Empty(t, keys, "keys left: %v %v", keys, err)
}

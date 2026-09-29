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
)

// liveStore is a Store over a Redis of the test's own with the table layer's
// functions loaded.
func liveStore(t *testing.T) (*Store, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	names := sprint.Names{Prefix: "f-"}
	st := &Store{B: &Redis{C: c, Names: names, Now: time.Now}, Names: names, Actor: "functional"}
	if err := st.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.B.RowsAdd(ctx, names.Table(sprint.Readers), []string{"reader-a", "reader-b", "reader-c"}); err != nil {
		t.Fatal(err)
	}
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
	if s.StreamCtl("s1").F("state") != sprint.StreamLanded {
		t.Fatalf("stream %s", s.StreamCtl("s1").F("state"))
	}
	v, err := st.Inbox(h.ctx, time.Hour, time.Hour, 1000)
	if err != nil || len(v.Groups) == 0 {
		t.Fatalf("inbox: %+v %v", v, err)
	}
}

// A verb over 300 cards is one invocation on the real store, in manifests
// under the 128 bound.
func TestRedisALargeSet(t *testing.T) {
	t.Parallel()
	st, _ := liveStore(t)
	h := &harness{t: t, st: st, ctx: context.Background(), now: time.Now(), live: []string{"m1", "m2"}}
	h.setup(300)
	res := h.must(DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 300}}))
	if len(res.Moved) != 300 {
		t.Fatalf("moved %d", len(res.Moved))
	}
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
	if _, err := cut.Run(h.ctx, DealStep(sprint.DealReq{Sel: sprint.Sel{Limit: 1}})); !errors.Is(err, ErrUnknown) {
		t.Fatalf("start with a lost reply: %v", err)
	}
	if f, _ := st.B.ReadFence(h.ctx); f.Pending == nil {
		t.Fatalf("no pending operation")
	}
	res := h.must(TakeStep(sprint.TakeReq{As: "m1"}))
	if len(res.Repaired) != 1 {
		t.Fatalf("not finished first: %+v", res)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	op := OpRecord{ID: "op-1", Verb: "test", At: time.Now(), Notes: []sprint.Note{{ID: "op-1.1", Kind: sprint.Judgment, Type: sprint.NWorkFailed,
		Stream: "s1", Primaries: []string{"p1"}, Count: 1, At: time.Now()}}}
	if ok, err := st.B.Acquire(ctx, f.Gen, op); !ok || err != nil {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- st.B.Release(ctx, op, true) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("release: %v", err)
		}
	}
	notes, _, err := st.B.NotesSince(ctx, "", 100)
	if err != nil || len(notes) != 1 {
		t.Fatalf("notifications: %d %v", len(notes), err)
	}
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
	if err != nil || res.To != 1 {
		t.Fatalf("clear: %+v %v", res, err)
	}
	h.clean("cleared")
	old, err := st.At(0).Load(h.ctx, All, nil)
	if err != nil || old.StateOf("s1-1") != sprint.Landed {
		t.Fatalf("the old epoch: %v", err)
	}
	held := uint64(0)
	step := MergeStep(sprint.MergeReq{Stream: "s1"})
	step.Epoch = &held
	if r, err := st.Run(h.ctx, step); err != nil || len(r.Refused) != 1 {
		t.Fatalf("a merge holding the old epoch: %+v %v", r, err)
	}
	h.must(AddStep(sprint.AddReq{Stream: "s1", Count: 2}))
	h.through("s1-1", "s1-2")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1"}))
	h.clean("landed again")
	if _, err := st.Teardown(h.ctx); err != nil {
		t.Fatal(err)
	}
	keys, err := c.Keys(h.ctx, "*f-*").Result()
	if err != nil || len(keys) != 0 {
		t.Fatalf("keys left: %v %v", keys, err)
	}
}

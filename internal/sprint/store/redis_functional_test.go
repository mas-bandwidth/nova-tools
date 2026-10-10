//go:build functional

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
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
	// Clear left the machine STOPPED, so through's take is refused until it is started.
	h.startMachine()
	h.through("s1-1", "s1-2")
	h.must(MergeStep(sprint.MergeReq{Stream: "s1"}))
	h.clean("landed again")
	h.stopMachine()
	_, err = st.Teardown(h.ctx)
	require.NoError(t, err)
	keys, err := c.Keys(h.ctx, "*f-*").Result()
	require.NoError(t, err, "keys left: %v %v", keys, err)
	require.Empty(t, keys, "keys left: %v %v", keys, err)
}

// The conditional row delete on the real table layer (RowsDelIf, the fleet sync's
// cleanup): a guard read before the member's control card was placed again
// deletes nothing, the row and the card stay; a guard read at the card's
// revision now, on no cell, deletes the row.
func TestRedisRowsDelIfKeepsARowWhoseRecordChanged(t *testing.T) {
	t.Parallel()
	st, _ := liveStore(t)
	h := &harness{t: t, st: st, ctx: context.Background(), now: time.Now(), live: []string{"m1", "m2"}}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	removeM2 := func() RowGuard {
		h.must(FleetStep(sprint.FleetReq{Op: "sync", Who: "functional", Sync: []sprint.SyncMember{{Name: "m1", Width: 4}}, Machines: []string{"m1"}}))
		pinned, err := st.pin(h.ctx)
		require.NoError(t, err)
		s, err := pinned.Load(h.ctx, []string{sprint.Fleet, sprint.Work}, sprint.NamedExtras(sprint.Fleet, []string{sprint.CtlID("m2")}))
		require.NoError(t, err)
		require.Nil(t, s.MemberCtl("m2"), "the sync took m2's control card off")
		rec := s.Fleet.Card(sprint.CtlID("m2"))
		require.NotNil(t, rec)
		id := pinned.sid(sprint.CtlID("m2"))
		return RowGuard{Row: "m2", ID: id, Key: pinned.Names.RecordKey(sprint.Fleet, id), Rev: rec.Rev}
	}
	stale := removeM2()
	got, err := st.RejoinMembers(h.ctx, []string{"m2"})
	require.NoError(t, err)
	require.Equal(t, []string{"m2"}, got)
	pinned, err := st.pin(h.ctx)
	require.NoError(t, err)
	deleted, err := pinned.B.RowsDelIf(h.ctx, pinned.Names.Table(sprint.Fleet), []RowGuard{stale})
	require.NoError(t, err)
	require.Empty(t, deleted, "the card was placed again after the guard's read: the row stays")
	require.NotNil(t, h.snap().MemberCtl("m2"))
	fresh := removeM2()
	deleted, err = pinned.B.RowsDelIf(h.ctx, pinned.Names.Table(sprint.Fleet), []RowGuard{fresh})
	require.NoError(t, err)
	require.Equal(t, []string{"m2"}, deleted)
	require.False(t, h.snap().Fleet.HasRow("m2"), "the row is deleted")
}

// A rejoin between the conditional delete's read and its EXEC keeps the row on
// the real table layer (RowsDelIf, the fleet sync's cleanup): the transaction
// watches the control card's record where the sprint's tables keep it
// (Names.RecordKey), so the rejoin's cell add aborts it, the read again finds
// the card placed, and the row, the card and the member's beat stay.
func TestRedisARejoinBetweenTheReadAndTheExecKeepsTheRow(t *testing.T) {
	t.Parallel()
	st, _ := liveStore(t)
	h := &harness{t: t, st: st, ctx: context.Background(), now: time.Now(), live: []string{"m1", "m2"}}
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
	h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
	h.must(FleetStep(sprint.FleetReq{Op: "sync", Who: "functional", Sync: []sprint.SyncMember{{Name: "m1", Width: 4}}, Machines: []string{"m1"}}))
	require.Nil(t, h.snap().MemberCtl("m2"), "the sync took m2's control card off")
	_, err := st.Beat(h.ctx, "m2", new(float64), hostload.Source{NCPU: 8})
	require.NoError(t, err)
	r := st.B.(*Redis)
	var once sync.Once
	r.beforeExec = func() {
		once.Do(func() {
			// another coordinator command, on its own connection: fleet up m2
			other := &Store{B: &Redis{C: r.C, Names: r.Names, Now: time.Now}, Names: st.Names, Actor: "other"}
			got, err := other.RejoinMembers(h.ctx, []string{"m2"})
			require.NoError(t, err)
			require.Equal(t, []string{"m2"}, got)
		})
	}
	dropped, err := st.DropMembers(h.ctx, []string{"m1"})
	require.NoError(t, err)
	assert.Empty(t, dropped, "m2 was placed again before the EXEC: nothing is deleted")
	s := h.snap()
	assert.True(t, s.Fleet.HasRow("m2"), "the row stays")
	assert.NotNil(t, s.MemberCtl("m2"), "the control card stays placed")
	beats, err := st.Beats(h.ctx, []string{"m2"})
	require.NoError(t, err)
	assert.Contains(t, beats, "m2", "the rejoined member's beat stays")
}

// HeldBack on the real table layer reads the work table's waiting cells alone and
// counts what no tick moves on its own (nova-tools#5096 item 16): a held sentinel
// and the cards behind it, not a ready card of another stream.
func TestRedisHeldBackReadsTheWaitingColumn(t *testing.T) {
	t.Parallel()
	st, _ := liveStore(t)
	ctx := context.Background()
	n, err := st.HeldBack(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, n, "an empty table holds nothing back")
	for _, r := range []sprint.AddReq{
		{Stream: "w", IDs: []string{"gate"}, Sentinel: true, Held: true, Who: "functional"},
		{Stream: "w", IDs: []string{"a", "b"}, Who: "functional"},
		{Stream: "v", IDs: []string{"c"}, Who: "functional"},
	} {
		res, err := st.Run(ctx, AddStep(r))
		require.NoError(t, err)
		require.Empty(t, res.Refused, "add %+v", r)
	}
	n, err = st.HeldBack(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, n, "gate, a and b are held back; c is ready")
}

// TestEveryFunctionalSocketPathFitsTheUnixBound pins internal/testredis's
// SocketPath to the shortest sockaddr_un bound the platforms this runs on
// allow (104 bytes, macOS), so a work directory a CI runner puts deep under a
// long TMPDIR still yields a socket redis-server can bind (STANDARD.md
// section 8, tests pin the rule). When SocketPath falls back to a private
// directory outside work, the test removes that directory, which is the
// caller's to remove (rule 10: a test writes only inside its own t.TempDir()).
func TestEveryFunctionalSocketPathFitsTheUnixBound(t *testing.T) {
	t.Parallel()
	longWork := filepath.Join(t.TempDir(), strings.Repeat("a", 200))
	sock, err := testredis.SocketPath(longWork, "test.sock")
	require.NoError(t, err)
	if !strings.HasPrefix(sock, longWork) {
		dir := filepath.Dir(sock)
		t.Cleanup(func() { _ = os.Remove(dir) })
	}
	assert.LessOrEqual(t, len(sock), 104, "socket path %s is %d bytes", sock, len(sock))
}

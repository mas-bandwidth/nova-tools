package table_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// TestSprintTableReadsOnlyTheCurrentEpoch (nova-tools#4238; Glenn 2026-09-26
// 9:25 AM ET: "if the numbers from the async things are not the same
// sequence as current table view, the display is zero"): over the fixture
// (cards in every state, copies on every consumer) one HINCRBY of
// sprint:epoch n zeroes every stream and consumer cell on the next tick;
// status and load, which read the beat's at, stay; a member written into the
// old epoch's set after that cannot make a cell non-zero, and a member of
// the new epoch's set shows. The tick that sees the new epoch re-reads once
// (like a membership change) and the ticks after it are one round trip.
func TestSprintTableReadsOnlyTheCurrentEpoch(t *testing.T) {
	t.Parallel()

	client, _, log := sprintStore(t)
	ctx, now := context.Background(), table.SprintFixtureNow()
	r := table.NewSprintReader(client, table.SprintFixtureConfig())
	before, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if before.Epoch != 0 || before.Streams[0].Total() == 0 || before.Consumers[0].Working == 0 {
		t.Fatalf("the fixture at epoch 0 reads empty: epoch=%d %+v %+v", before.Epoch, before.Streams[0], before.Consumers[0])
	}
	if err := client.HIncrBy(ctx, ws.EpochKey, ws.EpochField, 1).Err(); err != nil {
		t.Fatal(err)
	}
	// a writer still holding the old epoch: its member lands in epoch 0's set
	client.ZAdd(ctx, ws.ConsumerKeyAt(0, "bench:space", "ok"), redis.Z{Score: 1, Member: "late~1"})
	client.ZAdd(ctx, ws.KeyAt(0, "swarm: cards", "ready"), redis.Z{Score: 1, Member: "late"})
	log.reset()
	snap, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Epoch != 1 || snap.RoundTrips != 2 {
		t.Fatalf("the tick after the clear: epoch=%d round trips=%d, want 1 and 2 (the epoch changed under it)", snap.Epoch, snap.RoundTrips)
	}
	for _, s := range snap.Streams {
		if s.Total() != 0 {
			t.Fatalf("stream %q is not zero at epoch 1: %+v", s.Name, s)
		}
	}
	up, load := 0, 0
	for _, c := range snap.Consumers {
		if c.Ready != 0 || c.Working != 0 || c.OK != 0 || c.Fail != 0 {
			t.Fatalf("consumer %s is not zero at epoch 1: %+v", c.ID(), c)
		}
		if c.Up {
			up++
		}
		if c.Load != "-" {
			load++
		}
	}
	if up == 0 || load == 0 {
		t.Fatalf("status and load read the beat, not the epoch: up=%d load=%d", up, load)
	}
	names, _ := log.reset()
	for _, n := range names {
		if n == "GET" || n == "FCALL_RO" || n == "FCALL" {
			t.Fatalf("the tick read the epoch with %s; the tick reads the keyspace through HGET: %v", n, names)
		}
	}
	// the current epoch's sets are what the cells count
	client.ZAdd(ctx, ws.ConsumerKeyAt(1, "bench:space", "ok"), redis.Z{Score: 1, Member: "new~1"})
	client.ZAdd(ctx, ws.KeyAt(1, "swarm: cards", "ready"), redis.Z{Score: 1, Member: "new"})
	snap, err = r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.RoundTrips != 1 {
		t.Fatalf("a tick at a steady epoch took %d round trips, want 1", snap.RoundTrips)
	}
	var space table.ConsumerRow
	for _, c := range snap.Consumers {
		if c.ID() == "bench:space" {
			space = c
		}
	}
	if snap.Streams[0].Ready != 1 || snap.Streams[0].Total() != 1 || space.OK != 1 || space.Ready != 0 {
		t.Fatalf("epoch 1 members do not show: %+v %+v", snap.Streams[0], space)
	}
	if !strings.Contains(snap.Render(now), "swarm: cards") {
		t.Fatal("the rendered table lost its streams")
	}
}

// TestSprintTableRefusesAJunkEpoch: a sprint:epoch n that is not a uint64
// fails the tick with its value named, never a table of epoch 0's cells.
func TestSprintTableRefusesAJunkEpoch(t *testing.T) {
	t.Parallel()

	client, _, _ := sprintStore(t)
	ctx, now := context.Background(), table.SprintFixtureNow()
	client.HSet(ctx, ws.EpochKey, ws.EpochField, "soon")
	_, err := table.NewSprintReader(client, table.SprintFixtureConfig()).Read(ctx, now)
	if err == nil || !strings.Contains(err.Error(), `sprint:epoch n is "soon", not a uint64`) {
		t.Fatalf("err = %v, want the junk epoch named", err)
	}
}

// TestLiveTableReadsOnlyTheCurrentEpoch (nova-tools#4238): the live layout's
// bench card cells are the current epoch's sets, and a friend row's counts
// (written by presence.lua's friend_row with the epoch it read) show only
// while that epoch is current: a row stamped with an older epoch, or none,
// prints zero. The read stays the SCAN walk plus one pipeline.
func TestLiveTableReadsOnlyTheCurrentEpoch(t *testing.T) {
	t.Parallel()

	now := table.Fixture2674Now()
	at := now.Add(-1 * time.Second).UTC().Format("2006-01-02T15:04:05Z")
	client := liveStore(t, [][]string{
		{"HSET", "bench:alpha", "host", "alpha", "at", at, "load1", "1.00"},
		{"ZADD", ws.ConsumerKeyAt(0, "bench:alpha", "working"), "1", "old~1"},
		{"ZADD", ws.ConsumerKeyAt(0, "bench:alpha", "ok"), "1", "old~2"},
		{"ZADD", ws.ConsumerKeyAt(2, "bench:alpha", "working"), "1", "new~1"},
		{"HSET", "friend:rowan", "at", at, "up", "1", "ready", "3", "working", "4", "done", "5", "epoch", "1"},
		{"HSET", "friend:emma", "at", at, "up", "1", "ready", "1", "working", "1", "done", "1", "epoch", "2"},
		{"HSET", "friend:stella", "at", at, "up", "1", "ready", "6", "working", "6", "done", "6"},
		{"HSET", ws.EpochKey, ws.EpochField, "2"},
	})
	log := &cmdLog{}
	client.AddHook(log)
	cfg := table.LiveConfig{Friends: []string{"rowan", "emma", "stella"}, Sprint: "x"}
	snap, err := table.ReadLive(context.Background(), client, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, trips := log.reset(); trips != 2 {
		t.Fatalf("round trips = %d, want 2 (SCAN with the epoch, then one pipeline)", trips)
	}
	if snap.Epoch != 2 {
		t.Fatalf("epoch = %d, want 2", snap.Epoch)
	}
	rows := map[string]table.FriendRow{}
	for _, f := range snap.Friends {
		rows[f.Name] = f
	}
	if r := rows["rowan"]; r.Ready != "0" || r.Working != "0" || r.Done != "0" || r.Up != "1" {
		t.Fatalf("a row stamped epoch 1 shows at epoch 2: %+v", r)
	}
	if r := rows["stella"]; r.Ready != "0" || r.Working != "0" || r.Done != "0" {
		t.Fatalf("a row with no epoch shows at epoch 2: %+v", r)
	}
	if r := rows["emma"]; r.Ready != "1" || r.Working != "1" || r.Done != "1" {
		t.Fatalf("a row stamped with the current epoch is zero: %+v", r)
	}
	out := snap.RenderLive(now)
	if !strings.Contains(out, "alpha      |     0 |       1 |     0 |     0 |     0 |") {
		t.Fatalf("alpha's cells are not epoch 2's (working 1, ok 0):\n%s", out)
	}
}

// clearMidPipeline lands a sprint clear (one HINCRBY of sprint:epoch n,
// through another client on the same store) in the middle of the first
// pipeline that counts a cell: the commands before its first ZCARD run, then
// the clear, then the rest, the interleaving a clear racing a tick can make.
type clearMidPipeline struct {
	other *redis.Client
	fired bool
}

func (h *clearMidPipeline) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *clearMidPipeline) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }

func (h *clearMidPipeline) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if h.fired {
			return next(ctx, cmds)
		}
		for i, c := range cmds {
			if c.Name() != "zcard" {
				continue
			}
			h.fired = true
			// a command's own error (a GET's nil) comes back from next like
			// the pipeline's: the rest still runs, the first error is kept
			first := next(ctx, cmds[:i])
			if err := h.other.HIncrBy(ctx, ws.EpochKey, ws.EpochField, 1).Err(); err != nil {
				return err
			}
			if err := next(ctx, cmds[i:]); first == nil {
				first = err
			}
			return first
		}
		return next(ctx, cmds)
	}
}

func otherClient(t *testing.T, c *redis.Client) *redis.Client {
	t.Helper()
	o := redis.NewClient(&redis.Options{Addr: c.Options().Addr})
	t.Cleanup(func() { _ = o.Close() })
	return o
}

// TestSprintTableClearMidPipelineShowsNoOldFrame (nova-tools#4238, item 5 of
// the #4377 read): the tick reads sprint:epoch AFTER its cells, so a clear
// that lands between the pipeline's first commands and its cells is seen by
// that same tick, which reads again under the new epoch: the tick after the
// clear shows zeros, never one frame of the old epoch's cells.
func TestSprintTableClearMidPipelineShowsNoOldFrame(t *testing.T) {
	t.Parallel()

	client, _, _ := sprintStore(t)
	ctx, now := context.Background(), table.SprintFixtureNow()
	r := table.NewSprintReader(client, table.SprintFixtureConfig())
	if before, err := r.Read(ctx, now); err != nil || before.Streams[0].Total() == 0 {
		t.Fatalf("the fixture at epoch 0 reads empty: %v", err)
	}
	client.AddHook(&clearMidPipeline{other: otherClient(t, client)})
	snap, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Epoch != 1 || snap.RoundTrips != 2 {
		t.Fatalf("the tick the clear landed in: epoch=%d round trips=%d, want 1 and 2", snap.Epoch, snap.RoundTrips)
	}
	for _, s := range snap.Streams {
		if s.Total() != 0 {
			t.Fatalf("a clear mid-pipeline showed an old frame: stream %q %+v", s.Name, s)
		}
	}
	for _, c := range snap.Consumers {
		if c.Ready != 0 || c.Working != 0 || c.OK != 0 || c.Fail != 0 {
			t.Fatalf("a clear mid-pipeline showed an old frame: consumer %s %+v", c.ID(), c)
		}
	}
}

// TestLiveTableClearMidPipelineShowsNoOldFrame (#4238): ReadLive reads the
// epoch again after its cells and reads the cells again when it moved, so a
// clear landing between the SCAN and the cells shows the new epoch's cells.
func TestLiveTableClearMidPipelineShowsNoOldFrame(t *testing.T) {
	t.Parallel()

	now := table.Fixture2674Now()
	at := now.Add(-1 * time.Second).UTC().Format("2006-01-02T15:04:05Z")
	client := liveStore(t, [][]string{
		{"HSET", "bench:alpha", "host", "alpha", "at", at, "load1", "1.00"},
		{"ZADD", ws.ConsumerKeyAt(2, "bench:alpha", "working"), "1", "old~1", "2", "old~2"},
		{"ZADD", ws.ConsumerKeyAt(3, "bench:alpha", "ok"), "1", "new~1"},
		{"HSET", "friend:emma", "at", at, "up", "1", "ready", "1", "working", "1", "done", "1", "epoch", "2"},
		{"HSET", ws.EpochKey, ws.EpochField, "2"},
	})
	client.AddHook(&clearMidPipeline{other: otherClient(t, client)})
	snap, err := table.ReadLive(context.Background(), client, table.LiveConfig{Friends: []string{"emma"}, Sprint: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Epoch != 3 {
		t.Fatalf("epoch = %d, want 3 (the clear landed mid-read)", snap.Epoch)
	}
	if f := snap.Friends[0]; f.Ready != "0" || f.Working != "0" || f.Done != "0" {
		t.Fatalf("a row stamped with the old epoch shows after a mid-read clear: %+v", f)
	}
	out := snap.RenderLive(now)
	if !strings.Contains(out, "alpha      |     0 |       0 |     0 |     1 |     0 |") {
		t.Fatalf("alpha's cells are not epoch 3's (working 0, ok 1):\n%s", out)
	}
}

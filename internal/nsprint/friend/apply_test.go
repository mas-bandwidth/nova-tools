package friend_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

// apBase is the timeline epoch (UTC ms) of every apply test.
const apBase int64 = 1790000000000

func apAt(ms int64) time.Time { return time.UnixMilli(apBase + ms) }

// apBeats appends ada's beat events every 10 s over [from, to] s, pipelined
// through ns_friend_event (the one writer).
func apBeats(t *testing.T, client *redis.Client, f string, from, to int64) {
	t.Helper()
	ctx := context.Background()
	pipe := client.Pipeline()
	for s := from; s <= to; s += 10 {
		pipe.FCall(ctx, life.FunctionEvent, nil, f, life.EventBeat, "", strconv.FormatInt(apBase+s*1000, 10), f)
	}
	cmds, err := pipe.Exec(ctx)
	fsMust(t, err)
	for _, c := range cmds {
		if v := c.(*redis.Cmd).Val().([]any); v[0] != "OK" {
			t.Fatalf("beat event: %v", v)
		}
	}
}

func apApply(t *testing.T, st *store.Store, f string, ms int64, hook func()) friend.ApplyResult {
	t.Helper()
	res, err := friend.ApplyLife(context.Background(), st, f, apAt(ms), hook)
	fsMust(t, err)
	return res
}

func apLifeLog(t *testing.T, client *redis.Client) int {
	t.Helper()
	n := 0
	for _, m := range fsCapLog(t, client) {
		if m.Values["actor"] == friend.ActorLife {
			n++
		}
	}
	return n
}

// TestApplyWritesThroughNsFriendState: the 11 h beats-only fixture read
// through ApplyLife leaves offline-model written by ns_friend_state (actor
// life on cap:log, one entry wake on the outbox); a replayed tick is DUP and
// a later tick with a new beat sends no second wake.
func TestApplyWritesThroughNsFriendState(t *testing.T) {
	st, client := fsRedis(t)
	fsFriends(t, client, "ada", "stella", "johnny", "rowan")
	apBeats(t, client, "ada", 0, 3959*10)
	end := int64(3959*10*1000 + 5000)
	res := apApply(t, st, "ada", end, nil)
	if res.Status != "OK" || res.Class != life.ClassOfflineModel || res.State != friend.StateOfflineModel {
		t.Fatalf("apply: %+v", res)
	}
	state := fsState(client, "ada")
	if state["state"] != friend.StateOfflineModel || !strings.HasPrefix(state["idem"], "life:ada:") {
		t.Fatalf("stored %v", state)
	}
	found := false
	for _, m := range fsCapLog(t, client) {
		if m.Values["kind"] == "friend-state" && m.Values["actor"] == friend.ActorLife && m.Values["subject"] == "ada" {
			found = true
		}
	}
	if !found {
		t.Fatal("no cap:log friend-state entry with actor life")
	}
	if n := len(fsOutbox(t, client, "ada", "wake")); n != 1 {
		t.Fatalf("%d wakes on the outbox, want 1", n)
	}
	if res := apApply(t, st, "ada", end, nil); res.Status != "DUP" {
		t.Fatalf("replayed tick: %+v, want DUP", res)
	}
	apBeats(t, client, "ada", 3960*10, 3960*10)
	if res := apApply(t, st, "ada", end+10000, nil); res.Status != "OK" {
		t.Fatalf("next tick: %+v", res)
	}
	if n := len(fsOutbox(t, client, "ada", "wake")); n != 1 {
		t.Fatalf("%d wakes after a second offline-model tick, want 1", n)
	}
	if fsState(client, "ada")["state"] != friend.StateOfflineModel {
		t.Fatal("offline-model did not hold")
	}
}

// TestApplyConditionalOnRead: a human away that lands between ApplyLife's
// read and its FCALL survives: the writer answers STALE and writes nothing.
func TestApplyConditionalOnRead(t *testing.T) {
	away := func(t *testing.T, st *store.Store) func() {
		return func() {
			fsMust(t, friend.Report(context.Background(), st, friend.ReportRequest{Friend: "ada", State: friend.StateAway,
				Until: time.Now().Add(time.Hour).Truncate(time.Millisecond), Actor: "ada", Idem: "away-1"}))
		}
	}
	t.Run("clear", func(t *testing.T) {
		st, client := fsRedis(t)
		fsFriends(t, client, "ada", "stella", "johnny", "rowan")
		apBeats(t, client, "ada", 0, 300)
		if res := apApply(t, st, "ada", 300000, nil); res.State != friend.StateOfflineModel || res.Status != "OK" {
			t.Fatalf("setup: %+v", res)
		}
		fsEvent(t, client, "ada", life.EventTurnStart, "", apBase+305000)
		apBeats(t, client, "ada", 310, 310)
		res := apApply(t, st, "ada", 310000, away(t, st))
		if res.Class != life.ClassUp || res.Status != friend.StateStale {
			t.Fatalf("clear across an away: %+v, want STALE", res)
		}
		state := fsState(client, "ada")
		if state["state"] != friend.StateAway || state["until"] == "" {
			t.Fatalf("away erased: %v", state)
		}
		msgs := fsCapLog(t, client)
		seenAway := false
		for _, m := range msgs {
			if m.Values["idem"] == "away-1" {
				seenAway = true
			} else if seenAway && m.Values["actor"] == friend.ActorLife {
				t.Fatalf("a classifier entry after the away: %v", m.Values)
			}
		}
		n := len(msgs)
		if res := apApply(t, st, "ada", 310000, nil); res.Status != "HELD" || res.Values != nil {
			t.Fatalf("next tick: %+v, want HELD with no FCALL", res)
		}
		if len(fsCapLog(t, client)) != n {
			t.Fatal("HELD wrote cap:log")
		}
	})
	t.Run("write", func(t *testing.T) {
		st, client := fsRedis(t)
		fsFriends(t, client, "ada", "stella", "johnny", "rowan")
		apBeats(t, client, "ada", 0, 300)
		res := apApply(t, st, "ada", 300000, away(t, st))
		if res.Class != life.ClassOfflineModel || res.Status != friend.StateStale {
			t.Fatalf("offline-model across an away: %+v, want STALE", res)
		}
		if state := fsState(client, "ada"); state["state"] != friend.StateAway || state["until"] == "" {
			t.Fatalf("away overwritten: %v", state)
		}
		if n := len(fsOutbox(t, client, "ada", "wake")); n != 0 {
			t.Fatalf("%d wakes after a STALE write", n)
		}
	})
}

// apTick is one reconciler tick: ApplyLife, then one redistribute call.
func apTick(t *testing.T, st *store.Store, ms int64) (friend.ApplyResult, *life.Move) {
	t.Helper()
	res := apApply(t, st, "ada", ms, nil)
	moves, err := life.Redistribute(context.Background(), st, fsRoster, "reconciler", "tick-"+strconv.FormatInt(ms, 10))
	fsMust(t, err)
	return res, fsMoveOf(moves, "ada")
}

// apRecoverFixture: ada beats throughout with one open task; D1 at 0 s is
// never answered, so the 121 s tick writes wake-missed and moves the task.
// It returns D1's id and the outbox length after that tick.
func apRecoverFixture(t *testing.T, st *store.Store, client *redis.Client) (string, int64) {
	t.Helper()
	ctx := context.Background()
	fsFriends(t, client, "ada", "stella", "johnny", "rowan")
	fsPush(t, st, "ada", "build-1", task.KindWork, 0, "")
	apBeats(t, client, "ada", 0, 0)
	d1 := fsEvent(t, client, "ada", life.EventDeliver, "", apBase)
	apBeats(t, client, "ada", 10, 120)
	res, move := apTick(t, st, 121000)
	if res.Status != "OK" || res.State != friend.StateWakeMissed {
		t.Fatalf("121 s: %+v", res)
	}
	if move == nil || move.Moved != 1 {
		t.Fatalf("121 s moved %+v", move)
	}
	marker := "[moved from ada: wake-missed]"
	if title := client.HGet(ctx, "task:build-1", "title").Val(); strings.Count(title, marker) != 1 {
		t.Fatalf("title %q, want one %q", title, marker)
	}
	return d1, client.XLen(ctx, friend.OutboxKey).Val()
}

// TestApplyRecoversFromWakeMissed is Stella's sequence through the real
// Functions: D1 missed (tasks move), D2 pending (no write, no second wake,
// no move), D2 answered (UP clears the classifier's own wake-missed).
func TestApplyRecoversFromWakeMissed(t *testing.T) {
	st, client := fsRedis(t)
	ctx := context.Background()
	_, w := apRecoverFixture(t, st, client)

	apBeats(t, client, "ada", 130, 300)
	d2id := fsEvent(t, client, "ada", life.EventDeliver, "", apBase+300000)
	apBeats(t, client, "ada", 310, 330)
	logLen := len(fsCapLog(t, client))
	res := apApply(t, st, "ada", 330000, nil)
	if res.Status != friend.ApplyPending {
		t.Fatalf("330 s: %+v, want PENDING", res)
	}
	if len(fsCapLog(t, client)) != logLen || client.XLen(ctx, friend.OutboxKey).Val() != w {
		t.Fatal("PENDING wrote cap:log or the outbox")
	}
	if fsState(client, "ada")["state"] != friend.StateWakeMissed {
		t.Fatalf("330 s state %v", fsState(client, "ada"))
	}
	moves, err := life.Redistribute(ctx, st, fsRoster, "reconciler", "tick-330")
	fsMust(t, err)
	if m := fsMoveOf(moves, "ada"); m != nil && m.Moved != 0 {
		t.Fatalf("330 s moved %+v", m)
	}

	fsEvent(t, client, "ada", life.EventTurnStart, d2id, apBase+360000)
	apBeats(t, client, "ada", 340, 360)
	res, move := apTick(t, st, 361000)
	if res.Status != "OK" || len(res.Values) < 2 || res.Values[1] != "up" {
		t.Fatalf("361 s: %+v, want OK up", res)
	}
	if client.Exists(ctx, friend.StateKey("ada")).Val() != 0 {
		t.Fatalf("361 s state %v, want absent", fsState(client, "ada"))
	}
	if client.XLen(ctx, friend.OutboxKey).Val() != w {
		t.Fatal("a duplicate wake")
	}
	if move != nil {
		t.Fatalf("361 s redistribute row for ada: %+v", move)
	}

	for _, tc := range []struct {
		name   string
		turnMS int64
	}{{"turn_at_120s", 420000}, {"turn_at_121s", 421000}} {
		t.Run(tc.name, func(t *testing.T) {
			st, client := fsRedis(t)
			ctx := context.Background()
			apRecoverFixture(t, st, client)
			apBeats(t, client, "ada", 130, 300)
			d2 := fsEvent(t, client, "ada", life.EventDeliver, "", apBase+300000)
			missedD2 := "life:ada:" + d2 + ":wake-missed"
			apBeats(t, client, "ada", 310, 420)
			if res := apApply(t, st, "ada", 330000, nil); res.Status != friend.ApplyPending {
				t.Fatalf("330 s: %+v", res)
			}
			if res := apApply(t, st, "ada", 390000, nil); res.Status != friend.ApplyPending {
				t.Fatalf("390 s: %+v", res)
			}
			if tc.turnMS == 420000 {
				fsEvent(t, client, "ada", life.EventTurnStart, d2, apBase+tc.turnMS)
				if res := apApply(t, st, "ada", 420000, nil); res.Status != "OK" || res.Values[1] != "up" {
					t.Fatalf("420 s: %+v, want OK up", res)
				}
				for _, m := range fsCapLog(t, client) {
					if m.Values["idem"] == missedD2 {
						t.Fatalf("wake-missed written for D2 answered at 120 s: %v", m.Values)
					}
				}
				if fsState(client, "ada")["idem"] == missedD2 {
					t.Fatal("wake-missed stored for D2")
				}
				return
			}
			if res := apApply(t, st, "ada", 420500, nil); res.Status != "OK" || res.State != friend.StateWakeMissed {
				t.Fatalf("420.5 s: %+v, want wake-missed", res)
			}
			if got := fsState(client, "ada")["idem"]; got != missedD2 {
				t.Fatalf("idem %q, want %q", got, missedD2)
			}
			fsEvent(t, client, "ada", life.EventTurnStart, d2, apBase+tc.turnMS)
			if res := apApply(t, st, "ada", 421000, nil); res.Status != "OK" || res.Values[1] != "up" {
				t.Fatalf("421 s: %+v, want OK up", res)
			}
			if client.Exists(ctx, friend.StateKey("ada")).Val() != 0 {
				t.Fatal("421 s: wake-missed not cleared")
			}
		})
	}
}

// TestSweepAppliesLifeOnlyWhenAdopted: the reconciler sweep classifies a
// friend only once its wake mode is declared (its deliver and turn
// producers are proven); before that its beats alone never block it.
func TestSweepAppliesLifeOnlyWhenAdopted(t *testing.T) {
	st, client := fsRedis(t)
	ctx := context.Background()
	fsFriends(t, client, "ada", "stella", "johnny", "rowan")
	now := time.Now().UnixMilli()
	for i := int64(10); i >= 1; i-- {
		fsEvent(t, client, "ada", life.EventBeat, "", now-i*10000)
	}
	l := &friend.Ladder{Store: st, Roster: fsRoster, Actor: "reconciler"}
	res, err := l.Sweep(ctx)
	fsMust(t, err)
	if len(res.Life) != 0 || fsState(client, "ada")["state"] == friend.StateOfflineModel {
		t.Fatalf("an undeclared friend was classified: %+v %v", res.Life, fsState(client, "ada"))
	}
	fsMust(t, client.HSet(ctx, friend.WakeModeKey("ada"), "mode", life.WakeModeScheduled).Err())
	res, err = l.Sweep(ctx)
	fsMust(t, err)
	if len(res.Life) != 1 || res.Life[0].State != friend.StateOfflineModel || fsState(client, "ada")["state"] != friend.StateOfflineModel {
		t.Fatalf("a declared friend with beats and no turn: %+v %v", res.Life, fsState(client, "ada"))
	}
}

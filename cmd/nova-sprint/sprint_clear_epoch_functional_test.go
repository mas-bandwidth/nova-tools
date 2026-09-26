//go:build functional

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

// TestSprintClearIsOneEpochIncrement is the DONE-WHEN of nova-tools#4238
// over the whole-table fixture (cards in every state, copies on every
// consumer), driving the verb's one call, ns_sprint_clear, on a store of
// its own: it returns in under 10 ms with one HINCRBY of sprint:epoch n
// and moves nothing; the next tick prints zeros everywhere except status
// and load; a writer still holding the old epoch (a Go ZADD into the old
// names, a bench's beat of its old copy) cannot make a cell non-zero; a
// new card pushed after the clear is dealt and shown; and the pit stop
// the clear found is kept and named. The verb's own receipt lines are
// TestSprintClearZerosBothTables's (serial: it clears the seat user).
func TestSprintClearIsOneEpochIncrement(t *testing.T) {
	t.Parallel()

	_, client := wstest.Start(t)
	ctx := context.Background()
	for _, cmd := range table.SprintFixture() {
		args := make([]any, len(cmd))
		for i, v := range cmd {
			args[i] = v
		}
		if err := client.Do(ctx, args...).Err(); err != nil {
			t.Fatalf("seed %v: %v", cmd, err)
		}
	}
	// the fixture's open sprint carries the pit stop; the clear finds it
	// through sprint:order like the table does
	if err := client.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "fix"}).Err(); err != nil {
		t.Fatal(err)
	}
	clear := func(force string) ([]any, time.Duration) {
		start := time.Now()
		reply, err := client.FCall(ctx, "ns_sprint_clear", nil, "rowan", "fresh run", force).Slice()
		if err != nil {
			t.Fatal(err)
		}
		return reply, time.Since(start)
	}
	stream, oldWorking := "swarm: cards", ws.KeyAt(0, "swarm: cards", "working")
	wasWorking := client.ZCard(ctx, oldWorking).Val()
	wasSpaceOK := client.ZCard(ctx, ws.ConsumerKeyAt(0, "bench:space", "ok")).Val()
	if wasWorking == 0 || wasSpaceOK == 0 {
		t.Fatalf("the fixture has no working cards (%d) or space ok copies (%d)", wasWorking, wasSpaceOK)
	}

	if reply, _ := clear("0"); len(reply) < 2 || reply[0] != "REFUSED" || !strings.HasPrefix(reply[1].(string), "INFLIGHT ") {
		t.Fatalf("cards in flight: %v", reply)
	}
	reply, took := clear("1")
	if took >= 10*time.Millisecond {
		t.Fatalf("the clear took %s, want under 10 ms", took)
	}
	words := make([]string, len(reply))
	for i, v := range reply {
		words[i] = v.(string)
	}
	// CLEARED streams cards copies consumers epoch pit sprint, then per stream
	if len(words) < 8 || words[0] != "CLEARED" || words[1] != "10" || words[5] != "1" || words[6] != "kept" || words[7] != "fix" {
		t.Fatalf("receipt: %v", words)
	}
	if !strings.Contains(" "+strings.Join(words[8:], " ")+" ", " swarm: cards 164 ") {
		t.Fatalf("the per-stream count of what became invisible: %v", words[8:])
	}
	// one increment, and nothing moved or deleted: the old epoch's sets and
	// the pit stop are as they were
	if v := client.HGet(ctx, ws.EpochKey, ws.EpochField).Val(); v != "1" {
		t.Fatalf("sprint:epoch n = %q, want 1", v)
	}
	if n := client.ZCard(ctx, oldWorking).Val(); n != wasWorking {
		t.Fatalf("the clear moved cards: %s %d -> %d", oldWorking, wasWorking, n)
	}
	if n := client.ZCard(ctx, ws.ConsumerKeyAt(0, "bench:space", "ok")).Val(); n != wasSpaceOK {
		t.Fatalf("the clear deleted copies: bench:space:cards:ok %d -> %d", wasSpaceOK, n)
	}
	if client.Exists(ctx, "s:fix:pitstop").Val() != 1 {
		t.Fatal("the clear dropped the pit stop")
	}
	receipt := client.HGetAll(ctx, ws.EpochKey).Val()
	if receipt["by"] != "rowan" || receipt["why"] != "fresh run" || receipt["from"] != "0" || receipt["pitstop"] != "kept" || receipt["pitstop_sprint"] != "fix" {
		t.Fatalf("the receipt on sprint:epoch: %v", receipt)
	}

	// a writer still holding the old epoch: a Go ZADD into the old names, a
	// bench's beat and end of a copy it was working under epoch 0
	client.ZAdd(ctx, ws.ConsumerKeyAt(0, "bench:space", "ok"), redis.Z{Score: 1, Member: "late~1"})
	client.ZAdd(ctx, ws.KeyAt(0, stream, "landed"), redis.Z{Score: 1, Member: "late"})
	if reply, _ := client.FCall(ctx, "ns_cm_beat", nil, "bench:hetzner", "hetzner-working~0").Slice(); len(reply) < 2 || reply[0] != "REFUSED" || !strings.Contains(reply[1].(string), "NOTWORKING hetzner-working~0 is not in bench:hetzner:1:cards:working") {
		t.Fatalf("an old copy's beat: %v, want REFUSED NOTWORKING under epoch 1", reply)
	}
	now := table.SprintFixtureNow() // the fixture beats are fresh at its now
	snap, err := table.NewSprintReader(client, table.SprintConfig{}).Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Epoch != 1 {
		t.Fatalf("the tick read epoch %d, want 1", snap.Epoch)
	}
	for _, r := range snap.Streams {
		if r.Total() != 0 {
			t.Fatalf("stream %q is not zero after the clear: %+v", r.Name, r)
		}
	}
	up, load := 0, 0
	for _, c := range snap.Consumers {
		if c.Ready != 0 || c.Working != 0 || c.OK != 0 || c.Fail != 0 {
			t.Fatalf("consumer %s is not zero after the clear: %+v", c.ID(), c)
		}
		if c.Up {
			up++
		}
		if c.Load != "-" {
			load++
		}
	}
	if up == 0 || load == 0 {
		t.Fatalf("status and load are the beat's, not the epoch's: up=%d load=%d", up, load)
	}
	if !snap.Pitstop {
		t.Fatal("the table lost the pit stop after the clear")
	}

	// a new card pushed after the clear is dealt and shown
	if err := client.HSet(ctx, "bench:hetzner:desired", "slots", "4").Err(); err != nil {
		t.Fatal(err)
	}
	if v, err := client.FCall(ctx, "ns_tcard_push", nil, "after-1", "ready", "rowan", "test", "stream", stream, "kind", "build", "ref", "r-1").Text(); err != nil || !strings.HasPrefix(v, "PUSHED ready") {
		t.Fatalf("push after the clear: %q %v", v, err)
	}
	if e := client.HGet(ctx, "task:after-1", "epoch").Val(); e != "1" {
		t.Fatalf("the new card carries epoch %q, want 1", e)
	}
	dealt, err := client.FCall(ctx, "ns_cm_deal", nil, "bench:hetzner", "rowan", "1", stream).Slice()
	if err != nil || len(dealt) < 2 || dealt[0] != "DEALT" || dealt[1] != "1" {
		t.Fatalf("deal after the clear: %v %v", dealt, err)
	}
	snap, err = table.NewSprintReader(client, table.SprintConfig{}).Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	var swarm table.StreamRow
	for _, r := range snap.Streams {
		if r.Name == stream {
			swarm = r
		}
	}
	var hetzner table.ConsumerRow
	for _, c := range snap.Consumers {
		if c.ID() == "bench:hetzner" {
			hetzner = c
		}
	}
	if swarm.Total() != 1 || hetzner.Ready != 1 || hetzner.Working != 0 {
		t.Fatalf("the new card is not shown: stream %+v hetzner %+v", swarm, hetzner)
	}

	// a second clear counts only the current epoch's work and moves to 2
	reply, _ = clear("1")
	if len(reply) < 6 || reply[0] != "CLEARED" || reply[2] != "1" || reply[3] != "1" || reply[5] != "2" {
		t.Fatalf("second clear: %v", reply)
	}
}

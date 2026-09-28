//go:build functional

package fn_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

// Probes never ride the consumer sets (nova-tools#4237), against the real
// library: a record naming a probe is refused by every move into
// <bench|friend>:<name>:cards:<set> before it writes, the one ZADD raises
// when a move gets past that, card fsck names and removes one found there,
// and the bench beat keeps the probe result fleet build wrote.

const probeStream = "swarm: cards"

func probeStore(t *testing.T) (string, *redis.Client, taskcard.Consumer) {
	t.Helper()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	b := taskcard.Consumer{Kind: "bench", Name: "hetzner"}
	c.SAdd(ctx, "benches", b.Name)
	c.HSet(ctx, b.DesiredKey(), "slots", "4")
	c.HSet(ctx, "bench:"+b.Name+":beat", "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	if err := taskcard.Enroll(ctx, c, b, true); err != nil {
		t.Fatal(err)
	}
	return addr, c, b
}

func pushPrimary(t *testing.T, c *redis.Client, id string, fields ...string) {
	t.Helper()
	fields = append(fields, "base", "dev", "base_sha", strings.Repeat("ab", 20), "paths", "internal/x.go",
		"done_when", "go test ./internal/x passes")
	if _, err := taskcard.Push(context.Background(), c, taskcard.PushRequest{ID: id, Where: "waiting",
		Stream: probeStream, Sprint: "probe-4237", Kind: "build", Title: "primary " + id,
		Repo: "mas-bandwidth/nova-tools", By: "rowan", Fields: fields}); err != nil {
		t.Fatalf("push %s: %v", id, err)
	}
}

// cells is the consumer's four sets, as the table counts them.
func cells(c *redis.Client, k taskcard.Consumer) [4]int64 {
	ctx := context.Background()
	var n [4]int64
	for i, col := range []string{"ready", "working", "ok", "fail"} {
		n[i] = c.ZCard(ctx, k.KeyAt(0, col)).Val()
	}
	return n
}

func TestProbeIsNeverDealtToAConsumer(t *testing.T) {
	t.Parallel()

	_, c, b := probeStore(t)
	ctx := context.Background()
	pushPrimary(t, c, "probe-hetzner-flash", "probe", "fleet")

	_, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: b, IDs: []string{"probe-hetzner-flash"}, By: "rowan"})
	if why, ok := taskcard.IsRefused(err); !ok || !strings.HasPrefix(why, "PROBE probe-hetzner-flash is a probe (probe=fleet)") {
		t.Fatalf("named deal of a probe: %v, want REFUSED PROBE", err)
	}
	dealt, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: b, N: 4, By: "rowan"})
	if err != nil || len(dealt) != 0 {
		t.Fatalf("deal of the oldest: %v %v, want nothing (the one primary is a probe)", dealt, err)
	}
	if got := cells(c, b); got != [4]int64{} {
		t.Fatalf("%s cells %v after the probe's deals, want all 0", b, got)
	}

	// the control: consumer work deals as before
	pushPrimary(t, c, "work-1")
	dealt, err = taskcard.Deal(ctx, c, taskcard.DealRequest{To: b, N: 4, By: "rowan"})
	if err != nil || len(dealt) != 1 || dealt[0].Primary != "work-1" {
		t.Fatalf("deal of consumer work: %v %v", dealt, err)
	}
	if got := cells(c, b); got != [4]int64{1, 0, 0, 0} {
		t.Fatalf("%s cells %v, want 1 ready", b, got)
	}
}

func TestProbeIsNeverOnAFriendQueue(t *testing.T) {
	t.Parallel()

	_, c, _ := probeStore(t)
	ctx := context.Background()
	c.SAdd(ctx, "friends", "rowan")
	_, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "probe-route-1", Friend: "rowan", Sprint: "probe-4237",
		Kind: "build", Title: "route probe", By: "rowan", Fields: []string{"probe", "route"}})
	if why, ok := taskcard.IsRefused(err); !ok || !strings.HasPrefix(why, "PROBE probe-route-1 is a probe (probe=route)") {
		t.Fatalf("push of a probe onto a friend queue: %v, want REFUSED PROBE", err)
	}
	if c.Exists(ctx, "task:probe-route-1").Val() != 0 {
		t.Fatal("the refused push wrote task:probe-route-1")
	}
	if got := cells(c, taskcard.Consumer{Kind: "friend", Name: "rowan"}); got != [4]int64{} {
		t.Fatalf("friend:rowan cells %v, want all 0", got)
	}
}

// TestProbeLastGuardRaises: a copy whose own record names a probe (written
// past the moves) cannot enter working: cm_zadd raises PROBE, and the
// error reaches the caller.
func TestProbeLastGuardRaises(t *testing.T) {
	t.Parallel()

	_, c, b := probeStore(t)
	ctx := context.Background()
	pushPrimary(t, c, "work-1")
	dealt, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: b, N: 1, By: "rowan"})
	if err != nil || len(dealt) != 1 {
		t.Fatalf("deal: %v %v", dealt, err)
	}
	c.HSet(ctx, "task:"+dealt[0].Copy, "probe", "lineup")
	_, err = taskcard.Work(ctx, c, b, "rowan", 1, false, dealt[0].Copy)
	if err == nil || !strings.Contains(err.Error(), "PROBE "+dealt[0].Copy+" is a probe (probe=lineup)") {
		t.Fatalf("work of a probe copy: %v, want the PROBE raise", err)
	}
	if n := c.ZCard(ctx, b.KeyAt(0, "working")).Val(); n != 0 {
		t.Fatalf("%s holds %d, want 0", b.KeyAt(0, "working"), n)
	}
}

// TestProbeFsckNamesAndRepairs: a probe already in a consumer set (the
// morning's leak) is card fsck drift, and --repair removes it.
func TestProbeFsckNamesAndRepairs(t *testing.T) {
	t.Parallel()

	_, c, b := probeStore(t)
	ctx := context.Background()
	c.HSet(ctx, "task:quack-hetzner-flash~1", "card", "copy", "primary", "quack-hetzner-flash",
		"consumer", b.String(), "where", "ok", "probe", "quack")
	c.ZAdd(ctx, b.KeyAt(0, "ok"), redis.Z{Score: 1, Member: "quack-hetzner-flash~1"})

	r, err := taskcard.FsckMoves(ctx, c, false)
	if err != nil || r.Drift != 1 || len(r.Lines) != 1 || !strings.HasPrefix(r.Lines[0], "probe "+b.KeyAt(0, "ok")+" quack-hetzner-flash~1") {
		t.Fatalf("fsck: %+v %v, want one probe line", r, err)
	}
	if r, err = taskcard.FsckMoves(ctx, c, true); err != nil || r.Fixed != 1 {
		t.Fatalf("repair: %+v %v", r, err)
	}
	if got := cells(c, b); got != [4]int64{} {
		t.Fatalf("%s cells %v after repair, want all 0", b, got)
	}
}

// TestBenchBeatKeepsTheProbe: the bench's own beat never blanks the probe
// result fleet build wrote on it.
func TestBenchBeatKeepsTheProbe(t *testing.T) {
	t.Parallel()

	addr, c, b := probeStore(t)
	ctx := context.Background()
	st, err := store.Open(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	beat := func() {
		t.Helper()
		if _, err := life.BenchBeat(ctx, st, life.BenchRequest{Bench: b.Name, Host: b.Name, Session: "s1"}); err != nil {
			t.Fatal(err)
		}
	}
	beat()
	const result = "OK v0.16.0-dev.c8178673 2026-09-26T15:40:00Z"
	c.HSet(ctx, "bench:"+b.Name+":beat", "probe", result)
	beat()
	if got := c.HGet(ctx, "bench:"+b.Name+":beat", "probe").Val(); got != result {
		t.Fatalf("beat probe after a beat = %q, want %q", got, result)
	}
}

// TestProbeGuardHoldsAtEpochOne (nova-tools#4238): after a sprint clear the
// consumer sets are <kind>:<name>:<e>:cards:<col>, and the probe guard of
// #4237 must recognise that name too: a probe is still refused by the deal,
// and cm_zadd still raises on a probe copy entering the epoch's working set.
func TestProbeGuardHoldsAtEpochOne(t *testing.T) {
	t.Parallel()

	_, c, b := probeStore(t)
	ctx := context.Background()
	if err := c.HSet(ctx, ws.EpochKey, ws.EpochField, "1").Err(); err != nil {
		t.Fatal(err)
	}
	pushPrimary(t, c, "probe-hetzner-flash", "probe", "fleet")
	_, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: b, IDs: []string{"probe-hetzner-flash"}, By: "rowan"})
	if why, ok := taskcard.IsRefused(err); !ok || !strings.HasPrefix(why, "PROBE probe-hetzner-flash is a probe (probe=fleet)") {
		t.Fatalf("named deal of a probe at epoch 1: %v, want REFUSED PROBE", err)
	}
	pushPrimary(t, c, "work-1")
	dealt, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: b, N: 4, By: "rowan"})
	if err != nil || len(dealt) != 1 || dealt[0].Primary != "work-1" {
		t.Fatalf("deal at epoch 1: %v %v, want the one consumer card", dealt, err)
	}
	if n := c.ZCard(ctx, b.KeyAt(1, "ready")).Val(); n != 1 {
		t.Fatalf("%s holds %d, want the copy", b.KeyAt(1, "ready"), n)
	}
	c.HSet(ctx, "task:"+dealt[0].Copy, "probe", "lineup")
	_, err = taskcard.Work(ctx, c, b, "rowan", 1, false, dealt[0].Copy)
	if err == nil || !strings.Contains(err.Error(), "PROBE "+dealt[0].Copy+" is a probe (probe=lineup)") {
		t.Fatalf("work of a probe copy at epoch 1: %v, want the PROBE raise", err)
	}
	if n := c.ZCard(ctx, b.KeyAt(1, "working")).Val(); n != 0 {
		t.Fatalf("%s holds %d, want 0", b.KeyAt(1, "working"), n)
	}
}

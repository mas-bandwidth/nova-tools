package ci

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/bus"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestBusFriendSeatReachesOnlyBus is the bus's ACL check (nova-tools #3865,
// rowan-new specs/bus-redis.md "Tests"): the friend seat's grant for the bus
// is bus.SeatRules, whose one key pattern is ~bus:*. Every key bus.lua names
// is under bus:, every bus verb runs under exactly those rules on a throwaway
// redis-server, and the same seat is refused a key outside bus:* and any
// other library function.
func TestBusFriendSeatReachesOnlyBus(t *testing.T) {
	var keys []string
	for _, tok := range strings.Fields(bus.SeatRules) {
		if strings.HasPrefix(tok, "~") || strings.HasPrefix(tok, "%") || tok == "allkeys" || tok == "&*" || tok == "+@all" {
			keys = append(keys, tok)
		}
	}
	if strings.Join(keys, " ") != "~bus:*" {
		t.Fatalf("bus.SeatRules key grants %v; want exactly ~bus:*", keys)
	}
	src := readFile(t, filepath.Join(repoRoot(t), "internal", "nsprint", "fn", "lua", "bus.lua"))
	for _, m := range regexp.MustCompile(`'([a-z_]+):`).FindAllStringSubmatch(src, -1) {
		if m[1] != "bus" {
			t.Errorf("bus.lua names the key root %s:, outside bus:*", m[1])
		}
	}

	ctx := context.Background()
	addr := testutil.Start(t)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	if err := fn.Load(ctx, admin); err != nil {
		t.Fatal(err)
	}
	args := []any{"ACL", "SETUSER", "friendseat", "reset", "on", ">seat-pw"}
	for _, tok := range strings.Fields(bus.SeatRules) {
		args = append(args, tok)
	}
	if err := admin.Do(ctx, args...).Err(); err != nil {
		t.Fatalf("ACL SETUSER friendseat %s: %v", bus.SeatRules, err)
	}
	seat := redis.NewClient(&redis.Options{Addr: addr, Username: "friendseat", Password: "seat-pw"})
	t.Cleanup(func() { _ = seat.Close() })

	id, err := bus.Post(ctx, seat, bus.Message{From: "emma", To: "rowan", Kind: "note", Subject: "acl"})
	if err != nil {
		t.Fatalf("post as the friend seat: %v", err)
	}
	if _, err := bus.Post(ctx, seat, bus.Message{From: "rowan", To: "all", Kind: "note", Subject: "acl all"}); err != nil {
		t.Fatalf("post to all as the friend seat: %v", err)
	}
	es, err := bus.Fetch(ctx, seat, "rowan", 20, true)
	if err != nil || len(es) != 2 {
		t.Fatalf("read as the friend seat: %v %v", es, err)
	}
	if _, err := bus.Pending(ctx, seat, "rowan"); err != nil {
		t.Fatalf("pending as the friend seat: %v", err)
	}
	if err := bus.Ack(ctx, seat, "rowan", es); err != nil {
		t.Fatalf("ack as the friend seat: %v", err)
	}
	if _, err := bus.Lookup(ctx, seat, "rowan", id); err != nil {
		t.Fatalf("reply lookup as the friend seat: %v", err)
	}
	if _, err := bus.Ls(ctx, seat); err != nil {
		t.Fatalf("ls as the friend seat: %v", err)
	}
	// tail: the seat blocks in XREADGROUP and is woken by a post; the emit
	// ends it. The give-up is a test's, never a product bound.
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tailed := make(chan error, 1)
	go func() {
		tailed <- bus.Tail(tctx, seat, "rowan", 100*time.Millisecond, func(bus.Entry) { cancel() })
	}()
	if _, err := bus.Post(ctx, seat, bus.Message{From: "stella", To: "rowan", Kind: "note", Subject: "tail"}); err != nil {
		t.Fatalf("post for tail: %v", err)
	}
	select {
	case err := <-tailed:
		if err != nil {
			t.Fatalf("tail as the friend seat: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("tail as the friend seat never delivered")
	}

	for _, refused := range [][]any{
		{"HGET", "s:x", "f"},
		{"XADD", "friend:emma:log", "*", "k", "v"},
		{"XADD", "sprint:x:moves", "*", "k", "v"},
		{"FCALL", "ns_pitstop_set", "0", "S", "emma", "why", "0", ""},
	} {
		if err := seat.Do(ctx, refused...).Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
			t.Errorf("friend seat ran %v: %v; want NOPERM", refused, err)
		}
	}
}

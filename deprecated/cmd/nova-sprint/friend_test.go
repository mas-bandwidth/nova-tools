//go:build functional

package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func friendFixture(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	return addr, client
}

func TestFriendVerbLsAndShow(t *testing.T) {
	t.Parallel()
	addr, client := friendFixture(t)
	ctx := context.Background()
	now := time.Now()

	// Seed friends
	client.SAdd(ctx, "friends", "f1", "f2")
	client.HSet(ctx, "friend:f1:desired", "slots", "4", "machine", "studio")
	client.HSet(ctx, "friend:f1:beat",
		"host", "studio",
		"harness", "nova-friend",
		"models", "pro-model",
		"credits", "$10",
		"at", strconv.FormatInt(now.Add(-4*time.Second).UnixMilli(), 10),
	)
	epoch, _ := ws.Epoch(ctx, client)
	client.ZAdd(ctx, ws.ConsumerKeyAt(epoch, "friend:f1", "working"), redis.Z{
		Score:  float64(now.Add(-60 * time.Second).UnixMilli()),
		Member: "task-01~1",
	})
	client.HSet(ctx, "task:task-01~1", "primary", "task-01", "stream", "ci", "leg", "work", "title", "Test task")

	client.HSet(ctx, "friend:f2:desired", "slots", "2")
	client.HSet(ctx, "friend:f2:beat", "at", strconv.FormatInt(now.Add(-120*time.Second).UnixMilli(), 10))

	t.Run("friend ls", func(t *testing.T) {
		code, stdout, stderr := runVerb(t, "friend", "ls", "--redis", addr)
		if code != 0 {
			t.Fatalf("friend ls: %d %q %q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "FRIEND") || !strings.Contains(stdout, "f1") || !strings.Contains(stdout, "f2") {
			t.Errorf("friend ls output missing expected lines:\n%s", stdout)
		}
		if !strings.Contains(stdout, "FRIENDS n=2 up=1 down=1 working=1") {
			t.Errorf("friend ls output missing summary line:\n%s", stdout)
		}
	})

	t.Run("friend show", func(t *testing.T) {
		code, stdout, stderr := runVerb(t, "friend", "show", "--redis", addr, "f1")
		if code != 0 {
			t.Fatalf("friend show f1: %d %q %q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "FRIEND f1") || !strings.Contains(stdout, "Status:       up") {
			t.Errorf("friend show output missing status:\n%s", stdout)
		}
		if !strings.Contains(stdout, "task-01~1") {
			t.Errorf("friend show output missing working copy:\n%s", stdout)
		}
	})

	t.Run("friend show unregistered", func(t *testing.T) {
		code, _, stderr := runVerb(t, "friend", "show", "--redis", addr, "f999")
		if code != 2 || !strings.Contains(stderr, "unregistered friend") {
			t.Errorf("friend show f999: code=%d, want 2; stderr=%q", code, stderr)
		}
	})
}

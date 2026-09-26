//go:build functional

package jev_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/jev"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestStellaSentinelDoesNotAskForWorkerTier is Stella's reproduction
// (2026-09-26, stella-working/sync-20260926/sentinel-model-classification-
// reproduction_test.go), copied as she wrote it: a stream's sentinel is a
// mechanical card, so jev sync queues no tier and no worktype ask for it
// (#4318; Rowan's ruling on #4412).
func TestStellaSentinelDoesNotAskForWorkerTier(t *testing.T) {
	t.Parallel()

	_, c := wstest.Start(t)
	ctx := context.Background()
	c.SAdd(ctx, "friends", "rowan")
	k := taskcard.Consumer{Kind: "bench", Name: "b"}
	c.SAdd(ctx, "benches", "b")
	c.HSet(ctx, k.DesiredKey(), "slots", "2")
	c.HSet(ctx, k.BeatKey(), "at", strconv.FormatInt(time.Now().UnixMilli(), 10))
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "nova-tools-4316", Where: "waiting", Stream: "swarm: cards",
		Sprint: "jev-s", Kind: "build", Ref: "nova-tools#4316", Origin: "issue:nova-tools#4316", Title: "the ledger",
		Repo: "mas-bandwidth/nova-tools", By: "rowan",
		Fields: []string{"base", "dev", "base_sha", strings.Repeat("a", 40), "paths", "internal/nsprint/jev",
			"done_when", "jev report prints agreement", "route", "pro", "type", "verb"}}); err != nil {
		t.Fatal(err)
	}
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: k, IDs: []string{"nova-tools-4316"}, By: "rowan"})
	if err != nil || len(d) != 1 {
		t.Fatalf("deal %v %v", d, err)
	}
	if _, err := taskcard.Work(ctx, c, k, "rowan", 0, false, d[0].Copy); err != nil {
		t.Fatal(err)
	}
	if e, err := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{d[0].Copy}, Why: "child exit 1", By: "b",
		Fields: []string{"exit", "1", "model", "kimi-k3"}}); err != nil || e[0].To != "review" {
		t.Fatalf("end --fail %v %v", e, err)
	}

	sync := func() jev.SyncResult {
		t.Helper()
		r, err := jev.Sync(ctx, c, 1000)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := sync()
	t.Logf("sync: %+v", r)
	pending := c.SMembers(ctx, jev.KeyPending).Val()
	for _, row := range pending {
		if strings.HasSuffix(row, ":sentinel") && (strings.HasPrefix(row, "tier ") || strings.HasPrefix(row, "worktype ")) {
			t.Errorf("mechanical sentinel was queued for worker-model classification: %s; pending=%v", row, pending)
		}
	}
}

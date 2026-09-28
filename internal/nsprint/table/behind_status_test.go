package table_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// TestConsumerTableBehindStatus (#4356 C): an up bench whose last fleet play
// stopped (bench:<b>:play result failed:<role>) prints behind: <role> in
// status, over paused; a bench whose play was ok prints up; a down bench
// still prints down; a friend never reads a play receipt.
func TestConsumerTableBehindStatus(t *testing.T) {
	t.Parallel()
	now := table.SprintFixtureNow()
	at := strconv.FormatInt(now.Add(-time.Second).UnixMilli(), 10)
	client, _ := consumerStore(t, [][]string{
		{"SADD", "friends", "emma"},
		{"SADD", "benches", "batman", "hetzner", "hulk", "space"},
		{"HSET", "friend:emma:beat", "at", at},
		{"HSET", "friend:emma:play", "result", "failed:tools"},
		{"HSET", "bench:batman:beat", "load1", "0.10", "at", at},
		{"HSET", "bench:batman:play", "result", "ok", "role", "tools"},
		{"HSET", "bench:hetzner:beat", "load1", "0.20", "at", strconv.FormatInt(now.Add(-2*time.Minute).UnixMilli(), 10)},
		{"HSET", "bench:hetzner:play", "result", "failed:facts"},
		{"HSET", "bench:hulk:beat", "load1", "0.30", "at", at},
		{"HSET", "bench:hulk:desired", "paused", "1"},
		{"HSET", "bench:hulk:play", "result", "failed:bench", "role", "bench"},
		{"HSET", "bench:space:beat", "load1", "0.40", "at", at},
	})
	snap, err := table.NewSprintReader(client, table.SprintConfig{}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range snap.Consumers {
		got[r.ID()] = r.Status()
	}
	want := map[string]string{"friend:emma": "up", "bench:batman": "up", "bench:hetzner": "down",
		"bench:hulk": "behind: bench", "bench:space": "up"}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s status %q, want %q", id, got[id], w)
		}
	}
	if block := consumerBlock(t, snap.Render(now)); !strings.Contains(block, "| behind: bench | 0.30") {
		t.Errorf("hulk's row does not say behind: bench:\n%s", block)
	}
}

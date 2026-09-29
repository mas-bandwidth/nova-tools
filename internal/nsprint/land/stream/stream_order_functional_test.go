//go:build functional

package stream

import (
	"context"
	"strings"
	"testing"
	"time"

	jevledger "github.com/mas-bandwidth/nova-tools/internal/nsprint/jev"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

func TestFunctionalMembersReadsPathsAndDeps(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()

	streamName := "func-stream"
	r := "owner/repo"

	_ = c.ZAdd(ctx, WSKeyAt(0, streamName, "merging"), redis.Z{Score: 10, Member: "task-10"}).Err()
	_ = c.HSet(ctx, "task:task-10",
		"where", "merging",
		"stream", streamName,
		"state", "merging",
		"pr", "owner/repo#10",
		"blocked_on", "dep-a,dep-b",
		"paths", "internal/pkg/a.go,internal/pkg/b.go",
	).Err()

	_ = c.HSet(ctx, PRKey(r, 10),
		"repo", r,
		"n", "10",
		"head", "1111222233334444555566667777888899990000",
		"base_sha", "aaaabbbbccccddddeeeeffff0000111122223333",
		"state", "open",
		"reads", "SCORE who=reviewer head=1111222233334444555566667777888899990000 score=9/10",
	).Err()

	ms, skips, err := Members(ctx, c, r, []string{streamName}, 8)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(skips) > 0 {
		t.Fatalf("unexpected skips: %v", skips)
	}
	if len(ms) != 1 {
		t.Fatalf("got %d members, want 1", len(ms))
	}
	m := ms[0]
	if m.Task != "task-10" || m.N != 10 {
		t.Errorf("m.Task=%q, m.N=%d", m.Task, m.N)
	}
	if m.BaseSHA != "aaaabbbbccccddddeeeeffff0000111122223333" {
		t.Errorf("m.BaseSHA=%q", m.BaseSHA)
	}
	if len(m.Paths) != 2 || m.Paths[0] != "internal/pkg/a.go" || m.Paths[1] != "internal/pkg/b.go" {
		t.Errorf("m.Paths=%v", m.Paths)
	}
	if len(m.Deps) != 2 || m.Deps[0] != "dep-a" || m.Deps[1] != "dep-b" {
		t.Errorf("m.Deps=%v", m.Deps)
	}
}

func TestFunctionalLandStreamOrderMissHandlingAndRecovery(t *testing.T) {
	t.Parallel()

	c := newRedis(t)
	ctx := context.Background()

	streamName := "stream-miss"
	r := "owner/repo"

	// Set up task-1 and task-2
	_ = c.SAdd(ctx, "ws:names", streamName).Err()
	_ = c.ZAddNX(ctx, "ws:order", redis.Z{Score: 1, Member: streamName}).Err()
	_ = c.HSet(ctx, "s:open-sprint", "status", "open").Err()
	_ = c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "open-sprint"}).Err()

	_ = c.ZAdd(ctx, WSKeyAt(0, streamName, "merging"), redis.Z{Score: 2, Member: "task-2"}).Err()
	_ = c.HSet(ctx, "task:task-2", "where", "merging", "stream", streamName, "pr", r+"#2", "paths", "pkg/a.go").Err()
	_ = c.HSet(ctx, "task:task-1", "where", "waiting", "stream", streamName, "paths", "pkg/a.go").Err()

	miss := OrderMiss{
		Member: Member{
			Task:   "task-2",
			Stream: streamName,
			N:      2,
			Paths:  []string{"pkg/a.go"},
		},
		Missing:      "task-1",
		MissingPaths: []string{"pkg/a.go"},
		Base:         "12345678",
		Why:          "ORDER: built on 12345678, missing task-1",
	}

	res := Result{
		OrderMisses: []OrderMiss{miss},
	}

	// 1. Process misses as LandStream does
	by := "stream-lander"
	for _, m := range res.OrderMisses {
		_, err := ws.Move(ctx, c, m.Member.Task, "review", by, m.Why)
		if err != nil {
			t.Fatalf("ws.Move to review: %v", err)
		}
		_ = c.RPush(ctx, LinesKey(r, m.Member.N), m.Why).Err()
		_ = c.XAdd(ctx, &redis.XAddArgs{
			Stream: "ws:order:misses",
			Values: map[string]any{
				"stream":        m.Member.Stream,
				"card":          m.Member.Task,
				"missing":       m.Missing,
				"why":           m.Why,
				"paths":         strings.Join(m.Member.Paths, ","),
				"missing_paths": strings.Join(m.MissingPaths, ","),
			},
		}).Err()
		_ = jevledger.Record(ctx, c, jevledger.Decision{
			Type:    jevledger.TypeOrder,
			Subject: m.Member.Task + ":" + m.Missing,
			State:   m.Why,
			Fields: map[string]string{
				"stream":  m.Member.Stream,
				"card":    m.Member.Task,
				"missing": m.Missing,
			},
		})
	}

	// Verify task-2 moved to review
	w := c.HGet(ctx, "task:task-2", "where").Val()
	if w != "review" {
		t.Errorf("task:task-2 where=%q, want review", w)
	}

	// Verify lines key
	lines := c.LRange(ctx, LinesKey(r, 2), 0, -1).Val()
	if len(lines) != 1 || lines[0] != miss.Why {
		t.Errorf("lines=%v, want [%s]", lines, miss.Why)
	}

	// Verify ws:order:misses
	misses, err := ws.ReadOrderMisses(ctx, c, streamName)
	if err != nil || len(misses) != 1 {
		t.Fatalf("ReadOrderMisses err=%v, len=%d", err, len(misses))
	}
	if misses[0].Card != "task-2" || misses[0].Missing != "task-1" {
		t.Errorf("miss: %+v", misses[0])
	}

	// Verify Jev row
	jevRow := c.HGetAll(ctx, jevledger.RowKey(jevledger.TypeOrder, "task-2:task-1")).Val()
	if len(jevRow) == 0 || jevRow["state"] != miss.Why {
		t.Errorf("jev row: %v", jevRow)
	}

	// Verify SprintCounts shows order-miss
	reader := &ws.CountsReader{}
	counts, err := reader.Read(ctx, c, time.Now())
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.OrderMisses != 1 {
		t.Errorf("counts.OrderMisses=%d, want 1", counts.OrderMisses)
	}
	if !strings.Contains(counts.Header(), ", order-miss 1") {
		t.Errorf("header %q missing ', order-miss 1'", counts.Header())
	}

	// Verify order read back by OrderStream:
	// Put task-1 and task-2 in waiting.
	// Normally task-2 with issue 2 vs task-1 issue 1 would be ordered 1 then 2.
	// If task-2 has lower issue or tie, learned miss forces task-1 before task-2!
	_ = c.ZAdd(ctx, WSKeyAt(0, streamName, "waiting"), redis.Z{Score: 1, Member: "task-2"}, redis.Z{Score: 2, Member: "task-1"}).Err()
	_ = c.HSet(ctx, "task:task-1", "ref", "r#20", "paths", "pkg/a.go").Err() // higher issue
	_ = c.HSet(ctx, "task:task-2", "ref", "r#10", "paths", "pkg/a.go").Err() // lower issue
	if err := ws.OrderStream(ctx, c, streamName); err != nil {
		t.Fatalf("OrderStream: %v", err)
	}
	score1 := c.ZScore(ctx, WSKeyAt(0, streamName, "waiting"), "task-1").Val()
	score2 := c.ZScore(ctx, WSKeyAt(0, streamName, "waiting"), "task-2").Val()
	if score1 >= score2 {
		t.Errorf("task-1 score=%f should be < task-2 score=%f due to learned miss", score1, score2)
	}
}

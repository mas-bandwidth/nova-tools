package fold_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fold"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func startRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestFoldEstErrorPerOwner(t *testing.T) {
	client := startRedis(t)
	ctx := context.Background()
	sprint := "sprint-fold-fx"

	type taskData struct {
		id        string
		owner     string
		est       string
		claimedAt string
		closedAt  string
		state     string
		evidence  string
	}

	tasks := []taskData{
		// johnny has A (est 30, 40 min), B (est 45, 65 min) and C (no est, 20 min).
		{id: "A", owner: "johnny", est: "30", claimedAt: "1000000", closedAt: "3400000", state: "closed", evidence: "ev-A"},
		{id: "B", owner: "johnny", est: "45", claimedAt: "1000000", closedAt: "4900000", state: "closed", evidence: "ev-B"},
		{id: "C", owner: "johnny", est: "", claimedAt: "1000000", closedAt: "2200000", state: "closed", evidence: "ev-C"},
		// stella has D (est 60, 50 min).
		{id: "D", owner: "stella", est: "60", claimedAt: "1000000", closedAt: "4000000", state: "closed", evidence: "ev-D"},
		// emma has E (no est, 15 min) and G (est 20, closed with no claimed_at).
		{id: "E", owner: "emma", est: "", claimedAt: "1000000", closedAt: "1900000", state: "closed", evidence: "ev-E"},
		{id: "G", owner: "emma", est: "20", claimedAt: "", closedAt: "2200000", state: "closed", evidence: "ev-G"},
	}

	for _, td := range tasks {
		fields := map[string]any{
			"owner":      td.owner,
			"est":        td.est,
			"claimed_at": td.claimedAt,
			"closed_at":  td.closedAt,
			"state":      td.state,
			"evidence":   td.evidence,
		}
		if err := client.HSet(ctx, "task:"+td.id, fields).Err(); err != nil {
			t.Fatalf("hset task %s: %v", td.id, err)
		}
		if err := client.SAdd(ctx, "s:"+sprint+":idx:task:closed", td.id).Err(); err != nil {
			t.Fatalf("sadd closed %s: %v", td.id, err)
		}
	}

	lines, err := fold.EstimateLines(ctx, client, sprint, "")
	if err != nil {
		t.Fatalf("EstimateLines: %v", err)
	}

	wantLines := []string{
		"EST owner=emma n=0 est=0 actual=0 error=0 pct=- unest=1 unmeasured=1",
		"EST owner=johnny n=2 est=75 actual=105 error=+30 pct=+40 unest=1 unmeasured=0",
		"EST owner=stella n=1 est=60 actual=50 error=-10 pct=-17 unest=0 unmeasured=0",
		"EST all n=3 est=135 actual=155 error=+20 pct=+15 unest=2 unmeasured=1",
	}

	if len(lines) != len(wantLines) {
		t.Fatalf("got %d lines, want %d:\ngot:\n%s\nwant:\n%s",
			len(lines), len(wantLines), strings.Join(lines, "\n"), strings.Join(wantLines, "\n"))
	}

	for i, want := range wantLines {
		if lines[i] != want {
			t.Errorf("line %d = %q; want %q", i, lines[i], want)
		}
	}
}

func TestFoldPctDashWhenNoEstimate(t *testing.T) {
	client := startRedis(t)
	ctx := context.Background()

	// 1. Emma line pinned: when est=0 (n=0), prints est=0 actual=0 error=0 pct=-
	sprintEmma := "sprint-emma-pin"
	tasks := []struct {
		id        string
		owner     string
		est       string
		claimedAt string
		closedAt  string
	}{
		{id: "E", owner: "emma", est: "", claimedAt: "1000000", closedAt: "1900000"},
		{id: "G", owner: "emma", est: "20", claimedAt: "", closedAt: "2200000"},
	}
	for _, td := range tasks {
		fields := map[string]any{
			"owner":      td.owner,
			"est":        td.est,
			"claimed_at": td.claimedAt,
			"closed_at":  td.closedAt,
			"state":      "closed",
			"evidence":   "ev",
		}
		if err := client.HSet(ctx, "task:"+td.id, fields).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.SAdd(ctx, "s:"+sprintEmma+":idx:task:closed", td.id).Err(); err != nil {
			t.Fatal(err)
		}
	}
	emmaLines, err := fold.EstimateLines(ctx, client, sprintEmma, "emma")
	if err != nil {
		t.Fatalf("EstimateLines emma: %v", err)
	}
	wantEmma := "EST owner=emma n=0 est=0 actual=0 error=0 pct=- unest=1 unmeasured=1"
	if len(emmaLines) != 1 || emmaLines[0] != wantEmma {
		t.Fatalf("emma line = %v; want [%s]", emmaLines, wantEmma)
	}

	// 2. Sprint with no closed tasks at all: prints EST all n=0 est=0 actual=0 error=0 pct=-
	sprintEmpty := "sprint-empty"
	emptyLines, err := fold.EstimateLines(ctx, client, sprintEmpty, "")
	if err != nil {
		t.Fatalf("EstimateLines empty: %v", err)
	}
	if len(emptyLines) != 1 {
		t.Fatalf("got %d lines for empty sprint; want 1", len(emptyLines))
	}
	wantEmptyPrefix := "EST all n=0 est=0 actual=0 error=0 pct=-"
	if !strings.HasPrefix(emptyLines[0], wantEmptyPrefix) {
		t.Fatalf("empty sprint line = %q; want prefix %q", emptyLines[0], wantEmptyPrefix)
	}

	// 3. Hand-written stored est=0 lands in unest and not in n
	sprintZero := "sprint-handwritten-zero"
	td := map[string]any{
		"owner":      "rowan",
		"est":        "0",
		"claimed_at": "1000000",
		"closed_at":  "2200000",
		"state":      "closed",
		"evidence":   "ev",
	}
	if err := client.HSet(ctx, "task:t-zero", td).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.SAdd(ctx, "s:"+sprintZero+":idx:task:closed", "t-zero").Err(); err != nil {
		t.Fatal(err)
	}
	zeroLines, err := fold.EstimateLines(ctx, client, sprintZero, "rowan")
	if err != nil {
		t.Fatalf("EstimateLines handwritten zero: %v", err)
	}
	wantZero := "EST owner=rowan n=0 est=0 actual=0 error=0 pct=- unest=1 unmeasured=0"
	if len(zeroLines) != 1 || zeroLines[0] != wantZero {
		t.Fatalf("zero est line = %v; want [%s]", zeroLines, wantZero)
	}

	// 4. Friend with no closed tasks prints n=0 est=0 actual=0 error=0 pct=-
	noTaskLines, err := fold.EstimateLines(ctx, client, sprintEmma, "nobody")
	if err != nil {
		t.Fatalf("EstimateLines nobody: %v", err)
	}
	wantNobody := "EST owner=nobody n=0 est=0 actual=0 error=0 pct=- unest=0 unmeasured=0"
	if len(noTaskLines) != 1 || noTaskLines[0] != wantNobody {
		t.Fatalf("nobody line = %v; want [%s]", noTaskLines, wantNobody)
	}
}

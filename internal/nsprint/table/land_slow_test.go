package table_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

func TestLandSlowLine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		slow table.LandSlow
		want string
	}{
		{
			slow: table.LandSlow{
				Stream: "quack",
				Word:   "LAND-SLOW",
				Oldest: "q1",
				Age:    11 * time.Minute,
				Max:    10 * time.Minute,
			},
			want: "LAND-SLOW quack oldest=q1 age=11m0s max=10m0s",
		},
		{
			slow: table.LandSlow{
				Stream: "swarm: cards",
				Word:   "LAND-SLOW",
				Oldest: "t1",
				Age:    12*time.Minute + 30*time.Second,
				Max:    10 * time.Minute,
			},
			want: `LAND-SLOW swarm:\x20cards oldest=t1 age=12m30s max=10m0s`,
		},
		{
			slow: table.LandSlow{
				Stream: "landing: streams + lander",
				Word:   "LAND-WALL",
				Oldest: "l1",
				Age:    35 * time.Minute,
				Max:    30 * time.Minute,
			},
			want: `LAND-WALL landing:\x20streams\x20+\x20lander oldest=l1 age=35m0s max=30m0s`,
		},
	}

	for _, tc := range cases {
		if got := tc.slow.Line(); got != tc.want {
			t.Errorf("Line() = %q, want %q", got, tc.want)
		}
	}
}

func TestParseLandSlow(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1700000660000)

	// Empty hash
	if _, ok := table.ParseLandSlow("s1", nil, now); ok {
		t.Error("expected ok=false for nil hash")
	}
	if _, ok := table.ParseLandSlow("s1", map[string]string{}, now); ok {
		t.Error("expected ok=false for empty hash")
	}

	// Unknown word
	if _, ok := table.ParseLandSlow("s1", map[string]string{"word": "UNKNOWN"}, now); ok {
		t.Error("expected ok=false for unknown word")
	}

	// Standard LAND-SLOW
	hSlow := map[string]string{
		"word":      "LAND-SLOW",
		"oldest":    "t1",
		"oldest_at": "1700000000000",
		"age_ms":    "660000",
		"stalled":   "0",
		"at":        "1700000660000",
	}
	slow, ok := table.ParseLandSlow("swarm: cards", hSlow, now)
	if !ok {
		t.Fatal("expected ok=true for LAND-SLOW")
	}
	if slow.Stream != "swarm: cards" || slow.Word != "LAND-SLOW" || slow.Oldest != "t1" {
		t.Errorf("slow fields mismatch: %+v", slow)
	}
	if slow.Age != 11*time.Minute {
		t.Errorf("slow.Age = %v, want 11m0s", slow.Age)
	}
	if slow.Max != table.DefaultLandSlow {
		t.Errorf("slow.Max = %v, want %v", slow.Max, table.DefaultLandSlow)
	}
	if slow.Stalled {
		t.Error("slow.Stalled = true, want false")
	}

	// Standard LAND-WALL
	hWall := map[string]string{
		"word":      "LAND-WALL",
		"oldest":    "t2",
		"oldest_at": "1700000000000",
		"age_ms":    "1860000",
		"stalled":   "1",
		"at":        "1700000660000",
	}
	wall, ok := table.ParseLandSlow("quack", hWall, time.UnixMilli(1700001860000))
	if !ok {
		t.Fatal("expected ok=true for LAND-WALL")
	}
	if wall.Word != "LAND-WALL" || wall.Max != table.DefaultLandWall || !wall.Stalled {
		t.Errorf("wall fields mismatch: %+v", wall)
	}
	if wall.Age != 31*time.Minute {
		t.Errorf("wall.Age = %v, want 31m0s", wall.Age)
	}

	// Custom max_ms
	hCustom := map[string]string{
		"word":    "LAND-SLOW",
		"oldest":  "t3",
		"age_ms":  "120000",
		"max_ms":  "60000",
		"stalled": "0",
	}
	custom, ok := table.ParseLandSlow("quack", hCustom, time.Time{})
	if !ok || custom.Max != time.Minute || custom.Age != 2*time.Minute {
		t.Errorf("custom max_ms mismatch: %+v ok=%v", custom, ok)
	}
}

func TestSprintTableLandSlowPipelineAndRender(t *testing.T) {
	t.Parallel()

	client, _, log := sprintStore(t)
	ctx := context.Background()
	now := table.SprintFixtureNow()

	// Prime the reader with the fixture
	cfg := table.SprintFixtureConfig()
	r := table.NewSprintReader(client, cfg)
	if _, err := r.Read(ctx, now); err != nil {
		t.Fatal(err)
	}

	// Add slow records in Redis:
	// 1. "swarm: cards" has LAND-SLOW
	// 2. "landing: streams + lander" has LAND-WALL
	client.HSet(ctx, table.LandSlowKey("swarm: cards"),
		"word", "LAND-SLOW",
		"oldest", "t1",
		"oldest_at", strconv.FormatInt(now.Add(-11*time.Minute).UnixMilli(), 10),
		"age_ms", "660000",
		"stalled", "0",
		"at", strconv.FormatInt(now.UnixMilli(), 10),
	)
	client.HSet(ctx, table.LandSlowKey("landing: streams + lander"),
		"word", "LAND-WALL",
		"oldest", "t2",
		"oldest_at", strconv.FormatInt(now.Add(-35*time.Minute).UnixMilli(), 10),
		"age_ms", "2100000",
		"stalled", "1",
		"at", strconv.FormatInt(now.UnixMilli(), 10),
	)

	log.reset()
	snap, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}

	// Must preserve the one pipelined round trip in steady state
	names, trips := log.reset()
	if snap.RoundTrips != 1 || trips != 1 {
		t.Fatalf("steady tick with slow streams: RoundTrips=%d trips=%d; want 1 and 1", snap.RoundTrips, trips)
	}
	for _, n := range names {
		if n == "KEYS" || n == "SCAN" {
			t.Fatalf("the tick sent %s: %v", n, names)
		}
	}

	// Verify LandSlow records in snap
	if len(snap.LandSlow) != 2 {
		t.Fatalf("snap.LandSlow has %d records, want 2", len(snap.LandSlow))
	}
	if snap.LandSlow[0].Stream != "swarm: cards" || snap.LandSlow[0].Word != "LAND-SLOW" || snap.LandSlow[0].Oldest != "t1" {
		t.Errorf("LandSlow[0] = %+v", snap.LandSlow[0])
	}
	if snap.LandSlow[1].Stream != "landing: streams + lander" || snap.LandSlow[1].Word != "LAND-WALL" || snap.LandSlow[1].Oldest != "t2" {
		t.Errorf("LandSlow[1] = %+v", snap.LandSlow[1])
	}

	// Verify Streams[i].Slow field
	foundSlow := 0
	for _, s := range snap.Streams {
		if s.Slow != nil {
			foundSlow++
			if s.Name != "swarm: cards" && s.Name != "landing: streams + lander" {
				t.Errorf("unexpected slow stream %s", s.Name)
			}
		}
	}
	if foundSlow != 2 {
		t.Errorf("found %d streams with Slow != nil, want 2", foundSlow)
	}

	// Verify rendered output
	out := snap.Render(now)
	wantSlow1 := `LAND-SLOW swarm:\x20cards oldest=t1 age=11m0s max=10m0s` + "\n"
	wantSlow2 := `LAND-WALL landing:\x20streams\x20+\x20lander oldest=t2 age=35m0s max=30m0s` + "\n"

	if !strings.Contains(out, wantSlow1) {
		t.Errorf("output missing %q:\n%s", wantSlow1, out)
	}
	if !strings.Contains(out, wantSlow2) {
		t.Errorf("output missing %q:\n%s", wantSlow2, out)
	}

	// Verify position: the lines must appear under the streams table total and before the worker table
	streamsTotalIdx := strings.Index(out, "total                     |     559 |")
	slow1Idx := strings.Index(out, wantSlow1)
	slow2Idx := strings.Index(out, wantSlow2)
	workerTableIdx := strings.Index(out, "worker                    | ready |")

	if streamsTotalIdx == -1 || slow1Idx == -1 || slow2Idx == -1 || workerTableIdx == -1 {
		t.Fatalf("could not find all sections in output:\n%s", out)
	}

	if !(streamsTotalIdx < slow1Idx && slow1Idx < slow2Idx && slow2Idx < workerTableIdx) {
		t.Errorf("sections out of order: total=%d, slow1=%d, slow2=%d, worker=%d",
			streamsTotalIdx, slow1Idx, slow2Idx, workerTableIdx)
	}

	// When slow records are deleted, next tick prints no slow lines
	client.Del(ctx, table.LandSlowKey("swarm: cards"), table.LandSlowKey("landing: streams + lander"))
	snap2, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap2.LandSlow) != 0 {
		t.Fatalf("expected 0 LandSlow records after deletion, got %d", len(snap2.LandSlow))
	}
	out2 := snap2.Render(now)
	if strings.Contains(out2, "LAND-SLOW") || strings.Contains(out2, "LAND-WALL") {
		t.Errorf("output after deletion still contains LAND-SLOW / LAND-WALL:\n%s", out2)
	}
}

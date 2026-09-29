//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// TestDealStatusCLI verifies that `nova-sprint deal status` works via CLI,
// supports list mode and single-bench lookup mode, and reports NONE enrolled for enrolled benches.
func TestDealStatusCLI(t *testing.T) {
	t.Parallel()

	addr, c := silentRedis(t)
	ctx := context.Background()
	const S = "deal-cli-sprint"

	c.SAdd(ctx, "benches", "batman", "hulk")
	c.Set(ctx, "bench:batman:beat", "1", 0)
	c.HSet(ctx, "bench:batman:desired", "slots", "4", "paused", "0")
	c.SAdd(ctx, "consumers", "bench:batman")

	c.Set(ctx, "bench:hulk:beat", "1", 0)
	c.HSet(ctx, "bench:hulk:desired", "slots", "2", "paused", "1")

	c.ZAdd(ctx, ws.SprintListAt(0, S, "pool"), redis.Z{Score: 1, Member: "p1"})

	// List mode
	code, out, errOut := runCLI("deal", "status", "--redis", addr, "--sprint", S)
	if code != 0 {
		t.Fatalf("deal status list mode failed (%d): %s", code, errOut)
	}
	if !strings.Contains(out, "bench batman verdict=enrolled NONE enrolled free=4 ready=0 working=0 queue=0 ssh=- ssh_age=-") {
		t.Errorf("list mode out missing batman enrolled line: %s", out)
	}
	if !strings.Contains(out, "bench hulk verdict=paused NONE paused free=2 ready=0 working=0 queue=0 ssh=- ssh_age=-") {
		t.Errorf("list mode out missing hulk paused line: %s", out)
	}
	if !strings.Contains(out, "sprint "+S+" pool=1 waiting=0 dealt=0 last_deal_age=-") {
		t.Errorf("list mode out missing sprint line: %s", out)
	}

	// Lookup mode
	code, out, errOut = runCLI("deal", "status", "--redis", addr, "--sprint", S, "--bench", "batman")
	if code != 0 {
		t.Fatalf("deal status lookup mode failed (%d): %s", code, errOut)
	}
	if !strings.Contains(out, "bench batman verdict=enrolled NONE enrolled free=4 ready=0 working=0 queue=0 ssh=- ssh_age=-") {
		t.Errorf("lookup mode out missing batman line: %s", out)
	}

	// Usage error
	code, _, errOut = runCLI("deal", "status", "--redis", addr)
	if code != 2 || !strings.Contains(errOut, "--sprint is required") {
		t.Errorf("missing sprint code=%d errOut=%s", code, errOut)
	}
}

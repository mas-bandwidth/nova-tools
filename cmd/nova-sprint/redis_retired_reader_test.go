package main

// Reader reproduction (rowan-opus, cold read of #4399 at 98eec3cdc): the
// redis and redis-cli verbs swallow Parse's RetiredError, so `redis --store
// <addr> PING` prints a hand "wants ..." tail, not the corrected whole line.

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

func TestRedisRetiredSpellingPrintsTheCorrectedLine(t *testing.T) {
	t.Parallel()
	var out, errOut strings.Builder
	code := redisRaw(context.Background(), &seatcred.Selection{}, func(string) string { return "" },
		[]string{"--store", "127.0.0.1:1", "PING"}, &out, &errOut)
	want := "run: nova-sprint redis --redis 127.0.0.1:1 PING"
	if code != 2 || !strings.Contains(errOut.String(), want) {
		t.Fatalf("redis --store: exit %d, stderr %q; want exit 2 and %q", code, errOut.String(), want)
	}
}

// TestRedisCLIRetiredSpellingPrintsTheCorrectedLine: redis-cli's flags end
// at "--"; the corrected whole line keeps the command after it.
func TestRedisCLIRetiredSpellingPrintsTheCorrectedLine(t *testing.T) {
	t.Parallel()
	code, _, errOut := runSprint("redis-cli", "--store", "127.0.0.1:1", "--", "PING")
	want := "run: nova-sprint redis-cli --redis 127.0.0.1:1 -- PING"
	if code != 2 || !strings.Contains(errOut, want) {
		t.Fatalf("redis-cli --store: exit %d, stderr %q; want exit 2 and %q", code, errOut, want)
	}
}

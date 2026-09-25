package life_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
)

func TestSeatNeutralWakeHasNoTTL(t *testing.T) {
	t.Parallel()

	st, client := rdRedis(t)
	ctx := context.Background()
	if err := life.Wake(ctx, st, "g", "test", "g", ""); err != nil {
		t.Fatal(err)
	}
	if ttl := client.PTTL(ctx, "friend:g:wake").Val(); ttl != -1 {
		t.Fatalf("wake TTL = %v, want none", ttl)
	}
}

func TestSeatNeutralStaleWakeDiscarded(t *testing.T) {
	t.Parallel()

	st, client := rdRedis(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	for _, tc := range []struct {
		name, value string
		stale       bool
	}{
		{"old", strconv.FormatInt(now-600001, 10) + ":x", true},
		{"malformed", "not-a-time:x", true},
		{"fresh", strconv.FormatInt(now-1000, 10) + ":x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := client.XLen(ctx, "cap:log").Val()
			if err := client.LPush(ctx, "friend:g:wake", tc.value).Err(); err != nil {
				t.Fatal(err)
			}
			got, err := life.PollWake(ctx, st, "g")
			if err != nil {
				t.Fatal(err)
			}
			if tc.stale && got != "" {
				t.Fatalf("stale wake returned %q", got)
			}
			if !tc.stale && got != tc.value {
				t.Fatalf("fresh wake = %q", got)
			}
			entries, err := client.XRangeN(ctx, "cap:log", "-", "+", before+1).Result()
			if err != nil || len(entries) != int(before+1) {
				t.Fatalf("cap log: %v %d", err, len(entries))
			}
			last := entries[len(entries)-1].Values
			_, hasStale := last["stale"]
			if hasStale != tc.stale || fmt.Sprint(last["kind"]) != "friend-wake-consumed" || !strings.Contains(fmt.Sprint(last["subject"]), "g") {
				t.Fatalf("receipt %v stale=%t", last, tc.stale)
			}
		})
	}
}

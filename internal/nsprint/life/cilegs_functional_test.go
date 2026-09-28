//go:build functional

package life_test

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
)

// TestBenchBeatCarriesCILegs (nova-tools#4293, in Redis): the beat writes
// the CI leg count as ci every beat, a number or empty when unmeasured, so
// the deal reads the bench's legs where it reads its slots.
func TestBenchBeatCarriesCILegs(t *testing.T) {
	t.Parallel()
	st, client := rdRedis(t)
	ctx := context.Background()
	req := life.BenchRequest{Bench: "ci-legs", Host: "ci-legs", Session: "s1", Actor: "bench", CI: "4"}
	if res, err := life.BenchBeat(ctx, st, req); err != nil || !res.Accepted {
		t.Fatalf("beat %+v %v", res, err)
	}
	if got := client.HGet(ctx, "bench:ci-legs:beat", "ci").Val(); got != "4" {
		t.Fatalf("beat ci = %q, want 4", got)
	}
	req.CI = ""
	if _, err := life.BenchBeat(ctx, st, req); err != nil {
		t.Fatal(err)
	}
	if got, err := client.HGet(ctx, "bench:ci-legs:beat", "ci").Result(); err != nil || got != "" {
		t.Fatalf("unmeasured beat ci = %q %v, want the field present and empty", got, err)
	}
}

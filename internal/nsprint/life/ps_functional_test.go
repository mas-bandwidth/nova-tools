//go:build functional

package life_test

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
)

// TestBenchBeatCarriesTheProcessSample (#4338): ns_bench_beat writes the
// request's PS as the beat's ps field; a beat with no PS (a caller that
// predates it) leaves the last one in place. The CI count (#4365, args[21])
// and ps (args[22]) land in their own fields.
func TestBenchBeatCarriesTheProcessSample(t *testing.T) {
	t.Parallel()
	st, client := rdRedis(t)
	ctx := context.Background()
	sample := fleet.PSSample{At: 1_800_000_000, Units: []fleet.PSUnit{{Name: "com.nova.jev-loop", State: fleet.UnitUndeclared}}, UnitN: 1}.Encode()

	for i, ps := range []string{sample, ""} {
		res, err := life.BenchBeat(ctx, st, life.BenchRequest{Bench: "b1", Session: "s1", Actor: "bench", CI: "2", PS: ps})
		if err != nil || !res.Accepted {
			t.Fatalf("beat %d: %+v %v", i, res, err)
		}
		got, err := client.HMGet(ctx, "bench:b1:beat", "ps", "ci").Result()
		if err != nil || len(got) != 2 || got[0] != sample || got[1] != "2" {
			t.Fatalf("beat %d: ps, ci = %q (%v), want %q, \"2\"", i, got, err, sample)
		}
	}
}

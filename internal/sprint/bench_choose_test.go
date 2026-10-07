package sprint

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestTheBenchIsTheLeastLoadedMachineNotAName(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	benches := []GateBench{
		{Name: "bench-a", Bench: true, LoadKnown: true, Cores: 64, Load1: 164, GoProcesses: 31, At: now},
		{Name: "bench-b", Bench: true, LoadKnown: true, Cores: 32, Load1: 1, At: now},
	}
	got, err := ChooseGateBench(now, benches, 0)
	require.NoError(t, err)
	assert.Equal(t, "bench-b", got.Host, "the over-cap first bench cannot win by its name or array order")
	assert.Contains(t, got.Reason, "load 1.0 of 32 cores")
	assert.Contains(t, got.Reason, "bench-a")
}

func TestGateBenchSelectionSkipsUnknownFactsAndHonorsItsCap(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	base := []GateBench{
		{Name: "bench-a", Bench: true, LoadKnown: true, Cores: 2, Load1: 2, At: now},
		{Name: "bench-b", Bench: true, LoadKnown: true, Cores: 32, Load1: 4, At: now},
	}
	for _, tc := range []struct {
		name, want string
		factor     float64
		change     func([]GateBench)
	}{
		{name: "normalize by cores", want: "bench-b"},
		{name: "tie by name", want: "bench-a", change: func(b []GateBench) { b[1].Load1 = 32 }},
		{name: "declared bench only", want: "bench-a", change: func(b []GateBench) { b[1].Bench = false }},
		{name: "stale beat", want: "bench-a", change: func(b []GateBench) { b[1].At = now.Add(-BeatDeadline*MissedBeatsDown - time.Second) }},
		{name: "future beat", want: "bench-a", change: func(b []GateBench) { b[1].At = now.Add(time.Second) }},
		{name: "unknown load", want: "bench-a", change: func(b []GateBench) { b[1].LoadKnown = false }},
		{name: "unknown cores", want: "bench-a", change: func(b []GateBench) { b[1].Cores = 0 }},
		{name: "configured cap", factor: 0.1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := append([]GateBench(nil), base...)
			if tc.change != nil {
				tc.change(b)
			}
			before := append([]GateBench(nil), b...)
			got, err := ChooseGateBench(now, b, tc.factor)
			if tc.want == "" {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got.Host)
			}
			assert.Equal(t, before, b, "selection changes no inventory facts")
		})
	}
}

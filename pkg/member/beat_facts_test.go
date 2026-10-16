package member

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/hostload"
)

// TestABeatCarriesTheMachinesFacts: a member's beat carries its gate facts (Config.Facts) as
// fleet beat --facts, which the sprint writes onto its control card for the deal's
// GATE-NEEDS (nova-sprint v1.4.3). Before it, no beat carried any, and every member's facts
// read empty: a GATE-NEEDS linux card was dealt to no member at all.
func TestABeatCarriesTheMachinesFacts(t *testing.T) {
	t.Parallel()
	g := newWidthRig(1, Config{As: "m", Meter: hostload.NewSampler(hostload.Source{}), Facts: []string{"linux", "amd64", "landlock", "tla", "go"}})
	require.NoError(t, g.m.Beat())
	require.Equal(t, []string{"fleet beat m --stop-returns 0 --load 0.0 --facts linux,amd64,landlock,tla,go"}, g.s.lines("beat"))
}

// factsRefuser is a sprint from before v1.4.3: it refuses a beat that carries --facts, as
// its server's word check does, and takes every other.
type factsRefuser struct {
	mu    sync.Mutex
	calls []string
}

func (f *factsRefuser) Run(args ...string) (int, []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	if slices.Contains(args, "--facts") {
		return 2, []byte("a beat takes no word --facts: its words are `fleet beat <member> ...` and nothing more")
	}
	return 0, nil
}

// TestABeatAnOldSprintRefusesForItsFactsGoesAgainWithout: a sprint that does not know
// --facts refuses the beat (exit 2); the member beats again at once without them, so it never
// reads down for the word, and offers them again only after FactsRetry.
func TestABeatAnOldSprintRefusesForItsFactsGoesAgainWithout(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000_000, 0)
	g := newWidthRig(1, Config{As: "m", Meter: hostload.NewSampler(hostload.Source{}), Facts: []string{"darwin", "arm64"}, Now: func() time.Time { return now }})
	old := &factsRefuser{}
	g.m.sprint = old
	require.NoError(t, g.m.Beat(), "the beat without facts is taken")
	require.NoError(t, g.m.Beat(), "the next beat sends no facts inside FactsRetry")
	now = now.Add(FactsRetry)
	require.NoError(t, g.m.Beat(), "after FactsRetry the facts are offered again, and dropped again")
	require.Equal(t, []string{
		"fleet beat m --stop-returns 0 --load 0.0 --facts darwin,arm64",
		"fleet beat m --stop-returns 0 --load 0.0",
		"fleet beat m --stop-returns 0 --load 0.0",
		"fleet beat m --stop-returns 0 --load 0.0 --facts darwin,arm64",
		"fleet beat m --stop-returns 0 --load 0.0",
	}, old.calls)
}

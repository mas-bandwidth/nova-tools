package friend

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The push proof refuses, before anything is delivered, a harness whose adapter
// has no deliver command, with the adapter card as its remedy; a session the
// adapter cannot drive (dsh under an agent preset) is refused with the dsh
// remedy; any other failure leaves the remedy to the caller (docs/SPEC-FRIEND.md,
// The push proof).
func TestThePushProofRefusesWhatCannotBeDriven(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	check := func(harness string, run Exec) Conformance {
		d, err := NewDeliverer(harness, "/w/bob", "session-z", run, nil)
		require.NoError(t, err)
		now := start
		return Conformance{Friend: "bob", Harness: harness, Deliver: d, Store: bustest.NewFake(start, "ada", "bob"), Within: time.Minute,
			Now: func() time.Time { now = now.Add(time.Second); return now }, Nonce: func() string { return "n1" },
			Wait: func(context.Context) bool { now = now.Add(10 * time.Second); return true },
			Text: func(nonce string) string { return SessionCheckPrefix + nonce },
			Pong: func() (Pong, bool, error) { return Pong{}, false, nil }}
	}
	ran := 0
	exec := func(out string, exit int) Exec {
		return func(context.Context, string, string, []string, string) (string, int, error) {
			ran++
			return out, exit, nil
		}
	}
	for _, c := range []struct {
		name, harness, out, stage, why, remedy string
		exit                                   int
		undriven                               bool
		delivered                              int
	}{
		{name: "a surveyed harness", harness: "cursor", stage: StageDeliver, why: "no deliver command for cursor (", remedy: "the adapter card: give pkg/friend a deliver command for cursor", undriven: true},
		{name: "dsh under an agent preset", harness: "dsh", out: `dsh: session "session-z" runs under agent preset "minimal", which the one-shot runner does not compose`, exit: 1,
			stage: StageDeliver, why: `runs under agent preset "minimal"`, remedy: "start a session in /w/bob with no agent preset and name it with --session <id>", undriven: true, delivered: 1},
		{name: "a session that never answers", harness: "dsh", stage: StageAct, why: "no pong n1 from bob within 1m0s", delivered: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			ran = 0
			res, remedy, undriven := PushProof(context.Background(), check(c.harness, exec(c.out, c.exit)))
			assert.Equal(t, c.stage, res.Stage)
			assert.Contains(t, res.Why, c.why)
			if c.remedy == "" {
				assert.Empty(t, remedy, "any other failure: the caller names its remedy")
			} else {
				assert.Contains(t, remedy, c.remedy)
			}
			assert.Equal(t, c.undriven, undriven)
			assert.Equal(t, c.delivered, ran, "a stub is refused before anything is delivered")
		})
	}
	assert.Equal(t, []string{"opencode", "codex", "claude", "antigravity", "dsh", "gemini", "grok", "tmux"}, Pushing())
}

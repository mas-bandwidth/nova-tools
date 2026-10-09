package main

import (
	"context"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// silentGate never answers on its own. It records the ask, then blocks until
// the context the caller passed is done. It cancels nothing: a land by hand
// passes context.Background, and only gateRerun's minute ends the wait
// (landgate.go:87-89).
type silentGate struct {
	trace []string
}

func (g *silentGate) Name() string { return "silent" }

func (g *silentGate) Ask(ctx context.Context, _ decide.Schema, _ string) (map[string]decide.Answer, decide.Usage, error) {
	g.trace = append(g.trace, "asked")
	<-ctx.Done()
	g.trace = append(g.trace, "abandoned")
	return nil, decide.Usage{}, ctx.Err()
}

// TestLandGateAbandonsASilentGate is the trace of one gate that never answers
// (cmd/nova-sprint/landgate.go gateRerun, the wait at lines 87-89;
// tla/LandPass.tla, Never then Abandon). The parent context is Background,
// as cmdLand's is (land.go:357), and the test does not cancel it. The minute
// of WithTimeout ends the ask. The bubble's clock is synctest's, so that
// minute is not a minute of wall time. The record clock does not move. No
// sleep and no dial: gitRun answers the diff, so the bubble never execs git.
func TestLandGateAbandonsASilentGate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := &silentGate{}
		dir := t.TempDir()
		when := time.Date(2026, 10, 9, 13, 32, 0, 0, time.UTC)
		l := &lander{
			a: &app{},
			gitRun: func(context.Context, string, ...string) (string, error) {
				return "", nil
			},
			gate: &landGate{
				backend: g,
				now:     func() time.Time { return when },
				record:  filepath.Join(dir, "gate.jsonl"),
			},
		}
		const out = "--- FAIL: TestA (1.02s)\n    a_test.go:9: timed out after 1s\nFAIL\nFAIL\tm/p\t1.1s\n"
		got := l.gateRerun(context.Background(), dir, "s1", "main", "0123456789ab", nil, "the check failed", out)
		assert.Equal(t, []string{"asked", "abandoned"}, g.trace)
		assert.Contains(t, got, "no gate decision")
		assert.Contains(t, got, context.DeadlineExceeded.Error())
		assert.NotContains(t, got, context.Canceled.Error())
	})
}

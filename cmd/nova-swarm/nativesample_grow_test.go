package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// TestTheSamplerCountsTurnsAsTheLogGrows (2026-10-17): each sample read the whole harness
// log to count the card's turns. The sampler now keeps its count (swarm.LogTurns) and reads
// only what the log gained, and the card budget fires on the same count: two turns under a
// max_turns of 2 fire nothing, the third line the log gains fires stopped=max_turns. The
// read is a fake answering an observed usage; the clock is fixed at 1000 s.
func TestTheSamplerCountsTurnsAsTheLogGrows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "harness-output.log")
	require.NoError(t, os.WriteFile(logPath, []byte("assistant one\nassistant two\n"), 0o644))
	s := startLiveSampler(dir, 0, nativeRunConfig{worker: &swarm.Worker{MaxTurns: 2}}, logPath)
	defer s.Stop()
	s.now = func() time.Time { return time.Unix(1000, 0) }
	reads := 0
	s.read = func(string, time.Duration) (swarm.ProviderUsage, error) {
		reads++
		return swarm.ProviderUsage{Values: map[string]string{"input": "10", "output": "1"}, Observed: true, Turns: 1}, nil
	}
	s.readOnce()
	assert.Empty(t, s.StopWordAtFinal(0, false, ""), "two turns are not over two")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("Assistant three\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	s.readOnce()
	assert.Equal(t, 2, reads)
	assert.Equal(t, stoppedMaxTurns, s.StopWordAtFinal(0, false, ""), "the third turn the log gained fires the card budget")
	assert.Equal(t, 3, s.turnLog.Count(logPath))
}

// TestTheDefaultUsageIntervalIsFifteenSeconds: a sqlite3 launch per running card every 5 s
// across the bench width was the sampler's cost; 15 s is the default now.
func TestTheDefaultUsageIntervalIsFifteenSeconds(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 15*time.Second, swarm.DefaultUsageInterval)
}

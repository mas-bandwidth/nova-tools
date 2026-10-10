package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probationSupervisorFake is the supervisor behind installSupervisor, faked:
// it keeps the previous binary of every restart and opens no socket, starts no
// process.
type probationSupervisorFake struct {
	mu        sync.Mutex
	restarted []string
}

func (f *probationSupervisorFake) Restart(ctx context.Context, target, previous string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarted = append(f.restarted, previous)
	return nil
}

// probationFixture is a target binary, its previous binary and a candidate.
type probationFixture struct {
	dir       string
	target    string
	candidate string
	sup       *probationSupervisorFake
	pushed    []string
	log       bytes.Buffer
}

func newProbationFixture(t *testing.T) *probationFixture {
	t.Helper()
	f := &probationFixture{dir: t.TempDir(), sup: &probationSupervisorFake{}}
	f.target = filepath.Join(f.dir, "nova-sprint")
	f.candidate = filepath.Join(f.dir, "nova-sprint-candidate")
	require.NoError(t, os.WriteFile(f.target+".prev", []byte("the previous binary"), 0o755))
	require.NoError(t, os.WriteFile(f.target, []byte("the candidate binary"), 0o755))
	return f
}

// guard is the probation over the fixture: n ticks, the fake supervisor, a
// push recorder and the injected clock, with a rollback that copies the
// previous binary onto the target as sprint.ServerRollback does.
func (f *probationFixture) guard(n int) *probation {
	now := func() time.Time { return time.Date(2026, 10, 7, 17, 5, 0, 0, time.UTC) }
	rollback := func(ctx context.Context, target string) error {
		return os.WriteFile(target, []byte("the previous binary"), 0o755)
	}
	push := func(ctx context.Context, note string) error {
		f.pushed = append(f.pushed, note)
		return nil
	}
	return newProbation(f.target, f.candidate, f.target+".prev", n, f.sup, push, &f.log, now, rollback)
}

// TestASwappedServerThatMissesItsFirstTicksIsRolledBack: after a swap the new
// server is on probation for its first N ticks (docs/SPEC-SPRINT.md section 14,
// install-rollback-on-missed-ticks-b.w2; the model is tla/ServerInstall.tla).
// With a fake supervisor and an injected clock, a tick that misses its deadline
// or the process exiting within the first N ticks replaces the candidate with
// the previous binary, restarts it, logs the tick that failed and pushes the
// seat one note; N good ticks end the probation and the binary is kept; a
// missed tick after the probation ended never rolls back.
func TestASwappedServerThatMissesItsFirstTicksIsRolledBack(t *testing.T) {
	t.Parallel()

	t.Run("a tick that misses its deadline in the first N rolls back", func(t *testing.T) {
		t.Parallel()
		f := newProbationFixture(t)
		p := f.guard(3)
		for i := 0; i < 2; i++ {
			outcome, err := p.tickOK(context.Background())
			require.NoError(t, err)
			assert.Equal(t, probationWatching, outcome)
		}
		outcome, err := p.tickMissed(context.Background())
		require.NoError(t, err)
		assert.Equal(t, probationRolledBack, outcome)
		assert.Equal(t, []string{f.target + ".prev"}, f.sup.restarted, "the previous binary is restarted")
		restored, err := os.ReadFile(f.target)
		require.NoError(t, err)
		assert.Equal(t, "the previous binary", string(restored), "the previous binary is back on disk")
		assert.Contains(t, f.log.String(), "INSTALL ROLLBACK")
		assert.Contains(t, f.log.String(), "tick=3", "the log names the tick that failed")
		assert.Contains(t, f.log.String(), "missed the tick deadline")
		require.Len(t, f.pushed, 1, "the seat is pushed one note")
		assert.Contains(t, f.pushed[0], "rolled back")
		assert.Contains(t, f.pushed[0], "tick 3")
		// the rolled-back binary is refused by switch until a new binary is named
		assert.NotEmpty(t, rolledBackRefusal(f.target, f.candidate))
		assert.Empty(t, rolledBackRefusal(f.target, filepath.Join(f.dir, "a-new-binary")), "a new binary is admitted")
	})

	t.Run("a server that exits in the first N rolls back", func(t *testing.T) {
		t.Parallel()
		f := newProbationFixture(t)
		p := f.guard(3)
		_, err := p.tickOK(context.Background())
		require.NoError(t, err)
		outcome, err := p.exited(context.Background())
		require.NoError(t, err)
		assert.Equal(t, probationRolledBack, outcome)
		assert.Equal(t, []string{f.target + ".prev"}, f.sup.restarted)
		assert.Contains(t, f.log.String(), "the process exited")
		assert.Contains(t, f.log.String(), "tick=2")
		require.Len(t, f.pushed, 1)
	})

	t.Run("N good ticks end the probation and the binary is kept", func(t *testing.T) {
		t.Parallel()
		f := newProbationFixture(t)
		p := f.guard(3)
		var outcome probationOutcome
		var err error
		for i := 0; i < 3; i++ {
			outcome, err = p.tickOK(context.Background())
			require.NoError(t, err)
		}
		assert.Equal(t, probationKept, outcome)
		assert.Empty(t, f.sup.restarted, "a kept binary is never rolled back")
		assert.Empty(t, f.pushed, "a kept binary pushes no note")
		kept, err := os.ReadFile(f.target)
		require.NoError(t, err)
		assert.Equal(t, "the candidate binary", string(kept))
		assert.Contains(t, f.log.String(), "INSTALL PROBATION OK")
	})

	t.Run("a missed tick after the probation ended never rolls back", func(t *testing.T) {
		t.Parallel()
		f := newProbationFixture(t)
		p := f.guard(2)
		_, err := p.tickOK(context.Background())
		require.NoError(t, err)
		outcome, err := p.tickOK(context.Background())
		require.NoError(t, err)
		require.Equal(t, probationKept, outcome)
		outcome, err = p.tickMissed(context.Background())
		require.NoError(t, err)
		assert.Equal(t, probationKept, outcome, "a kept binary is never rolled back: tla/ServerInstall.tla, NoLateRollback")
		assert.Empty(t, f.sup.restarted)
	})
}

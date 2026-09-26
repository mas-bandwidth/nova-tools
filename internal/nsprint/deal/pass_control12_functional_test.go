//go:build functional

package deal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The fixture's positive control bounds its wait on the wall clock (the
// guard), so it runs in the functional tier: the unit tier's ledger of
// wall-clock waits only shrinks (internal/ci TestNoUnitTestWaitsOnTheWallClock,
// #4413); stream table merge of #4377 onto dev f7aa36530.

// TestControl12FixtureSSHDAllowsTwoSessions is control 12's positive control
// on the fixture itself (#2756), its own test so each stays under the unit
// tier's 1 s budget (#4328).
func TestControl12FixtureSSHDAllowsTwoSessions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	// The positive control on the fixture itself: sessions at once, the
	// old launcher's shape, are closed before the command past the two
	// it allows. The two it accepts hold until every other one is
	// refused, so the control is exact (8 of 10) and waits on no clock
	// (ten, not fifty: the unit tier's 1 s budget, #4328).
	f := newFixture(t)
	r := f.remote()
	b := upBench("ctl-probe", 64)
	f.set(t, b.Name, "sleep", "0")
	f.set(t, b.Name, "hold", "")
	hold := filepath.Join(f.dir, b.Name, "hold")
	const sessions = 10
	results := make(chan error, sessions)
	for i := 0; i < sessions; i++ {
		go func() { results <- r.Dial(b).Run(ctx, []byte("x\n")) }()
	}
	bound := time.NewTimer(guard)
	defer bound.Stop()
	refused := 0
	for got := 0; got < sessions; got++ {
		select {
		case err := <-results:
			var se *SessionError
			if !errors.As(err, &se) || se.State != SSHRefused {
				continue
			}
			if refused++; refused == sessions-2 {
				if err := os.Remove(hold); err != nil {
					t.Fatal(err)
				}
			}
		case <-bound.C:
			_ = os.Remove(hold)
			t.Fatalf("fixture refused %d of %d concurrent sessions within %s; it must allow only two", refused, sessions, guard)
		}
	}
	if refused != sessions-2 {
		t.Fatalf("fixture refused %d of %d concurrent sessions; it must allow exactly two", refused, sessions)
	}
}

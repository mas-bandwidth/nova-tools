//go:build functional && (unix || windows)

package filelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// H1 witness in its concurrent form: an asker (a refused taker asking for the
// shared lock, as takeExclusive does) holds the shared lock for an instant, over
// and over, and a taker landing in that instant must not be told held, because
// nobody holds (tla/FileLock.tla, HeldIsTrue; the "busyisheld" witness breaks it).
// Busy is a true answer and is counted, not failed. Functional tier: a take and
// release cost about 10 ms on macOS (two full fsyncs), so 300 of them are 3 s;
// the unit-tier witness is TestTryLock_AskerIsNotAHolder. With the asker a probe,
// at ff635f74f this refused 45 and 19 of 2,000 takes as held on macOS and 108 of
// 2,000 on Linux. The probe left the package on 2026-10-02; the asker is the
// shared ask every refused taker still makes.
func TestFunctional_AskerDoesNotDisturbTaker(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "asked.lock")
	if err := os.WriteFile(path, nil, 0666); err != nil {
		require.NoError(t, err, err)
	}
	asker, err := openFileSafe(path, os.O_RDWR, 0)
	require.NoError(t, err)
	defer asker.Close()
	stop := make(chan struct{})
	done := make(chan int)
	go func() {
		asks := 0
		for {
			select {
			case <-stop:
				done <- asks
				return
			default:
			}
			ok, err := trySharedLock(asker)
			if err != nil {
				assert.NoError(t, err, "the asker's shared lock: %v", err)
			}
			if ok {
				unlockFile(asker)
			}
			asks++
		}
	}()

	const takes = 300
	held, busy := 0, 0
	var firstHeld error
	for i := 0; i < takes; i++ {
		lock, err := TryLock(path, "taker")
		switch {
		case err == nil:
			lock.Unlock()
		case errors.Is(err, ErrBusy):
			busy++
		case errors.Is(err, ErrHeld):
			held++
			if firstHeld == nil {
				firstHeld = err
			}
		default:
			require.Fail(t, fmt.Sprintf("TryLock: %v", err))
		}
	}
	close(stop)
	asks := <-done
	t.Logf("%d takes beside %d asks: %d told held, %d told busy", takes, asks, held, busy)
	if held > 0 {
		assert.LessOrEqual(t, held, 0, "told held %d times of %d with nobody holding, only an asker; first: %v", held, takes, firstHeld)
	}
}

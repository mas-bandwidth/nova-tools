package main

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// blockingWriter is a writer that blocks on Write until released.
type blockingWriter struct {
	mu    sync.Mutex
	rel   chan struct{}
	wrote []string
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{rel: make(chan struct{})}
}

func (w *blockingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	w.wrote = append(w.wrote, string(p))
	w.mu.Unlock()
	<-w.rel
	return len(p), nil
}

func (w *blockingWriter) release() {
	close(w.rel)
}

// TestTickDeadlineBoundedRefusesANegativeDeadline tests that a negative tick deadline is refused.
func TestTickDeadlineBoundedRefusesANegativeDeadline(t *testing.T) {
	t.Parallel()
	var errb bytes.Buffer
	a := newApp(func(string) string { return "" })
	code := a.cmdRun([]string{"--tick-deadline=-1s"}, &bytes.Buffer{}, &errb)
	assert.Equal(t, 2, code)
	assert.Contains(t, errb.String(), "--tick-deadline")
	assert.Contains(t, errb.String(), "-1s")
}

// TestTickDeadlineBoundedZeroStillWaitsForEver tests that 0 deadline does not use the clock.
func TestTickDeadlineBoundedZeroStillWaitsForEver(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	afterCalled := false
	a.after = func(time.Duration) <-chan time.Time {
		afterCalled = true
		return make(chan time.Time)
	}
	var out, errb bytes.Buffer
	res, over, ended, err := a.tickWithin(context.Background(), func(context.Context) (store.TickResult, error) {
		return store.TickResult{State: store.Running}, nil
	}, 0, time.Time{}, &out, &errb)
	assert.False(t, over)
	assert.NoError(t, err)
	assert.Equal(t, store.Running, res.State)
	assert.False(t, afterCalled)
	<-ended
}

// TestTickDeadlineBoundedOverRunsPastABlockedStdoutAndStderr tests that tickWithin returns
// over even when the writers block on their writes.
func TestTickDeadlineBoundedOverRunsPastABlockedStdoutAndStderr(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	blockedOut := newBlockingWriter()
	blockedErr := newBlockingWriter()
	afterCount := 0
	a.after = func(d time.Duration) <-chan time.Time {
		afterCount++
		fired := make(chan time.Time, 1)
		fired <- a.now()
		return fired
	}
	began := a.now().Add(-10 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, over, ended, err := a.tickWithin(ctx, func(c context.Context) (store.TickResult, error) {
		<-c.Done()
		return store.TickResult{}, c.Err()
	}, time.Millisecond, began, blockedOut, blockedErr)
	assert.True(t, over)
	assert.NoError(t, err)
	assert.Greater(t, afterCount, 0)
	// Release the writers so they don't block forever
	blockedOut.release()
	blockedErr.release()
	<-ended
}

// TestTickDeadlineBoundedErrorStillPropagates tests that a tick's own error within the deadline is returned unchanged.
func TestTickDeadlineBoundedErrorStillPropagates(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	a.after = func(time.Duration) <-chan time.Time {
		return make(chan time.Time)
	}
	testErr := fmt.Errorf("test error")
	var out, errb bytes.Buffer
	_, over, ended, err := a.tickWithin(context.Background(), func(context.Context) (store.TickResult, error) {
		return store.TickResult{}, testErr
	}, TickDeadline, time.Time{}, &out, &errb)
	assert.False(t, over)
	assert.Equal(t, testErr, err)
	assert.Empty(t, out.String()+errb.String())
	<-ended
}

// TestTickDeadlineBoundedWedgedExitsFourPastABlockedStderr proves that three
// further deadline expiries return false even while both diagnostic destinations
// are blocked; runLoop turns that false result into exitTickDeadline without
// starting another tick.
func TestTickDeadlineBoundedWedgedExitsFourPastABlockedStderr(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	a.after = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- a.now()
		return ch
	}
	blockedOut := newBlockingWriter()
	blockedErr := newBlockingWriter()
	ended := make(chan struct{})
	wedged := 0
	assert.False(t, a.awaitGivenUp(ended, time.Millisecond, a.now(), &wedged, blockedOut, blockedErr))
	assert.Equal(t, TickWedgedToExit, wedged)
	blockedOut.release()
	blockedErr.release()
}

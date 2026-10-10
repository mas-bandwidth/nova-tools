package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	}, 10*time.Second, began, blockedOut, blockedErr)
	assert.True(t, over)
	assert.NoError(t, err)
	assert.Greater(t, afterCount, 0)
	// Release the writers so they don't block forever
	blockedOut.release()
	blockedErr.release()
	<-ended
}

// TestTickDeadlineBoundedWedgedExitsFourPastABlockedStderr tests that three wedged ticks
// with a blocked stderr still reach a.exit(4).
func TestTickDeadlineBoundedWedgedExitsFourPastABlockedStderr(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	ta.ok("start")
	st, _, code := ta.a.machineVerb("run", nil, &bytes.Buffer{})
	require.NotNil(t, st)
	exits := make([]int, 0)
	var exitMu sync.Mutex
	ta.a.exit = func(code int) {
		exitMu.Lock()
		exits = append(exits, code)
		exitMu.Unlock()
	}
	var exitMu2 sync.Mutex
	calls := 0
	ta.a.tickDeadline = TickDeadline
	blockedErr := newBlockingWriter()
	s := newAfterScript("deadline", "further", "stop")
	ta.a.after = s.after
	ta.a.tickFn = func(ctx context.Context, st *store.Store) (store.TickResult, error) {
		exitMu2.Lock()
		calls++
		exitMu2.Unlock()
		return s.deaf(ctx)
	}
	var out bytes.Buffer
	go func() {
		ta.a.runLoop(context.Background(), st, 20, 10, &out, blockedErr)
	}()
	// Wait for wedged messages and release the writer
	go func() {
		for len(blockedErr.wrote) < 3 {
			time.Sleep(10 * time.Millisecond)
		}
		blockedErr.release()
	}()
	// Wait for exit
	time.Sleep(2 * time.Second)
	blockedErr.release()
	exitMu.Lock()
	actualExits := exits
	exitMu.Unlock()
	assert.Contains(t, actualExits, exitTickDeadline)
	exitMu2.Lock()
	assert.Greater(t, calls, 0)
	exitMu2.Unlock()
}

// TestTickDeadlineBoundedErrorStillPropagates tests that a tick's own error within the deadline is returned unchanged.
func TestTickDeadlineBoundedErrorStillPropagates(t *testing.T) {
	t.Parallel()
	a := newApp(func(string) string { return "" })
	a.after = func(time.Duration) <-chan time.Time {
		return make(chan time.Time)
	}
	testErr := strings.NewReplacer("test error")
	var out, errb bytes.Buffer
	res, over, ended, err := a.tickWithin(context.Background(), func(context.Context) (store.TickResult, error) {
		return store.TickResult{}, testErr
	}, TickDeadline, time.Time{}, &out, &errb)
	assert.False(t, over)
	assert.Equal(t, testErr, err)
	assert.Empty(t, out.String()+errb.String())
	<-ended
}

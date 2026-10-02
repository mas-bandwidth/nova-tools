package main

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The production run loop, rather than a test wrapper around tick, must take
// the same mutex as serve. Removing runLoop's lock must fail this check.
func TestServerReviewRunLoopOwnsSerializationDuringATick(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1:2 --readers reader-a,reader-b")
	ta.ok("add --stream s1 --count 4")
	ta.ok("start")
	st, _, code := ta.a.machineVerb("run", nil, &bytes.Buffer{})
	require.NotNil(t, st, "run exit=%d", code)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	st.Updates = []sprint.TableUpdate{{Table: sprint.Fleet, Parts: []sprint.TickPartDef{{Name: "review gate", Fn: func(*sprint.Snapshot, sprint.TickReq) (sprint.Plan, int) {
		once.Do(func() { close(entered); <-release })
		return sprint.Plan{}, 0
	}}}}}
	ctx := context.Background()
	done := make(chan struct{})
	go func() { defer close(done); ta.a.runLoop(ctx, st, 20, 1, &bytes.Buffer{}, &bytes.Buffer{}) }()
	t.Cleanup(func() { close(release); <-done })
	select {
	case <-entered:
	case <-done:
		require.FailNow(t, "tick never reached its update")
	}
	free := ta.a.serial.TryLock()
	if free {
		ta.a.serial.Unlock()
	}
	assert.False(t, free, "a worker must not acquire the shared mutex inside the production tick")
}

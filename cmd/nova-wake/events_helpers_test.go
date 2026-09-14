package main

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

// holdLock takes the advisory lock this repo's own tools take on a path, so a
// test can put a lock source's source in the one state it cannot otherwise
// reach: held by somebody else. It is internal/bus's own primitive, because the
// probe under test speaks that protocol and a second lock implementation in a
// test proves nothing about the first.
func holdLock(t *testing.T, path string) func() {
	t.Helper()
	release, err := bus.LockFile(path, 0)
	if err != nil {
		t.Fatalf("holding %s: %v", path, err)
	}
	var once sync.Once
	return func() { once.Do(release) }
}

// wakeRunStopped runs a watch that is stopped by its caller partway through.
// A test cannot send itself a SIGTERM without ending the test binary, so the
// stop arrives through the same channel the signal would: watchStopHook is the
// injection point, set only here, exactly as watchKillPoint is.
func wakeRunStopped(t *testing.T, args ...string) result {
	t.Helper()
	clock := wake.NewFake(at)
	stop := make(chan struct{})
	var once sync.Once
	clock.OnSleep = func(time.Time) { once.Do(func() { close(stop) }) }
	watchStopHook = func() <-chan struct{} { return stop }
	t.Cleanup(func() { watchStopHook = nil })
	var out, errb bytes.Buffer
	exit := runWith(args, &out, &errb, clock)
	return result{exit: exit, stdout: out.String(), stderr: errb.String(), clock: clock}
}

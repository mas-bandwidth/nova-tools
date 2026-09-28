package main

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/wake"
)

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

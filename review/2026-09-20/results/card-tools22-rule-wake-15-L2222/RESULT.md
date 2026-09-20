RULE tools22-rule-wake-15-L2222 sha=5298f6be12ea — does the code at this base do what docs/SPEC-WAKE.md rule 15 says?
CONFORMS cmd/nova-wake/main.go:875
SPEC docs/SPEC-WAKE.md:2222 rule 15
PKG cmd/nova-wake (source implementations under internal/wake)
ASK An implementation must create a signal-based cancellation context (SIGINT/SIGTERM) that cancels all source polling operations, check for this cancellation at the top of each loop iteration to halt further polling while still completing the current observation+print cycle (rule 11), then print the WAKE STOPPED verdict with proper counters, release the state lock, and exit 0.

DECIDING LINES:

--- cmd/nova-wake/main.go:875-887 ---
	// Rule 15: a wait ends on the first change, at its deadline, or on the
	// CALLER'S STOP, and never otherwise. The stop is SIGINT or SIGTERM, and it
	// cancels the context too, so a gh call in flight is abandoned rather than
	// waited out.
	var stopCh <-chan struct{}
	if watchStopHook != nil {
		stopCh = watchStopHook()
	} else {
		stopCtx, stopStop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stopStop()
		ctx = stopCtx
		stopCh = stopCtx.Done()
	}

--- cmd/nova-wake/main.go:1267-1280 ---
// stopRequested answers rule 15's third ending. It never blocks: the stop is
// read where the deadline is read, at the top of an iteration, so a call that
// has been ended polls nothing more and prints its verdict.
func (w *watcher) stopRequested() bool {
	if w.stopCh == nil {
		return false
	}
	select {
	case <-w.stopCh:
		return true
	default:
		return false
	}
}

--- cmd/nova-wake/main.go:1155-1158 ---
		stopped := w.stopRequested()
		w.busRead, w.busFail = false, false
		broken := ""
		if !reached && !stopped {

--- cmd/nova-wake/main.go:1191-1252 ---
		// Rule 11, step 1: every observation is in the state before anything is
		// printed. A kill here leaves the entry pending, which is a repeated
		// wake and never a lost one.
		w.save()
		if watchKillPoint == "after-observed" {
			return 0
		}
		_, news := w.printQueue(now)
		if w.killed || watchKillPoint == "after-marks" {
			return 0
		}
		...
		switch {
		...
		case stopped:
			w.sourceLine()
			fmt.Fprintf(w.stdout, "WAKE STOPPED after=%s polls=%d pending=%d: stopped by the caller\n",
				oneline.Field(wake.Dur(now.Sub(start))), w.polls, w.st.Pending())
			return 0

--- cmd/nova-wake/main.go:866 ---
	defer release()

--- internal/wake/forge.go:42-49 ---
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	raw, err := cmd.Output()
	if err == nil {
		return raw, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("gh timed out after %s", Dur(timeout))
	}

--- internal/wake/bus.go:293-300 ---
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nova-bus", args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.String(), 0, fmt.Errorf("nova-bus timed out")
	}

--- cmd/nova-wake/main.go:1286-1299 ---
// rest is the sleep between polls, and it ends on the caller's stop. On the
// injected clock it is the clock's own hands moving, which is what makes a
// twenty-minute watch a millisecond of test; on the real one it is a select, so
// a SIGTERM inside an interval does not wait the interval out.
func (w *watcher) rest(d time.Duration) {
	if d <= 0 {
		return
	}
	if _, real := w.clock.(wake.Real); real && w.stopCh != nil {
		select {
		case <-w.stopCh:
			return
		case <-time.After(d):
			return
		}
	}
	w.clock.Sleep(d)
}

GUARDED-BY cmd/nova-wake/events_test.go:467 TestAStopIsAVerdict

GREPS RAN:
grep -rn "WAKE STOPPED" --include='*.go' .
grep -rn "stopped by the caller" --include='*.go' .
grep -rn "\-\-max" --include='*.go' internal/wake/
grep -rn "SIGINT\|SIGTERM\|StopChan\|stopChan\|signal.Stop" --include='*.go' .
grep -rn "polls=\|%d.*polls\|after=" --include='*.go' . | head -30
grep -rn "ctx.Done\|<-ctx" internal/wake/bus.go
grep -rn "ctx.Done\|<-ctx" internal/wake/pr.go
grep -n "func gh\|exec.CommandContext" internal/wake/pr.go
grep -rn "func gh(" --include='*.go' internal/wake/
grep -n "ctx.Done\|<-ctx\|--advance" internal/wake/bus.go
grep -n "exec.CommandContext\|ctx\|timeout" internal/wake/pr.go | head -15
grep -n "release\|defer \|Release\|Unlock\|close(" cmd/nova-wake/main.go | head -30
grep -n "func.*poll\|ctx.Done\|context.Context" cmd/nova-wake/main.go | head -30
grep -rn "rule.*15\|Rule.*15" cmd/nova-wake/*.go
grep -n "func Test" cmd/nova-wake/*_test.go

Left owed
git status --short

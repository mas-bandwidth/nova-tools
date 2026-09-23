package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestStateMachine_AllValidTransitions tests every valid lifecycle transition:
// - Start: idle -> running
// - Pause: running -> paused
// - Resume: paused -> running
// - Drain from running: running -> draining -> stopped
// - Drain from paused: paused -> draining -> stopped
// - Stop from running: running -> stopped
// - Stop from paused: paused -> stopped
// - Stop from draining: draining -> stopped
func TestStateMachine_AllValidTransitions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)

	// 1. idle -> running -> paused -> running
	{
		dir := t.TempDir()
		sm := NewStateMachine(StateIdle, WithDir(dir), WithNowFunc(func() time.Time { return now }))
		if st := sm.Current(); st != StateIdle {
			t.Fatalf("initial state want idle, got %s", st)
		}

		// Start: idle -> running
		if err := sm.Start(); err != nil {
			t.Fatalf("Start() error: %v", err)
		}
		if st := sm.Current(); st != StateRunning {
			t.Fatalf("state after Start want running, got %s", st)
		}

		// Pause: running -> paused
		if err := sm.Pause(); err != nil {
			t.Fatalf("Pause() error: %v", err)
		}
		if st := sm.Current(); st != StatePaused {
			t.Fatalf("state after Pause want paused, got %s", st)
		}
		// STOP file must exist to suspend dispatch
		if _, err := os.Stat(filepath.Join(dir, SprintStopFile)); err != nil {
			t.Fatalf("STOP file must exist in paused state: %v", err)
		}

		// Resume: paused -> running
		if err := sm.Resume(); err != nil {
			t.Fatalf("Resume() error: %v", err)
		}
		if st := sm.Current(); st != StateRunning {
			t.Fatalf("state after Resume want running, got %s", st)
		}
		// STOP file must be removed
		if _, err := os.Stat(filepath.Join(dir, SprintStopFile)); !os.IsNotExist(err) {
			t.Fatalf("STOP file must be removed in running state")
		}
	}

	// 2. running -> draining -> stopped (auto complete when active count = 0)
	{
		dir := t.TempDir()
		var polls int32
		leases := LeaseFunc(func() (int, error) {
			p := atomic.AddInt32(&polls, 1)
			if p < 3 {
				return 2, nil // 2 cards still finishing
			}
			return 0, nil // completed
		})

		sm := NewStateMachine(StateRunning, WithDir(dir), WithLeaseChecker(leases), WithNowFunc(func() time.Time { return now }))
		err := sm.Drain(5*time.Millisecond, 1*time.Second, func(d time.Duration) {})
		if err != nil {
			t.Fatalf("Drain() error: %v", err)
		}
		if st := sm.Current(); st != StateStopped {
			t.Fatalf("state after Drain completed want stopped, got %s", st)
		}
		receipt := sm.LastReceipt()
		if receipt == nil || receipt.State != StateStopped || receipt.ActiveCards != 0 {
			t.Fatalf("unexpected receipt after drain: %+v", receipt)
		}
	}

	// 3. paused -> draining -> stopped
	{
		dir := t.TempDir()
		leases := LeaseFunc(func() (int, error) {
			return 0, nil // immediately done
		})
		sm := NewStateMachine(StatePaused, WithDir(dir), WithLeaseChecker(leases), WithNowFunc(func() time.Time { return now }))
		if err := sm.Drain(5*time.Millisecond, 1*time.Second, nil); err != nil {
			t.Fatalf("Drain() from paused error: %v", err)
		}
		if st := sm.Current(); st != StateStopped {
			t.Fatalf("state after Drain from paused want stopped, got %s", st)
		}
	}

	// 4. Stop from running (cancels active tasks, records terminal receipt)
	{
		dir := t.TempDir()
		var cancelled bool
		leases := LeaseFunc(func() (int, error) { return 3, nil })
		sm := NewStateMachine(StateRunning, WithDir(dir), WithLeaseChecker(leases), WithCancelFunc(func() {
			cancelled = true
		}), WithNowFunc(func() time.Time { return now }))

		receipt, err := sm.Stop("operator abort")
		if err != nil {
			t.Fatalf("Stop() from running error: %v", err)
		}
		if !cancelled {
			t.Fatalf("Stop() must invoke cancel callback")
		}
		if st := sm.Current(); st != StateStopped {
			t.Fatalf("state after Stop want stopped, got %s", st)
		}
		if receipt.ActiveCards != 3 || receipt.Reason != "operator abort" {
			t.Fatalf("unexpected stop receipt: %+v", receipt)
		}
		// Terminal receipt file must be written
		if _, err := os.Stat(filepath.Join(dir, SprintReceiptFile)); err != nil {
			t.Fatalf("missing TERMINAL-RECEIPT.json: %v", err)
		}
	}

	// 5. Stop from paused
	{
		dir := t.TempDir()
		sm := NewStateMachine(StatePaused, WithDir(dir), WithNowFunc(func() time.Time { return now }))
		receipt, err := sm.Stop("paused cancelled")
		if err != nil {
			t.Fatalf("Stop() from paused error: %v", err)
		}
		if st := sm.Current(); st != StateStopped {
			t.Fatalf("state after Stop from paused want stopped, got %s", st)
		}
		if receipt.Reason != "paused cancelled" {
			t.Fatalf("unexpected stop receipt: %+v", receipt)
		}
	}

	// 6. Stop from draining
	{
		dir := t.TempDir()
		sm := NewStateMachine(StateDraining, WithDir(dir), WithNowFunc(func() time.Time { return now }))
		receipt, err := sm.Stop("drain interrupted")
		if err != nil {
			t.Fatalf("Stop() from draining error: %v", err)
		}
		if st := sm.Current(); st != StateStopped {
			t.Fatalf("state after Stop from draining want stopped, got %s", st)
		}
		if receipt.Reason != "drain interrupted" {
			t.Fatalf("unexpected stop receipt: %+v", receipt)
		}
	}
}

// TestStateMachine_InvalidTransitionRefusals ensures every invalid state transition is refused.
func TestStateMachine_InvalidTransitionRefusals(t *testing.T) {
	t.Parallel()

	// Start only valid from idle
	for _, invalid := range []SprintState{StateRunning, StatePaused, StateDraining, StateStopped} {
		sm := NewStateMachine(invalid)
		if err := sm.Start(); err == nil {
			t.Fatalf("Start() must fail from state %s", invalid)
		} else if !strings.Contains(err.Error(), "invalid transition") {
			t.Fatalf("unexpected error message: %v", err)
		}
	}

	// Pause only valid from running
	for _, invalid := range []SprintState{StateIdle, StatePaused, StateDraining, StateStopped} {
		sm := NewStateMachine(invalid)
		if err := sm.Pause(); err == nil {
			t.Fatalf("Pause() must fail from state %s", invalid)
		} else if !strings.Contains(err.Error(), "invalid transition") {
			t.Fatalf("unexpected error message: %v", err)
		}
	}

	// Resume only valid from paused
	for _, invalid := range []SprintState{StateIdle, StateRunning, StateDraining, StateStopped} {
		sm := NewStateMachine(invalid)
		if err := sm.Resume(); err == nil {
			t.Fatalf("Resume() must fail from state %s", invalid)
		} else if !strings.Contains(err.Error(), "invalid transition") {
			t.Fatalf("unexpected error message: %v", err)
		}
	}

	// Drain only valid from running or paused
	for _, invalid := range []SprintState{StateIdle, StateDraining, StateStopped} {
		sm := NewStateMachine(invalid)
		if err := sm.Drain(10*time.Millisecond, 100*time.Millisecond, nil); err == nil {
			t.Fatalf("Drain() must fail from state %s", invalid)
		} else if !strings.Contains(err.Error(), "invalid transition") {
			t.Fatalf("unexpected error message: %v", err)
		}
	}

	// Stop only valid from running, paused, draining
	for _, invalid := range []SprintState{StateIdle, StateStopped} {
		sm := NewStateMachine(invalid)
		if _, err := sm.Stop("test"); err == nil {
			t.Fatalf("Stop() must fail from state %s", invalid)
		} else if !strings.Contains(err.Error(), "invalid transition") {
			t.Fatalf("unexpected error message: %v", err)
		}
	}
}

// TestSprintLifecycle_CLIIntegration exercises all CLI verbs and transitions.
func TestSprintLifecycle_CLIIntegration(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)
	var out, errb bytes.Buffer

	// 1. Prep / Idle
	code := SprintPrep(SprintPrepInput{
		Dir:    dir,
		Now:    func() time.Time { return now },
		Stdout: &out,
		Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("prep failed: %d, %s", code, errb.String())
	}
	st, _ := ReadSprintState(dir)
	if st != StateIdle {
		t.Fatalf("want idle, got %s", st)
	}

	// 2. Start
	out.Reset()
	errb.Reset()
	code = SprintStart(SprintStartInput{
		Dir:    dir,
		Now:    func() time.Time { return now.Add(time.Minute) },
		Stdout: &out,
		Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("start failed: %d, %s", code, errb.String())
	}
	st, _ = ReadSprintState(dir)
	if st != StateRunning {
		t.Fatalf("want running, got %s", st)
	}

	// 3. Pause
	out.Reset()
	errb.Reset()
	code = SprintPause(SprintPauseInput{
		Dir:    dir,
		Stdout: &out,
		Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("pause failed: %d, %s", code, errb.String())
	}
	st, _ = ReadSprintState(dir)
	if st != StatePaused {
		t.Fatalf("want paused, got %s", st)
	}

	// 4. Resume
	out.Reset()
	errb.Reset()
	code = SprintResume(SprintResumeInput{
		Dir:    dir,
		Stdout: &out,
		Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("resume failed: %d, %s", code, errb.String())
	}
	st, _ = ReadSprintState(dir)
	if st != StateRunning {
		t.Fatalf("want running, got %s", st)
	}

	// 5. Drain
	out.Reset()
	errb.Reset()
	leases := LeaseFunc(func() (int, error) { return 0, nil })
	code = SprintDrain(SprintDrainInput{
		Dir:    dir,
		Leases: leases,
		Now:    func() time.Time { return now.Add(2 * time.Minute) },
		Stdout: &out,
		Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("drain failed: %d, %s", code, errb.String())
	}
	st, _ = ReadSprintState(dir)
	if st != StateDraining {
		t.Fatalf("want draining after drain, got %s", st)
	}

	// 6. Stop
	out.Reset()
	errb.Reset()
	code = SprintStop(SprintStopInput{
		Dir:    dir,
		Leases: leases,
		Strict: true,
		Now:    func() time.Time { return now.Add(3 * time.Minute) },
		Stdout: &out,
		Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("stop failed: %d, %s", code, errb.String())
	}
	st, _ = ReadSprintState(dir)
	if st != StateStopped {
		t.Fatalf("want stopped after stop, got %s", st)
	}

	// 7. Status
	out.Reset()
	errb.Reset()
	code = SprintStatus(SprintStatusInput{
		Dir:    dir,
		Leases: leases,
		Stdout: &out,
		Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("status failed: %d, %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "state=stopped") || !strings.Contains(out.String(), "active=0") {
		t.Fatalf("unexpected status output: %s", out.String())
	}
}

// TestSprintDrain_Timeout verifies timeout returns exit 1.
func TestSprintDrain_Timeout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = WriteSprintState(dir, StateRunning)
	var out, errb bytes.Buffer

	leases := LeaseFunc(func() (int, error) { return 2, nil })
	currentTime := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)

	code := SprintDrain(SprintDrainInput{
		Dir:          dir,
		Leases:       leases,
		PollInterval: 10 * time.Millisecond,
		Timeout:      50 * time.Millisecond,
		Now:          func() time.Time { return currentTime },
		Sleep: func(d time.Duration) {
			currentTime = currentTime.Add(d)
		},
		Stdout: &out,
		Stderr: &errb,
	})

	if code != 1 {
		t.Fatalf("expected exit 1 on drain timeout, got %d", code)
	}
	if !strings.Contains(errb.String(), "SPRINT DRAIN TIMEOUT: 2 active leases remain") {
		t.Fatalf("unexpected stderr: %s", errb.String())
	}
}

// TestSprintStop_StrictRefusesLingeringLeases verifies strict stop refuses lingering active leases.
func TestSprintStop_StrictRefusesLingeringLeases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = WriteSprintState(dir, StateRunning)
	var out, errb bytes.Buffer

	leases := LeaseFunc(func() (int, error) { return 2, nil })
	code := SprintStop(SprintStopInput{
		Dir:    dir,
		Leases: leases,
		Strict: true,
		Stdout: &out,
		Stderr: &errb,
	})

	if code != 1 {
		t.Fatalf("expected exit 1 when strict stop encounters lingering leases, got %d", code)
	}
	if !strings.Contains(errb.String(), "SPRINT STOP WORKING: 2 active leases remain") {
		t.Fatalf("unexpected stderr: %s", errb.String())
	}
}

// TestMutation_LifecycleTeeth tests sensitivity to deliberate mutation of state constraints.
func TestMutation_LifecycleTeeth(t *testing.T) {
	t.Parallel()

	// Tooth 1: Pause must NOT succeed if already paused
	{
		sm := NewStateMachine(StatePaused)
		if err := sm.Pause(); err == nil {
			t.Fatalf("tooth 1 failed: pause succeeded when already paused")
		}
	}

	// Tooth 2: Resume must NOT succeed if running
	{
		sm := NewStateMachine(StateRunning)
		if err := sm.Resume(); err == nil {
			t.Fatalf("tooth 2 failed: resume succeeded when running")
		}
	}

	// Tooth 3: Start must NOT succeed if running
	{
		sm := NewStateMachine(StateRunning)
		if err := sm.Start(); err == nil {
			t.Fatalf("tooth 3 failed: start succeeded when running")
		}
	}

	// Tooth 4: Stop must cancel active tasks and record receipt
	{
		dir := t.TempDir()
		var cancelled bool
		sm := NewStateMachine(StateRunning, WithDir(dir), WithCancelFunc(func() {
			cancelled = true
		}))
		receipt, err := sm.Stop("test-stop")
		if err != nil {
			t.Fatalf("tooth 4 failed: stop error: %v", err)
		}
		if !cancelled {
			t.Fatalf("tooth 4 failed: cancel callback was not invoked")
		}
		if receipt == nil || receipt.State != StateStopped {
			t.Fatalf("tooth 4 failed: receipt not recorded: %+v", receipt)
		}
	}

	// Tooth 5: Strict SprintStop MUST refuse with exit 1 if active leases remain, and MUST NOT stamp SPRINT-END
	{
		dir := t.TempDir()
		_ = WriteSprintState(dir, StateRunning)
		var out, errb bytes.Buffer
		leases := LeaseFunc(func() (int, error) { return 1, nil })
		code := SprintStop(SprintStopInput{
			Dir:    dir,
			Leases: leases,
			Strict: true,
			Stdout: &out,
			Stderr: &errb,
		})
		if code != 1 {
			t.Fatalf("tooth 5 failed: strict stop did not refuse with exit 1, got %d", code)
		}
		if _, err := os.Stat(filepath.Join(dir, SprintEndFile)); !os.IsNotExist(err) {
			t.Fatalf("tooth 5 failed: SPRINT-END stamped despite active leases remaining")
		}
		st, _ := ReadSprintState(dir)
		if st != StateRunning {
			t.Fatalf("tooth 5 failed: state altered on refused stop, got %s", st)
		}
	}

	// Tooth 6: SprintDrain sets state to StateDraining, writes STOP, and NEVER stamps SPRINT-END
	{
		dir := t.TempDir()
		_ = WriteSprintState(dir, StateRunning)
		var out, errb bytes.Buffer
		leases := LeaseFunc(func() (int, error) { return 0, nil })
		code := SprintDrain(SprintDrainInput{
			Dir:    dir,
			Leases: leases,
			Stdout: &out,
			Stderr: &errb,
		})
		if code != 0 {
			t.Fatalf("tooth 6 failed: drain failed: %d, %s", code, errb.String())
		}
		st, _ := ReadSprintState(dir)
		if st != StateDraining {
			t.Fatalf("tooth 6 failed: want state draining, got %s", st)
		}
		if _, err := os.Stat(filepath.Join(dir, SprintStopFile)); err != nil {
			t.Fatalf("tooth 6 failed: STOP file missing after drain")
		}
		if _, err := os.Stat(filepath.Join(dir, SprintEndFile)); !os.IsNotExist(err) {
			t.Fatalf("tooth 6 failed: SPRINT-END stamped during drain (must be reserved for stop)")
		}
	}

	// Tooth 7: SprintDrain refuses if already stopped or idle
	{
		for _, invalid := range []SprintState{StateIdle, StateStopped} {
			dir := t.TempDir()
			_ = WriteSprintState(dir, invalid)
			var out, errb bytes.Buffer
			code := SprintDrain(SprintDrainInput{
				Dir:    dir,
				Stdout: &out,
				Stderr: &errb,
			})
			if code != 2 {
				t.Fatalf("tooth 7 failed: drain did not refuse from state %s (got %d)", invalid, code)
			}
			if !strings.Contains(errb.String(), "SPRINT REFUSED") {
				t.Fatalf("tooth 7 failed: expected SPRINT REFUSED, got %s", errb.String())
			}
		}
	}

	// Tooth 8: SprintStop refuses if idle (never started, nothing to stop)
	{
		dir := t.TempDir()
		_ = WriteSprintState(dir, StateIdle)
		var out, errb bytes.Buffer
		code := SprintStop(SprintStopInput{
			Dir:    dir,
			Stdout: &out,
			Stderr: &errb,
		})
		if code != 2 {
			t.Fatalf("tooth 8 failed: stop did not refuse from state %s (got %d)", StateIdle, code)
		}
		if !strings.Contains(errb.String(), "SPRINT REFUSED") {
			t.Fatalf("tooth 8 failed: expected SPRINT REFUSED, got %s", errb.String())
		}
	}

	// Tooth 9: SprintStop is idempotent when already stopped (safe retry
	// after a lost successful response), and MUST NOT refuse.
	{
		dir := t.TempDir()
		_ = WriteSprintState(dir, StateStopped)
		stamp := "2026-09-23T00:00:00Z"
		if err := os.WriteFile(filepath.Join(dir, SprintEndFile), []byte(stamp+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		code := SprintStop(SprintStopInput{
			Dir:    dir,
			Stdout: &out,
			Stderr: &errb,
		})
		if code != 0 {
			t.Fatalf("tooth 9 failed: idempotent re-stop refused (got %d): %s", code, errb.String())
		}
		if !strings.Contains(out.String(), "SPRINT-END "+stamp) {
			t.Fatalf("tooth 9 failed: expected prior SPRINT-END stamp echoed, got %s", out.String())
		}
		st, _ := ReadSprintState(dir)
		if st != StateStopped {
			t.Fatalf("tooth 9 failed: state changed on idempotent retry, got %s", st)
		}
	}

	// Tooth 10: forced (non-strict) SprintStop with lingering leases invokes
	// CancelTasks and reports the override on stderr instead of silently
	// stamping stopped.
	{
		dir := t.TempDir()
		_ = WriteSprintState(dir, StateRunning)
		var out, errb bytes.Buffer
		var cancelled bool
		leases := LeaseFunc(func() (int, error) { return 3, nil })
		code := SprintStop(SprintStopInput{
			Dir:         dir,
			Leases:      leases,
			Strict:      false,
			CancelTasks: func() { cancelled = true },
			Stdout:      &out,
			Stderr:      &errb,
		})
		if code != 0 {
			t.Fatalf("tooth 10 failed: forced stop refused (got %d): %s", code, errb.String())
		}
		if !cancelled {
			t.Fatalf("tooth 10 failed: CancelTasks was not invoked on forced stop with lingering leases")
		}
		if !strings.Contains(errb.String(), "SPRINT STOP FORCED: 3 active leases") {
			t.Fatalf("tooth 10 failed: expected forced-override audit line, got %s", errb.String())
		}
	}
}

func TestSprintStop_StrictDiscoversLaunchedCardsByDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	launchedDir := filepath.Join(dir, "launched")
	if err := os.MkdirAll(launchedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(launchedDir, "card-001.md"), []byte("active\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = WriteSprintState(dir, StateRunning)

	var out, errb bytes.Buffer
	code := SprintStop(SprintStopInput{
		Dir:    dir,
		Strict: true,
		Stdout: &out,
		Stderr: &errb,
	})
	if code != 1 {
		t.Fatalf("expected exit 1 when active card exists in <dir>/launched with Leases nil, got %d", code)
	}
	if !strings.Contains(errb.String(), "SPRINT STOP WORKING: 1 active leases remain") {
		t.Fatalf("unexpected stderr: %s", errb.String())
	}
}

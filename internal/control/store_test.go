package control_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/control"
)

func TestOpen(t *testing.T) {
	// Empty dir rejected
	if _, err := control.Open("", 30); err == nil {
		t.Fatal("expected error for empty controlDir, got nil")
	}
	if _, err := control.Open("   ", 30); err == nil {
		t.Fatal("expected error for whitespace controlDir, got nil")
	}

	// Non-positive maxRUN rejected
	tmpDir := t.TempDir()
	if _, err := control.Open(tmpDir, 0); err == nil {
		t.Fatal("expected error for maxRUN=0, got nil")
	}
	if _, err := control.Open(tmpDir, -5); err == nil {
		t.Fatal("expected error for maxRUN=-5, got nil")
	}

	// Valid Open creates controlDir and acks dir
	ctrlDir := filepath.Join(tmpDir, "ctrl")
	h, err := control.Open(ctrlDir, 60, control.WithLockTimeout(2*time.Second))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if h.Dir() != ctrlDir {
		t.Fatalf("expected Dir %s, got %s", ctrlDir, h.Dir())
	}
	if h.MaxRUNDuration() != 60*time.Second {
		t.Fatalf("expected MaxRUNDuration 60s, got %s", h.MaxRUNDuration())
	}

	if _, err := os.Stat(ctrlDir); err != nil {
		t.Fatalf("expected control directory to exist: %v", err)
	}
	if _, err := os.Stat(h.AcksDir()); err != nil {
		t.Fatalf("expected acks directory to exist: %v", err)
	}
}

func TestLoad_AbsentStateFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()
	_, err = h.Load(now)
	if err == nil {
		t.Fatal("expected error loading absent state, got nil")
	}
	if !errors.Is(err, control.ErrStateNotFound) {
		t.Fatalf("expected ErrStateNotFound, got %v", err)
	}
}

func TestUpdate_InitAndCAS(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 60)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()
	expires := now.Add(30 * time.Second)

	// Attempting CAS with expectedGeneration=1 when empty must fail
	_, err = h.Update(now, 1, control.State{
		Desired: control.DesiredRun,
		By:      "test-caller",
		Expires: &expires,
	})
	if err == nil {
		t.Fatal("expected CAS mismatch error when expectedGen=1 on empty store, got nil")
	}
	if !errors.Is(err, control.ErrGenerationMismatch) {
		t.Fatalf("expected ErrGenerationMismatch, got %v", err)
	}

	// Initialization from expectedGen=0 creates generation 1
	st1, err := h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "test-init",
		Reason:  "initial startup",
		Expires: &expires,
	})
	if err != nil {
		t.Fatalf("initial Update failed: %v", err)
	}
	if st1.Generation != 1 {
		t.Fatalf("expected generation 1, got %d", st1.Generation)
	}
	if st1.Desired != control.DesiredRun {
		t.Fatalf("expected desired RUN, got %s", st1.Desired)
	}
	if st1.Scope != control.ScopeFleet {
		t.Fatalf("expected scope fleet, got %s", st1.Scope)
	}

	// Verify durable state.json on disk
	loaded, err := h.Load(now)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Generation != 1 || loaded.Desired != control.DesiredRun {
		t.Fatalf("loaded state mismatch: %+v", loaded)
	}

	// Renewal: expectedGen=1 creates generation 2
	expires2 := now.Add(45 * time.Second)
	st2, err := h.Update(now, 1, control.State{
		Desired: control.DesiredRun,
		By:      "test-renew",
		Reason:  "renewal",
		Expires: &expires2,
	})
	if err != nil {
		t.Fatalf("renewal Update failed: %v", err)
	}
	if st2.Generation != 2 {
		t.Fatalf("expected generation 2, got %d", st2.Generation)
	}

	// Stale CAS expectedGen=1 must fail now that state is generation 2
	_, err = h.Update(now, 1, control.State{
		Desired: control.DesiredPause,
		By:      "stale-caller",
	})
	if err == nil {
		t.Fatal("expected CAS error for stale generation, got nil")
	}
	if !errors.Is(err, control.ErrGenerationMismatch) {
		t.Fatalf("expected ErrGenerationMismatch, got %v", err)
	}
}

func TestUpdate_RUNExpiryBounds(t *testing.T) {
	tmpDir := t.TempDir()
	// maxRUN is 30 seconds
	h, err := control.Open(tmpDir, 30)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()

	// Missing expires on RUN must fail
	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
	})
	if err == nil {
		t.Fatal("expected error for RUN without expires, got nil")
	}

	// Expires in the past must fail
	past := now.Add(-5 * time.Second)
	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
		Expires: &past,
	})
	if err == nil {
		t.Fatal("expected error for past expires, got nil")
	}

	// Expires equal to now must fail
	nowExact := now
	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
		Expires: &nowExact,
	})
	if err == nil {
		t.Fatal("expected error for expires == now, got nil")
	}

	// Expires exceeding maxRUN must fail
	tooFar := now.Add(31 * time.Second)
	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
		Expires: &tooFar,
	})
	if err == nil {
		t.Fatal("expected error for expires exceeding maxRUN, got nil")
	}

	// Valid expires within (now, now+maxRUN] succeeds
	validExpires := now.Add(25 * time.Second)
	st, err := h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
		Expires: &validExpires,
	})
	if err != nil {
		t.Fatalf("valid RUN update failed: %v", err)
	}
	if st.Generation != 1 {
		t.Fatalf("expected generation 1, got %d", st.Generation)
	}
}

func TestUpdate_PauseDrainStop_AfterExpiredRun(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 10)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	t0 := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	exp0 := t0.Add(5 * time.Second)

	// 1. Initial RUN at generation 1
	st1, err := h.Update(t0, 0, control.State{
		Desired: control.DesiredRun,
		By:      "coordinator",
		Expires: &exp0,
	})
	if err != nil {
		t.Fatalf("init update failed: %v", err)
	}
	if st1.Generation != 1 {
		t.Fatalf("expected generation 1, got %d", st1.Generation)
	}

	// 2. Advance time past expiry
	tExpired := t0.Add(10 * time.Second)
	loaded, err := h.Load(tExpired)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if !loaded.IsExpired(tExpired) {
		t.Fatal("expected state to be expired")
	}
	if err := loaded.IsValidAuthority(tExpired); err == nil {
		t.Fatal("expected IsValidAuthority to fail on expired state")
	}

	// 3. Updating to PAUSE after expired RUN must still work (generation 1 -> 2)
	st2, err := h.Update(tExpired, 1, control.State{
		Desired: control.DesiredPause,
		By:      "emergency-pause",
		Reason:  "pause fleet",
	})
	if err != nil {
		t.Fatalf("update to PAUSE after expired RUN failed: %v", err)
	}
	if st2.Generation != 2 || st2.Desired != control.DesiredPause {
		t.Fatalf("expected generation 2 PAUSE, got %+v", st2)
	}

	// 4. Update to DRAIN (generation 2 -> 3)
	st3, err := h.Update(tExpired, 2, control.State{
		Desired: control.DesiredDrain,
		By:      "drain-operator",
	})
	if err != nil {
		t.Fatalf("update to DRAIN failed: %v", err)
	}
	if st3.Generation != 3 || st3.Desired != control.DesiredDrain {
		t.Fatalf("expected generation 3 DRAIN, got %+v", st3)
	}

	// 5. Update to STOP (generation 3 -> 4)
	st4, err := h.Update(tExpired, 3, control.State{
		Desired: control.DesiredStop,
		By:      "stop-operator",
	})
	if err != nil {
		t.Fatalf("update to STOP failed: %v", err)
	}
	if st4.Generation != 4 || st4.Desired != control.DesiredStop {
		t.Fatalf("expected generation 4 STOP, got %+v", st4)
	}
}

func TestWithCoordinator_AuthorityEnforcement(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()
	ctx := context.Background()

	// 1. WithCoordinator on absent state: must fail without executing callback
	called := false
	err = h.WithCoordinator(ctx, now, func(st control.State) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("expected error on absent state, got nil")
	}
	if called {
		t.Fatal("callback must not be called on absent state")
	}

	// 2. State is PAUSE: must refuse authority
	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredPause,
		By:      "admin",
	})
	if err != nil {
		t.Fatalf("update to PAUSE failed: %v", err)
	}

	called = false
	err = h.WithCoordinator(ctx, now, func(st control.State) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("expected error when state is PAUSE, got nil")
	}
	if !errors.Is(err, control.ErrInvalidAuthority) {
		t.Fatalf("expected ErrInvalidAuthority, got %v", err)
	}
	if called {
		t.Fatal("callback must not be called when state is PAUSE")
	}

	// 3. State is RUN but expired: must refuse authority
	expPast := now.Add(5 * time.Second)
	_, err = h.Update(now, 1, control.State{
		Desired: control.DesiredRun,
		By:      "admin",
		Expires: &expPast,
	})
	if err != nil {
		t.Fatalf("update to RUN failed: %v", err)
	}

	called = false
	tPast := now.Add(10 * time.Second)
	err = h.WithCoordinator(ctx, tPast, func(st control.State) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("expected error when RUN is expired, got nil")
	}
	if !errors.Is(err, control.ErrInvalidAuthority) {
		t.Fatalf("expected ErrInvalidAuthority, got %v", err)
	}
	if called {
		t.Fatal("callback must not be called when RUN is expired")
	}

	// 4. Valid unexpired RUN: callback IS called and receives State
	expFuture := now.Add(20 * time.Second)
	_, err = h.Update(now, 2, control.State{
		Desired: control.DesiredRun,
		By:      "admin",
		Expires: &expFuture,
	})
	if err != nil {
		t.Fatalf("update to RUN gen 3 failed: %v", err)
	}

	called = false
	var receivedGen int64
	err = h.WithCoordinator(ctx, now, func(st control.State) error {
		called = true
		receivedGen = st.Generation
		return nil
	})
	if err != nil {
		t.Fatalf("WithCoordinator on valid RUN failed: %v", err)
	}
	if !called {
		t.Fatal("expected callback to be called on valid RUN")
	}
	if receivedGen != 3 {
		t.Fatalf("expected received generation 3, got %d", receivedGen)
	}
}

func TestWithCoordinator_CallbackErrorReconciliation(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()
	ctx := context.Background()
	exp := now.Add(20 * time.Second)

	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "admin",
		Expires: &exp,
	})
	if err != nil {
		t.Fatalf("init update failed: %v", err)
	}

	// Simulated callback error (e.g. durable STARTING append failed or crashed after append)
	simulatedErr := errors.New("simulated append failure")
	err = h.WithCoordinator(ctx, now, func(st control.State) error {
		return simulatedErr
	})
	if !errors.Is(err, simulatedErr) {
		t.Fatalf("expected simulated error returned, got %v", err)
	}

	// State on disk is not rolled back or mutated
	loaded, err := h.Load(now)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.Generation != 1 || loaded.Desired != control.DesiredRun {
		t.Fatalf("state was corrupted or rolled back: %+v", loaded)
	}

	// Subsequent WithCoordinator call still executes with valid authority
	secondCallSuccess := false
	err = h.WithCoordinator(ctx, now, func(st control.State) error {
		secondCallSuccess = true
		return nil
	})
	if err != nil {
		t.Fatalf("subsequent WithCoordinator failed: %v", err)
	}
	if !secondCallSuccess {
		t.Fatal("expected second WithCoordinator call to succeed")
	}
}

func TestWithCoordinator_LockMutualExclusion(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30, control.WithLockTimeout(500*time.Millisecond))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()
	ctx := context.Background()
	exp := now.Add(20 * time.Second)

	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "admin",
		Expires: &exp,
	})
	if err != nil {
		t.Fatalf("init update failed: %v", err)
	}

	var active int32
	var maxActive int32
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := h.WithCoordinator(ctx, now, func(st control.State) error {
				curr := atomic.AddInt32(&active, 1)
				for {
					max := atomic.LoadInt32(&maxActive)
					if curr <= max || atomic.CompareAndSwapInt32(&maxActive, max, curr) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				atomic.AddInt32(&active, -1)
				return nil
			})
			if err != nil {
				t.Errorf("concurrent WithCoordinator error: %v", err)
			}
		}()
	}

	wg.Wait()

	if maxActive > 1 {
		t.Fatalf("coordinator.lock violated mutual exclusion: maxActive was %d", maxActive)
	}
}

func TestWriteAck_And_LoadAcks(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()

	// Invalid owner path traversal rejected
	err = h.WriteAck(now, control.Ack{
		Generation: 1,
		Owner:      "../evil",
		Bench:      "bench-1",
		Desired:    control.DesiredRun,
	})
	if err == nil {
		t.Fatal("expected error for path traversal in ack owner, got nil")
	}

	// Valid acks for two separate bench owners
	ack1 := control.Ack{
		Generation: 1,
		Owner:      "bench-1",
		Bench:      "bench-1",
		Desired:    control.DesiredRun,
		Observed:   now,
	}
	ack2 := control.Ack{
		Generation: 1,
		Owner:      "bench-2",
		Bench:      "bench-2",
		Desired:    control.DesiredRun,
		Observed:   now,
	}

	if err := h.WriteAck(now, ack1); err != nil {
		t.Fatalf("WriteAck 1 failed: %v", err)
	}
	if err := h.WriteAck(now, ack2); err != nil {
		t.Fatalf("WriteAck 2 failed: %v", err)
	}

	// Verify separate files exist
	if _, err := os.Stat(h.AckPath("bench-1")); err != nil {
		t.Fatalf("ack file for bench-1 does not exist: %v", err)
	}
	if _, err := os.Stat(h.AckPath("bench-2")); err != nil {
		t.Fatalf("ack file for bench-2 does not exist: %v", err)
	}

	// Load individual ack
	loadedAck, err := h.LoadAck("bench-1")
	if err != nil {
		t.Fatalf("LoadAck failed: %v", err)
	}
	if loadedAck.Owner != "bench-1" || loadedAck.Generation != 1 || loadedAck.Desired != control.DesiredRun {
		t.Fatalf("loaded ack mismatch: %+v", loadedAck)
	}

	// Load all acks
	allAcks, err := h.LoadAcks()
	if err != nil {
		t.Fatalf("LoadAcks failed: %v", err)
	}
	if len(allAcks) != 2 {
		t.Fatalf("expected 2 acks, got %d", len(allAcks))
	}
}

func TestStatusAggregation(t *testing.T) {
	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()
	exp := now.Add(20 * time.Second)

	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "admin",
		Expires: &exp,
	})
	if err != nil {
		t.Fatalf("init update failed: %v", err)
	}

	requiredOwners := []string{"bench-1", "bench-2", "bench-3"}

	// Before any acks written: 0 acked, 3 pending, complete=false
	status, err := h.Status(now, requiredOwners)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.Acked != 0 || status.Pending != 3 || status.Complete {
		t.Fatalf("unexpected status: %+v", status)
	}

	// bench-1 and bench-2 write matching ack
	_ = h.WriteAck(now, control.Ack{Generation: 1, Owner: "bench-1", Bench: "bench-1", Desired: control.DesiredRun})
	_ = h.WriteAck(now, control.Ack{Generation: 1, Owner: "bench-2", Bench: "bench-2", Desired: control.DesiredRun})

	status, err = h.Status(now, requiredOwners)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.Acked != 2 || status.Pending != 1 || status.Complete {
		t.Fatalf("unexpected status after 2 acks: %+v", status)
	}

	// bench-3 writes matching ack: complete becomes true!
	_ = h.WriteAck(now, control.Ack{Generation: 1, Owner: "bench-3", Bench: "bench-3", Desired: control.DesiredRun})

	status, err = h.Status(now, requiredOwners)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.Acked != 3 || status.Pending != 0 || !status.Complete {
		t.Fatalf("unexpected status after all acks: %+v", status)
	}
}

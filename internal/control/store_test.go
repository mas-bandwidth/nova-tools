package control_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/control"
)

func TestOpen(t *testing.T) {
	t.Parallel()

	if _, err := control.Open("", 30*time.Second); err == nil {
		t.Fatal("expected error for empty controlDir, got nil")
	}
	if _, err := control.Open("   ", 30*time.Second); err == nil {
		t.Fatal("expected error for whitespace controlDir, got nil")
	}

	tmpDir := t.TempDir()
	if _, err := control.Open(tmpDir, 0); err == nil {
		t.Fatal("expected error for maxRUN=0, got nil")
	}
	if _, err := control.Open(tmpDir, -5); err == nil {
		t.Fatal("expected error for negative maxRUN, got nil")
	}

	ctrlDir := filepath.Join(tmpDir, "ctrl")
	h, err := control.Open(ctrlDir, 60*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if _, err := os.Stat(ctrlDir); err != nil {
		t.Fatalf("expected control directory to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ctrlDir, "acks")); err != nil {
		t.Fatalf("expected acks directory to exist: %v", err)
	}

	now := time.Now().UTC()
	tooFar := now.Add(61 * time.Second)
	if _, err := h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
		Expires: &tooFar,
	}); err == nil {
		t.Fatal("expected error for expires exceeding maxRUN duration")
	}
	okExp := now.Add(30 * time.Second)
	if _, err := h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
		Expires: &okExp,
	}); err != nil {
		t.Fatalf("valid RUN within maxRUN duration failed: %v", err)
	}
}

func TestOpen_MaxRUNIsDurationNotIntegerHeuristic(t *testing.T) {
	t.Parallel()

	// 999999 as an integer used to mean seconds (~11 days) while 1000000 was
	// treated as nanoseconds. Open takes time.Duration, so 999999ns is 999999ns.
	h, err := control.Open(t.TempDir(), 999999*time.Nanosecond)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	now := time.Now().UTC()
	exp := now.Add(time.Second)
	if _, err := h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
		Expires: &exp,
	}); err == nil {
		t.Fatal("999999 nanoseconds must not be treated as 999999 seconds")
	}

	h2, err := control.Open(t.TempDir(), time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	short := now.Add(500 * time.Millisecond)
	if _, err := h2.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
		Expires: &short,
	}); err != nil {
		t.Fatalf("1s maxRUN must accept a 500ms expiry: %v", err)
	}
}

func TestLoad_AbsentStateFailsClosed(t *testing.T) {
	t.Parallel()

	h, err := control.Open(t.TempDir(), 30*time.Second)
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
	t.Parallel()

	h, err := control.Open(t.TempDir(), 60*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()
	expires := now.Add(30 * time.Second)

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

	loaded, err := h.Load(now)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Generation != 1 || loaded.Desired != control.DesiredRun {
		t.Fatalf("loaded state mismatch: %+v", loaded)
	}

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
	t.Parallel()

	h, err := control.Open(t.TempDir(), 30*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()

	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
	})
	if err == nil {
		t.Fatal("expected error for RUN without expires, got nil")
	}

	past := now.Add(-5 * time.Second)
	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
		Expires: &past,
	})
	if err == nil {
		t.Fatal("expected error for past expires, got nil")
	}

	nowExact := now
	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
		Expires: &nowExact,
	})
	if err == nil {
		t.Fatal("expected error for expires == now, got nil")
	}

	tooFar := now.Add(31 * time.Second)
	_, err = h.Update(now, 0, control.State{
		Desired: control.DesiredRun,
		By:      "tester",
		Expires: &tooFar,
	})
	if err == nil {
		t.Fatal("expected error for expires exceeding maxRUN, got nil")
	}

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
	t.Parallel()

	h, err := control.Open(t.TempDir(), 10*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	t0 := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC)
	exp0 := t0.Add(5 * time.Second)

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

func TestUpdate_NonRUNExpiresRefused(t *testing.T) {
	t.Parallel()

	h, err := control.Open(t.TempDir(), 30*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	now := time.Now().UTC()
	exp := now.Add(10 * time.Second)
	if _, err := h.Update(now, 0, control.State{
		Desired: control.DesiredStop,
		By:      "admin",
		Expires: &exp,
	}); err == nil {
		t.Fatal("STOP with expires must be refused")
	}
}

func TestWithCoordinator_AuthorityEnforcement(t *testing.T) {
	t.Parallel()

	h, err := control.Open(t.TempDir(), 30*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()
	ctx := context.Background()

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
	t.Parallel()

	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30*time.Second)
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

	marker := filepath.Join(tmpDir, "starting.marker")
	simulatedErr := errors.New("simulated append failure")
	err = h.WithCoordinator(ctx, now, func(st control.State) error {
		if werr := os.WriteFile(marker, []byte("STARTING\n"), 0o644); werr != nil {
			return werr
		}
		return simulatedErr
	})
	if !errors.Is(err, control.ErrAmbiguous) {
		t.Fatalf("expected ErrAmbiguous, got %v", err)
	}
	if !errors.Is(err, simulatedErr) {
		t.Fatalf("ambiguous error must preserve the cause, got %v", err)
	}
	var amb *control.AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("expected *AmbiguousError, got %T", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("durable STARTING marker must remain: %v", statErr)
	}

	loaded, err := h.Load(now)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.Generation != 1 || loaded.Desired != control.DesiredRun {
		t.Fatalf("state was corrupted or rolled back: %+v", loaded)
	}

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
	t.Parallel()

	h, err := control.Open(t.TempDir(), 30*time.Second)
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

func TestWriteAck_And_LoadAck(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	now := time.Now().UTC()

	err = h.WriteAck(now, control.Ack{
		Generation: 1,
		Owner:      "../evil",
		Bench:      "bench-1",
		Desired:    control.DesiredRun,
	})
	if err == nil {
		t.Fatal("expected error for path traversal in ack owner, got nil")
	}

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

	if _, err := os.Stat(filepath.Join(tmpDir, "acks", "bench-1.json")); err != nil {
		t.Fatalf("ack file for bench-1 does not exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "acks", "bench-2.json")); err != nil {
		t.Fatalf("ack file for bench-2 does not exist: %v", err)
	}

	loadedAck, err := h.LoadAck("bench-1")
	if err != nil {
		t.Fatalf("LoadAck failed: %v", err)
	}
	if loadedAck.Owner != "bench-1" || loadedAck.Generation != 1 || loadedAck.Desired != control.DesiredRun {
		t.Fatalf("loaded ack mismatch: %+v", loadedAck)
	}
}

func TestLoadAck_OwnerMustMatchFilename(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	now := time.Now().UTC()
	acks := filepath.Join(tmpDir, "acks")
	body := fmt.Sprintf(`{
  "generation": 1,
  "owner": "other-bench",
  "bench": "other-bench",
  "desired": "RUN",
  "observed": %q
}
`, now.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(acks, "bench-1.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = h.LoadAck("bench-1")
	if err == nil {
		t.Fatal("LoadAck must refuse a file whose JSON owner does not match the filename")
	}
	if !errors.Is(err, control.ErrInvalidAck) {
		t.Fatalf("expected ErrInvalidAck, got %v", err)
	}
}

func TestWriteAck_RefusesBenchRebind(t *testing.T) {
	t.Parallel()

	h, err := control.Open(t.TempDir(), 30*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	now := time.Now().UTC()
	if err := h.WriteAck(now, control.Ack{
		Generation: 1,
		Owner:      "mac-studio",
		Bench:      "mac-studio",
		Desired:    control.DesiredRun,
	}); err != nil {
		t.Fatalf("first WriteAck failed: %v", err)
	}
	err = h.WriteAck(now, control.Ack{
		Generation: 2,
		Owner:      "mac-studio",
		Bench:      "other-bench",
		Desired:    control.DesiredRun,
	})
	if err == nil {
		t.Fatal("WriteAck must preserve the owner-to-bench binding")
	}
	loaded, err := h.LoadAck("mac-studio")
	if err != nil {
		t.Fatalf("LoadAck after refused rebind: %v", err)
	}
	if loaded.Bench != "mac-studio" {
		t.Fatalf("bench binding was overwritten: %+v", loaded)
	}
	if err := h.WriteAck(now, control.Ack{
		Generation: 2,
		Owner:      "mac-studio",
		Bench:      "mac-studio",
		Desired:    control.DesiredPause,
	}); err != nil {
		t.Fatalf("same-bench replacement must be allowed: %v", err)
	}
}

func TestWriteAck_ConcurrentCompetingBenchesRefused(t *testing.T) {
	t.Parallel()

	h, err := control.Open(t.TempDir(), 30*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	const n = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)

	results := make([]error, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx] = h.WriteAck(time.Now().UTC(), control.Ack{
				Generation: 1,
				Owner:      "shared-owner",
				Bench:      fmt.Sprintf("bench-%d", idx),
				Desired:    control.DesiredRun,
			})
		}(i)
	}

	close(start)
	wg.Wait()

	var successCount int
	var failCount int
	for _, resErr := range results {
		if resErr == nil {
			successCount++
		} else if errors.Is(resErr, control.ErrInvalidAck) {
			failCount++
		} else {
			t.Errorf("unexpected error: %v", resErr)
		}
	}

	if successCount != 1 {
		t.Fatalf("expected exactly 1 success, got %d", successCount)
	}
	if failCount != n-1 {
		t.Fatalf("expected exactly %d rebind refusals, got %d", n-1, failCount)
	}

	loaded, err := h.LoadAck("shared-owner")
	if err != nil {
		t.Fatalf("LoadAck failed: %v", err)
	}
	var winningBench string
	for i, resErr := range results {
		if resErr == nil {
			winningBench = fmt.Sprintf("bench-%d", i)
			break
		}
	}
	if loaded.Bench != winningBench {
		t.Fatalf("loaded bench %q != winning bench %q", loaded.Bench, winningBench)
	}
}

func TestLoad_UnknownFieldsRefused(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	now := time.Now().UTC()
	exp := now.Add(20 * time.Second)
	body := fmt.Sprintf(`{
  "generation": 1,
  "desired": "RUN",
  "scope": "fleet",
  "by": "admin",
  "at": %q,
  "expires": %q,
  "extra": true
}
`, now.Format(time.RFC3339Nano), exp.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(tmpDir, "state.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(now); err == nil {
		t.Fatal("Load must refuse unknown JSON fields")
	}
}

func TestLoad_TrailingJSONRefused(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	now := time.Now().UTC()
	exp := now.Add(20 * time.Second)
	body := fmt.Sprintf(`{
  "generation": 1,
  "desired": "RUN",
  "scope": "fleet",
  "by": "admin",
  "at": %q,
  "expires": %q
}
{"generation": 99}
`, now.Format(time.RFC3339Nano), exp.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(tmpDir, "state.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(now); err == nil {
		t.Fatal("Load must refuse a trailing JSON value")
	}
}

func TestLoadAck_UnknownAndTrailingJSONRefused(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	now := time.Now().UTC()
	acks := filepath.Join(tmpDir, "acks")
	unknown := fmt.Sprintf(`{
  "generation": 1,
  "owner": "bench-1",
  "bench": "bench-1",
  "desired": "RUN",
  "observed": %q,
  "extra": true
}
`, now.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(acks, "bench-1.json"), []byte(unknown), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.LoadAck("bench-1"); err == nil {
		t.Fatal("LoadAck must refuse unknown JSON fields")
	}

	trailing := fmt.Sprintf(`{
  "generation": 1,
  "owner": "bench-2",
  "bench": "bench-2",
  "desired": "RUN",
  "observed": %q
}
{"generation": 9}
`, now.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(acks, "bench-2.json"), []byte(trailing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.LoadAck("bench-2"); err == nil {
		t.Fatal("LoadAck must refuse a trailing JSON value")
	}
}

func TestOpen_SymlinkedControlDirRefused(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	root := t.TempDir()
	link := filepath.Join(root, "ctrl")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("this filesystem will not make a symlink: %v", err)
	}
	_, err := control.Open(link, 30*time.Second)
	if err == nil {
		t.Fatal("Open must refuse a symlinked control directory")
	}
	if !errors.Is(err, control.ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath, got %v", err)
	}
	if entries, err := os.ReadDir(outside); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("Open wrote through the symlink into %s: %v", outside, names(entries))
	}
}

func TestWriteAck_SymlinkedAcksDirRefused(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	h, err := control.Open(tmpDir, 30*time.Second)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	outside := t.TempDir()
	acks := filepath.Join(tmpDir, "acks")
	if err := os.RemoveAll(acks); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, acks); err != nil {
		t.Skipf("this filesystem will not make a symlink: %v", err)
	}
	err = h.WriteAck(time.Now().UTC(), control.Ack{
		Generation: 1,
		Owner:      "bench-1",
		Bench:      "bench-1",
		Desired:    control.DesiredRun,
	})
	if err == nil {
		t.Fatal("WriteAck must refuse a symlinked acks directory")
	}
	if !errors.Is(err, control.ErrUnsafePath) {
		t.Fatalf("expected ErrUnsafePath, got %v", err)
	}
	if entries, err := os.ReadDir(outside); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("WriteAck escaped into %s: %v", outside, names(entries))
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

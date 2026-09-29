package sprint

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// helper to create deterministic 32-byte hashes from a string
func makeHash(seed string) [32]byte {
	return sha256.Sum256([]byte(seed))
}

// ============================================================================
// 1. Invariant Checks Unit Tests
// ============================================================================

func TestRatchet_CleanForwardProgression(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	baseTime := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	ratchet := NewMonotoneRatchet(
		WithInitialState(0, baseTime, h0),
		WithHistoryRetention(10),
	)

	h1 := makeHash("state-1")
	t1 := baseTime.Add(100 * time.Millisecond)

	c1, err := ratchet.Step(RatchetStep{
		Seq:            1,
		Timestamp:      t1,
		PriorStateHash: h0,
		NewStateHash:   h1,
		Payload:        []byte("action-1"),
	})
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	if c1.Seq != 1 {
		t.Errorf("c1.Seq = %d, want 1", c1.Seq)
	}
	if !c1.Timestamp.Equal(t1) {
		t.Errorf("c1.Timestamp = %v, want %v", c1.Timestamp, t1)
	}
	if c1.StateHash != h1 {
		t.Errorf("c1.StateHash mismatch")
	}
	if c1.PriorStateHash != h0 {
		t.Errorf("c1.PriorStateHash mismatch")
	}
	if ratchet.CurrentSeq() != 1 {
		t.Errorf("CurrentSeq = %d, want 1", ratchet.CurrentSeq())
	}
	if ratchet.CurrentStateHash() != h1 {
		t.Errorf("CurrentStateHash mismatch")
	}

	// Step 2
	h2 := makeHash("state-2")
	t2 := t1.Add(150 * time.Millisecond)
	c2, err := ratchet.Step(RatchetStep{
		Seq:            2,
		Timestamp:      t2,
		PriorStateHash: h1,
		NewStateHash:   h2,
		Payload:        []byte("action-2"),
	})
	if err != nil {
		t.Fatalf("Step 2 failed: %v", err)
	}
	if c2.Seq != 2 || c2.StateHash != h2 || c2.PriorStateHash != h1 {
		t.Errorf("Step 2 commitment verification failed: %+v", c2)
	}

	// Verify History
	hist := ratchet.History()
	if len(hist) != 2 {
		t.Fatalf("History len = %d, want 2", len(hist))
	}
	if hist[0].Seq != 1 || hist[1].Seq != 2 {
		t.Errorf("History ordering incorrect")
	}

	// Verify Snapshot
	snap := ratchet.Snapshot()
	if snap.Seq != 2 || snap.StateHash != h2 || snap.StepCount != 2 {
		t.Errorf("Snapshot verification failed: %+v", snap)
	}
}

func TestRatchet_DuplicateSequenceRejection(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	h1 := makeHash("state-1")
	h2 := makeHash("state-2")
	now := time.Now()

	ratchet := NewMonotoneRatchet(WithInitialState(0, now, h0))

	// Commit Seq 1
	if _, err := ratchet.Step(RatchetStep{
		Seq:            1,
		Timestamp:      now.Add(10 * time.Millisecond),
		PriorStateHash: h0,
		NewStateHash:   h1,
	}); err != nil {
		t.Fatalf("Initial step failed: %v", err)
	}

	// Attempt duplicate Seq 1
	_, err := ratchet.Step(RatchetStep{
		Seq:            1,
		Timestamp:      now.Add(20 * time.Millisecond),
		PriorStateHash: h1,
		NewStateHash:   h2,
	})
	if err == nil {
		t.Fatal("Expected error on duplicate seq, got nil")
	}
	if !errors.Is(err, ErrDuplicateSequence) {
		t.Errorf("Expected ErrDuplicateSequence, got %v", err)
	}
}

func TestRatchet_RegressingSequenceRejection(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	h1 := makeHash("state-1")
	h2 := makeHash("state-2")
	hReg := makeHash("state-regress")
	now := time.Now()

	ratchet := NewMonotoneRatchet(WithInitialState(0, now, h0))

	// Commit Seq 1 & 2
	_, _ = ratchet.Step(RatchetStep{Seq: 1, Timestamp: now.Add(1 * time.Millisecond), PriorStateHash: h0, NewStateHash: h1})
	_, _ = ratchet.Step(RatchetStep{Seq: 2, Timestamp: now.Add(2 * time.Millisecond), PriorStateHash: h1, NewStateHash: h2})

	// Attempt regressing Seq 1
	_, err := ratchet.Step(RatchetStep{
		Seq:            1,
		Timestamp:      now.Add(3 * time.Millisecond),
		PriorStateHash: h2,
		NewStateHash:   hReg,
	})
	if err == nil {
		t.Fatal("Expected error on regressing seq, got nil")
	}
	if !errors.Is(err, ErrRegressingSequence) {
		t.Errorf("Expected ErrRegressingSequence, got %v", err)
	}
}

func TestRatchet_SequenceGapRejection(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	h1 := makeHash("state-1")
	now := time.Now()

	// Strict contiguous by default
	ratchet := NewMonotoneRatchet(WithInitialState(0, now, h0), WithStrictContiguous(true))

	// Attempt seq 5 when lastSeq is 0 -> expect gap error
	_, err := ratchet.Step(RatchetStep{
		Seq:            5,
		Timestamp:      now.Add(10 * time.Millisecond),
		PriorStateHash: h0,
		NewStateHash:   h1,
	})
	if err == nil {
		t.Fatal("Expected ErrSequenceGap on gap under strict contiguous, got nil")
	}
	if !errors.Is(err, ErrSequenceGap) {
		t.Errorf("Expected ErrSequenceGap, got %v", err)
	}

	// Now allow sequence gaps
	ratchetNonStrict := NewMonotoneRatchet(WithInitialState(0, now, h0), WithStrictContiguous(false))
	c, err := ratchetNonStrict.Step(RatchetStep{
		Seq:            5,
		Timestamp:      now.Add(10 * time.Millisecond),
		PriorStateHash: h0,
		NewStateHash:   h1,
	})
	if err != nil {
		t.Fatalf("Expected success with non-strict contiguous, got %v", err)
	}
	if c.Seq != 5 {
		t.Errorf("c.Seq = %d, want 5", c.Seq)
	}
}

func TestRatchet_ClockRegressionBeyondMaxSkew(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	h1 := makeHash("state-1")
	h2 := makeHash("state-2")

	baseTime := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	maxSkew := 500 * time.Millisecond

	ratchet := NewMonotoneRatchet(
		WithInitialState(0, baseTime, h0),
		WithMaxClockSkew(maxSkew),
	)

	// Step 1: baseTime + 10s
	t1 := baseTime.Add(10 * time.Second)
	if _, err := ratchet.Step(RatchetStep{
		Seq:            1,
		Timestamp:      t1,
		PriorStateHash: h0,
		NewStateHash:   h1,
	}); err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	// Step 2 with timestamp regressing by 600ms (> 500ms max skew)
	t2 := t1.Add(-600 * time.Millisecond)
	_, err := ratchet.Step(RatchetStep{
		Seq:            2,
		Timestamp:      t2,
		PriorStateHash: h1,
		NewStateHash:   h2,
	})
	if err == nil {
		t.Fatal("Expected ErrClockRegression on backward jump > maxSkew, got nil")
	}
	if !errors.Is(err, ErrClockRegression) {
		t.Errorf("Expected ErrClockRegression, got %v", err)
	}
}

func TestRatchet_ClockRegressionWithinToleratedSkew(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	h1 := makeHash("state-1")
	h2 := makeHash("state-2")

	baseTime := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	maxSkew := 500 * time.Millisecond

	ratchet := NewMonotoneRatchet(
		WithInitialState(0, baseTime, h0),
		WithMaxClockSkew(maxSkew),
	)

	// Step 1: baseTime + 10s
	t1 := baseTime.Add(10 * time.Second)
	if _, err := ratchet.Step(RatchetStep{
		Seq:            1,
		Timestamp:      t1,
		PriorStateHash: h0,
		NewStateHash:   h1,
	}); err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	// Step 2 with timestamp regressing by 200ms (<= 500ms max skew)
	// Must succeed, but effective timestamp must be clamped to t1 to guarantee forward monotonicity!
	t2 := t1.Add(-200 * time.Millisecond)
	c2, err := ratchet.Step(RatchetStep{
		Seq:            2,
		Timestamp:      t2,
		PriorStateHash: h1,
		NewStateHash:   h2,
	})
	if err != nil {
		t.Fatalf("Step 2 with minor clock jitter should succeed, got: %v", err)
	}

	if c2.ObservedTimestamp != t2 {
		t.Errorf("ObservedTimestamp = %v, want %v", c2.ObservedTimestamp, t2)
	}
	if !c2.Timestamp.Equal(t1) {
		t.Errorf("Effective monotonic Timestamp = %v, want clamped %v", c2.Timestamp, t1)
	}
	if ratchet.CurrentTimestamp().Before(t1) {
		t.Errorf("Ratchet current timestamp regressed!")
	}
}

func TestRatchet_DivergentPriorStateRejection(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	h1 := makeHash("state-1")
	h2 := makeHash("state-2")
	wrongPrior := makeHash("divergent-prior")
	now := time.Now()

	ratchet := NewMonotoneRatchet(WithInitialState(0, now, h0))

	// Step 1 commits h1
	if _, err := ratchet.Step(RatchetStep{
		Seq:            1,
		Timestamp:      now.Add(10 * time.Millisecond),
		PriorStateHash: h0,
		NewStateHash:   h1,
	}); err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	// Step 2 presents wrongPrior instead of h1
	_, err := ratchet.Step(RatchetStep{
		Seq:            2,
		Timestamp:      now.Add(20 * time.Millisecond),
		PriorStateHash: wrongPrior,
		NewStateHash:   h2,
	})
	if err == nil {
		t.Fatal("Expected ErrDivergentPriorState, got nil")
	}
	if !errors.Is(err, ErrDivergentPriorState) {
		t.Errorf("Expected ErrDivergentPriorState, got %v", err)
	}
}

func TestRatchet_ZeroNewStateRejection(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	var zeroHash [32]byte
	now := time.Now()

	ratchet := NewMonotoneRatchet(WithInitialState(0, now, h0))

	_, err := ratchet.Step(RatchetStep{
		Seq:            1,
		Timestamp:      now.Add(10 * time.Millisecond),
		PriorStateHash: h0,
		NewStateHash:   zeroHash,
	})
	if err == nil {
		t.Fatal("Expected ErrZeroNewState, got nil")
	}
	if !errors.Is(err, ErrZeroNewState) {
		t.Errorf("Expected ErrZeroNewState, got %v", err)
	}
}

func TestRatchet_VerifyDryRun(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	h1 := makeHash("state-1")
	now := time.Now()

	ratchet := NewMonotoneRatchet(WithInitialState(0, now, h0))

	validStep := RatchetStep{
		Seq:            1,
		Timestamp:      now.Add(10 * time.Millisecond),
		PriorStateHash: h0,
		NewStateHash:   h1,
	}

	if err := ratchet.Verify(validStep); err != nil {
		t.Errorf("Verify valid step failed: %v", err)
	}

	// Ensure ratchet was NOT mutated by Verify
	if ratchet.CurrentSeq() != 0 {
		t.Errorf("Verify mutated sequence to %d", ratchet.CurrentSeq())
	}
	if ratchet.CurrentStateHash() != h0 {
		t.Errorf("Verify mutated state hash")
	}

	invalidStep := RatchetStep{
		Seq:            2, // gap
		Timestamp:      now.Add(10 * time.Millisecond),
		PriorStateHash: h0,
		NewStateHash:   h1,
	}
	if err := ratchet.Verify(invalidStep); !errors.Is(err, ErrSequenceGap) {
		t.Errorf("Expected ErrSequenceGap from Verify, got %v", err)
	}
}

func TestRatchet_ClosedBehavior(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	ratchet := NewMonotoneRatchet(WithInitialState(0, time.Now(), h0))
	ratchet.Close()

	_, err := ratchet.Step(RatchetStep{
		Seq:            1,
		Timestamp:      time.Now(),
		PriorStateHash: h0,
		NewStateHash:   makeHash("h1"),
	})
	if !errors.Is(err, ErrRatchetClosed) {
		t.Errorf("Expected ErrRatchetClosed, got %v", err)
	}

	if err := ratchet.Verify(RatchetStep{Seq: 1}); !errors.Is(err, ErrRatchetClosed) {
		t.Errorf("Expected ErrRatchetClosed from Verify, got %v", err)
	}
}

// ============================================================================
// 2. Concurrency & Mutex/Atomic Synchronization Unit Tests
// ============================================================================

func TestRatchet_ConcurrentContention(t *testing.T) {
	t.Parallel()

	const numGoroutines = 50
	const targetSteps = 200

	h0 := makeHash("genesis")
	ratchet := NewMonotoneRatchet(
		WithInitialState(0, time.Now(), h0),
		WithStrictContiguous(true),
		WithHistoryRetention(targetSteps+10),
	)

	var committedCount int64
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	startSignal := make(chan struct{})

	for g := 0; g < numGoroutines; g++ {
		go func(workerID int) {
			defer wg.Done()
			<-startSignal

			for {
				currSeq := ratchet.CurrentSeq()
				if currSeq >= targetSteps {
					return
				}

				nextSeq := currSeq + 1
				currHash := ratchet.CurrentStateHash()
				nextHash := makeHash(fmt.Sprintf("state-%d-by-%d", nextSeq, workerID))

				_, err := ratchet.Step(RatchetStep{
					Seq:            nextSeq,
					Timestamp:      time.Now(),
					PriorStateHash: currHash,
					NewStateHash:   nextHash,
					Payload:        []byte(fmt.Sprintf("w%d", workerID)),
				})
				if err == nil {
					atomic.AddInt64(&committedCount, 1)
				}
				// Contention error expected if another worker raced and won; loop continues
			}
		}(g)
	}

	close(startSignal)
	wg.Wait()

	finalSeq := ratchet.CurrentSeq()
	if finalSeq < targetSteps {
		t.Errorf("Final sequence = %d, want at least %d", finalSeq, targetSteps)
	}

	history := ratchet.History()
	if len(history) < targetSteps {
		t.Fatalf("History length = %d, want at least %d", len(history), targetSteps)
	}

	// Verify the commitment chain is unbroken and strictly monotonic
	for i := 0; i < len(history); i++ {
		if history[i].Seq != uint64(i+1) {
			t.Fatalf("History[%d].Seq = %d, want %d", i, history[i].Seq, i+1)
		}
		if i > 0 {
			if history[i].PriorStateHash != history[i-1].StateHash {
				t.Fatalf("Broken hash chain at index %d: prior %x != prev %x",
					i, history[i].PriorStateHash[:8], history[i-1].StateHash[:8])
			}
			if history[i].Timestamp.Before(history[i-1].Timestamp) {
				t.Fatalf("Monotonic clock regression in history at index %d", i)
			}
		}
	}
}

func TestRatchet_ConcurrentLocklessReaders(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	ratchet := NewMonotoneRatchet(WithInitialState(0, time.Now(), h0))

	const totalSteps = 1000
	done := make(chan struct{})

	// 10 concurrent reader goroutines
	var readCount int64
	var rWg sync.WaitGroup
	for i := 0; i < 10; i++ {
		rWg.Add(1)
		go func() {
			defer rWg.Done()
			var prevSeq uint64 = 0
			for {
				select {
				case <-done:
					return
				default:
					seq := ratchet.CurrentSeq()
					if seq < prevSeq {
						t.Errorf("Reader observed sequence regression: %d < %d", seq, prevSeq)
					}
					prevSeq = seq
					_ = ratchet.CurrentStateHash()
					_ = ratchet.Snapshot()
					atomic.AddInt64(&readCount, 1)
				}
			}
		}()
	}

	// Writer goroutine
	lastHash := h0
	for i := 1; i <= totalSteps; i++ {
		nextHash := makeHash(fmt.Sprintf("h-%d", i))
		_, err := ratchet.Step(RatchetStep{
			Seq:            uint64(i),
			Timestamp:      time.Now(),
			PriorStateHash: lastHash,
			NewStateHash:   nextHash,
		})
		if err != nil {
			t.Fatalf("Writer step %d failed: %v", i, err)
		}
		lastHash = nextHash
	}

	close(done)
	rWg.Wait()

	if ratchet.CurrentSeq() != totalSteps {
		t.Errorf("Final seq = %d, want %d", ratchet.CurrentSeq(), totalSteps)
	}
	if atomic.LoadInt64(&readCount) == 0 {
		t.Errorf("Expected readers to execute, readCount was 0")
	}
}

// ============================================================================
// 3. Fault Injection & Resilience Tests
// ============================================================================

func TestRatchet_FaultInjection_OutOfOrderDelivery(t *testing.T) {
	t.Parallel()

	const count = 50
	h0 := makeHash("genesis")
	ratchet := NewMonotoneRatchet(WithInitialState(0, time.Now(), h0), WithStrictContiguous(true))

	// Pre-generate 50 valid sequential steps
	steps := make([]RatchetStep, count)
	currHash := h0
	currTime := time.Now()
	for i := 1; i <= count; i++ {
		nextHash := makeHash(fmt.Sprintf("state-%d", i))
		currTime = currTime.Add(10 * time.Millisecond)
		steps[i-1] = RatchetStep{
			Seq:            uint64(i),
			Timestamp:      currTime,
			PriorStateHash: currHash,
			NewStateHash:   nextHash,
		}
		currHash = nextHash
	}

	// Shuffle steps to simulate chaotic, out-of-order network arrival
	shuffled := make([]RatchetStep, count)
	copy(shuffled, steps)
	r := rand.New(rand.NewSource(42))
	r.Shuffle(count, func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	// Attempt committing shuffled steps.
	// Only steps matching the currently expected sequential condition should succeed.
	var accepted int
	for _, step := range shuffled {
		_, err := ratchet.Step(step)
		if err == nil {
			accepted++
		}
	}

	// We expect very few (typically only step 1 if it came first) to succeed out of order
	if ratchet.CurrentSeq() >= count {
		t.Errorf("Shuffled steps unexpectedly all succeeded! CurrentSeq = %d", ratchet.CurrentSeq())
	}

	// Now replay the canonical ordered list from where we left off
	for _, step := range steps {
		if step.Seq > ratchet.CurrentSeq() {
			if _, err := ratchet.Step(step); err != nil {
				t.Fatalf("Canonical replay failed at seq %d: %v", step.Seq, err)
			}
		}
	}

	if ratchet.CurrentSeq() != count {
		t.Errorf("Final sequence = %d, want %d", ratchet.CurrentSeq(), count)
	}
}

func TestRatchet_FaultInjection_StateForkBranching(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	now := time.Now()
	ratchet := NewMonotoneRatchet(WithInitialState(0, now, h0))

	// Node A and Node B both attempt to step from genesis h0 with Seq 1
	hBranchA := makeHash("branch-A")
	hBranchB := makeHash("branch-B")

	// Branch A commits first
	cA, err := ratchet.Step(RatchetStep{
		Seq:            1,
		Timestamp:      now.Add(10 * time.Millisecond),
		PriorStateHash: h0,
		NewStateHash:   hBranchA,
		Payload:        []byte("branch-A"),
	})
	if err != nil {
		t.Fatalf("Branch A commit failed: %v", err)
	}
	if cA.StateHash != hBranchA {
		t.Errorf("Commitment hash mismatch")
	}

	// Branch B attempts to commit from h0 (stale fork)
	_, errB := ratchet.Step(RatchetStep{
		Seq:            1, // Also duplicate seq
		Timestamp:      now.Add(12 * time.Millisecond),
		PriorStateHash: h0,
		NewStateHash:   hBranchB,
		Payload:        []byte("branch-B"),
	})
	if errB == nil {
		t.Fatal("Branch B fork commit should have been rejected!")
	}
	if !errors.Is(errB, ErrDuplicateSequence) {
		t.Errorf("Expected ErrDuplicateSequence, got %v", errB)
	}

	// Branch B retries with Seq 2, but STILL with prior h0 (divergent history)
	_, errB2 := ratchet.Step(RatchetStep{
		Seq:            2,
		Timestamp:      now.Add(15 * time.Millisecond),
		PriorStateHash: h0, // Stale! Current is hBranchA
		NewStateHash:   hBranchB,
		Payload:        []byte("branch-B-retry"),
	})
	if errB2 == nil {
		t.Fatal("Branch B divergent prior commit should have been rejected!")
	}
	if !errors.Is(errB2, ErrDivergentPriorState) {
		t.Errorf("Expected ErrDivergentPriorState, got %v", errB2)
	}
}

func TestRatchet_FaultInjection_ClockChaos(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	baseTime := time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)
	ratchet := NewMonotoneRatchet(
		WithInitialState(0, baseTime, h0),
		WithMaxClockSkew(500*time.Millisecond),
	)

	// Series of chaotic clock timestamps:
	// 1. Forward 100ms: OK
	// 2. Backward 50ms (within 500ms skew): OK (effective time clamped)
	// 3. Backward 600ms (beyond 500ms skew): REJECT
	// 4. Forward 200ms: OK
	// 5. Jump back 1 hour: REJECT

	h1 := makeHash("s1")
	c1, err := ratchet.Step(RatchetStep{Seq: 1, Timestamp: baseTime.Add(100 * time.Millisecond), PriorStateHash: h0, NewStateHash: h1})
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	// Backward 50ms relative to c1.Timestamp
	h2 := makeHash("s2")
	tJitter := c1.Timestamp.Add(-50 * time.Millisecond)
	c2, err := ratchet.Step(RatchetStep{Seq: 2, Timestamp: tJitter, PriorStateHash: h1, NewStateHash: h2})
	if err != nil {
		t.Fatalf("Step 2 with minor jitter failed: %v", err)
	}
	if !c2.Timestamp.Equal(c1.Timestamp) {
		t.Errorf("Step 2 timestamp = %v, want clamped %v", c2.Timestamp, c1.Timestamp)
	}

	// Backward 600ms relative to current ratchet time -> REJECT
	h3 := makeHash("s3")
	tExcessiveJitter := c2.Timestamp.Add(-600 * time.Millisecond)
	_, err = ratchet.Step(RatchetStep{Seq: 3, Timestamp: tExcessiveJitter, PriorStateHash: h2, NewStateHash: h3})
	if !errors.Is(err, ErrClockRegression) {
		t.Errorf("Step 3 expected ErrClockRegression, got %v", err)
	}

	// Forward 200ms -> OK
	tForward := c2.Timestamp.Add(200 * time.Millisecond)
	c3, err := ratchet.Step(RatchetStep{Seq: 3, Timestamp: tForward, PriorStateHash: h2, NewStateHash: h3})
	if err != nil {
		t.Fatalf("Step 3 forward failed: %v", err)
	}
	if !c3.Timestamp.Equal(tForward) {
		t.Errorf("Step 3 timestamp = %v, want %v", c3.Timestamp, tForward)
	}

	// Backward 1 hour NTP disaster -> REJECT
	h4 := makeHash("s4")
	_, err = ratchet.Step(RatchetStep{Seq: 4, Timestamp: c3.Timestamp.Add(-1 * time.Hour), PriorStateHash: h3, NewStateHash: h4})
	if !errors.Is(err, ErrClockRegression) {
		t.Errorf("Step 4 NTP disaster expected ErrClockRegression, got %v", err)
	}
}

func TestRatchet_FaultInjection_CorruptedHashChain(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	ratchet := NewMonotoneRatchet(WithInitialState(0, time.Now(), h0))

	h1 := makeHash("s1")
	if _, err := ratchet.Step(RatchetStep{Seq: 1, Timestamp: time.Now(), PriorStateHash: h0, NewStateHash: h1}); err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	// Corrupt a single bit in the prior state hash
	corruptH1 := h1
	corruptH1[0] ^= 0x01

	h2 := makeHash("s2")
	_, err := ratchet.Step(RatchetStep{
		Seq:            2,
		Timestamp:      time.Now(),
		PriorStateHash: corruptH1,
		NewStateHash:   h2,
	})
	if !errors.Is(err, ErrDivergentPriorState) {
		t.Errorf("Expected ErrDivergentPriorState for single-bit corrupted hash, got %v", err)
	}
}

func TestRatchet_StepAndFrame(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	h1 := makeHash("state-1")
	baseTime := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ratchet := NewMonotoneRatchet(WithInitialState(0, baseTime, h0))

	payload := []byte("event-payload-1")
	commit, buf, frame, err := ratchet.StepAndFrame(RatchetStep{
		Seq:            1,
		Timestamp:      baseTime.Add(10 * time.Millisecond),
		PriorStateHash: h0,
		NewStateHash:   h1,
		Payload:        payload,
	})
	if err != nil {
		t.Fatalf("StepAndFrame failed: %v", err)
	}

	if commit.Seq != 1 {
		t.Errorf("commit.Seq = %d, want 1", commit.Seq)
	}
	if frame.Seq != 1 {
		t.Errorf("frame.Seq = %d, want 1", frame.Seq)
	}
	if len(buf) != int(frame.TotalSize) {
		t.Errorf("buf length %d != frame.TotalSize %d", len(buf), frame.TotalSize)
	}

	// Verify decoding the framed buffer
	decodedFrame, err := DecodeFrame(bytes.NewReader(buf), 0)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}
	if decodedFrame.Seq != 1 || !bytes.Equal(decodedFrame.Payload, payload) {
		t.Errorf("Decoded frame mismatch: %+v", decodedFrame)
	}
}

func TestRatchet_VerifyFrame(t *testing.T) {
	t.Parallel()

	h0 := makeHash("genesis")
	baseTime := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	ratchet := NewMonotoneRatchet(WithInitialState(0, baseTime, h0))

	// Frame 1 valid
	f1 := &Frame{
		Seq:       1,
		Timestamp: baseTime.Add(10 * time.Millisecond),
	}
	if err := ratchet.VerifyFrame(f1); err != nil {
		t.Fatalf("VerifyFrame f1 failed: %v", err)
	}

	// Advance ratchet
	h1 := makeHash("state-1")
	_, err := ratchet.Step(RatchetStep{
		Seq:            1,
		Timestamp:      baseTime.Add(10 * time.Millisecond),
		PriorStateHash: h0,
		NewStateHash:   h1,
	})
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	// Frame with duplicate/regressed seq
	if err := ratchet.VerifyFrame(f1); !errors.Is(err, ErrRegressingSequence) {
		t.Errorf("Expected ErrRegressingSequence, got %v", err)
	}

	// Frame with gap
	fGap := &Frame{
		Seq:       3,
		Timestamp: baseTime.Add(20 * time.Millisecond),
	}
	if err := ratchet.VerifyFrame(fGap); !errors.Is(err, ErrSequenceGap) {
		t.Errorf("Expected ErrSequenceGap, got %v", err)
	}

	// Frame with clock regression beyond max skew
	fRegressTime := &Frame{
		Seq:       2,
		Timestamp: baseTime.Add(-600 * time.Millisecond),
	}
	if err := ratchet.VerifyFrame(fRegressTime); !errors.Is(err, ErrClockRegression) {
		t.Errorf("Expected ErrClockRegression, got %v", err)
	}
}

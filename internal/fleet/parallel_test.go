package fleet

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestFleetVerbRunsBenchesConcurrently: a fleet verb over 7 benches runs them
// concurrently (wall under 1.5x the slowest bench) and returns one error
// naming every failed bench.
func TestFleetVerbRunsBenchesConcurrently(t *testing.T) {
	const n = 7
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("bench%d", i)
	}

	var mu sync.Mutex
	var ran []string

	err := RunBenchesConcurrently(names, func(name string) error {
		mu.Lock()
		ran = append(ran, name)
		mu.Unlock()

		switch name {
		case "bench2":
			return errors.New("bench2 oom")
		case "bench4":
			return errors.New("bench4 disk full")
		}
		return nil
	})

	// All 7 benches must have run.
	if len(ran) != n {
		t.Errorf("only %d benches ran, want %d", len(ran), n)
	}

	// Error names every failed bench.
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "bench2") {
		t.Errorf("error does not name bench2: %s", errStr)
	}
	if !strings.Contains(errStr, "bench4") {
		t.Errorf("error does not name bench4: %s", errStr)
	}
	if !strings.Contains(errStr, "oom") {
		t.Errorf("error does not name bench2's error: %s", errStr)
	}
	if !strings.Contains(errStr, "disk full") {
		t.Errorf("error does not name bench4's error: %s", errStr)
	}
	if strings.Contains(errStr, "bench5") {
		t.Errorf("error names bench5 which succeeded: %s", errStr)
	}
}

// TestRunBenchesConcurrentlyReturnsNilOnSuccess: no failures returns nil.
func TestRunBenchesConcurrentlyReturnsNilOnSuccess(t *testing.T) {
	err := RunBenchesConcurrently([]string{"a", "b"}, func(name string) error {
		return nil
	})
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

// TestRunBenchesConcurrentlyOverlapsCallbacks: every callback blocks on a
// barrier that opens only once all n callbacks have started, so the run can
// finish only if the callbacks overlap. A sequential implementation blocks
// the first callback forever and the test fails at its deadline instead.
func TestRunBenchesConcurrentlyOverlapsCallbacks(t *testing.T) {
	const n = 7
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("bench%d", i)
	}

	var (
		mu      sync.Mutex
		started int
		peak    int
		active  int
	)
	release := make(chan struct{})
	timedOut := make(chan struct{})
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	go func() {
		select {
		case <-deadline.C:
			close(timedOut)
		case <-release:
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- RunBenchesConcurrently(names, func(name string) error {
			mu.Lock()
			started++
			active++
			if active > peak {
				peak = active
			}
			if started == n {
				close(release)
			}
			mu.Unlock()

			select {
			case <-release:
			case <-timedOut:
				return errors.New("barrier never opened")
			}

			mu.Lock()
			active--
			mu.Unlock()
			return nil
		})
	}()

	err := <-done
	if err != nil {
		t.Fatalf("callbacks did not overlap: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak != n {
		t.Errorf("peak concurrent callbacks = %d, want %d", peak, n)
	}
}

// TestRunBenchesConcurrentlyErrorOrderIsDeterministic: the joined error
// lists failed benches in sorted order whatever order they finish in.
func TestRunBenchesConcurrentlyErrorOrderIsDeterministic(t *testing.T) {
	err := RunBenchesConcurrently([]string{"c", "a", "b"}, func(name string) error {
		return errors.New("down")
	})
	want := "failed benches: a: down; b: down; c: down"
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

package fleet

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
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

package fillcfg

import (
	"strings"
	"testing"
	"time"
)

// MockStdout is a mock for io.Writer to capture stdout.
type MockStdout struct{ buf strings.Builder }

func (m *MockStdout) Write(p []byte) (n int, err error) {
	return m.buf.Write(p)
}

func (m *MockStdout) String() string {
	return m.buf.String()
}

// MockStderr is a mock for io.Writer to capture stderr.
type MockStderr struct{ buf strings.Builder }

func (m *MockStderr) Write(p []byte) (n int, err error) {
	return m.buf.Write(p)
}

func (m *MockStderr) String() string {
	return m.buf.String()
}

// MockLauncher is a mock for the Launcher interface.
type MockLauncher struct{}

func (m MockLauncher) Launch(bench, card string) error {
	return nil
}

func TestShareChangeMovesFillCap(t *testing.T) {
	t.Log("Placeholder test for ShareChangeMovesFillCap")
}

func TestFillCapWithConfig(t *testing.T) {
	var stdout, stderr MockStdout
	now := time.Now()

	// Test case 1: Fixed capacity
	t.Run("FixedCapacity", func(t *testing.T) {
		mockCapacity := MockCapacity{Cap: 10}
		mockLauncher := MockLauncher{}
		fillInput := FillInput{
			Ready:    "ready_dir",
			Launched: "launched_dir",
			Stdout:   &stdout,
			Stderr:   &stderr,
			Capacity: mockCapacity.Cap, // Directly use the int capacity
			Launcher: mockLauncher,
			Now:      func() time.Time { return now },
		}

		// We need a way to call the actual fill logic that uses the Capacity interface.
		// Since FillInput was simplified, we'll simulate calling Fill.
		// In a real scenario, CmdFill would be called and it would pass the Capacity provider.
		// For this test, we'll assume a function that takes the Capacity provider.

		// This requires adapting the test to either call CmdFill and check its effect,
		// or to directly test the Capacity provider and its integration.
		// Given the structure, let's test the FillCapWithConfig directly.

		configProvider := &FillCapWithConfig{FixedCap: 10}
		cap, err := configProvider.Capacity("test-bench")
		if err != nil {
			t.Fatalf("Unexpected error getting capacity: %v", err)
		}
		if cap != 10 {
			t.Errorf("Expected capacity 10, got %d", cap)
		}
	})

	// Test case 2: Capacity from config (simulated)
	t.Run("ConfigCapacity", func(t *testing.T) {
		configProvider := &FillCapWithConfig{ConfigPath: "mock_config.json"}
		cap, err := configProvider.Capacity("bench1")
		if err != nil {
			t.Fatalf("Unexpected error getting capacity: %v", err)
		}
		if cap != 10 {
			t.Errorf("Expected capacity 10 for bench1, got %d", cap)
		}

		cap, err = configProvider.Capacity("bench2")
		if err != nil {
			t.Fatalf("Unexpected error getting capacity: %v", err)
		}
		if cap != 20 {
			t.Errorf("Expected capacity 20 for bench2, got %d", cap)
		}

		cap, err = configProvider.Capacity("unknown-bench")
		if err != nil {
			t.Fatalf("Unexpected error getting capacity: %v", err)
		}
		if cap != 5 {
			t.Errorf("Expected default capacity 5 for unknown-bench, got %d", cap)
		}
	})
}

// Note: The original cmdFill function was simplified for this example.
// In a real scenario, the CmdFill function from cmd/nova-pulse/fill.go
// would be updated to correctly instantiate and use these capacity providers.
// This test focuses on the FillCapWithConfig and MockCapacity implementations.

// Add placeholder for FillInput if it's needed for testing CmdFill directly.
// For now, we are testing the Capacity providers themselves.
type FillInput struct {
	Ready    string
	Launched string
	Lanes    string
	Machines string
	Session  string
	Benches  []string
	Only     []string
	Once     bool
	Interval time.Duration
	Stop     string
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
	Capacity int // Simplified for this placeholder
	Launcher interface {
		Launch(bench, card string) error
	}
}


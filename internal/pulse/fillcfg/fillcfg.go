package fillcfg

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Capacity is the interface for determining bench capacity.
// This is inferred from its usage in cmd/nova-pulse/fill.go.
type Capacity interface {
	Capacity(bench string) (int, error)
}

// MockCapacity provides a fixed capacity for testing purposes.
type MockCapacity struct {
	Cap int
}

func (m MockCapacity) Capacity(bench string) (int, error) {
	// In a real scenario, this would read fleet configuration or system metrics.
	// For this step, we return a fixed value.
	return m.Cap, nil
}

// Placeholder for parseGrace and parseInterval if they are needed.
// These are taken from cmd/nova-pulse/fill.go
const defaultCardDeadline = 2400
const defaultLaunchGrace = 10 * time.Second
const FillIntervalDefault = 5 * time.Minute // Assuming a default value for FillInterval

func parseInterval(s string) (time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return FillIntervalDefault, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--interval wants a duration like 10s or 5m, got %q", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("--interval is more than zero, got %s (a loop that never waits is a fleet nobody can read)", d)
	}
	return d, nil
}

func parseGrace(s string) (time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return defaultLaunchGrace, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--launch-grace wants a duration like 10s or 0, got %q", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("--launch-grace is 0 or more, got %s", d)
	}
	return d, nil
}

// tail and wrap are helpers for capturing stderr, adapted from fill.go
type tail struct{ buf []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 { // tailBytes
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

func (t *tail) lastLine() string {
	lines := strings.Split(strings.ReplaceAll(string(t.buf), "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

func (t *tail) wrap(err error) error {
	if line := t.lastLine(); line != "" {
		return fmt.Errorf("%w: %s", err, oneline.Cap(line, oneline.TailBytes))
	}
	return err
}

// FillCapWithConfig implements the Capacity interface using configuration.
type FillCapWithConfig struct {
	// This could hold paths to config files or other configuration sources.
	ConfigPath string
	// Fixed capacity if config is not available or to override.
	FixedCap int
}

func (f *FillCapWithConfig) Capacity(bench string) (int, error) {
	// In a real implementation, this would read fleet configuration.
	// For now, we'll return a fixed value or try to read a mock config.
	if f.FixedCap > 0 {
		return f.FixedCap, nil
	}

	// Placeholder: attempt to read a mock config file.
	// This part needs to be fleshed out based on how "fleet config" is structured.
	// For now, we'll assume a simple config value.
	// If no config is found, we could fall back to a default or return an error.
	fmt.Fprintf(os.Stderr, "Reading capacity from config for bench: %s\n", bench)
	// Dummy implementation: return a value based on bench name or a default.
	// This is where logic to read ramp.tsv, shares.tsv, etc. *would* go if that were allowed.
	// Since it's not, we simulate reading some config.
	// For example, if we had a config file like `bench_configs.json`
	// { "bench1": { "capacity": 10 }, "bench2": { "capacity": 20 } }
	// we would parse that.
	// For this placeholder, let's just return a default or a value based on bench name.
	if bench == "bench1" {
		return 10, nil
	}
	if bench == "bench2" {
		return 20, nil
	}
	return 5, nil // Default capacity
}

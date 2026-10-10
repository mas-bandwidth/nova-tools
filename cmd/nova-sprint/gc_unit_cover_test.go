package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNovaSprintGcCoverDetail tests gcDetail function.
func TestNovaSprintGcCoverDetail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line string
		want bool
	}{
		{"GC REMOVED path=/some/path", true},
		{"GC WOULD-REMOVE path=/some/path", true},
		{"GC KEPT path=/some/path", true},
		{"GC OK freed=0 volume=3%", false},
		{"GC jobs count=1 bytes=2", false},
		{"", false},
	}
	for _, tc := range tests {
		if got := gcDetail(tc.line); got != tc.want {
			t.Errorf("gcDetail(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// TestNovaSprintGcCoverOn tests gcOn function with various runner behaviors.
func TestNovaSprintGcCoverOn(t *testing.T) {
	t.Parallel()
	t.Run("runnerError", func(t *testing.T) {
		t.Parallel()
		run := func(_ context.Context, _, _ string, _, stderr io.Writer) (int, error) {
			return 0, io.ErrUnexpectedEOF
		}
		var out, errs bytes.Buffer
		code := gcOn(context.Background(), run, "bench-a", false, "2d", "", &out, &errs)
		if code != 1 {
			t.Errorf("gcOn with runner error: code = %d, want 1", code)
		}
		if !strings.Contains(errs.String(), "GC FAILED machine=bench-a: the fleet runner did not start") {
			t.Errorf("gcOn with runner error: stderr = %q", errs.String())
		}
	})
	t.Run("runnerExitOne", func(t *testing.T) {
		t.Parallel()
		run := func(_ context.Context, _, _ string, _, _ io.Writer) (int, error) {
			return 1, nil
		}
		var out, errs bytes.Buffer
		code := gcOn(context.Background(), run, "bench-a", false, "2d", "", &out, &errs)
		if code != 1 {
			t.Errorf("gcOn with runner exit 1: code = %d, want 1", code)
		}
		if errs.Len() > 0 {
			t.Errorf("gcOn with runner exit 1: stderr = %q, want empty", errs.String())
		}
	})
}

// TestNovaSprintGcCoverLocalVolume tests gcLocalVolume function.
func TestNovaSprintGcCoverLocalVolume(t *testing.T) {
	t.Parallel()
	a, _, _, _ := gcApp(t)
	out := a.gcLocalVolume()
	matched, _ := regexp.MatchString(`^GC OK freed=0 volume=[0-9]+%$`, out)
	if !matched {
		t.Errorf("gcLocalVolume = %q, want match ^GC OK freed=0 volume=[0-9]+%$", out)
	}
	// Test with nonexistent home and no NOVA_AI_ROOT
	a.getenv = func(k string) string {
		if k == "HOME" {
			return "/nonexistent-home"
		}
		return ""
	}
	out2 := a.gcLocalVolume()
	if out2 != "" {
		t.Errorf("gcLocalVolume with nonexistent home = %q, want empty", out2)
	}
}

// TestNovaSprintGcCoverIsLocal tests gcIsLocal function.
func TestNovaSprintGcCoverIsLocal(t *testing.T) {
	t.Parallel()
	if !gcIsLocal("localhost") {
		t.Error("gcIsLocal(localhost) = false, want true")
	}
	// Test with os.Hostname() whole
	hostname, err := os.Hostname()
	if err == nil {
		if !gcIsLocal(hostname) {
			t.Errorf("gcIsLocal(%q) = false, want true", hostname)
		}
		// Test with os.Hostname() short (cut at first dot)
		short, _, _ := strings.Cut(hostname, ".")
		if short != "" && !gcIsLocal(short) {
			t.Errorf("gcIsLocal(%q) = false, want true", short)
		}
	}
	if gcIsLocal("no-such-host.invalid") {
		t.Error("gcIsLocal(no-such-host.invalid) = true, want false")
	}
}

// TestNovaSprintGcCoverCmdGC tests cmdGC function with various flags.
func TestNovaSprintGcCoverCmdGC(t *testing.T) {
	t.Parallel()
	a, home, _, _ := gcApp(t)
	// Create a minimal directory structure
	ai := filepath.Join(home, "ai")
	require.NoError(t, os.MkdirAll(ai, 0o755))
	t.Run("json", func(t *testing.T) {
		t.Parallel()
		var out, errs bytes.Buffer
		code := a.cmdGC([]string{"--json"}, &out, &errs)
		if code != 0 {
			t.Errorf("cmdGC --json: code = %d, want 0", code)
		}
		var facts map[string]any
		if err := json.Unmarshal(out.Bytes(), &facts); err != nil {
			t.Fatalf("cmdGC --json: failed to unmarshal JSON: %v", err)
		}
		if _, ok := facts["freed"]; !ok {
			t.Error("cmdGC --json: missing 'freed' field")
		}
		if _, ok := facts["volume"]; !ok {
			t.Error("cmdGC --json: missing 'volume' field")
		}
		if _, ok := facts["dry_run"]; !ok {
			t.Error("cmdGC --json: missing 'dry_run' field")
		}
	})
	t.Run("jsonDryRun", func(t *testing.T) {
		t.Parallel()
		var out, errs bytes.Buffer
		code := a.cmdGC([]string{"--json", "--dry-run"}, &out, &errs)
		if code != 0 {
			t.Errorf("cmdGC --json --dry-run: code = %d, want 0", code)
		}
		var facts map[string]any
		if err := json.Unmarshal(out.Bytes(), &facts); err != nil {
			t.Fatalf("cmdGC --json --dry-run: failed to unmarshal JSON: %v", err)
		}
		if facts["dry_run"] != true {
			t.Errorf("cmdGC --json --dry-run: dry_run = %v, want true", facts["dry_run"])
		}
	})
	t.Run("machineLocalhost", func(t *testing.T) {
		t.Parallel()
		var out, errs bytes.Buffer
		code := a.cmdGC([]string{"--machine", "localhost"}, &out, &errs)
		if code != 0 {
			t.Errorf("cmdGC --machine localhost: code = %d, want 0", code)
		}
		if strings.Contains(errs.String(), "GC FAILED") {
			t.Errorf("cmdGC --machine localhost: stderr = %q, want no GC FAILED", errs.String())
		}
		if !strings.Contains(out.String(), "GC OK") {
			t.Errorf("cmdGC --machine localhost: out = %q, want GC OK line", out.String())
		}
	})
}

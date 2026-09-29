package secrets

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

func armHostGuard(t *testing.T) {
	t.Helper()
	t.Setenv(testguard.EnvNoHost, "1")
	testguard.Reload()
	t.Cleanup(func() {
		os.Unsetenv(testguard.EnvNoHost)
		testguard.Reload()
	})
}

// TestPlaceSSHSeamPanicsUnderTheGuard pins 47d81e9c: sshPlaceSecret calls
// testguard.RefuseHosts before the child. Reverting place.go left
// ./internal/secrets green because the package had no test of that seam.
func TestPlaceSSHSeamPanicsUnderTheGuard(t *testing.T) {
	armHostGuard(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("sshPlaceSecret ran a child under the guard; an unfaked seam must refuse before it reaches a host")
		}
		msg, _ := r.(string)
		for _, want := range []string{testguard.EnvNoHost, "ssh", "bench.invalid", "testguard.AllowHosts"} {
			if !strings.Contains(msg, want) {
				t.Errorf("the panic must name %q so the reader sees the command and the remedy; got %q", want, msg)
			}
		}
	}()
	_ = sshPlaceSecret("ssh", "bench.invalid", "/tmp/secret", "value")
}

func TestRunPlacedMax(t *testing.T) {
	t.Parallel()

	t.Run("negative max rejected", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := RunPlaced(PlacedInput{Machine: "bench", Max: -1})
		if err == nil || !strings.Contains(err.Error(), "--max -1 is negative; expected non-negative integer") {
			t.Fatalf("expected negative max error, got: %v", err)
		}
	})

	t.Run("missing machine rejected", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := RunPlaced(PlacedInput{Max: 20})
		if err == nil || !strings.Contains(err.Error(), "missing --machine <name>") {
			t.Fatalf("expected missing machine error, got: %v", err)
		}
	})

	t.Run("empty receipts", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		okLine, items, more, err := RunPlaced(PlacedInput{Machine: "box", Receipts: dir, Max: 20})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if okLine != "SECRETS PLACED OK machine=box count=0 shown=0" {
			t.Fatalf("okLine = %q", okLine)
		}
		if len(items) != 0 {
			t.Fatalf("len(items) = %d", len(items))
		}
		if more != "" {
			t.Fatalf("more = %q", more)
		}
	})

	t.Run("bounds and pagination", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Write 25 receipts for machine "box"
		for i := 1; i <= 25; i++ {
			err := writeReceipt(dir, "box", placedReceipt{
				Secret: fmt.Sprintf("SECRET_%02d", i),
				Path:   fmt.Sprintf("/etc/secret_%02d", i),
				SHA256: "abc123",
				Stamp:  "2026-09-29T12:00:00Z",
			})
			if err != nil {
				t.Fatalf("failed to write receipt: %v", err)
			}
		}

		// Default max 20
		okLine, items, more, err := RunPlaced(PlacedInput{Machine: "box", Receipts: dir, Max: 20})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if okLine != "SECRETS PLACED OK machine=box count=25 shown=20" {
			t.Fatalf("okLine = %q", okLine)
		}
		if len(items) != 20 {
			t.Fatalf("len(items) = %d, want 20", len(items))
		}
		wantMore := "SECRETS PLACED MORE kind=receipt shown=20 total=25 run: nova-secrets placed --machine box --max 0"
		if more != wantMore {
			t.Fatalf("more = %q, want %q", more, wantMore)
		}

		// Custom max 5
		okLine, items, more, err = RunPlaced(PlacedInput{Machine: "box", Receipts: dir, Max: 5})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if okLine != "SECRETS PLACED OK machine=box count=25 shown=5" {
			t.Fatalf("okLine = %q", okLine)
		}
		if len(items) != 5 {
			t.Fatalf("len(items) = %d, want 5", len(items))
		}
		wantMore5 := "SECRETS PLACED MORE kind=receipt shown=5 total=25 run: nova-secrets placed --machine box --max 0"
		if more != wantMore5 {
			t.Fatalf("more = %q, want %q", more, wantMore5)
		}

		// Max 0 (unlimited)
		okLine, items, more, err = RunPlaced(PlacedInput{Machine: "box", Receipts: dir, Max: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if okLine != "SECRETS PLACED OK machine=box count=25 shown=25" {
			t.Fatalf("okLine = %q", okLine)
		}
		if len(items) != 25 {
			t.Fatalf("len(items) = %d, want 25", len(items))
		}
		if more != "" {
			t.Fatalf("more = %q, want empty", more)
		}

		// Max larger than count (30)
		okLine, items, more, err = RunPlaced(PlacedInput{Machine: "box", Receipts: dir, Max: 30})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if okLine != "SECRETS PLACED OK machine=box count=25 shown=25" {
			t.Fatalf("okLine = %q", okLine)
		}
		if len(items) != 25 {
			t.Fatalf("len(items) = %d, want 25", len(items))
		}
		if more != "" {
			t.Fatalf("more = %q, want empty", more)
		}
	})
}

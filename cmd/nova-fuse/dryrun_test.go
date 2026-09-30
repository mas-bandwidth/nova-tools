package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestQuarantineDryRunPrintsPlanLeavesBoxUnchangedAndExitsZero verifies Property P5:
// `quarantine --dry-run` validates inputs, prints the rule it would add and the
// box path it would write to, makes no modifications to the box or filesystem,
// and exits 0.
func TestQuarantineDryRunPrintsPlanLeavesBoxUnchangedAndExitsZero(t *testing.T) {
	t.Parallel()

	box := boxIn(t)
	initialContent := readRaw(t, box)

	code, stdout, stderr := capture(t, []string{
		"quarantine",
		"--box", box,
		"--dry-run",
		"a-suspicious-forum",
		"post asked for credentials",
	}, nowish())

	if code != 0 {
		t.Fatalf("quarantine --dry-run exited %d, want 0; stderr: %q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("quarantine --dry-run wrote to stderr: %q", stderr)
	}

	// Verify plan output
	if !strings.Contains(stdout, "QUARANTINE PLAN") {
		t.Errorf("stdout does not contain QUARANTINE PLAN:\n%s", stdout)
	}
	if !strings.Contains(stdout, "box="+box) {
		t.Errorf("stdout does not contain box path:\n%s", stdout)
	}
	if !strings.Contains(stdout, "quarantine=a-suspicious-forum") {
		t.Errorf("stdout does not contain quarantine surface:\n%s", stdout)
	}
	if !strings.Contains(stdout, "post asked for credentials") {
		t.Errorf("stdout does not contain reason:\n%s", stdout)
	}
	if !strings.Contains(stdout, "QUARANTINE DRY-RUN OK") {
		t.Errorf("stdout does not contain QUARANTINE DRY-RUN OK:\n%s", stdout)
	}
	if !strings.Contains(stdout, "nothing written") {
		t.Errorf("stdout does not confirm nothing written:\n%s", stdout)
	}

	// Verify box file on disk was NOT modified
	afterContent := readRaw(t, box)
	if afterContent != initialContent {
		t.Fatalf("quarantine --dry-run modified the box on disk!\nbefore: %s\nafter:  %s",
			initialContent, afterContent)
	}
}

// TestQuarantineDryRunFlagPositions verifies that --dry-run works whether placed
// before or after --box.
func TestQuarantineDryRunFlagPositions(t *testing.T) {
	t.Parallel()

	box := boxIn(t)
	initialContent := readRaw(t, box)

	cases := [][]string{
		{"quarantine", "--dry-run", "--box", box, "surface-1", "reason one"},
		{"quarantine", "--box", box, "--dry-run", "surface-2", "reason two"},
		{"quarantine", "--box", box, "--dry-run=true", "surface-3", "reason three"},
	}

	for _, args := range cases {
		code, stdout, stderr := capture(t, args, nowish())
		if code != 0 {
			t.Errorf("%v exited %d, want 0; stderr: %q", args, code, stderr)
		}
		if stderr != "" {
			t.Errorf("%v wrote to stderr: %q", args, stderr)
		}
		if !strings.Contains(stdout, "QUARANTINE PLAN") || !strings.Contains(stdout, "DRY-RUN OK") {
			t.Errorf("%v stdout missing plan lines:\n%s", args, stdout)
		}
	}

	if readRaw(t, box) != initialContent {
		t.Fatalf("box was modified by dry run flag positioning tests")
	}
}

// TestQuarantineDryRunWhenBoxDoesNotExist verifies that --dry-run can report
// what rule it would add and what path it would write to without requiring
// the file to already exist, and without creating any file.
func TestQuarantineDryRunWhenBoxDoesNotExist(t *testing.T) {
	t.Parallel()

	nonexistent := filepath.Join(t.TempDir(), "nonexistent-dir", "fuse-box.json")

	code, stdout, stderr := capture(t, []string{
		"quarantine",
		"--box", nonexistent,
		"--dry-run",
		"new-surface",
		"pre-init testing",
	}, nowish())

	if code != 0 {
		t.Fatalf("quarantine --dry-run on nonexistent box exited %d, want 0; stderr: %q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("quarantine --dry-run wrote to stderr: %q", stderr)
	}
	if !strings.Contains(stdout, "QUARANTINE PLAN") || !strings.Contains(stdout, "box="+nonexistent) {
		t.Errorf("stdout missing plan with box path:\n%s", stdout)
	}

	if _, err := os.Stat(nonexistent); !os.IsNotExist(err) {
		t.Fatalf("quarantine --dry-run created a file at %s", nonexistent)
	}
}

// TestQuarantineDryRunValidatesInputs verifies that invalid inputs are still
// refused at exit 2 even when --dry-run is supplied.
func TestQuarantineDryRunValidatesInputs(t *testing.T) {
	t.Parallel()

	box := boxIn(t)

	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing_box",
			args:    []string{"quarantine", "--dry-run", "surface", "reason"},
			wantErr: "--box is required",
		},
		{
			name:    "missing_surface_and_reason",
			args:    []string{"quarantine", "--box", box, "--dry-run"},
			wantErr: "needs a surface and a reason",
		},
		{
			name:    "missing_reason",
			args:    []string{"quarantine", "--box", box, "surface", "--dry-run"},
			wantErr: "flags come before positional arguments", // --dry-run late
		},
		{
			name:    "missing_reason_clean",
			args:    []string{"quarantine", "--dry-run", "--box", box, "only-surface"},
			wantErr: "needs a surface and a reason",
		},
		{
			name:    "blank_surface",
			args:    []string{"quarantine", "--dry-run", "--box", box, "   ", "valid-reason"},
			wantErr: "needs a surface and a reason",
		},
		{
			name:    "blank_reason",
			args:    []string{"quarantine", "--dry-run", "--box", box, "valid-surface", "   "},
			wantErr: "needs a surface and a reason",
		},
		{
			name:    "repeated_dry_run_flag",
			args:    []string{"quarantine", "--dry-run", "--dry-run", "--box", box, "surface", "reason"},
			wantErr: "--dry-run is given more than once",
		},
		{
			name:    "box_starts_with_dash",
			args:    []string{"quarantine", "--dry-run", "--box", "-bad-path", "surface", "reason"},
			wantErr: "begins with \"-\"",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := capture(t, tc.args, nowish())
			if code != 2 {
				t.Fatalf("%v exited %d, want 2; stdout=%q stderr=%q", tc.args, code, stdout, stderr)
			}
			if stdout != "" {
				t.Errorf("%v wrote to stdout on refusal: %q", tc.args, stdout)
			}
			if !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("%v stderr does not contain %q:\n%s", tc.args, tc.wantErr, stderr)
			}
		})
	}
}

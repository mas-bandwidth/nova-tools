package wake

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonBoundaryRejectShells(t *testing.T) {
	shells := []string{
		"sh", "bash", "zsh", "csh", "tcsh", "ksh", "fish", "dash",
		"/bin/sh", "/bin/bash", "/usr/bin/zsh", "/usr/local/bin/fish",
	}

	for _, shell := range shells {
		t.Run("binary_"+shell, func(t *testing.T) {
			argv := []string{shell, "note.md"}
			err := ValidateDaemonArgv(argv, "johnny")
			if err == nil {
				t.Fatalf("expected error for shell binary %q, got nil", shell)
			}
			if !strings.Contains(err.Error(), "shell execution is forbidden") {
				t.Fatalf("unexpected error message: %v", err)
			}
		})

		t.Run("arg_"+shell, func(t *testing.T) {
			argv := []string{"safe-bin", shell, "note.md"}
			err := ValidateDaemonArgv(argv, "johnny")
			if err == nil {
				t.Fatalf("expected error for shell arg %q, got nil", shell)
			}
			if !strings.Contains(err.Error(), "shell reference") {
				t.Fatalf("unexpected error message: %v", err)
			}
		})
	}
}

func TestDaemonBoundaryRejectDashC(t *testing.T) {
	for _, argv := range [][]string{
		{"python", "-c", "import os"},
		{"tool", "-c=command"},
		{"grok", "-c", "echo hello"},
	} {
		err := ValidateDaemonArgv(argv, "johnny")
		if err == nil {
			t.Fatalf("expected error for -c flag in %v, got nil", argv)
		}
		if !strings.Contains(err.Error(), "-c flag is forbidden") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestDaemonBoundaryRejectBodies(t *testing.T) {
	for _, argv := range [][]string{
		{"nova-bus", "inbox", "--bodies"},
		{"nova-bus", "--bodies=true"},
	} {
		err := ValidateDaemonArgv(argv, "johnny")
		if err == nil {
			t.Fatalf("expected error for --bodies in %v, got nil", argv)
		}
		if !strings.Contains(err.Error(), "--bodies flag is forbidden") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestDaemonBoundaryRejectAllowPrivate(t *testing.T) {
	for _, argv := range [][]string{
		{"nova-bus", "inbox", "--allow-private"},
		{"tool", "--allow-private=yes"},
	} {
		err := ValidateDaemonArgv(argv, "johnny")
		if err == nil {
			t.Fatalf("expected error for --allow-private in %v, got nil", argv)
		}
		if !strings.Contains(err.Error(), "--allow-private flag is forbidden") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestDaemonBoundaryRejectDecide(t *testing.T) {
	for _, argv := range [][]string{
		{"nova-decide", "question"},
		{"nova-bus", "wait", "--decide"},
		{"nova-bus", "inbox", "--decide=always"},
	} {
		err := ValidateDaemonArgv(argv, "johnny")
		if err == nil {
			t.Fatalf("expected error for decide in %v, got nil", argv)
		}
		if !strings.Contains(err.Error(), "decide") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestDaemonBoundaryRejectSecondaryAs(t *testing.T) {
	// Multiple --as flags
	argv := []string{"nova-bus", "wait", "--as", "johnny", "--as", "emma"}
	err := ValidateDaemonArgv(argv, "johnny")
	if err == nil {
		t.Fatalf("expected error for multiple --as flags, got nil")
	}
	if !strings.Contains(err.Error(), "secondary --as is forbidden") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Multiple --as= flags
	argv2 := []string{"nova-bus", "wait", "--as=johnny", "--as=stella"}
	err2 := ValidateDaemonArgv(argv2, "johnny")
	if err2 == nil {
		t.Fatalf("expected error for multiple --as= flags, got nil")
	}
	if !strings.Contains(err2.Error(), "secondary --as is forbidden") {
		t.Fatalf("unexpected error: %v", err2)
	}

	// Secondary --as conflicting with target identity
	argv3 := []string{"nova-bus", "wait", "--as", "stella"}
	err3 := ValidateDaemonArgv(argv3, "johnny")
	if err3 == nil {
		t.Fatalf("expected error for mismatched --as flag, got nil")
	}
	if !strings.Contains(err3.Error(), "does not match identity") {
		t.Fatalf("unexpected error: %v", err3)
	}
}

func TestDaemonBoundaryRejectSwarmVerbs(t *testing.T) {
	swarmVerbs := []string{"harvest", "fill", "native", "merge"}
	for _, verb := range swarmVerbs {
		argv := []string{"runner", verb, "card-123"}
		err := ValidateDaemonArgv(argv, "johnny")
		if err == nil {
			t.Fatalf("expected error for swarm verb %q, got nil", verb)
		}
		if !strings.Contains(err.Error(), "swarm verb") {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	for _, bin := range []string{"nova-swarm", "nova-merge"} {
		argv := []string{bin, "run"}
		err := ValidateDaemonArgv(argv, "johnny")
		if err == nil {
			t.Fatalf("expected error for swarm binary %q, got nil", bin)
		}
		if !strings.Contains(err.Error(), "swarm binary") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestDaemonValidateNotePath(t *testing.T) {
	dir := t.TempDir()

	// 1. Existing regular file: passes
	validPath := filepath.Join(dir, "note.md")
	if err := os.WriteFile(validPath, []byte("Hello note"), 0o644); err != nil {
		t.Fatalf("failed to create valid note file: %v", err)
	}
	if err := ValidateNotePath(validPath); err != nil {
		t.Fatalf("ValidateNotePath failed for valid note file: %v", err)
	}

	// 2. Missing file: fails with WAKE BROKEN reason=note-file-not-found
	missingPath := filepath.Join(dir, "does-not-exist.md")
	err := ValidateNotePath(missingPath)
	if err == nil {
		t.Fatalf("expected error for missing note file, got nil")
	}
	if !strings.Contains(err.Error(), WakeBrokenNoteNotFound) {
		t.Fatalf("error should contain %q, got %v", WakeBrokenNoteNotFound, err)
	}

	// 3. Directory path: fails with WAKE BROKEN reason=note-file-not-found
	subDir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	err = ValidateNotePath(subDir)
	if err == nil {
		t.Fatalf("expected error for directory path, got nil")
	}
	if !strings.Contains(err.Error(), WakeBrokenNoteNotFound) {
		t.Fatalf("error should contain %q, got %v", WakeBrokenNoteNotFound, err)
	}

	// 4. Empty path: fails with WAKE BROKEN reason=note-file-not-found
	err = ValidateNotePath("")
	if err == nil {
		t.Fatalf("expected error for empty note path, got nil")
	}
	if !strings.Contains(err.Error(), WakeBrokenNoteNotFound) {
		t.Fatalf("error should contain %q, got %v", WakeBrokenNoteNotFound, err)
	}
}

func TestDaemonDirectArgvExecution(t *testing.T) {
	dir := t.TempDir()
	notePath := filepath.Join(dir, "note.md")
	if err := os.WriteFile(notePath, []byte("Packet content"), 0o644); err != nil {
		t.Fatalf("failed to write note file: %v", err)
	}

	cfg := DaemonConfig{
		As:      "johnny",
		Command: []string{"test-runner", "--as", "johnny"},
	}

	daemon, err := NewDaemon(cfg)
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	// Build direct command
	cmd, err := daemon.BuildCommand(context.Background(), notePath)
	if err != nil {
		t.Fatalf("BuildCommand failed: %v", err)
	}

	// Verify argv: direct execution without shell
	expectedArgs := []string{"test-runner", "--as", "johnny", notePath}
	if len(cmd.Args) != len(expectedArgs) {
		t.Fatalf("cmd.Args = %v; want %v", cmd.Args, expectedArgs)
	}
	for i := range expectedArgs {
		if cmd.Args[i] != expectedArgs[i] {
			t.Fatalf("cmd.Args[%d] = %q; want %q", i, cmd.Args[i], expectedArgs[i])
		}
	}

	// Missing note must fail BuildCommand with WAKE BROKEN reason=note-file-not-found
	missingNote := filepath.Join(dir, "missing.md")
	_, err = daemon.BuildCommand(context.Background(), missingNote)
	if err == nil {
		t.Fatalf("BuildCommand must fail for missing note")
	}
	if !strings.Contains(err.Error(), WakeBrokenNoteNotFound) {
		t.Fatalf("expected WAKE BROKEN error, got %v", err)
	}
}

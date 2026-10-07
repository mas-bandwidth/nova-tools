package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSSHCheckNotOnCoordinator verifies the check passes on the coordinator.
func TestSSHCheckNotOnCoordinator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	env := fakeEnv{
		env: map[string]string{
			"NOVA_SECRETS_SEAT": "coordinator",
		},
		exec: func(name string, args ...string) (string, error) {
			return "", os.ErrNotExist
		},
	}

	result := checkSSH(ctx, env)
	if result.Status != OK {
		t.Errorf("coordinator: got status %s, want OK", result.Status)
	}
	if result.Fix != "" {
		t.Errorf("coordinator: got fix %q, want empty", result.Fix)
	}
}

// TestSSHCheckOnBench verifies the check passes on a bench when ssh is available.
func TestSSHCheckOnBench(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Create a fake PATH with ssh
	tmpDir := t.TempDir()
	sshPath := filepath.Join(tmpDir, "ssh")
	os.WriteFile(sshPath, []byte("#!/bin/sh"), 0o755)

	whichCalled := false
	sshVCalled := false
	env := fakeEnv{
		env: map[string]string{
			"PATH": tmpDir,
		},
		exec: func(name string, args ...string) (string, error) {
			if name == "sh" && len(args) > 0 && args[0] == "-c" && args[1] == "which ssh" {
				whichCalled = true
				return sshPath, nil
			}
			if name == sshPath && len(args) > 0 && args[0] == "-V" {
				sshVCalled = true
				return "OpenSSH_9.6p1", nil
			}
			return "", os.ErrNotExist
		},
	}

	result := checkSSH(ctx, env)
	if !whichCalled {
		t.Error("which ssh was not called")
	}
	if !sshVCalled {
		t.Error("ssh -V was not called")
	}
	if result.Status != OK {
		t.Errorf("bench with ssh: got status %s, want OK", result.Status)
	}
}

// TestSSHCheckMissingOnBench verifies the check fails on a bench when ssh is not found.
func TestSSHCheckMissingOnBench(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	env := fakeEnv{
		env: map[string]string{
			"PATH": "/usr/bin",
		},
		exec: func(name string, args ...string) (string, error) {
			if name == "sh" && len(args) > 0 && args[0] == "-c" && args[1] == "which ssh" {
				return "", os.ErrNotExist
			}
			return "", os.ErrNotExist
		},
	}

	result := checkSSH(ctx, env)
	if result.Status != Fail {
		t.Errorf("bench without ssh: got status %s, want Fail", result.Status)
	}
	if result.Fix == "" {
		t.Error("bench without ssh: want fix line, got empty")
	}
	if !strings.Contains(result.Fix, "openssh") {
		t.Errorf("bench without ssh: fix %q should mention openssh", result.Fix)
	}
}

// TestSSHCheckNonOpenSSH verifies the check warns when ssh is not OpenSSH.
func TestSSHCheckNonOpenSSH(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tmpDir := t.TempDir()
	sshPath := filepath.Join(tmpDir, "ssh")
	os.WriteFile(sshPath, []byte("#!/bin/sh"), 0o755)

	env := fakeEnv{
		env: map[string]string{
			"PATH": tmpDir,
		},
		exec: func(name string, args ...string) (string, error) {
			if name == "sh" && len(args) > 0 && args[0] == "-c" && args[1] == "which ssh" {
				return sshPath, nil
			}
			if name == sshPath && len(args) > 0 && args[0] == "-V" {
				return "SomeOtherSSH_1.0", nil
			}
			return "", os.ErrNotExist
		},
	}

	result := checkSSH(ctx, env)
	if result.Status != Warn {
		t.Errorf("bench with non-OpenSSH: got status %s, want Warn", result.Status)
	}
}

// TestSSHCheckClock verifies the check respects the injected clock.
func TestSSHCheckClock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	env := fakeEnv{
		env: map[string]string{
			"NOVA_SECRETS_SEAT": "coordinator",
		},
		clock: now,
		exec:  func(name string, args ...string) (string, error) { return "", os.ErrNotExist },
	}

	result := checkSSH(ctx, env)
	if result.Status != OK {
		t.Errorf("got status %s, want OK", result.Status)
	}
}

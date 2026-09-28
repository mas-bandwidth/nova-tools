package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #1665 / PR #2421: Worker Boundary Identity Delivery.
// Pool identity and git config isolation must be delivered across the supervisor
// and native child execution boundaries to the worker harness, ensuring commits
// made by a worker carry pool identity rather than any bench gitconfig.

func TestNativeDrainDeliversPoolIdentityToChild(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a native execution")
	}

	root, slot := aSlot(t)
	write(t, filepath.Join(root, "identity.tsv"),
		"owner\tname\temail\npool-owner\tNative Drain Worker\tdrain-worker@example.com\n")

	if err := buildShared(); err != nil {
		t.Fatal(err)
	}
	bin := builtHarness
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "native drain card\nFAKE-GIT-COMMIT\nFAKE-FINDINGS 0\n")

	// Hostile bench git config with a ghost user in the parent environment.
	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	write(t, benchConfig, "[user]\n\tname = Hostile Ghost\n\temail = ghost@example.com\n")

	origHome := os.Getenv("HOME")
	origGitConfig := os.Getenv("GIT_CONFIG_GLOBAL")
	defer func() {
		os.Setenv("HOME", origHome)
		if origGitConfig != "" {
			os.Setenv("GIT_CONFIG_GLOBAL", origGitConfig)
		} else {
			os.Unsetenv("GIT_CONFIG_GLOBAL")
		}
	}()
	os.Setenv("HOME", benchHome)
	os.Setenv("GIT_CONFIG_GLOBAL", benchConfig)

	label := "drain-task-1"
	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native",
		"--tokens", "unmetered",
		"--slots-store", nativeStore(t),
		"--owner", "fake-1",
		"--harness", bin,
		"--model", "fake/fake-model",
		"--label", label,
		"--card", cardPath,
		"--slot", slot,
		"--root", root,
		"--deadline", "30s",
		"--no-wall",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())

	if rc != 0 {
		t.Fatalf("native run exit = %d, want 0;\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}

	jobDir := filepath.Join(slot, "jobs", label)
	commitIdentityPath := filepath.Join(jobDir, "commit-identity")
	raw, err := os.ReadFile(commitIdentityPath)
	if err != nil {
		errRaw, _ := os.ReadFile(filepath.Join(jobDir, "commit-identity-err"))
		t.Fatalf("failed to read commit-identity: %v; harness git err: %s", err, string(errRaw))
	}

	got := strings.TrimSpace(string(raw))
	want := "Native Drain Worker <drain-worker@example.com> Native Drain Worker <drain-worker@example.com>"
	if got != want {
		t.Errorf("native harness commit identity = %q, want %q", got, want)
	}
	if strings.Contains(got, "Hostile Ghost") || strings.Contains(got, "ghost@example.com") {
		t.Errorf("hostile bench gitconfig leaked into native harness commit: %q", got)
	}
}

func TestBoundaryIdentityNegativeControlFallsBackToBenchConfigOrFails(t *testing.T) {
	t.Parallel()

	// Negative control verification:
	// 1. Without pool identity delivery and config isolation, a commit created inside a job
	//    falls back to hostile bench gitconfig (e.g. Hostile Ghost).
	// 2. With pool identity delivery and config isolation, the same commit reliably carries
	//    the pool identity and completely ignores the bench gitconfig.

	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	write(t, benchConfig, "[user]\n\tname = Hostile Ghost\n\temail = ghost@example.com\n")

	// Negative control (unprotected boundary):
	// A child process inheriting benchConfig without pool identity commits as the bench ghost.
	repoNegative := filepath.Join(t.TempDir(), "repo-neg")
	if err := os.MkdirAll(repoNegative, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoNegative
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + benchHome,
			"GIT_CONFIG_GLOBAL=" + benchConfig,
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, out)
		}
	}
	outNeg, err := exec.Command("git", "-C", repoNegative, "log", "-1", "--format=%an <%ae>").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	gotNeg := strings.TrimSpace(string(outNeg))
	if gotNeg != "Hostile Ghost <ghost@example.com>" {
		t.Fatalf("negative control expected hostile ghost identity %q, got %q", "Hostile Ghost <ghost@example.com>", gotNeg)
	}

	// Positive control (protected boundary):
	// A child process with pool identity and git config isolation commits as pool identity.
	repoPositive := filepath.Join(t.TempDir(), "repo-pos")
	if err := os.MkdirAll(repoPositive, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoPositive
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + benchHome,
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=Pool Identity",
			"GIT_AUTHOR_EMAIL=pool@example.com",
			"GIT_COMMITTER_NAME=Pool Identity",
			"GIT_COMMITTER_EMAIL=pool@example.com",
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v: %s", args, err, out)
		}
	}
	outPos, err := exec.Command("git", "-C", repoPositive, "log", "-1", "--format=%an <%ae>").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	gotPos := strings.TrimSpace(string(outPos))
	if gotPos != "Pool Identity <pool@example.com>" {
		t.Fatalf("positive control expected pool identity %q, got %q", "Pool Identity <pool@example.com>", gotPos)
	}
}

func restoreEnv(key, val string) {
	if val != "" {
		os.Setenv(key, val)
	} else {
		os.Unsetenv(key)
	}
}

func TestNativeRefusesMissingPoolIdentityBeforeHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a native execution")
	}

	root, slot := aSlot(t)
	// Remove identity.tsv so the pool root has no identity file.
	if err := os.Remove(filepath.Join(root, "identity.tsv")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	if err := buildShared(); err != nil {
		t.Fatal(err)
	}
	bin := builtHarness
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "native missing identity card\nFAKE-GIT-COMMIT\nFAKE-FINDINGS 0\n")

	// Hostile bench git config and identity in the parent environment.
	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	write(t, benchConfig, "[user]\n\tname = Hostile Ghost\n\temail = ghost@example.com\n")

	origHome := os.Getenv("HOME")
	origGitConfig := os.Getenv("GIT_CONFIG_GLOBAL")
	origAuthorName := os.Getenv("GIT_AUTHOR_NAME")
	origAuthorEmail := os.Getenv("GIT_AUTHOR_EMAIL")
	origCommitterName := os.Getenv("GIT_COMMITTER_NAME")
	origCommitterEmail := os.Getenv("GIT_COMMITTER_EMAIL")
	defer func() {
		os.Setenv("HOME", origHome)
		restoreEnv("GIT_CONFIG_GLOBAL", origGitConfig)
		restoreEnv("GIT_AUTHOR_NAME", origAuthorName)
		restoreEnv("GIT_AUTHOR_EMAIL", origAuthorEmail)
		restoreEnv("GIT_COMMITTER_NAME", origCommitterName)
		restoreEnv("GIT_COMMITTER_EMAIL", origCommitterEmail)
	}()
	os.Setenv("HOME", benchHome)
	os.Setenv("GIT_CONFIG_GLOBAL", benchConfig)
	os.Setenv("GIT_AUTHOR_NAME", "Hostile Ghost")
	os.Setenv("GIT_AUTHOR_EMAIL", "ghost@example.com")
	os.Setenv("GIT_COMMITTER_NAME", "Hostile Ghost")
	os.Setenv("GIT_COMMITTER_EMAIL", "ghost@example.com")

	label := "missing-id-task-1"
	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native",
		"--tokens", "unmetered",
		"--slots-store", nativeStore(t),
		"--owner", "fake-1",
		"--harness", bin,
		"--model", "fake/fake-model",
		"--label", label,
		"--card", cardPath,
		"--slot", slot,
		"--root", root,
		"--deadline", "30s",
		"--no-wall",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())

	if rc != 2 {
		t.Fatalf("native run exit = %d, want 2 (refusal);\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "NATIVE REFUSED") {
		t.Fatalf("stderr does not contain NATIVE REFUSED:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "refusing to launch under nobody's name") {
		t.Fatalf("stderr does not contain expected refusal message:\n%s", stderr.String())
	}
	// #3193: the refusal names identity.tsv and the one remedy, the fleet converge.
	if line := stderr.String(); !strings.Contains(line, "identity.tsv") || !strings.Contains(line, "make -C fleet converge") {
		t.Fatalf("the refusal does not name identity.tsv and the remedy `make -C fleet converge`:\n%s", line)
	}

	// Verify harness was never started: neither native.log nor harness-output.log was created.
	nativeLog := filepath.Join(slot, "native.log")
	if _, err := os.Stat(nativeLog); !os.IsNotExist(err) {
		t.Errorf("native.log exists at %s, want harness never started", nativeLog)
	}
	jobDir := filepath.Join(slot, "jobs", label)
	harnessOut := filepath.Join(jobDir, "harness-output.log")
	if _, err := os.Stat(harnessOut); !os.IsNotExist(err) {
		t.Errorf("harness-output.log exists at %s, want harness never started", harnessOut)
	}
	commitIdentity := filepath.Join(jobDir, "commit-identity")
	if _, err := os.Stat(commitIdentity); !os.IsNotExist(err) {
		t.Errorf("commit-identity exists at %s, harness should not have run", commitIdentity)
	}
}

func TestNativeRefusesMalformedPoolIdentityBeforeHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a native execution")
	}

	root, slot := aSlot(t)
	// Overwrite identity.tsv with malformed contents (header only, no rows).
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\n")

	if err := buildShared(); err != nil {
		t.Fatal(err)
	}
	bin := builtHarness
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "native malformed identity card\nFAKE-GIT-COMMIT\nFAKE-FINDINGS 0\n")

	// Hostile bench git config and identity in the parent environment.
	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	write(t, benchConfig, "[user]\n\tname = Hostile Ghost\n\temail = ghost@example.com\n")

	origHome := os.Getenv("HOME")
	origGitConfig := os.Getenv("GIT_CONFIG_GLOBAL")
	origAuthorName := os.Getenv("GIT_AUTHOR_NAME")
	origAuthorEmail := os.Getenv("GIT_AUTHOR_EMAIL")
	origCommitterName := os.Getenv("GIT_COMMITTER_NAME")
	origCommitterEmail := os.Getenv("GIT_COMMITTER_EMAIL")
	defer func() {
		os.Setenv("HOME", origHome)
		restoreEnv("GIT_CONFIG_GLOBAL", origGitConfig)
		restoreEnv("GIT_AUTHOR_NAME", origAuthorName)
		restoreEnv("GIT_AUTHOR_EMAIL", origAuthorEmail)
		restoreEnv("GIT_COMMITTER_NAME", origCommitterName)
		restoreEnv("GIT_COMMITTER_EMAIL", origCommitterEmail)
	}()
	os.Setenv("HOME", benchHome)
	os.Setenv("GIT_CONFIG_GLOBAL", benchConfig)
	os.Setenv("GIT_AUTHOR_NAME", "Hostile Ghost")
	os.Setenv("GIT_AUTHOR_EMAIL", "ghost@example.com")
	os.Setenv("GIT_COMMITTER_NAME", "Hostile Ghost")
	os.Setenv("GIT_COMMITTER_EMAIL", "ghost@example.com")

	label := "malformed-id-task-1"
	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native",
		"--tokens", "unmetered",
		"--slots-store", nativeStore(t),
		"--owner", "fake-1",
		"--harness", bin,
		"--model", "fake/fake-model",
		"--label", label,
		"--card", cardPath,
		"--slot", slot,
		"--root", root,
		"--deadline", "30s",
		"--no-wall",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())

	if rc != 2 {
		t.Fatalf("native run exit = %d, want 2 (refusal);\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "NATIVE REFUSED") {
		t.Fatalf("stderr does not contain NATIVE REFUSED:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "refusing to launch under nobody's name") {
		t.Fatalf("stderr does not contain expected refusal message:\n%s", stderr.String())
	}
	// #3193: the refusal names identity.tsv and the one remedy, the fleet converge.
	if line := stderr.String(); !strings.Contains(line, "identity.tsv") || !strings.Contains(line, "make -C fleet converge") {
		t.Fatalf("the refusal does not name identity.tsv and the remedy `make -C fleet converge`:\n%s", line)
	}

	// Verify harness was never started: neither native.log nor harness-output.log was created.
	nativeLog := filepath.Join(slot, "native.log")
	if _, err := os.Stat(nativeLog); !os.IsNotExist(err) {
		t.Errorf("native.log exists at %s, want harness never started", nativeLog)
	}
	jobDir := filepath.Join(slot, "jobs", label)
	harnessOut := filepath.Join(jobDir, "harness-output.log")
	if _, err := os.Stat(harnessOut); !os.IsNotExist(err) {
		t.Errorf("harness-output.log exists at %s, want harness never started", harnessOut)
	}
	commitIdentity := filepath.Join(jobDir, "commit-identity")
	if _, err := os.Stat(commitIdentity); !os.IsNotExist(err) {
		t.Errorf("commit-identity exists at %s, harness should not have run", commitIdentity)
	}
}

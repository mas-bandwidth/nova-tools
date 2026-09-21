package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCard(t *testing.T, dir, name, dependsOn string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	content := "RESULT " + strings.TrimSuffix(name, ".md") + " sha=1234567890ab\n"
	if dependsOn != "" {
		content += "depends-on: " + dependsOn + "\n"
	}
	content += "STEP 1. echo hello\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFillRefusesDependencyCycle(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "ready")
	launched := filepath.Join(root, "launched")
	for _, d := range []string{ready, launched} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// 2-card cycle: card-a -> card-b -> card-a
	writeCard(t, ready, "card-a.md", "card-b")
	writeCard(t, ready, "card-b.md", "card-a")

	// Setup fake machines registry
	machinesFile := filepath.Join(root, "machines.tsv")
	machinesContent := "studio.local\tstudio.local\tlinux/x64\tbench\tswarm-studio\t64\tcertified=2026-09-20\n"
	if err := os.WriteFile(machinesFile, []byte(machinesContent), 0o644); err != nil {
		t.Fatal(err)
	}

	launcherBin := writeMainFile(t, root, "bin/launcher.sh", "#!/bin/sh\nexit 0\n")
	_ = os.Chmod(launcherBin, 0o755)

	args := []string{
		"fill",
		"--ready", ready,
		"--launched", launched,
		"--machines", machinesFile,
		"--bench", "studio.local",
		"--capacity", "2",
		"--launcher", launcherBin,
		"--once",
	}

	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, time.Now().UTC())

	// Exit code must be 2 on cycle refusal
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d; stderr:\n%s", code, stderr.String())
	}

	// Stderr must contain the exact diagnostic
	wantDiag := "CYCLE REFUSED: card-a -> card-b -> card-a"
	if !strings.Contains(stderr.String(), wantDiag) {
		t.Fatalf("expected stderr to contain %q, got:\n%s", wantDiag, stderr.String())
	}

	// No card in a cycle is ever launched or written
	launchedEntries, err := os.ReadDir(launched)
	if err != nil {
		t.Fatal(err)
	}
	if len(launchedEntries) != 0 {
		t.Fatalf("expected launched dir to be empty, found %d entries", len(launchedEntries))
	}
}

func TestQueueRefusesDependencyCycle(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "ready")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}

	writeCard(t, ready, "card-a.md", "card-b")
	writeCard(t, ready, "card-b.md", "card-a")

	var stdout, stderr bytes.Buffer
	code := run([]string{"queue", "--ready", ready}, &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d; stderr:\n%s", code, stderr.String())
	}

	wantDiag := "CYCLE REFUSED: card-a -> card-b -> card-a"
	if !strings.Contains(stderr.String(), wantDiag) {
		t.Fatalf("expected %q, got:\n%s", wantDiag, stderr.String())
	}
}

func TestQueue3NodeCycle(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "ready")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}

	writeCard(t, ready, "card-1.md", "card-2")
	writeCard(t, ready, "card-2.md", "card-3")
	writeCard(t, ready, "card-3.md", "card-1")

	var stdout, stderr bytes.Buffer
	code := run([]string{"queue", "--queue", root}, &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d; stderr:\n%s", code, stderr.String())
	}

	wantDiag := "CYCLE REFUSED: card-1 -> card-2 -> card-3 -> card-1"
	if !strings.Contains(stderr.String(), wantDiag) {
		t.Fatalf("expected %q, got:\n%s", wantDiag, stderr.String())
	}
}

func TestLintRefusesDependencyCycle(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "ready")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}

	cardAPath := writeCard(t, ready, "card-a.md", "card-b")
	writeCard(t, ready, "card-b.md", "card-a")

	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "--card", cardAPath}, &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d; stderr:\n%s", code, stderr.String())
	}

	wantDiag := "CYCLE REFUSED: card-a -> card-b -> card-a"
	if !strings.Contains(stderr.String(), wantDiag) {
		t.Fatalf("expected %q, got:\n%s", wantDiag, stderr.String())
	}
}

func TestLintRefusesSelfCycle(t *testing.T) {
	root := t.TempDir()
	selfPath := writeCard(t, root, "card-self.md", "card-self")

	var stdout, stderr bytes.Buffer
	code := run([]string{"lint", "--card", selfPath}, &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("expected exit code 2, got %d; stderr:\n%s", code, stderr.String())
	}

	wantDiag := "CYCLE REFUSED: card-self -> card-self"
	if !strings.Contains(stderr.String(), wantDiag) {
		t.Fatalf("expected %q, got:\n%s", wantDiag, stderr.String())
	}
}

func TestQueueAndLintPassAcyclic(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "ready")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}

	writeCard(t, ready, "card-base.md", "")
	writeCard(t, ready, "card-mid.md", "card-base")
	writeCard(t, ready, "card-top.md", "card-mid")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"queue", "--ready", ready}, &stdout, &stderr, time.Now().UTC()); code != 0 {
		t.Fatalf("queue expected exit 0, got %d; stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "QUEUE OK") {
		t.Fatalf("expected QUEUE OK, got:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"lint", "--ready", ready}, &stdout, &stderr, time.Now().UTC()); code != 0 {
		t.Fatalf("lint expected exit 0, got %d; stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "LINT OK") {
		t.Fatalf("expected LINT OK, got:\n%s", stdout.String())
	}
}

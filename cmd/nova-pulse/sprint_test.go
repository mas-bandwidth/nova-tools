package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func makeTestMachines(t *testing.T, dir string) string {
	t.Helper()
	content := strings.Join([]string{
		"# name\tssh\tos/arch\troles\tseat\tcores\tnotes",
		"hulk\thulk\tlinux/x64\tbench\tswarm-hulk\t64\t-",
		"space\tspace\tlinux/x64\tbench\tswarm-space\t32\t-",
		"",
	}, "\n")
	path := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func makeTestPlan(t *testing.T, dir string) string {
	t.Helper()
	content := strings.Join([]string{
		"# test plan",
		"bench\thulk\t32\t2.50\t50.00",
		"bench\tspace\t16\t1.50\t25.00",
		"route\tflash\tgemini-2.5-flash",
		"route\tpro\tgemini-2.5-pro",
		"probe\t10.0",
		"",
	}, "\n")
	path := filepath.Join(dir, "plan.tsv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSprintCLIRequiresSubverb(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"sprint"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "subverb required") {
		t.Fatalf("stderr missing subverb required: %s", errb.String())
	}
}

func TestSprintCLIUnknownSubverb(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"sprint", "dance"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "unknown subverb \"dance\"") {
		t.Fatalf("stderr missing unknown subverb: %s", errb.String())
	}
}

func TestSprintStartCLIValidation(t *testing.T) {
	dir := t.TempDir()
	planPath := makeTestPlan(t, dir)
	machinesPath := makeTestMachines(t, dir)
	queueDir := filepath.Join(dir, "queue")

	// Missing --queue
	var out, errb bytes.Buffer
	code := run([]string{"sprint", "start", "--plan", planPath, "--machines", machinesPath}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("missing --queue exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--queue is required") {
		t.Fatalf("stderr missing --queue is required: %s", errb.String())
	}

	// Missing --plan
	out.Reset()
	errb.Reset()
	code = run([]string{"sprint", "start", "--queue", queueDir, "--machines", machinesPath}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("missing --plan exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--plan is required") {
		t.Fatalf("stderr missing --plan is required: %s", errb.String())
	}

	// Missing --machines
	out.Reset()
	errb.Reset()
	code = run([]string{"sprint", "start", "--plan", planPath, "--queue", queueDir}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("missing --machines exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--machines is required") {
		t.Fatalf("stderr missing --machines is required: %s", errb.String())
	}
}

func TestSprintCLISubcommandsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	planPath := makeTestPlan(t, dir)
	machinesPath := makeTestMachines(t, dir)
	queueDir := filepath.Join(dir, "queue")

	// 1. sprint set before start refuses on missing file
	var out, errb bytes.Buffer
	code := run([]string{"sprint", "set", "--queue", queueDir, "space", "cap", "64"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("sprint set before start exit = %d, want 2", code)
	}

	// 2. Mock mirror directory so git fetch succeeds or skip
	_ = os.MkdirAll(filepath.Join(queueDir, "mirrors", "hulk"), 0o755)
	_ = os.MkdirAll(filepath.Join(queueDir, "mirrors", "space"), 0o755)

	// Create fake git that always exits 0
	binDir := filepath.Join(dir, "bin")
	_ = os.MkdirAll(binDir, 0o755)
	fakeGit := filepath.Join(binDir, "git")
	_ = os.WriteFile(fakeGit, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+origPath)

	// 3. sprint start
	out.Reset()
	errb.Reset()
	code = run([]string{
		"sprint", "start",
		"--plan", planPath,
		"--queue", queueDir,
		"--machines", machinesPath,
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("sprint start exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "SPRINT OK") {
		t.Errorf("sprint start output missing SPRINT OK: %s", out.String())
	}

	// 4. sprint status
	out.Reset()
	errb.Reset()
	code = run([]string{
		"sprint", "status",
		"--plan", planPath,
		"--queue", queueDir,
		"--machines", machinesPath,
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("sprint status exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "drift=none") {
		t.Errorf("sprint status missing drift=none: %s", out.String())
	}

	// 5. sprint set
	out.Reset()
	errb.Reset()
	code = run([]string{
		"sprint", "set",
		"--queue", queueDir,
		"space", "cap", "64",
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("sprint set exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "SPRINT SET bench=space cap=16->64") {
		t.Errorf("sprint set output mismatch: %s", out.String())
	}

	// 6. sprint table (agent mode)
	out.Reset()
	errb.Reset()
	code = run([]string{
		"sprint", "table",
		"--queue", queueDir,
		"--bench", "space",
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("sprint table agent exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "SPRINT TABLE OK bench=space") {
		t.Errorf("sprint table output mismatch: %s", out.String())
	}

	// 7. sprint table (viewer mode)
	out.Reset()
	errb.Reset()
	code = run([]string{
		"sprint", "table",
		"--queue", queueDir,
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("sprint table viewer exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "SPRINT ROW host=space") {
		t.Errorf("sprint table viewer missing host=space: %s", out.String())
	}

	// 8. sprint funnel
	out.Reset()
	errb.Reset()
	code = run([]string{
		"sprint", "funnel",
		"--queue", queueDir,
		"--wave", "test-wave",
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("sprint funnel exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "SPRINT FUNNEL wave=test-wave") {
		t.Errorf("sprint funnel missing wave=test-wave: %s", out.String())
	}

	// 9. sprint stop
	out.Reset()
	errb.Reset()
	code = run([]string{
		"sprint", "stop",
		"--plan", planPath,
		"--queue", queueDir,
		"--machines", machinesPath,
	}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("sprint stop exit = %d, want 0; err=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "SPRINT STOP OK") {
		t.Errorf("sprint stop missing SPRINT STOP OK: %s", out.String())
	}
}

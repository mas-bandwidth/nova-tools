package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusPassCLI_CleanDirLintAndCleanTree(t *testing.T) {
	dir := t.TempDir()
	goCode := `package sample

func Answer() int {
	return 42
}
`
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte(goCode), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	args := []string{"status", "--pass", "--dir", dir, "--checks", "lint,clean-tree"}
	code := run(args, &stdout, &stderr, time.Now().UTC())
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr:\n%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "STATUS CHECK check=lint status=PASS") {
		t.Errorf("missing lint PASS in stdout:\n%s", out)
	}
	if !strings.Contains(out, "STATUS CHECK check=clean-tree status=PASS") {
		t.Errorf("missing clean-tree PASS in stdout:\n%s", out)
	}
	if !strings.Contains(out, "STATUS PASS OK") {
		t.Errorf("missing STATUS PASS OK in stdout:\n%s", out)
	}
}

func TestStatusPassCLI_ScriptCardAdmissionAndRefusal(t *testing.T) {
	dir := t.TempDir()

	// 1. Valid script card admitted with unmetered spend "-"
	validCard := filepath.Join(dir, "card-valid.md")
	content := "MODE: script\nARGV: [\"echo\", \"hello\"]\nRESULT: CARD-10 test\n"
	if err := os.WriteFile(validCard, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "--script-card", validCard}, &stdout, &stderr, time.Now().UTC())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 on valid script card; stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SCRIPT ADMITTED mode=script spend=- argv=[echo hello]") {
		t.Errorf("stdout = %q, want SCRIPT ADMITTED line with spend=-", stdout.String())
	}

	// 2. Login shell refused at admission
	loginCard := filepath.Join(dir, "card-login.md")
	loginContent := "MODE: script\nARGV: [\"bash\", \"-l\", \"-c\", \"echo bad\"]\nRESULT: CARD-11 test\n"
	if err := os.WriteFile(loginCard, []byte(loginContent), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"status", "--script-card", loginCard}, &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2 on login shell refusal", code)
	}
	if !strings.Contains(stderr.String(), "login shell") {
		t.Errorf("stderr does not name login shell refusal:\n%s", stderr.String())
	}
}

func TestCutKindCLI_RequireStatusPassFlag(t *testing.T) {
	dir := t.TempDir()
	queue, out := filepath.Join(dir, "queue"), filepath.Join(dir, "queue", "pending")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}

	// When --require-status-pass is specified on read card without valid base/git diff,
	// status pass fails red-first check, refusing cut.
	var stdout, stderr bytes.Buffer
	args := []string{
		"cut", "--kind", "read", "--repo", "mas-bandwidth/nova-tools", "--pr", "901",
		"--head", "abc123def456", "--title", "require status pass test",
		"--out", out, "--queue", queue, "--require-status-pass",
	}
	code := run(args, &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2 on failed status pass refusal", code)
	}
	if !strings.Contains(stderr.String(), "CUT REFUSED: status pass failed") {
		t.Errorf("stderr does not name CUT REFUSED: status pass failed:\n%s", stderr.String())
	}
}

func TestStatusPassCLI_ScriptCardExec_RefusalOnMissingSandbox(t *testing.T) {
	dir := t.TempDir()
	cardPath := filepath.Join(dir, "card-exec.md")
	content := "MODE: script\nRUN: [\"echo\", \"hello world\"]\nRESULT: CARD-20 test\n"
	if err := os.WriteFile(cardPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	args := []string{
		"status", "--script-card", cardPath, "--exec",
		"--sandbox", "/nonexistent/path/to/nova-sandbox",
	}
	code := run(args, &stdout, &stderr, time.Now().UTC())
	if code != 125 {
		t.Fatalf("exit = %d, want 125 when sandbox binary is missing for NetDeny; stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "nova-sandbox") {
		t.Errorf("stderr missing mention of nova-sandbox:\n%s", stderr.String())
	}
}

func TestStatusPassCLI_IdentityFailure(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	args := []string{
		"status", "--pass", "--dir", dir,
		"--head", "deadbeef12345678", "--repo", "mas-bandwidth/nova-tools",
	}
	code := run(args, &stdout, &stderr, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2 on identity resolution failure", code)
	}
	if !strings.Contains(stdout.String(), "check=identity") {
		t.Errorf("stdout does not name check=identity failure:\n%s", stdout.String())
	}
}

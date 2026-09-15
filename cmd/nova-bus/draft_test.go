package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #327: nova-bus draft prints the note to stdout and writes no file,
// so send has no path unless the caller redirected stdout or provided --file.
// These tests verify that:
// 1. When no --file is given, draft prints the skeleton to stdout and leaves stderr clean (released contract).
// 2. When --file is given, draft writes the skeleton to that file outside the bus,
//    prints DRAFT OK path=<file>, and exits 0.
// 3. When --file points inside the bus checkout, draft refuses with code 2.

func TestDraftWithoutFilePrintsSkeletonToStdoutAndLeavesStderrClean(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate").
		mustCode(t, 0).
		mustContain(t, "stdout", "To: Bo").
		mustContain(t, "stdout", "From: Ada").
		mustContain(t, "stdout", "Subject: gate")

	if r.stderr != "" {
		t.Fatalf("draft without --file wrote to stderr: %q", r.stderr)
	}
	if strings.Contains(r.stdout, "DRAFT OK") {
		t.Fatalf("stdout without --file should not contain DRAFT OK:\n%s", r.stdout)
	}
}

func TestDraftWritesFileWhenFileFlagGiven(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	draftFile := filepath.Join(t.TempDir(), "draft.md")
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--file", draftFile).
		mustCode(t, 0).
		mustContain(t, "stdout", "DRAFT OK path="+draftFile)

	data, err := os.ReadFile(draftFile)
	if err != nil {
		t.Fatalf("draft file was not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "To: Bo") || !strings.Contains(content, "From: Ada") || !strings.Contains(content, "Subject: gate") {
		t.Fatalf("draft file content missing expected headers:\n%s", content)
	}
}

func TestDraftRefusesFileInsideBusCheckout(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	insideFile := filepath.Join(checkout, "from-ada", "draft.md")
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--file", insideFile).
		mustCode(t, 2).
		mustContain(t, "stderr", "DRAFT REFUSED: --file")
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #327: nova-bus draft prints the note to stdout and writes no file,
// so send has no path unless the caller redirected stdout or provided --out.
// These tests verify that:
// 1. When no --out is given, draft prints the skeleton to stdout and hints the
//    redirect to send on stderr, so the silence between printing and writing is closed.
// 2. When --out is given, draft writes the skeleton to that file outside the bus,
//    prints DRAFT OK path=<file>, and exits 0.
// 3. When --out points inside the bus checkout, draft refuses with code 2.

func TestDraftWithoutFilePrintsSkeletonToStdoutAndHintsSendOnStderr(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate").
		mustCode(t, 0).
		mustContain(t, "stdout", "To: Bo").
		mustContain(t, "stdout", "From: Ada").
		mustContain(t, "stdout", "Subject: gate").
		mustContain(t, "stderr", "DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>")

	if strings.Contains(r.stdout, "DRAFT OK") {
		t.Fatalf("stdout without --out should not contain DRAFT OK:\n%s", r.stdout)
	}
}

func TestDraftWritesFileWhenOutFlagGiven(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	draftFile := filepath.Join(t.TempDir(), "draft.md")
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", draftFile).
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

// TestDraftUsageShowsRedirectSynopsis holds the synopsis promise: the draft line of
// `nova-bus help` must say the skeleton goes to stdout, so a reader Redirects it to a file
// instead of expecting draft to write one. The released synopsis spells the redirect
// `> <file>`.
func TestDraftUsageShowsRedirectSynopsis(t *testing.T) {
	t.Parallel()
	r := invoke(t, "", "help").mustCode(t, 0)
	if !strings.Contains(r.stdout, "> <file>") {
		t.Fatalf("draft synopsis does not show the redirect `> <file>`:\n%s", r.stdout)
	}
}

// TestDraftPrintsSendHintOnStderr closes the silence the card names: after the skeleton
// is printed to stdout and no --out was given, draft prints one line to stderr naming
// the next step, so a sender who just ran it knows the skeleton is a draft to redirect
// and then send.
func TestDraftPrintsSendHintOnStderr(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate").
		mustCode(t, 0).
		mustContain(t, "stderr", "nova-bus send --file")
	lines := strings.Split(strings.TrimRight(r.stderr, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("draft without --out should print exactly one hint line to stderr, got %d: %q", len(lines), r.stderr)
	}
}

func TestDraftRefusesFileInsideBusCheckout(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	insideFile := filepath.Join(checkout, "from-ada", "draft.md")
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", insideFile).
		mustCode(t, 2).
		mustContain(t, "stderr", "DRAFT REFUSED: --out")
}

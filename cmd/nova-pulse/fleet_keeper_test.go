package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFleetKeeperRefusesMissingQueue verifies that running fleet keeper without --queue
// refuses cleanly with exit 2 naming --queue.
func TestFleetKeeperRefusesMissingQueue(t *testing.T) {
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "keeper"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--queue") {
		t.Fatalf("refusal does not name --queue:\n%s", errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must be empty on refusal:\n%s", out.String())
	}
}

// TestFleetKeeperRunsThroughCommandLine tests end-to-end execution of the fleet keeper CLI.
func TestFleetKeeperRunsThroughCommandLine(t *testing.T) {
	qDir := t.TempDir()
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)
	oldNow := fleetNow
	fleetNow = func() time.Time { return now }
	defer func() { fleetNow = oldNow }()

	var out, errb bytes.Buffer
	code := run([]string{"fleet", "keeper", "--queue", qDir}, &out, &errb, now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:%s\nstderr:%s", code, out.String(), errb.String())
	}

	stdoutStr := out.String()
	if !strings.Contains(stdoutStr, "KEEPER WAKE hour=2026-09-21T15:00:00Z") {
		t.Errorf("stdout does not contain expected KEEPER WAKE line: %s", stdoutStr)
	}
	if !strings.Contains(stdoutStr, "receipt=") {
		t.Errorf("stdout does not name receipt path: %s", stdoutStr)
	}

	// Verify receipt file exists
	expectedReceipt := filepath.Join(qDir, "wake", "2026-09-21T15Z.receipt")
	content, err := os.ReadFile(expectedReceipt)
	if err != nil {
		t.Fatalf("expected receipt %s not found: %v", expectedReceipt, err)
	}

	text := string(content)
	if !strings.Contains(text, "# KEEPER WAKE RECEIPT: 2026-09-21T15:00:00Z") {
		t.Errorf("receipt header missing or wrong:\n%s", text)
	}
	if !strings.Contains(text, "## Durable Loops") {
		t.Errorf("receipt missing Durable Loops section:\n%s", text)
	}
	if !strings.Contains(text, "## Merge Bases") {
		t.Errorf("receipt missing Merge Bases section:\n%s", text)
	}
	if !strings.Contains(text, "## Results Folded") {
		t.Errorf("receipt missing Results Folded section:\n%s", text)
	}
}

// TestFleetKeeperHelpAndUnknownSubVerb confirms keeper is enumerated in fleet sub-verbs.
func TestFleetKeeperHelpAndUnknownSubVerb(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	if !strings.Contains(out.String(), "fleet   keeper") {
		t.Errorf("help does not contain 'fleet   keeper':\n%s", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"fleet", "nonesuch"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("unknown sub-verb exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "keeper") {
		t.Errorf("unknown sub-verb refusal does not list 'keeper':\n%s", errb.String())
	}
}

// TestFleetKeeperMutationRefusalEnsuresNoReceipt proves that an invalid command mutates nothing
// and leaves no receipt files on disk.
func TestFleetKeeperMutationRefusalEnsuresNoReceipt(t *testing.T) {
	qDir := t.TempDir()

	// Mutation: bad flag
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "keeper", "--queue", qDir, "--bogus-flag"}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}

	wakeDir := filepath.Join(qDir, "wake")
	if _, err := os.Stat(wakeDir); !os.IsNotExist(err) {
		t.Fatalf("wake directory must NOT be created when flag parsing fails: %v", err)
	}
}

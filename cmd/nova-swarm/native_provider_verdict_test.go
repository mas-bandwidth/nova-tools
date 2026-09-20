package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #2001 & #2011: NATIVE PROVIDER verdict.
//
// A run that fails on a provider 5xx, UnknownError, or 429 rate limit without publishing
// a result must be classified as NATIVE PROVIDER, not NATIVE INCOMPLETE.
// The verdict distinguishes provider infrastructure failure from model inability to produce code.

func nativeRunCapture(t *testing.T, label, card string) (stdout, stderr string, exitCode int, slotDir string) {
	t.Helper()
	t.Setenv("NOVA_SWARM_PROVIDER_BACKOFF", "0s")
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, label+".md")
	if err := os.WriteFile(cardPath, []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	var outBuf, errBuf bytes.Buffer
	rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &outBuf, &errBuf, time.Now())
	return outBuf.String(), errBuf.String(), rc, slot
}

// TestNativeProviderVerdictOnUnknownError proves that an UnknownError JSON payload
// (issue #2001) produces NATIVE PROVIDER with why=unexpected-server-error and ref=<ref>,
// and sets end=provider in usage.tsv.
func TestNativeProviderVerdictOnUnknownError(t *testing.T) {
	out, _, _, slot := nativeRunCapture(t, "unknown-err", "FAKE-UNKNOWN-ERROR\n")

	if strings.Contains(out, "NATIVE OK") {
		t.Fatalf("a provider error must never say OK:\n%s", out)
	}
	if strings.Contains(out, "NATIVE INCOMPLETE") {
		t.Fatalf("a provider error must say NATIVE PROVIDER, not NATIVE INCOMPLETE:\n%s", out)
	}
	if !strings.Contains(out, "NATIVE PROVIDER ") {
		t.Fatalf("verdict must be NATIVE PROVIDER:\n%s", out)
	}
	if !strings.Contains(out, "why=unexpected-server-error") {
		t.Fatalf("verdict must carry why=unexpected-server-error:\n%s", out)
	}
	if !strings.Contains(out, "ref=err_29c29bd4") {
		t.Fatalf("verdict must carry ref=err_29c29bd4:\n%s", out)
	}

	// Verify usage.tsv records end=provider
	usageRaw, err := os.ReadFile(filepath.Join(slot, "jobs", "unknown-err", "usage.tsv"))
	if err != nil {
		t.Fatalf("usage.tsv missing: %v", err)
	}
	if !strings.Contains(string(usageRaw), "\tprovider\t") {
		t.Fatalf("usage.tsv must record end=provider:\n%s", usageRaw)
	}
}

// TestNativeProviderVerdictOn5xx proves that a 5xx server error produces NATIVE PROVIDER.
func TestNativeProviderVerdictOn5xx(t *testing.T) {
	out, _, _, slot := nativeRunCapture(t, "err-5xx", "FAKE-5XX\n")

	if !strings.Contains(out, "NATIVE PROVIDER ") {
		t.Fatalf("verdict must be NATIVE PROVIDER:\n%s", out)
	}
	if !strings.Contains(out, "why=unexpected-server-error") {
		t.Fatalf("verdict must carry why=unexpected-server-error:\n%s", out)
	}
	if !strings.Contains(out, "ref=err_fake_5xx") {
		t.Fatalf("verdict must carry ref=err_fake_5xx:\n%s", out)
	}

	usageRaw, err := os.ReadFile(filepath.Join(slot, "jobs", "err-5xx", "usage.tsv"))
	if err != nil {
		t.Fatalf("usage.tsv missing: %v", err)
	}
	if !strings.Contains(string(usageRaw), "\tprovider\t") {
		t.Fatalf("usage.tsv must record end=provider:\n%s", usageRaw)
	}
}

// TestNativeProviderVerdictOnRateLimit proves that HTTP 429 produces NATIVE PROVIDER why=rate-limit.
func TestNativeProviderVerdictOnRateLimit(t *testing.T) {
	out, _, _, _ := nativeRunCapture(t, "err-429", "FAKE-429\n")

	if !strings.Contains(out, "NATIVE PROVIDER ") {
		t.Fatalf("verdict must be NATIVE PROVIDER:\n%s", out)
	}
	if !strings.Contains(out, "why=rate-limit") {
		t.Fatalf("verdict must carry why=rate-limit:\n%s", out)
	}
}

// TestNativePublishedResultIsNeverProviderDeath proves that a run that published
// RESULT.md is NATIVE OK even if logs mention a provider error earlier.
func TestNativePublishedResultIsNeverProviderDeath(t *testing.T) {
	out, _, _, _ := nativeRunCapture(t, "green-pub", "FAKE-PUBLISH-FIRST\nFAKE-5XX\n")

	if !strings.Contains(out, "NATIVE OK ") {
		t.Fatalf("a run with published result must be NATIVE OK:\n%s", out)
	}
	if strings.Contains(out, "NATIVE PROVIDER") {
		t.Fatalf("a published result cannot be NATIVE PROVIDER:\n%s", out)
	}
}

// TestNativeNonProviderFailureRemainsIncomplete proves that a card bug (e.g. exit 1 without provider error)
// remains NATIVE INCOMPLETE.
func TestNativeNonProviderFailureRemainsIncomplete(t *testing.T) {
	out, _, _, _ := nativeRunCapture(t, "plain-fail", "FAKE-RC 1\n")

	if !strings.Contains(out, "NATIVE INCOMPLETE ") {
		t.Fatalf("a normal card failure must be NATIVE INCOMPLETE:\n%s", out)
	}
	if strings.Contains(out, "NATIVE PROVIDER") {
		t.Fatalf("a normal card failure must not be NATIVE PROVIDER:\n%s", out)
	}
}

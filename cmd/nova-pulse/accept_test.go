package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The verb's door: the five required flags and the identity, refused together and by
// name, and the help line a stranger pastes.
func TestAcceptVerbRefusesWithoutItsFlags(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"accept"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, want := range []string{"--job", "--card", "--base", "--bench", "--cert", "--identity"} {
		if !strings.Contains(errb.String(), want) {
			t.Fatalf("the refusal does not name %s:\n%s", want, errb.String())
		}
	}
	errb.Reset()
	if code := run([]string{"accept", "--job", "j", "--card", "c", "--base", "b", "--bench", "n", "--cert", "f", "--identity", "Rowan"}, &out, &errb, time.Now().UTC()); code != 2 || !strings.Contains(errb.String(), "Name <email>") {
		t.Fatalf("a malformed --identity was not refused by shape: exit %d\n%s", code, errb.String())
	}
}

func TestAcceptUsageLine(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	if !strings.Contains(out.String(), "nova-pulse accept  --selftest --root <dir> --bench <name> --cert <path>") {
		t.Fatalf("help has no accept line:\n%s", out.String())
	}
}

// The embedded fixture set must let the selftest run with no --fixtures flag at all --
// the default invocation docs/CLI.md documents. Before the fix this REFUSES because
// go:embed silently drops base/, a nested module boundary (cold read of PR #1730, Emma
// ebdc9f57d190). Scoped to that ONE mechanism, deliberately: it never asks the gate to
// run to a full PASS, which would need the gate's wall tool -- this card must not name
// or invoke it; see DESIGN DEFAULT 3.
func TestAcceptSelftestRunsWithoutExplicitFixtures(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.txt")
	if err := os.WriteFile(cert, []byte("bench=lab legs=go,git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	run([]string{"accept", "--selftest", "--root", dir, "--bench", "lab", "--cert", cert}, &out, &errb, time.Now().UTC())
	if strings.Contains(errb.String(), "could not build the fixture repository") {
		t.Fatalf("base/ is still missing from the embedded fixtures:\nstdout:%s\nstderr:%s", out.String(), errb.String())
	}
}

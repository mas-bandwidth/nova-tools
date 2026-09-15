package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// THE NATIVE OPENCODE PATH (issue #296, slice 2). A frozen run configuration is executed
// directly against the real fake harness, with no pool, no worker description and no
// supervisor: the binary, the model, the card text, the slot and the deadline are the whole
// truth. These tests run the same fake harness the dispatcher tests run, so the child that
// starts is the one whose argv, sleep and exit the machinery already trusts.

func nativeHarness(t *testing.T) string {
	t.Helper()
	bin, err := build(t, t.TempDir(), "fake-harness", "./cmd/nova-swarm/testdata/fakeharness")
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// aSlot returns a slot dir and the root it is under, both fresh.
func aSlot(t *testing.T) (root, slot string) {
	t.Helper()
	root = t.TempDir()
	slot = filepath.Join(root, "slot-1")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, slot
}

// TestNativeRunRefusesMissingBinary: a binary that does not exist, and one that exists but
// is not executable, are both the refusal that runs before any child can start.
func TestNativeRunRefusesMissingBinary(t *testing.T) {
	root, slot := aSlot(t)
	for _, tc := range []struct {
		name   string
		binary string
		word   string
	}{
		{"missing", filepath.Join(t.TempDir(), "no-such-binary"), "missing"},
		{"not_executable", func() string {
			p := filepath.Join(t.TempDir(), "data")
			if err := os.WriteFile(p, []byte("not a program\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return p
		}(), "not executable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: tc.binary, model: "fake/fake-model", label: "lbl",
				card: []byte("a card\n"), slotDir: slot, root: root, deadline: time.Second,
			}, &errOut)
			if code != 2 {
				t.Fatalf("a bad binary exits 2, got %d:\n%s", code, errOut.String())
			}
			if !strings.Contains(errOut.String(), "NATIVE REFUSED") {
				t.Fatalf("the refusal is one REFUSED line, got:\n%s", errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.word) {
				t.Fatalf("the refusal names its reason (%s):\n%s", tc.word, errOut.String())
			}
			if got := strings.Count(strings.TrimSpace(errOut.String()), "\n") + 1; got != 1 {
				t.Fatalf("exactly one REFUSED line, got %d:\n%s", got, errOut.String())
			}
		})
	}
}

// TestNativeRunRecordsCardAndBinaryHashes: a run that finishes records the child's exit
// code, the wall it took, and the sha256 of the card text and of the binary itself.
func TestNativeRunRecordsCardAndBinaryHashes(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := []byte("the card text, byte for byte\nwith a second line\n")

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "a-label",
		card: card, slotDir: slot, root: root, deadline: 30 * time.Second,
	}, &errOut)
	if code != 0 {
		t.Fatalf("a finished run exits 0, got %d:\n%s", code, errOut.String())
	}
	if res.rc != 0 {
		t.Fatalf("the child exits 0, recorded %d", res.rc)
	}
	if res.wallSeconds <= 0 {
		t.Fatalf("the wall is positive, recorded %v", res.wallSeconds)
	}
	wantCardSum := sha256.Sum256(card)
	wantCard := hex.EncodeToString(wantCardSum[:])
	if res.cardSHA256 != wantCard {
		t.Errorf("card sha256 is %s, want %s", res.cardSHA256, wantCard)
	}
	wantBinary, err := fileSHA256(bin)
	if err != nil {
		t.Fatal(err)
	}
	if res.binarySHA256 != wantBinary {
		t.Errorf("binary sha256 is %s, want %s", res.binarySHA256, wantBinary)
	}
}

// TestNativeRunKillsAtDeadline: a child that sleeps past the wall is killed by it, and the
// run records a non-zero exit rather than hanging.
func TestNativeRunKillsAtDeadline(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	start := time.Now()
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card: []byte("FAKE-SLEEP 60\n"), slotDir: slot, root: root, deadline: time.Second,
	}, &errOut)
	elapsed := time.Since(start)
	if code != 0 {
		t.Fatalf("a deadline kill is not a refusal, got exit %d:\n%s", code, errOut.String())
	}
	if res.rc == 0 {
		t.Fatalf("the deadline killed the child, and the run records a non-zero exit")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the deadline should cut the run short, but it took %v", elapsed)
	}
}

// TestNativeRunAuthCopyIs0600: the named provider's entry is copied from the auth file into
// the data home, mode 0600, and no other provider's entry travels with it.
func TestNativeRunAuthCopyIs0600(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(auth, []byte(`{"fake":"the-fake-secret","other":"the-other-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, authFile: auth, deadline: 30 * time.Second,
	}, &errOut)
	if code != 0 {
		t.Fatalf("an 0600 auth copy runs, got exit %d:\n%s", code, errOut.String())
	}
	copied := filepath.Join(slot, "data", "auth.json")
	st, err := os.Stat(copied)
	if err != nil {
		t.Fatalf("the auth copy was not written: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("the auth copy is mode %04o, want 0600", st.Mode().Perm())
	}
	body, err := os.ReadFile(copied)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"fake":"the-fake-secret"}` {
		t.Errorf("the copy holds only the named provider entry, got %s", body)
	}
}

// TestNativeRunRefusalsNameTheirReason drives the remaining three refusals -- a model with
// no provider prefix, an auth file looser than 0600, and a slot outside its root -- so each
// prints its one REFUSED line.
func TestNativeRunRefusalsNameTheirReason(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	looseAuth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(looseAuth, []byte(`{"fake":"secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-slot")

	cases := []struct {
		name string
		cfg  nativeRunConfig
		word string
	}{
		{
			"model_no_prefix",
			nativeRunConfig{binary: bin, model: "no-prefix", card: []byte("x\n"), slotDir: slot, root: root, deadline: time.Second},
			"no provider prefix",
		},
		{
			"auth_not_0600",
			nativeRunConfig{binary: bin, model: "fake/fake-model", card: []byte("x\n"), slotDir: slot, root: root, authFile: looseAuth, deadline: time.Second},
			"would not be 0600",
		},
		{
			"slot_outside_root",
			nativeRunConfig{binary: bin, model: "fake/fake-model", card: []byte("x\n"), slotDir: outside, root: root, deadline: time.Second},
			"outside the configured root",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer
			_, code := nativeRun(tc.cfg, &errOut)
			if code != 2 {
				t.Fatalf("the refusal exits 2, got %d:\n%s", code, errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.word) {
				t.Fatalf("the refusal names its reason (%s):\n%s", tc.word, errOut.String())
			}
		})
	}
}

// TestCmdNativeCLI verifies the native subcommand entry point via run().
func TestCmdNativeCLI(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("test card line 1\nline 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Missing flags -> exit 2 with refusal
	var stdout, stderr bytes.Buffer
	rc := run([]string{"native"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 2 {
		t.Fatalf("missing flags must exit 2, got %d", rc)
	}
	if !strings.Contains(stderr.String(), "--harness is required") {
		t.Fatalf("expected --harness is required, got:\n%s", stderr.String())
	}

	// Success run -> exit 0 with NATIVE OK
	stdout.Reset()
	stderr.Reset()
	args := []string{
		"native",
		"--harness", bin,
		"--model", "fake/fake-model",
		"--label", "test-label",
		"--card", cardPath,
		"--slot", slot,
		"--root", root,
		"--deadline", "10s",
	}
	rc = run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("native run must exit 0, got %d:\nstdout: %s\nstderr: %s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE OK") {
		t.Fatalf("stdout must contain NATIVE OK, got:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "label=test-label") {
		t.Fatalf("stdout must contain label=test-label, got:\n%s", stdout.String())
	}
}

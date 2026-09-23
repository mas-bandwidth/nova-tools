package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE PULSE LAUNCH BENCH SLOT LEASE (nova-tools#1903).
//
// SPEC-SWARM "Bench slot leases" rule 2: every launcher — including
// `nova-pulse launch` — takes a lease per card; a launch without a lease is
// refused by the launcher. `native` already refuses without --slots-store and
// --owner. `nova-swarm batch` without a --runner does too. Pulse launch always
// execs `batch --runner nova-native-runner.sh` and never passed the two flags,
// so batch treated the runner as somebody else's program and started the card
// with no lease. These tests pin the hole closed: without the flags launch
// refuses before any batch, and with them the flags reach the batch argv.
//
// No paid native, no real harness: the fake nova-swarm on PATH records argv
// and exits 0. The lease itself is native's; this verb's job is to require
// the flags and pass them through.

func aPulseSlotStore(t *testing.T) string {
	t.Helper()
	store := filepath.Join(t.TempDir(), "slots-store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "shares.tsv"), []byte("capacity\t8\nreserve\t0\nfake-1\t8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return store
}

// TestLaunchWithoutASlotsStoreRefuses: a pulse with cards, free slots, and a
// fake swarm still refuses when --slots-store/--owner are missing. The refusal
// is native's one line, whole, so a caller who greps for it finds the same
// string. No batch is started.
func TestLaunchWithoutASlotsStoreRefuses(t *testing.T) {
	const want = swarm.NoSlotsStoreRefusal
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 1)

	for _, tc := range []struct {
		name  string
		store string
		owner string
	}{
		{"neither", "", ""},
		{"store_without_owner", "STORE", ""},
		{"owner_without_store", "", "fake-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := tc.store
			if store == "STORE" {
				store = aPulseSlotStore(t)
			}
			var stdout, stderr bytes.Buffer
			code := Launch(LaunchInput{
				Cards: cards, Root: root, Slots: 2, Deadline: "120",
				SlotsStore: store, SlotOwner: tc.owner,
				Stdout: &stdout, Stderr: &stderr,
				Now: func() time.Time { return time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC) },
			})
			out, errb := stdout.String(), stderr.String()
			if code != 2 {
				t.Fatalf("a launch with no bench slot lease is refused with exit 2, got %d:\n%s%s", code, out, errb)
			}
			if got := strings.TrimSuffix(errb, "\n"); got != want {
				t.Errorf("the refusal is exactly\n  %s\nand it printed\n  %s", want, got)
			}
			if n := len(strings.Split(strings.TrimSuffix(errb, "\n"), "\n")); n != 1 {
				t.Errorf("the refusal is ONE line, got %d:\n%s", n, errb)
			}
			if out != "" {
				t.Errorf("a refusal writes nothing to stdout, got: %q", out)
			}
			if raw, err := os.ReadFile(argvLog); err == nil && strings.TrimSpace(string(raw)) != "" {
				t.Fatalf("zero batch runs, got %q", raw)
			}
		})
	}
}

// TestLaunchWithSlotsStorePassesFlagsToBatch is the negative control: with a
// store and an owner, launch still admits, and the batch argv carries both
// flags. A launch that required the flags and then dropped them would still
// start cards with no lease.
func TestLaunchWithSlotsStorePassesFlagsToBatch(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 1)
	store := aPulseSlotStore(t)

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 2, Deadline: "120",
		SlotsStore: store, SlotOwner: "fake-1",
		Now: func() time.Time { return time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC) },
	})
	if code != 0 {
		t.Fatalf("a launch with a bench slot store still works, got exit %d; stderr=%s", code, errb)
	}
	if !strings.Contains(out, "PULSE OK") {
		t.Fatalf("stdout=%q, want PULSE OK", out)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	if line == "" {
		t.Fatal("the batch ran; argv log is empty")
	}
	if !strings.Contains(line, "--slots-store "+store) {
		t.Fatalf("batch argv lacks --slots-store %s: %q", store, line)
	}
	if !strings.Contains(line, "--owner fake-1") {
		t.Fatalf("batch argv lacks --owner fake-1: %q", line)
	}
}

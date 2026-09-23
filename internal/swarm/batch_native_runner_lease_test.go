package swarm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// THE NATIVE-RUNNER BATCH LEASE (nova-tools#1903).
//
// `batch --runner` skipped the no-store refusal because "the runner is
// somebody else's program". Pulse launch's runner is this project's
// `nova-native-runner.sh`: it is `native` with extra argv, not somebody
// else's program. A batch that names that runner without --slots-store and
// --owner is refused with native's one line, before any card starts.
//
// A custom test runner (the rest of this package) is still somebody else's
// program and is not held to this. The runner path is never started: the
// refusal is before scatter.

func TestBatchNativeRunnerWithoutASlotsStoreRefuses(t *testing.T) {
	const want = NoSlotsStoreRefusal
	dir := t.TempDir()
	cards := writeCards(t, dir, [][2]string{{"a", "RESULT a\nok\n"}})
	// The path's basename is the native runner. The file need not exist: a
	// launch without a lease is refused before any process starts.
	runner := filepath.Join(dir, "nova-native-runner.sh")

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
				store = aBenchSlotStore(t)
			}
			var out, errb bytes.Buffer
			code := Batch(BatchInput{
				ID: "omit-runner", Deadline: 10 * time.Second, Cards: cards, Root: dir,
				Runner: runner, SlotsStore: store, SlotOwner: tc.owner,
				Stdout: &out, Stderr: &errb,
			})
			if code != 2 {
				t.Fatalf("a native-runner batch with no bench slot lease is refused with exit 2, got %d:\n%s%s", code, out.String(), errb.String())
			}
			if got := strings.TrimSuffix(errb.String(), "\n"); got != want {
				t.Errorf("the refusal is exactly\n  %s\nand it printed\n  %s", want, got)
			}
			if n := len(strings.Split(strings.TrimSuffix(errb.String(), "\n"), "\n")); n != 1 {
				t.Errorf("the refusal is ONE line, got %d:\n%s", n, errb.String())
			}
			if out.String() != "" {
				t.Errorf("a refusal writes nothing to stdout, got: %q", out.String())
			}
			if _, err := os.Stat(filepath.Join(dir, "1", "jobs", "a")); !os.IsNotExist(err) {
				t.Fatalf("no job directory is made when the lease is refused: %v", err)
			}
		})
	}
}

// TestBatchNativeRunnerWithSlotsStoreStillRuns is the negative control on
// the batch side: the native-runner name with a store is not itself a
// refusal, and the second hop forwards the store and owner as
// NOVA_SWARM_SLOTS_STORE and NOVA_SWARM_SLOT_OWNER on the runner's
// environment. A test that only checked exit 0 would stay green if that
// forwarding were deleted.
func TestBatchNativeRunnerWithSlotsStoreStillRuns(t *testing.T) {
	dir := t.TempDir()
	cards := writeCards(t, dir, [][2]string{{"a", "RESULT a\nok\n"}})
	store := aBenchSlotStore(t)
	const owner = "fake-1"
	named := runnerDoing(t, dir, "nova-native-runner.sh",
		runnerStep{Op: "write", Path: "{root}/.envstore", Body: "{env:NOVA_SWARM_SLOTS_STORE}"},
		runnerStep{Op: "write", Path: "{root}/.envowner", Body: "{env:NOVA_SWARM_SLOT_OWNER}"},
		runnerStep{Op: "mkdir", Path: "{job}"},
		publishCard("{job}"),
	)

	var out, errb bytes.Buffer
	code := Batch(BatchInput{
		ID: "with-store", Deadline: 30 * time.Second, Cards: cards, Root: dir,
		Runner: named, SlotsStore: store, SlotOwner: owner, Tokens: "unmetered",
		Stdout: &out, Stderr: &errb,
	})
	if strings.Contains(errb.String(), NoSlotsStoreRefusal) {
		t.Fatalf("a native-runner batch with a store is not the no-store refusal:\n%s", errb.String())
	}
	if code != 0 {
		t.Fatalf("a native-runner batch with a store still runs, got exit %d:\n%s%s", code, out.String(), errb.String())
	}
	if got := strings.TrimSpace(readTestFile(t, filepath.Join(dir, ".envstore"))); got != store {
		t.Fatalf("NOVA_SWARM_SLOTS_STORE is %q, want the exact store %q", got, store)
	}
	if got := strings.TrimSpace(readTestFile(t, filepath.Join(dir, ".envowner"))); got != owner {
		t.Fatalf("NOVA_SWARM_SLOT_OWNER is %q, want %q", got, owner)
	}
}

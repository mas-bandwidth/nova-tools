//go:build slow

package swarm

// #636 END TO END, behind the slow tag (#516): this case links a real nova-swarm and runs a
// card through its own `native` under the wall. The `go build` it costs took internal/swarm
// from 63 s to 95 s on one bench, which is the whole reason the tag exists; the argv
// contract it proves is covered in process by TestSelfNativeBuildsTheNativeArgv on every PR.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBatchRunsACardThroughItsOwnNativeWithNoRunner is issue #636's red test: with no
// --runner, batch refused the whole invocation ("--runner is required; it wants the command
// to start once per card"), although `native` lives in this same binary. Now --harness names
// the harness and each card runs through this binary's own native verb: the harness.log
// carries native's own NATIVE OK line.
func TestBatchRunsACardThroughItsOwnNativeWithNoRunner(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	harness := filepath.Join(dir, "harness")
	if err := os.WriteFile(harness, []byte("#!/bin/sh\necho FAKE-HARNESS \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(dir, "nova-swarm")
	if b, err := exec.Command("go", "build", "-o", self, "github.com/mas-bandwidth/nova-tools/cmd/nova-swarm").CombinedOutput(); err != nil {
		t.Fatalf("go build nova-swarm: %v: %s", err, b)
	}
	tsv := slotsCard(t, dir, "card-d", "-")
	var out, errb bytes.Buffer
	Batch(BatchInput{
		ID: "R4", Deadline: 25 * time.Second, Cards: tsv, Root: root,
		Harness: harness, Slots: "1-2", Self: self,
		Stdout: &out, Stderr: &errb,
	})
	if strings.Contains(errb.String(), "--runner is required") {
		t.Fatalf("batch still demands a runner script: %q", errb.String())
	}
	raw, err := os.ReadFile(filepath.Join(root, "1", "jobs", "card-d", "harness.log"))
	if err != nil {
		t.Fatalf("no harness log under the slot, so no native ran: %v; err=%q", err, errb.String())
	}
	if !strings.Contains(string(raw), "NATIVE OK label=card-d") {
		t.Fatalf("the card did not run through this binary's own native: %q", raw)
	}
}


package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// ISSUE #1585, Stella's finding, as two `native` runs in one physical job directory.
//
// The bench store gives two concurrent runs different SEATS, and #1562's identity repair
// makes those seats correct. Their `<slot>/jobs/<label>` was still ONE PATH, and so were
// the data home, the temp directory and the logs under it. The first run to exit removed
// `<job>/.lease`, and the second -- alive, still working -- lost its reaper protection,
// because its heartbeat was a bare Chtimes that cannot restore a file that is gone.
//
// SPEC-SWARM settles which repair is right before either is written. Under **Slots** a
// worker has "its own data home", "its own job directory", and "a slot is held by exactly
// one worker"; under **the races, taken out**, two workers on one data home is the
// 2026-09-10 `database is locked` failure, closed on purpose. Two live runs in one job
// directory is therefore not a thing to make safe: it is a thing to REFUSE. The lease is
// also pid-fenced (internal/swarm, TestAReleaseNeverRemovesAnotherRunsLease), so that a
// release can never remove a lease it did not take even when a hand, an older binary or a
// bench script has freed the path.
//
// THE BARRIER IS THE NOTE FILE, not a duration: the first run's harness blocks on
// `FAKE-AWAIT-NOTE` until this test writes `<job>/note`, so the overlap window is exactly
// as long as the assertions take and no run here waits on a chosen number of seconds.
func TestASecondNativeRunInOneJobDirectoryIsRefusedAndTheFirstIsUntouched(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	const label = "shared-label"
	jobDir := filepath.Join(slot, "jobs", label)
	lease := filepath.Join(jobDir, swarm.JobLeaseName)

	// RUN A: it takes the job directory and then blocks in its harness on the note.
	type outcome struct {
		code int
		err  string
	}
	first := make(chan outcome, 1)
	go func() {
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: label,
			card:    []byte("FAKE-AWAIT-NOTE 120\n"),
			slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
		}, &errOut)
		first <- outcome{code: code, err: errOut.String()}
	}()

	// A holds the directory the moment its lease is on disk. That file IS the readiness
	// signal: it is written before the child starts and it is what the reaper reads.
	held := awaitLease(t, lease)
	if !strings.Contains(held, "label="+label+"\n") {
		t.Fatalf("A's lease does not name its card:\n%s", held)
	}

	// RUN B: the same slot, the same label, the same physical directory.
	var errB bytes.Buffer
	_, codeB := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card:    []byte("a second card\n"),
		slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
	}, &errB)

	if codeB != 2 {
		t.Errorf("a second run in a live job directory exits 2, got %d:\n%s", codeB, errB.String())
	}
	if !strings.Contains(errB.String(), "NATIVE REFUSED") {
		t.Errorf("the refusal is one REFUSED line, got:\n%s", errB.String())
	}
	if !strings.Contains(errB.String(), "held by a live run") {
		t.Errorf("the refusal does not say why:\n%s", errB.String())
	}
	if !strings.Contains(errB.String(), "pid=") {
		t.Errorf("the refusal does not name the holder:\n%s", errB.String())
	}
	if got := strings.Count(strings.TrimSpace(errB.String()), "\n") + 1; got != 1 {
		t.Errorf("exactly one REFUSED line, got %d:\n%s", got, errB.String())
	}

	// AND THE FIRST RUN IS EXACTLY AS IT WAS. The lease is still A's, byte for byte: the
	// refused launch neither truncated it, rewrote it, nor removed it.
	now, err := os.ReadFile(lease)
	if err != nil {
		t.Fatalf("the refused second run removed the live run's lease: %v (#1585)", err)
	}
	if string(now) != held {
		t.Errorf("the refused second run rewrote the live run's lease:\nbefore:\n%s\nafter:\n%s", held, now)
	}

	// Release the barrier and let A finish on its own terms.
	if err := os.WriteFile(filepath.Join(jobDir, "note"), []byte("go on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := <-first
	if got.code != 0 {
		t.Fatalf("the first run did not finish cleanly after the second was refused: exit %d\n%s", got.code, got.err)
	}
	// A finished, so ITS release removes ITS lease: a finished job leaves nothing behind
	// that pretends to be alive.
	if _, err := os.Lstat(lease); !os.IsNotExist(err) {
		t.Errorf("the finished run left its lease behind: %v", err)
	}
}

// awaitLease answers the lease file's bytes as soon as there are any. The deadline only
// bounds a failure; nothing here waits on a chosen duration when the file is already there.
func awaitLease(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			return string(raw)
		}
		if time.Now().After(deadline) {
			t.Fatalf("no lease appeared at %s: the first run never took the job directory", path)
		}
	}
}

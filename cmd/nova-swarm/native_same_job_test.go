package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// ISSUE #1585, as two `native` runs in one physical job directory.
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
	t.Parallel()

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
	require.Contains(t, held, "label="+label+"\n", "A's lease does not name its card:\n%s", held)

	// RUN B: the same slot, the same label, the same physical directory.
	var errB bytes.Buffer
	_, codeB := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card:    []byte("a second card\n"),
		slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
	}, &errB)

	assert.Equal(t, 2, codeB, "a second run in a live job directory exits 2, got %d:\n%s", codeB, errB.String())
	assert.Contains(t, errB.String(), "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", errB.String())
	assert.Contains(t, errB.String(), "held by a live run", "the refusal does not say why:\n%s", errB.String())
	assert.Contains(t, errB.String(), "pid=", "the refusal does not name the holder:\n%s", errB.String())
	lines := strings.Count(strings.TrimSpace(errB.String()), "\n") + 1
	assert.Equal(t, 1, lines, "exactly one REFUSED line, got %d:\n%s", lines, errB.String())

	// AND THE FIRST RUN IS EXACTLY AS IT WAS. The lease is still A's, byte for byte: the
	// refused launch neither truncated it, rewrote it, nor removed it.
	now, err := os.ReadFile(lease)
	require.NoError(t, err, "the refused second run removed the live run's lease (#1585)")
	assert.Equal(t, held, string(now), "the refused second run rewrote the live run's lease:\nbefore:\n%s\nafter:\n%s", held, now)

	// Release the barrier and let A finish on its own terms.
	require.NoError(t, os.WriteFile(filepath.Join(jobDir, "note"), []byte("go on\n"), 0o644))
	got := <-first
	require.Equal(t, 0, got.code, "the first run did not finish cleanly after the second was refused: exit %d\n%s", got.code, got.err)
	// A finished, so ITS release removes ITS lease: a finished job leaves nothing behind
	// that pretends to be alive.
	_, err = os.Lstat(lease)
	assert.True(t, os.IsNotExist(err), "the finished run left its lease behind: %v", err)
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
		require.False(t, time.Now().After(deadline), "no lease appeared at %s: the first run never took the job directory", path)
	}
}

// SECOND P1 AT THE VERB (#1585, HOLD on 6146897a). With `.lease` a path the
// launcher cannot establish ownership at -- an owned directory, here -- the take used to
// answer "no lease, carry on", and BOTH of two runs were told they held the job directory.
// A run that cannot prove it owns its job directory now does not start, and it says so in
// one line with a remedy, BEFORE it has written anything into the shared place: no data
// home, no temp directory, no harness output, no argv for the wall.
func TestANativeRunThatCannotEstablishOwnershipRefusesBeforeItWritesAnything(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	const label = "unownable"
	jobDir := filepath.Join(slot, "jobs", label)
	require.NoError(t, os.MkdirAll(filepath.Join(jobDir, swarm.JobLeaseName), 0o755))

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card:    []byte("a card\n"),
		slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
	}, &errOut)

	require.Equal(t, 2, code, "a run that cannot take its job lease exits 2, got %d:\n%s", code, errOut.String())
	assert.Contains(t, errOut.String(), "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", errOut.String())
	assert.Contains(t, errOut.String(), "cannot prove it owns its job directory", "the refusal does not say what it could not establish:\n%s", errOut.String())
	assert.Contains(t, errOut.String(), "run it again", "the refusal carries no remedy:\n%s", errOut.String())
	got := strings.Count(strings.TrimSpace(errOut.String()), "\n") + 1
	assert.Equal(t, 1, got, "exactly one REFUSED line, got %d:\n%s", got, errOut.String())

	// NOTHING WAS WRITTEN. The job directory holds what the test put there and not one
	// thing more, and the slot has no data home or temp directory for this label.
	entries, err := os.ReadDir(jobDir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.Equal(t, swarm.JobLeaseName, e.Name(), "the refused run wrote %q into a job directory it does not own", e.Name())
	}
	_, err = os.Stat(filepath.Join(slot, "tmp", label))
	assert.True(t, os.IsNotExist(err), "the refused run made its temp directory anyway: %v", err)
}

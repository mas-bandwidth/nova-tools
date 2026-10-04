//go:build functional

package update

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Rule 25: hold the production report writer after sync/close and before its
// atomic rename. A real process kill must preserve the old snapshot and both the
// interrupted writer's bytes and a different writer's temporary. The later
// reporter is the built CLI, with the ordinary production rename operation.
func TestRule25SnapshotSurvivesAReporterKilledWhileWriting(t *testing.T) {
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "s.json")
	first := manifest(t, row("x", "tool", printer(t, "v1.0.0"), "npm:unused", "none"))
	var goodOut bytes.Buffer
	if rc := Run("nova-update", []string{"report", "--file", first, "--snapshot", snapshot}, "test", &goodOut, &goodOut, Environment{}); rc != 0 {
		require.EqualValuesf(t, 0, rc, "exit %d\n%s", rc, goodOut.String())
	}
	settled, err := os.ReadFile(snapshot)
	if err != nil {
		require.NoError(t, err, err)
	}
	foreign := filepath.Join(dir, snapshotTempPrefix+"another-writer")
	foreignBytes := []byte("another writer's unfinished work\n")
	if err := os.WriteFile(foreign, foreignBytes, 0600); err != nil {
		require.NoError(t, err, err)
	}

	second := manifest(t, row("x", "tool", printer(t, "v2.0.0"), "npm:unused", "none"))
	ready := filepath.Join(dir, "before-rename")
	input, heldOpen, err := os.Pipe()
	if err != nil {
		require.NoError(t, err, err)
	}
	t.Cleanup(func() { _ = input.Close(); _ = heldOpen.Close() })
	c := exec.Command(os.Args[0], "-test.run=^TestSnapshotRenameBarrierHelper$")
	c.Env = append(os.Environ(), "NOVA_SNAPSHOT_BARRIER="+ready,
		"NOVA_SNAPSHOT_PATH="+snapshot, "NOVA_SNAPSHOT_MANIFEST="+second)
	c.Stdin = input // the parent never writes or closes this pipe before the kill
	var output bytes.Buffer
	c.Stdout, c.Stderr = &output, &output
	if err := c.Start(); err != nil {
		require.NoError(t, err, err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = c.Wait(); close(done) }()
	t.Cleanup(func() { _ = c.Process.Kill(); <-done })

	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	var interrupted string
	for interrupted == "" {
		select {
		case <-done:
			require.Failf(t, "", "reporter exited before its rename barrier: %v\n%s", waitErr, output.String())
		case <-deadline.C:
			require.Fail(t, fmt.Sprintln("reporter did not reach its rename barrier"))
		case <-tick.C:
			if name, err := os.ReadFile(ready); err == nil {
				candidate := string(name)
				if filepath.Dir(candidate) == dir && strings.HasPrefix(filepath.Base(candidate), snapshotTempPrefix) {
					if _, err := os.Stat(candidate); err == nil {
						interrupted = candidate
					}
				}
			}
		}
	}
	inFlight, err := os.ReadFile(interrupted)
	if err != nil {
		require.NoError(t, err, err)
	}
	if bytes.Equal(inFlight, settled) {
		require.Fail(t, fmt.Sprintln("replacement must differ from the previously committed snapshot"))
	}
	if err := validateSnapshot(inFlight); err != nil {
		require.NoErrorf(t, err, "writer reached rename with invalid replacement: %v", err)
	}
	if err := c.Process.Kill(); err != nil {
		require.NoErrorf(t, err, "could not kill the held reporter: %v", err)
	}
	<-done
	if _, ok := waitErr.(*exec.ExitError); !ok {
		require.Failf(t, "", "reporter was not terminated unsuccessfully: %v", waitErr)
	}
	assertBytes := func(path string, want []byte) {
		t.Helper()
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			require.Failf(t, "", "%s did not preserve its exact bytes: %v", filepath.Base(path), err)
		}
	}
	assertBytes(snapshot, settled)
	assertBytes(interrupted, inFlight)
	assertBytes(foreign, foreignBytes)

	var finalOut bytes.Buffer
	if rc := Run("nova-update", []string{"report", "--file", second, "--snapshot", snapshot}, "test", &finalOut, &finalOut, Environment{}); rc != 0 {
		require.EqualValuesf(t, 0, rc, "a later reporter could not use the surviving snapshot: exit %d\n%s", rc, finalOut.String())
	}
	state, err := readSnapshot(snapshot)
	if err != nil || len(state.Observed) != 1 {
		require.Failf(t, "", "later snapshot must contain one readable observation: %v", err)
	}
	for _, observation := range state.Observed {
		if observation.Raw != "v2.0.0" || observation.Status != "known" {
			require.Failf(t, "", "later reporter did not commit the replacement observation: %+v", observation)
		}
	}
	assertBytes(interrupted, inFlight)
	assertBytes(foreign, foreignBytes)
	t.Log("terminated one reporter at the held pre-rename boundary; old snapshot and both temporaries preserved; later reporter succeeded")
}

// The hook and its environment protocol exist only in this test executable.
// Main runs the same report path as the CLI. On arrival at the rename operation,
// the writer has synced and closed its actual temporary but cannot rename it
// until stdin is released. The parent instead kills this process at that point.
func TestSnapshotRenameBarrierHelper(t *testing.T) {
	ready := os.Getenv("NOVA_SNAPSHOT_BARRIER")
	if ready == "" {
		return
	}
	renameSnapshot = func(oldPath, newPath string) error {
		if err := os.WriteFile(ready, []byte(oldPath), 0600); err != nil {
			return err
		}
		var release [1]byte
		if _, err := io.ReadFull(os.Stdin, release[:]); err != nil {
			return err
		}
		return os.Rename(oldPath, newPath)
	}
	os.Exit(Main("nova-update", []string{"report", "--file", os.Getenv("NOVA_SNAPSHOT_MANIFEST"),
		"--snapshot", os.Getenv("NOVA_SNAPSHOT_PATH")}, "test", os.Stdout, os.Stderr))
}

// SPEC-UPDATE: "Those three usage lines are the string `nova-update help` prints,
// byte for byte: one string in the binary, so the spec and the help cannot drift
// apart." Nothing made that true until this test read the spec and compared.

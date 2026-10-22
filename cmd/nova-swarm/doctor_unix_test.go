//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A binary that prints its stamp and then hangs is read for the stamp and reported for the
// hang. The stub tells the test over a pipe that it has printed, and the test cancels the read
// then, so no wait pays the deadline and none depends on how fast the stub starts: the
// comparison and the refusal are those of a run that was cut off at its deadline. The stub
// runs once for the preflight and once for the doctor verb, and both are ended after both
// have printed. A stub that fails to start ends its run with an error, so the test fails on
// its assertions and closing the pipe at cleanup ends the goroutine that waits on it.
func TestPreflightComparesTheStampOfABinaryThatPrintsThenHangs(t *testing.T) {
	t.Parallel()
	signal := filepath.Join(t.TempDir(), "printed")
	require.NoError(t, syscall.Mkfifo(signal, 0o600))
	// Opened for reading and writing, the pipe never ends and a stub's write never blocks.
	pipe, err := os.OpenFile(signal, os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pipe.Close() })

	env, pathBin, _ := doctorStubs(t,
		"echo 'nova-swarm stale-stamp'; echo printed >> '"+signal+"'; exec sleep 30",
		"echo 'nova-swarm good-stamp'", "")
	readDefault := env.read

	var mu sync.Mutex
	var cancels []context.CancelFunc
	cancelAll := func() {
		mu.Lock()
		defer mu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
	}
	env.read = func(p string) (string, error) {
		if p != pathBin {
			return readDefault(p)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		mu.Lock()
		cancels = append(cancels, cancel)
		mu.Unlock()
		return readVersionLineUnder(ctx, p, 2*time.Second, doctorGoodGrace, doctorVersionLineMax)
	}
	go func() {
		lines := bufio.NewReader(pipe)
		for i := 0; i < 2; i++ {
			if _, err := lines.ReadString('\n'); err != nil {
				break
			}
		}
		cancelAll()
	}()

	var out, derr, errOut bytes.Buffer
	var dcode int
	done := make(chan struct{})
	go func() {
		defer close(done)
		dcode = env.cmdDoctor(nil, &out, &derr)
	}()
	code, stop := env.preflight([]string{"native", "--tokens", "unmetered"}, &errOut)
	<-done
	require.Equal(t, 2, code, "preflight(exit=%d, stop=%v), want (2, true)\n%s", code, stop, errOut.String())
	require.True(t, stop, "preflight(exit=%d, stop=%v), want (2, true)\n%s", code, stop, errOut.String())
	got := errOut.String()
	for _, want := range []string{"DOCTOR DRIFT path=", "stale-stamp", "shadows", "DOCTOR UNREADABLE", "timed out after 2s"} {
		assert.Contains(t, got, want, "stderr lacks %q:\n%s", want, got)
	}
	assert.Equal(t, 2, dcode, "doctor exit %d, want 2 with the preflight's finding\nstdout: %q\nstderr: %q\nwant stderr: %q", dcode, out.String(), derr.String(), got)
	assert.Equal(t, 0, out.Len(), "doctor exit %d, want 2 with the preflight's finding\nstdout: %q\nstderr: %q\nwant stderr: %q", dcode, out.String(), derr.String(), got)
	assert.Equal(t, got, derr.String(), "doctor exit %d, want 2 with the preflight's finding\nstdout: %q\nstderr: %q\nwant stderr: %q", dcode, out.String(), derr.String(), got)
}

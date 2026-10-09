//go:build linux

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A stream's gate owns its deadline from worker admission, even while the pass
// still waits for an earlier stream. Both gates are real child processes held
// on pipes: the later one's deadline must stop and reap it before the first
// gate exits. No wall clock or scheduler delay decides the result.
func TestLandDeadlineBeginsWhenEachParallelWorkerStarts(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	twoStreams(t, r,
		map[string]map[string]string{"a1": {"a1.go": "package main\n\nfunc a1() {}\n"}},
		map[string]map[string]string{"b1": {"b1.go": "package main\n\nfunc b1() {}\n"}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.a.landCtx = ctx
	defer func() { r.a.landCtx = nil }()

	timers := make(chan chan time.Time, 2)
	parentAfter := r.a.after
	r.a.after = func(d time.Duration) <-chan time.Time {
		if d != LandDeadline {
			return parentAfter(d)
		}
		ch := make(chan time.Time, 1)
		timers <- ch
		return ch
	}
	type gateProcess struct {
		stream string
		input  *os.File
	}
	started := make(chan gateProcess, 2)
	exited := make(chan string, 2)
	var firstGates atomic.Int32
	b := r.a.landState()
	b.mu.Lock()
	b.gateBench = func(ctx context.Context, _, dir string, _ [][]string, _ bool) (string, int, error) {
		stream := "s2"
		if strings.HasSuffix(dir, "@s1") {
			stream = "s1"
			if firstGates.Add(1) == 1 { // the shared base gate
				return "", 0, nil
			}
		}
		read, write, err := os.Pipe()
		if err != nil {
			return "", 1, err
		}
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLandGateChild$")
		cmd.Env = append(os.Environ(), "NOVA_LAND_GATE_CHILD=1")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = read, io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			_ = read.Close()
			_ = write.Close()
			return "", 1, err
		}
		_ = read.Close()
		started <- gateProcess{stream: stream, input: write}
		err = cmd.Wait() // every cancellation reaps the owned process before the worker exits
		_ = write.Close()
		exited <- stream
		if err != nil {
			return "", 1, err
		}
		return "", 0, nil
	}
	b.mu.Unlock()
	done := make(chan struct{})
	var code int
	var out, errs string
	go func() { code, out, errs = r.do("land --land-parallel 2"); close(done) }()
	defer func() {
		cancel()
		<-done // a failing assertion never leaves the pass or its children running
	}()

	var gates []gateProcess
	for len(gates) < 2 {
		select {
		case g := <-started:
			gates = append(gates, g)
		case <-ctx.Done():
			t.Fatal("both gate processes did not start")
		}
	}
	first := <-timers
	var second chan time.Time
	select {
	case second = <-timers:
	default:
		for _, g := range gates {
			_ = g.input.Close()
		}
		t.Fatal("the second gate ran before the pass created its deadline")
	}
	second <- time.Now()
	select {
	case stream := <-exited:
		assert.Equal(t, "s2", stream, "the later process exits on its own deadline while the first still runs")
	case <-ctx.Done():
		t.Fatal("the later gate process did not exit on its deadline")
	}
	select {
	case stream := <-exited:
		t.Errorf("the first process exited before its deadline: %s", stream)
	default:
	}
	first <- time.Now()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("land did not wait for its canceled workers to exit")
	}
	select {
	case stream := <-exited:
		assert.Equal(t, "s1", stream, "the first child is reaped before the pass returns")
	default:
		t.Error("the first child is still running after the pass returned")
	}
	for _, g := range gates {
		_ = g.input.Close()
	}
	require.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, errs, "gate of this batch exceeded")
	assert.Equal(t, 1, strings.Count(errs, "LAND REFUSED stream=s1"), "the first timeout has one refusal")
	assert.Equal(t, 1, strings.Count(errs, "LAND REFUSED stream=s2"), "the second timeout has one refusal")
	assert.Equal(t, map[string]string{"a1": "merging/queued", "b1": "merging/queued"}, r.places("a1", "b1"))
	r.clean()
}

// TestLandGateChild is only selected by the guarded test binary's re-exec.
// Its stdin is an owned pipe, so closing that pipe releases a successful gate;
// canceling the parent context kills and reaps the real child process.
func TestLandGateChild(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_LAND_GATE_CHILD") != "1" {
		return
	}
	_, err := io.Copy(io.Discard, os.Stdin)
	require.NoError(t, err)
}

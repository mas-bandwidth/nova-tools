//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func plantNativeResultFIFO(t *testing.T, job string) {
	t.Helper()
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(job, "RESULT.md"), 0o644); err != nil {
		t.Skipf("this platform will not make a FIFO: %v", err)
	}
}

func plantedWaitBound() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// native must not open a FIFO planted at RESULT.md: a follow-up ReadFile would park the
// run in open(2) until a writer appears, and nothing ever will.
func TestNativeDoesNotBlockOnAPlantedFIFOAtResult(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "planted-fifo"
	jobDir := filepath.Join(slot, "jobs", label)
	plantNativeResultFIFO(t, jobDir)

	done := make(chan struct {
		harness string
		code    int
		err     string
	}, 1)
	go func() {
		var errOut bytes.Buffer
		res, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: label,
			card: []byte("FAKE-NORESULT\n"), slotDir: slot, root: root,
			deadline: 30 * time.Second, noWall: true,
		}, &errOut)
		done <- struct {
			harness string
			code    int
			err     string
		}{res.harness, code, errOut.String()}
	}()
	select {
	case got := <-done:
		if got.harness == "ok" {
			t.Fatal("native treated a FIFO at RESULT.md as a published result")
		}
	case <-time.After(plantedWaitBound()):
		t.Fatal("STILL BLOCKED after waiting on a FIFO at RESULT.md: native is wedged")
	}
}

// The lookup harnessState uses must not park on a FIFO at RESULT.md.
func TestNativeHarnessStateDoesNotBlockOnAPlantedFIFO(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	plantNativeResultFIFO(t, job)
	done := make(chan string, 1)
	go func() { done <- harnessState(job) }()
	select {
	case got := <-done:
		if got == "ok" {
			t.Fatal("native's result lookup treated a FIFO at RESULT.md as a published result")
		}
	case <-time.After(plantedWaitBound()):
		t.Fatal("STILL BLOCKED after waiting on a FIFO at RESULT.md: native's result lookup is wedged")
	}
}

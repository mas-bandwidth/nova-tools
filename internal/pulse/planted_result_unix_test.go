//go:build !windows

package pulse

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// plantResultFIFO makes a named pipe at the job's RESULT.md. The helper is unix-only:
// syscall.Mkfifo does not compile on Windows.
func plantResultFIFO(t *testing.T, job string) {
	t.Helper()
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(job, "RESULT.md")
	if err := syscall.Mkfifo(path, 0o644); err != nil {
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

// Harvest must not open a FIFO planted at a job's RESULT.md: os.ReadFile of one blocks
// in open(2) until a writer appears, and nothing ever will.
func TestHarvestDoesNotBlockOnAPlantedFIFOAtResult(t *testing.T) {
	root, specs, arglog := setupPulse(t)
	fakeGit(t, specs, arglog)
	fakeGH(t, specs, arglog, "https://github.com/owner/repo/pull/233")

	addCard(t, root, "plant", "1", "flash", "RESULT plant sha=aaa", "")
	plantResultFIFO(t, filepath.Join(root, "1", "jobs", "plant"))

	done := make(chan struct{})
	go func() {
		runHarvest(t, root)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(plantedWaitBound()):
		t.Fatal("STILL BLOCKED after waiting on a FIFO at RESULT.md: harvest is wedged")
	}
}

// Harvest --working must not park on a FIFO planted at a job's RESULT.md.
func TestHarvestWorkingDoesNotBlockOnAPlantedFIFOAtResult(t *testing.T) {
	working := t.TempDir()
	specs := fakePATH(t)
	arglog := filepath.Join(working, "argv.log")
	fakeTool(t, specs, "git", fakeSpec{Log: arglog})
	fakeTool(t, specs, "gh", fakeSpec{Log: arglog, Rules: []fakeRule{{Arg: 2, Equals: "list", Stdout: "[]"}}})
	job := wkJob(t, working, "g-plant", "plant", "")
	plantResultFIFO(t, job)

	done := make(chan struct{})
	go func() {
		wkRun(t, HarvestInput{Working: working, Base: "0000000000000000000000000000000000000000", Max: 20})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(plantedWaitBound()):
		t.Fatal("STILL BLOCKED after waiting on a FIFO at RESULT.md: harvest --working is wedged")
	}
}

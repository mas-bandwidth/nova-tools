package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// TestControlCardResultsSurviveSweep is issue #2632. A control card's RESULT.md,
// usage.tsv and report are published under <results-root>/<label>/<attempt>/,
// which is not the job directory. --sweep-now, and the existing bench sweep
// (nova-pulse hygiene delete-job), each leave that directory intact and the
// job directory gone. nova-pulse status reads the spend from the results root,
// not from the job that was deleted.
func TestControlCardResultsSurviveSweep(t *testing.T) {
	windowsIsNotABench(t)
	t.Run("sweep-now", func(t *testing.T) {
		controlCardSurvivesSweep(t, true)
	})
	t.Run("bench-sweep", func(t *testing.T) {
		controlCardSurvivesSweep(t, false)
	})
}

func controlCardSurvivesSweep(t *testing.T, sweepNow bool) {
	t.Helper()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "control"
	resultsRoot := filepath.Join(t.TempDir(), "results")
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-SAY control-report-line\nRESULT: control\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--idle", "0", "--no-wall",
		"--results-root", resultsRoot}
	if sweepNow {
		args = append(args, "--sweep-now")
	}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("the control card exits 0, got %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE OK ") {
		t.Fatalf("the control card publishes a result:\n%s\n%s", stdout.String(), stderr.String())
	}

	job := filepath.Join(slot, "jobs", label)
	if !sweepNow {
		if _, err := os.Stat(job); err != nil {
			t.Fatalf("the bench sweep needs a job directory to delete: %v", err)
		}
		var hout, herr bytes.Buffer
		code := pulse.Hygiene(pulse.HygieneInput{
			Verb:   "delete-job",
			Slot:   filepath.Base(slot),
			Job:    label,
			Home:   t.TempDir(),
			Roots:  []string{root},
			Stdout: &hout,
			Stderr: &herr,
		})
		if code != 0 {
			t.Fatalf("hygiene delete-job exits 0, got %d\n%s%s", code, hout.String(), herr.String())
		}
	}

	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatalf("the job directory must be gone after the sweep, stat=%v", err)
	}
	attempt := filepath.Join(resultsRoot, label, "1")
	if rel, err := filepath.Rel(job, attempt); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("results %s sit inside the job directory %s", attempt, job)
	}
	for _, name := range []string{"RESULT.md", "usage.tsv", "report"} {
		if _, err := os.Stat(filepath.Join(attempt, name)); err != nil {
			t.Fatalf("%s was not published under %s: %v\nstderr:\n%s", name, attempt, err, stderr.String())
		}
	}
	report, err := os.ReadFile(filepath.Join(attempt, "report"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "control-report-line") {
		t.Fatalf("the report does not carry what the harness said:\n%s", report)
	}
	usage, err := os.ReadFile(filepath.Join(attempt, "usage.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(usage), label) {
		t.Fatalf("usage.tsv does not name the card:\n%s", usage)
	}

	bench := filepath.Join(root, "width-only")
	if err := os.MkdirAll(bench, 0o755); err != nil {
		t.Fatal(err)
	}
	var sout, serr bytes.Buffer
	code := pulse.Status(pulse.StatusInput{
		Queue:       t.TempDir(),
		Roots:       bench,
		ResultsRoot: resultsRoot,
		Stdout:      &sout,
		Stderr:      &serr,
		Now:         func() time.Time { return time.Now().UTC() },
	})
	if code != 0 {
		t.Fatalf("nova-pulse status exits 0, got %d\n%s%s", code, sout.String(), serr.String())
	}
	if !strings.Contains(sout.String(), "STATUS RATE ") || strings.Contains(sout.String(), "usd_per_card=-") {
		t.Fatalf("status did not read the results root (the bench root has no usage):\n%s%s", sout.String(), serr.String())
	}
}

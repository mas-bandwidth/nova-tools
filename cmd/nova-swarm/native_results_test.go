package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// TestControlCardResultsSurviveSweep is issue #2632. A control card's RESULT.md,
// usage.tsv and report are published under
// <results-root>/<label>/<runID>/<attempt>/, which is not the job directory.
// --sweep-now, and the existing bench sweep (nova-pulse hygiene delete-job),
// each leave that directory intact and the job directory gone. nova-pulse
// status reads the spend from the results root, not from the job that was deleted.
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
	attempt := oneRunAttempt(t, resultsRoot, label)
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

// TestTwoInvocationsPreserveResults is the preservation control for a repeated
// label. Attempt numbers restart at 1 every invocation, so the run directory
// has to be the identity. Both sweeps run, and each run's report, RESULT.md
// and usage.tsv stay intact and unmixed.
func TestTwoInvocationsPreserveResults(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "control"
	resultsRoot := filepath.Join(t.TempDir(), "results")
	store := nativeStore(t)
	for _, card := range []struct{ say, findings string }{
		{"run-one-report", "1"},
		{"run-two-report", "2"},
	} {
		cardPath := filepath.Join(root, "card-"+card.findings+".md")
		body := fmt.Sprintf("FAKE-SAY %s\nFAKE-FINDINGS %s\n", card.say, card.findings)
		if err := os.WriteFile(cardPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--slots-store", store, "--owner", "fake-1",
			"--harness", bin, "--model", "fake/fake-model", "--label", label,
			"--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "30s", "--idle", "0", "--no-wall",
			"--results-root", resultsRoot, "--sweep-now"},
			strings.NewReader(""), &stdout, &stderr, time.Now())
		if rc != 0 {
			t.Fatalf("invocation findings=%s exits 0, got %d\n%s\n%s", card.findings, rc, stdout.String(), stderr.String())
		}
	}
	job := filepath.Join(slot, "jobs", label)
	if _, err := os.Stat(job); !os.IsNotExist(err) {
		t.Fatalf("the job directory must be gone after both sweeps, stat=%v", err)
	}
	runs := runDirs(t, resultsRoot, label)
	if len(runs) != 2 {
		t.Fatalf("two invocations need two run directories, got %v", runs)
	}
	if runs[0] == runs[1] {
		t.Fatalf("the run ids collided: %v", runs)
	}
	seenSay := map[string]bool{}
	seenFindings := map[string]bool{}
	for _, id := range runs {
		attempt := filepath.Join(resultsRoot, label, id, "1")
		report, err := os.ReadFile(filepath.Join(attempt, "report"))
		if err != nil {
			t.Fatalf("run %s report: %v", id, err)
		}
		result, err := os.ReadFile(filepath.Join(attempt, "RESULT.md"))
		if err != nil {
			t.Fatalf("run %s RESULT.md: %v", id, err)
		}
		if n := usageDataRows(t, filepath.Join(attempt, "usage.tsv")); n != 1 {
			t.Fatalf("run %s usage has %d data rows, want 1 (a second invocation must not append here)", id, n)
		}
		switch {
		case strings.Contains(string(report), "run-one-report"):
			seenSay["one"] = true
			if strings.Contains(string(report), "run-two-report") {
				t.Fatalf("run %s report mixes both invocations:\n%s", id, report)
			}
		case strings.Contains(string(report), "run-two-report"):
			seenSay["two"] = true
			if strings.Contains(string(report), "run-one-report") {
				t.Fatalf("run %s report mixes both invocations:\n%s", id, report)
			}
		default:
			t.Fatalf("run %s report is neither invocation:\n%s", id, report)
		}
		switch {
		case strings.Contains(string(result), "findings: 1\n"):
			seenFindings["1"] = true
			if strings.Contains(string(result), "findings: 2\n") {
				t.Fatalf("run %s RESULT mixes both invocations:\n%s", id, result)
			}
		case strings.Contains(string(result), "findings: 2\n"):
			seenFindings["2"] = true
			if strings.Contains(string(result), "findings: 1\n") {
				t.Fatalf("run %s RESULT mixes both invocations:\n%s", id, result)
			}
		default:
			t.Fatalf("run %s RESULT lost its findings line:\n%s", id, result)
		}
	}
	if !seenSay["one"] || !seenSay["two"] || !seenFindings["1"] || !seenFindings["2"] {
		t.Fatalf("both reports and both results must survive, says=%v findings=%v", seenSay, seenFindings)
	}
}

// TestPublicationFailureKeepsTheJob is the capture-failure control. The harness
// unlinks harness-output.log, so the required report cannot be published.
// --sweep-now must leave the job directory in place.
func TestPublicationFailureKeepsTheJob(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "control"
	resultsRoot := filepath.Join(t.TempDir(), "results")
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-DROP-CAPTURE\nFAKE-SAY kept-in-the-job\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--idle", "0", "--no-wall",
		"--results-root", resultsRoot, "--sweep-now"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("the card still exits 0, got %d\n%s\n%s", rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "the report could not be published") {
		t.Fatalf("a missing capture must be named:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "left") || !strings.Contains(stderr.String(), "in place") {
		t.Fatalf("--sweep-now must say it left the job in place:\n%s", stderr.String())
	}
	job := filepath.Join(slot, "jobs", label)
	if _, err := os.Stat(job); err != nil {
		t.Fatalf("the job directory must be kept when the report cannot be published: %v", err)
	}
	if _, err := os.Stat(filepath.Join(job, "RESULT.md")); err != nil {
		t.Fatalf("the job still holds RESULT.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(job, "harness-output.log")); !os.IsNotExist(err) {
		t.Fatalf("the capture was dropped, stat=%v", err)
	}
	_ = filepath.WalkDir(resultsRoot, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == "report" {
			t.Errorf("a failed publish still wrote %s", path)
		}
		return nil
	})
}

func oneRunAttempt(t *testing.T, resultsRoot, label string) string {
	t.Helper()
	runs := runDirs(t, resultsRoot, label)
	if len(runs) != 1 {
		t.Fatalf("one run directory under %s, got %v", label, runs)
	}
	if runs[0] == "1" || !strings.Contains(runs[0], "-") {
		t.Fatalf("run id %q is the restarting attempt number, not an invocation identity", runs[0])
	}
	return filepath.Join(resultsRoot, label, runs[0], "1")
}

func runDirs(t *testing.T, resultsRoot, label string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(resultsRoot, label))
	if err != nil {
		t.Fatalf("reading %s: %v", filepath.Join(resultsRoot, label), err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs
}

func usageDataRows(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line != "" && !strings.HasPrefix(line, "job\t") {
			n++
		}
	}
	return n
}

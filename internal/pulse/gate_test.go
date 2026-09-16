package pulse

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRuns is the RunSource a test drives the gate with: no gh, no network, and a record of
// every ask so a test can prove the answer came from here.
type fakeRuns struct {
	run    CIRun
	runErr error
	job    CIJob
	jobErr error
	asked  []string
}

func (f *fakeRuns) LatestRun(repo, branch string) (CIRun, error) {
	f.asked = append(f.asked, "latest "+repo+" "+branch)
	return f.run, f.runErr
}

func (f *fakeRuns) FailedJob(repo string, runID int64) (CIJob, error) {
	f.asked = append(f.asked, fmt.Sprintf("job %s %d", repo, runID))
	return f.job, f.jobErr
}

func gateRun(t *testing.T, queue string, src RunSource) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Gate(GateInput{Repo: "mas-bandwidth/nova-tools", Branch: "main", Queue: queue, Source: src, Stdout: &out, Stderr: &errb})
	return out.String(), errb.String(), code
}

func readStop(t *testing.T, queue string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(queue, StopFile))
	if err != nil {
		t.Fatalf("reading STOP: %v", err)
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

// gate-red-writes-stop-with-an-admission-name (class C): a failed run writes STOP, line 1
// naming the sha, the run, the failing job and the failing test, and line 2 the admission
// name -- the issue the job log names, so the red's own fix card is the one card that can
// launch while the bench is red.
func TestGateWritesStopWithTheIssueAsAdmissionName(t *testing.T) {
	queue := t.TempDir()
	src := &fakeRuns{
		run: CIRun{ID: 35120376309, Status: "completed", Conclusion: "failure", HeadSHA: "8f3714d1c0de0000"},
		job: CIJob{Name: "studio-fast", Log: "--- FAIL: TestGateHoldsTheBench (0.04s)\n    gate_test.go:12: fix #828 first\n"},
	}
	out, errb, code := gateRun(t, queue, src)
	if code != 1 {
		t.Fatalf("gate on a red exit = %d, want 1; out=%q err=%q", code, out, errb)
	}
	stop := readStop(t, queue)
	if len(stop) != 2 {
		t.Fatalf("STOP is %d lines, want 2: %q", len(stop), stop)
	}
	want := "MAIN-RED 8f3714d1c0de run=35120376309 job=studio-fast test=TestGateHoldsTheBench"
	if stop[0] != want {
		t.Fatalf("STOP line 1 = %q, want %q", stop[0], want)
	}
	if stop[1] != "#828" {
		t.Fatalf("STOP line 2 = %q, want the issue the job log names, #828", stop[1])
	}
	lines := nonEmptyLines(out + errb)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "GATE RED ") {
		t.Fatalf("want exactly one GATE line, got %q", lines)
	}
	if !strings.Contains(lines[0], "admit=#828") || !strings.Contains(lines[0], "run=35120376309") {
		t.Fatalf("GATE line does not carry the admission or the run: %q", lines[0])
	}
}

// gate-red-falls-back-to-the-test-name: a job log naming no issue admits by the failing
// test's name instead, so a red always admits something nameable.
func TestGateAdmissionFallsBackToTheTestName(t *testing.T) {
	queue := t.TempDir()
	src := &fakeRuns{
		run: CIRun{ID: 7, Status: "completed", Conclusion: "failure", HeadSHA: "abcdefabcdef0123"},
		job: CIJob{Name: "space (2)", Log: "ok  \tpkg/one\n--- FAIL: TestRefillCounts\nFAIL\tpkg/two\n"},
	}
	out, errb, code := gateRun(t, queue, src)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; %q %q", code, out, errb)
	}
	stop := readStop(t, queue)
	if stop[1] != "TestRefillCounts" {
		t.Fatalf("STOP line 2 = %q, want TestRefillCounts", stop[1])
	}
	// A job name with a space is ONE field, escaped the way internal/oneline escapes it:
	// the field law is SPEC.md's, and a STOP line is read by a machine before a person.
	if !strings.Contains(stop[0], "test=TestRefillCounts") || !strings.Contains(stop[0], `job=space\x20(2)`) {
		t.Fatalf("STOP line 1 = %q, want the job as one escaped field and the test named", stop[0])
	}
}

// gate-holds-on-cancelled-or-running (bug 8): dev's run cancelled by the next merge is NOT
// a red. The gate changes nothing -- no STOP written, a STOP already there untouched -- and
// says held in one line.
func TestGateHoldsOnCancelledAndInProgress(t *testing.T) {
	for _, tc := range []struct{ name, status, conclusion string }{
		{"cancelled", "completed", "cancelled"},
		{"in_progress", "in_progress", ""},
		{"queued", "queued", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queue := t.TempDir()
			src := &fakeRuns{run: CIRun{ID: 3, Status: tc.status, Conclusion: tc.conclusion, HeadSHA: "0123456789abcdef"}}
			out, errb, code := gateRun(t, queue, src)
			if code != 0 {
				t.Fatalf("exit = %d, want 0; %q %q", code, out, errb)
			}
			if _, err := os.Stat(filepath.Join(queue, StopFile)); !os.IsNotExist(err) {
				t.Fatalf("a held gate wrote a STOP (err=%v)", err)
			}
			lines := nonEmptyLines(out + errb)
			if len(lines) != 1 || !strings.HasPrefix(lines[0], "GATE HELD ") {
				t.Fatalf("want one GATE HELD line, got %q", lines)
			}
			if !strings.Contains(lines[0], "reason="+tc.name) {
				t.Fatalf("GATE HELD does not name why: %q", lines[0])
			}
		})
	}
}

// gate-held-leaves-an-existing-stop-alone: the bench is red and the run is cancelled --
// the STOP stays exactly as it was, byte for byte.
func TestGateHeldLeavesAnExistingStopAlone(t *testing.T) {
	queue := t.TempDir()
	body := "MAIN-RED deadbeefdead run=1 job=studio test=TestOne\n#828\n"
	if err := os.WriteFile(filepath.Join(queue, StopFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &fakeRuns{run: CIRun{ID: 9, Status: "in_progress"}}
	if _, _, code := gateRun(t, queue, src); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	raw, err := os.ReadFile(filepath.Join(queue, StopFile))
	if err != nil || string(raw) != body {
		t.Fatalf("STOP = %q (err=%v), want it untouched", raw, err)
	}
}

// gate-green-clears-only-the-gates-own-stop: a green run removes the STOP the gate wrote
// (line 1 MAIN-RED) and keeps the one a person wrote -- RESUME is the gate's alone, and a
// person's hold is nobody else's to lift.
func TestGateGreenClearsOnlyTheGatesOwnStop(t *testing.T) {
	t.Run("the gate's", func(t *testing.T) {
		queue := t.TempDir()
		if err := os.WriteFile(filepath.Join(queue, StopFile), []byte("MAIN-RED deadbeefdead run=1 job=j test=T\n#828\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		src := &fakeRuns{run: CIRun{ID: 11, Status: "completed", Conclusion: "success", HeadSHA: "feedfacefeedface"}}
		out, errb, code := gateRun(t, queue, src)
		if code != 0 {
			t.Fatalf("exit = %d, want 0; %q %q", code, out, errb)
		}
		if _, err := os.Stat(filepath.Join(queue, StopFile)); !os.IsNotExist(err) {
			t.Fatalf("the gate's own STOP survived a green (err=%v)", err)
		}
		lines := nonEmptyLines(out + errb)
		if len(lines) != 1 || !strings.Contains(lines[0], "stop=cleared") {
			t.Fatalf("want one GATE line saying stop=cleared, got %q", lines)
		}
	})
	t.Run("a person's", func(t *testing.T) {
		queue := t.TempDir()
		body := "Glenn: hold the bench until I say\n"
		if err := os.WriteFile(filepath.Join(queue, StopFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		src := &fakeRuns{run: CIRun{ID: 12, Status: "completed", Conclusion: "success", HeadSHA: "feedfacefeedface"}}
		out, errb, code := gateRun(t, queue, src)
		if code != 0 {
			t.Fatalf("exit = %d, want 0; %q %q", code, out, errb)
		}
		raw, err := os.ReadFile(filepath.Join(queue, StopFile))
		if err != nil || string(raw) != body {
			t.Fatalf("a person's STOP = %q (err=%v), want it kept byte for byte", raw, err)
		}
		lines := nonEmptyLines(out + errb)
		if len(lines) != 1 || !strings.Contains(lines[0], "stop=kept") {
			t.Fatalf("want one GATE line saying stop=kept, got %q", lines)
		}
	})
	t.Run("none at all", func(t *testing.T) {
		queue := t.TempDir()
		src := &fakeRuns{run: CIRun{ID: 13, Status: "completed", Conclusion: "success", HeadSHA: "feedfacefeedface"}}
		out, errb, code := gateRun(t, queue, src)
		if code != 0 {
			t.Fatalf("exit = %d, want 0; %q %q", code, out, errb)
		}
		lines := nonEmptyLines(out + errb)
		if len(lines) != 1 || !strings.Contains(lines[0], "stop=none") {
			t.Fatalf("want one GATE line saying stop=none, got %q", lines)
		}
	})
}

// gate-refuses-an-unreadable-source: a source that cannot answer is a refusal with a
// remedy, exit 2, and no STOP written -- a dead source is never green, and never red either.
func TestGateRefusesAnUnreadableSource(t *testing.T) {
	queue := t.TempDir()
	src := &fakeRuns{runErr: fmt.Errorf("gh exited 4: not logged in")}
	out, errb, code := gateRun(t, queue, src)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; %q %q", code, out, errb)
	}
	if _, err := os.Stat(filepath.Join(queue, StopFile)); !os.IsNotExist(err) {
		t.Fatalf("a refused gate wrote a STOP (err=%v)", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(errb), "GATE REFUSED ") || !strings.Contains(errb, "(") {
		t.Fatalf("the refusal does not name a remedy: %q", errb)
	}
}

// gate-reads-a-source-file: --source reads the runs and the failing job out of a file, so
// the whole verb runs with no gh and no network at all.
func TestGateReadsRunsFromASourceFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.json")
	body := `{"runs":[{"databaseId":21,"status":"completed","conclusion":"failure","headSha":"1111222233334444"}],
	          "job":{"name":"dev-gate","log":"--- FAIL: TestSweepEnqueues\nsee #781\n"}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := NewFileRunSource(path)
	if err != nil {
		t.Fatalf("NewFileRunSource: %v", err)
	}
	queue := t.TempDir()
	out, errb, code := gateRun(t, queue, src)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; %q %q", code, out, errb)
	}
	stop := readStop(t, queue)
	if stop[0] != "MAIN-RED 111122223333 run=21 job=dev-gate test=TestSweepEnqueues" || stop[1] != "#781" {
		t.Fatalf("STOP = %q", stop)
	}
}

// gate-refuses-to-guess-its-own-inputs (taken from PR #833, which validated inside the
// verb): Gate called with no repo, branch or queue refuses rather than writing a STOP at
// the root of wherever it is standing, and a nil writer is never a panic.
func TestGateRefusesMissingInput(t *testing.T) {
	for _, in := range []GateInput{
		{Branch: "main", Queue: t.TempDir(), Source: &fakeRuns{}},
		{Repo: "o/r", Queue: t.TempDir(), Source: &fakeRuns{}},
		{Repo: "o/r", Branch: "main", Source: &fakeRuns{}},
		{Repo: "o/r", Branch: "main", Queue: t.TempDir()},
	} {
		var errb bytes.Buffer
		in.Stderr = &errb
		if code := Gate(in); code != 2 {
			t.Fatalf("Gate(%+v) = %d, want 2", in, code)
		}
		if !strings.Contains(errb.String(), "GATE REFUSED") {
			t.Fatalf("stderr = %q, want a GATE REFUSED line", errb.String())
		}
	}
}

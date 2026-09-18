package ci

// failed_test.go is the red-test contract of `nova-ci failed`, fed by the real thing:
// testdata/failed/ holds cuts of four job logs from 2026-09-18, the day Rowan pulled the
// failing lines out of them by hand six times.
//
//	windows-sandbox.log       job 105673713280, test-windows-pr (0): plain `go test`
//	                          output, CRLF and ANSI, five failing tests in one package
//	studio-review.log         job 105673768922, test (3/4 studio): `go test -json`
//	                          frames, one failing test interleaved with passing ones
//	pulse-flake.log           job 105696546293, test (2/4 studio): `go test -json`, the
//	                          power-runner flake
//	merge-darwin-timeout.log  job 105698657603, test-hosted-merge (darwin, 1) of run
//	                          35375346271: `panic: test timed out after 1m40s`
//
// Each file is a window of the real log, ANSI, timestamps and all, with nothing added.
// Nothing here reads the network: the fixtures are on disk and the forge is a fake.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// (0) The help line this verb is entered under is the one docs/SPEC-CI.md prints, so the
// document and the code cannot drift apart in a rename.
func TestFailedVerbLineMatchesTheSpec(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "SPEC-CI.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), FailedVerbLine) {
		t.Errorf("the failed verb line is not in docs/SPEC-CI.md:\n%s", FailedVerbLine)
	}
}

// fixture reads one of the four job logs.
func failedFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "failed", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// find returns the one failure with this test name.
func findFailure(t *testing.T, fs []TestFailure, name string) TestFailure {
	t.Helper()
	for _, f := range fs {
		if f.Test == name {
			return f
		}
	}
	var got []string
	for _, f := range fs {
		got = append(got, f.Test)
	}
	t.Fatalf("no failure named %s; got %v", name, got)
	return TestFailure{}
}

// (1) A plain `go test` log, with the runner's timestamps, its ANSI colour and its CRLF
// line endings, yields one finding per failing test, each with its package, its file and
// its line -- the three things the hand pipeline had to be read for.
func TestAPlainGoTestLogNamesEveryFailingTestWithItsFileAndLine(t *testing.T) {
	failures, timeouts := ParseJobLog("test-windows-pr (0)", failedFixture(t, "windows-sandbox.log"))
	if len(timeouts) != 0 {
		t.Errorf("a plain failure log holds no timeout, got %d", len(timeouts))
	}
	want := map[string]string{
		"TestDenialsInsideTheAllowedSetAreNotReported":    "denied_test.go:73",
		"TestTheDeniedLineNamesThePathTheOpAndTheRemedy":  "denied_test.go:90",
		"TestRunNamesEveryBadFlagAtOnce":                  "run_test.go:222",
		"TestAFailedRunIsToldWhatTheWallDenied":           "run_test.go:526",
		"TestWindowsALeakExitsThreeAndNamesTheOneCommand": "runwin_test.go:514",
	}
	if len(failures) != len(want) {
		var got []string
		for _, f := range failures {
			got = append(got, f.Test)
		}
		t.Fatalf("%d failing tests, want %d: %v", len(failures), len(want), got)
	}
	for name, at := range want {
		f := findFailure(t, failures, name)
		if f.At != at {
			t.Errorf("%s at = %q, want %q", name, f.At, at)
		}
		if f.Package != "github.com/mas-bandwidth/nova-tools/cmd/nova-sandbox" {
			t.Errorf("%s pkg = %q, want the package the FAIL trailer named", name, f.Package)
		}
		if f.Job != "test-windows-pr (0)" {
			t.Errorf("%s job = %q, want the job it was read from", name, f.Job)
		}
		if len(f.Lines) == 0 {
			t.Errorf("%s printed no message lines; the test's own words are the diagnosis", name)
		}
	}
}

// (2) A test's own message lines come back in the order it printed them, including the
// continuation lines indented under a t.Errorf -- the part a grep for `_test.go:` drops.
func TestATestsOwnWordsComeBackWhole(t *testing.T) {
	failures, _ := ParseJobLog("test-windows-pr (0)", failedFixture(t, "windows-sandbox.log"))
	f := findFailure(t, failures, "TestTheDeniedLineNamesThePathTheOpAndTheRemedy")
	if len(f.Lines) < 4 {
		t.Fatalf("%d message lines, want the two findings and their continuations:\n%s", len(f.Lines), strings.Join(f.Lines, "\n"))
	}
	if !strings.Contains(f.Lines[0], "does not name the directory as the remedy") {
		t.Errorf("first line = %q, want the test's own first sentence", f.Lines[0])
	}
	if !strings.Contains(f.Lines[1], "SANDBOX DENIED") {
		t.Errorf("second line = %q, want the continuation line under it", f.Lines[1])
	}
}

// (3) A `go test -json` log is read by its frames, not by position: the go command
// interleaves parallel tests, so the message line of a failing test can sit between two
// other tests' lines and still belong to it.
func TestAJSONLogAttributesLinesByFrameNotByPosition(t *testing.T) {
	failures, timeouts := ParseJobLog("test (3/4 studio)", failedFixture(t, "studio-review.log"))
	if len(timeouts) != 0 {
		t.Errorf("no timeout in this log, got %d", len(timeouts))
	}
	if len(failures) != 1 {
		var got []string
		for _, f := range failures {
			got = append(got, f.Test)
		}
		t.Fatalf("%d failing tests, want 1: %v", len(failures), got)
	}
	f := failures[0]
	if f.Test != "TestMutateRemovesItsWorktreeOnBothPaths" {
		t.Errorf("test = %q", f.Test)
	}
	if f.Package != "github.com/mas-bandwidth/nova-tools/internal/review" {
		t.Errorf("pkg = %q, want the package the frame named", f.Package)
	}
	if f.At != "mutate_test.go:326" {
		t.Errorf("at = %q, want mutate_test.go:326", f.At)
	}
	if len(f.Lines) != 1 || !strings.Contains(f.Lines[0], "a temp worktree directory was left behind") {
		t.Errorf("lines = %q, want the one line the test printed", f.Lines)
	}
}

// (4) The second JSON log, a different job and a different package, reads the same way.
func TestTheSecondJSONLogReadsTheSameWay(t *testing.T) {
	failures, _ := ParseJobLog("test (2/4 studio)", failedFixture(t, "pulse-flake.log"))
	if len(failures) != 1 {
		t.Fatalf("%d failing tests, want 1", len(failures))
	}
	f := failures[0]
	if f.Test != "TestPowerRunnerTimeoutKillsProcessGroup" || f.At != "power_unix_test.go:63" {
		t.Errorf("failure = %+v", f)
	}
	if f.Package != "github.com/mas-bandwidth/nova-tools/cmd/nova-pulse" {
		t.Errorf("pkg = %q", f.Package)
	}
}

// (5) A timed-out package is not a failing test: it is a TIMEOUT naming the package, the
// budget it blew and the tests that were still running when the alarm went off.
func TestATimeoutNamesThePackageAndTheTestsStillRunning(t *testing.T) {
	failures, timeouts := ParseJobLog("test-hosted-merge (darwin, 1)", failedFixture(t, "merge-darwin-timeout.log"))
	if len(failures) != 0 {
		t.Errorf("a timeout is not a failing test, got %d failures", len(failures))
	}
	if len(timeouts) != 1 {
		t.Fatalf("%d timeouts, want 1", len(timeouts))
	}
	to := timeouts[0]
	if to.After != 100*time.Second {
		t.Errorf("after = %s, want 1m40s", to.After)
	}
	if to.Package != "github.com/mas-bandwidth/nova-tools/cmd/nova-merge" {
		t.Errorf("pkg = %q, want the package the FAIL trailer named", to.Package)
	}
	if len(to.Running) != 10 {
		t.Errorf("%d running tests, want the 10 the panic listed: %v", len(to.Running), to.Running)
	}
	if len(to.Running) > 0 && to.Running[0] != "TestABranchEntryMergesOnAGateAndAReadWithNoHostedChecks" {
		t.Errorf("first running test = %q", to.Running[0])
	}
	if to.Job != "test-hosted-merge (darwin, 1)" {
		t.Errorf("job = %q", to.Job)
	}
}

// (6) A cancellation leaves nothing in the log, so it is read off the job's steps: the
// step that was cut and how long it had been running.
func TestACancelledStepIsReadFromTheJobNotTheLog(t *testing.T) {
	start := time.Date(2026, 9, 18, 16, 25, 10, 0, time.UTC)
	job := FailedJob{
		ID:         105673768913,
		Name:       "test (2/4 studio)",
		Conclusion: "cancelled",
		Steps: []FailedStep{
			{Name: "vet", Conclusion: "success", Started: start.Add(-4 * time.Second), Completed: start},
			{Name: "test", Conclusion: "cancelled", Started: start, Completed: start.Add(195 * time.Second)},
		},
	}
	got := CancelledSteps(job)
	if len(got) != 1 {
		t.Fatalf("%d cancellations, want 1", len(got))
	}
	if got[0].Step != "test" || got[0].After != 195*time.Second || got[0].Job != job.Name {
		t.Errorf("cancellation = %+v", got[0])
	}
	if !job.Failed() {
		t.Error("a cancelled job is one whose log this verb reads")
	}
	for _, conclusion := range []string{"success", "skipped", ""} {
		if (FailedJob{Conclusion: conclusion}).Failed() {
			t.Errorf("a %q job is not read", conclusion)
		}
	}
}

// (7) A test's own words are bounded: --max-lines of them print and the rest are
// counted, never dropped in silence.
func TestTheMessageLinesAreCappedAndTheRestCounted(t *testing.T) {
	report := FailedReport{Jobs: 1, Failures: []TestFailure{{
		Job: "j", Package: "p", Test: "TestX", At: "x_test.go:1",
		Lines: []string{"a", "b", "c", "d", "e"},
	}}}
	lines := report.Lines(2)
	want := []string{
		`FAILED job="j" pkg=p test=TestX at=x_test.go:1`,
		"a", "b",
		"    ...+3 more lines",
		"FAILED OK jobs=1 tests=1",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if report.ExitCode() != 1 {
		t.Errorf("exit = %d, want 1 when the run said something red", report.ExitCode())
	}
}

// (8) A clean run prints the one summary line and exits 0; a report is never silent.
func TestACleanRunIsOneLineAndExitZero(t *testing.T) {
	report := FailedReport{Jobs: 0}
	lines := report.Lines(0)
	if len(lines) != 1 || lines[0] != "FAILED OK jobs=0 tests=0" {
		t.Errorf("lines = %q, want the one summary line", lines)
	}
	if report.ExitCode() != 0 {
		t.Errorf("exit = %d, want 0", report.ExitCode())
	}
}

// (9) The running list is capped at three with the rest counted, the same cap-and-count
// rule slowtests uses for its slowest packages.
func TestTheRunningListIsCappedAndCounted(t *testing.T) {
	report := FailedReport{Jobs: 1, Timeouts: []Timeout{{
		Job: "j", Package: "p", After: 100 * time.Second,
		Running: []string{"TestA", "TestB", "TestC", "TestD", "TestE"},
	}}}
	lines := report.Lines(8)
	if lines[0] != `TIMEOUT job="j" pkg=p running=TestA,TestB,TestC,+2` {
		t.Errorf("line = %q", lines[0])
	}
	none := FailedReport{Timeouts: []Timeout{{Job: "j", Package: "p"}}}
	if got := none.Lines(8)[0]; !strings.HasSuffix(got, "running=none") {
		t.Errorf("line = %q, want running=none said out loud", got)
	}
}

// (10) The stripper takes the three things a GitHub Actions log wraps every line in --
// the timestamp, the ANSI colour and the carriage return -- and nothing else.
func TestStripLogLineTakesTheWrapperAndNothingElse(t *testing.T) {
	raw := "2026-09-18T16:21:26.7443911Z \x1b[36;1m--- FAIL: TestX (0.00s)\x1b[0m\r"
	if got := StripLogLine(raw); got != "--- FAIL: TestX (0.00s)" {
		t.Errorf("stripped = %q", got)
	}
	plain := "    denied_test.go:73: a denial: {Path:/Volumes/nova-j1/work/inside.txt Op:read}"
	if got := StripLogLine(plain); got != plain {
		t.Errorf("a line with no wrapper changed: %q", got)
	}
}

// fakeFailForge answers from the fixtures on disk, the way the real forge answers from gh.
// It is the seam: nothing in these tests reaches a network.
type fakeFailForge struct {
	run    int64
	jobs   []FailedJob
	logs   map[int64]string
	calls  []string
	runErr error
}

func (f *fakeFailForge) ResolveRun(sel RunSelector) (int64, error) {
	f.calls = append(f.calls, fmt.Sprintf("resolve run=%d pr=%d branch=%s mergeGroup=%v", sel.Run, sel.PR, sel.Branch, sel.MergeGroup))
	if f.runErr != nil {
		return 0, f.runErr
	}
	if sel.Run > 0 {
		return sel.Run, nil
	}
	return f.run, nil
}

func (f *fakeFailForge) Jobs(runID int64) ([]FailedJob, error) {
	f.calls = append(f.calls, fmt.Sprintf("jobs %d", runID))
	return f.jobs, nil
}

func (f *fakeFailForge) JobLog(jobID int64) (string, error) {
	f.calls = append(f.calls, fmt.Sprintf("log %d", jobID))
	log, ok := f.logs[jobID]
	if !ok {
		return "", fmt.Errorf("no log for job %d", jobID)
	}
	return log, nil
}

// runFixtureForge is the four fixtures behind one run, the shape run 35375346271 and the
// two before it had between them.
func runFixtureForge(t *testing.T) *fakeFailForge {
	t.Helper()
	return &fakeFailForge{
		run: 35375346271,
		jobs: []FailedJob{
			{ID: 1, Name: "test (1/4 studio)", Conclusion: "success"},
			{ID: 2, Name: "test-windows-pr (0)", Conclusion: "failure"},
			{ID: 3, Name: "test (3/4 studio)", Conclusion: "failure"},
			{ID: 4, Name: "test-hosted-merge (darwin, 1)", Conclusion: "failure"},
			{ID: 5, Name: "fleet-probe", Conclusion: "skipped"},
		},
		logs: map[int64]string{
			2: failedFixture(t, "windows-sandbox.log"),
			3: failedFixture(t, "studio-review.log"),
			4: failedFixture(t, "merge-darwin-timeout.log"),
		},
	}
}

// (11) A whole run: only the jobs that did not succeed are read, and one report holds
// every failing test of every one of them.
func TestARunReadsOnlyTheJobsThatDidNotSucceed(t *testing.T) {
	f := runFixtureForge(t)
	runID, report, err := ReadFailedRun(f, RunSelector{Run: 35375346271}, "")
	if err != nil {
		t.Fatal(err)
	}
	if runID != 35375346271 {
		t.Errorf("run = %d", runID)
	}
	if report.Jobs != 3 {
		t.Errorf("jobs = %d, want the three that did not succeed", report.Jobs)
	}
	if len(report.Failures) != 6 {
		t.Errorf("%d failing tests, want 5 from windows and 1 from studio", len(report.Failures))
	}
	if len(report.Timeouts) != 1 {
		t.Errorf("%d timeouts, want the darwin one", len(report.Timeouts))
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "log 1") || strings.HasPrefix(c, "log 5") {
			t.Errorf("read the log of a job that did not fail: %q", c)
		}
	}
	if got := report.SummaryLine(); got != "FAILED OK jobs=3 tests=6" {
		t.Errorf("summary = %q", got)
	}
}

// (12) --job keeps one job's findings and reads no other job's log: a forty-leg matrix
// costs one call, not forty.
func TestTheJobFilterReadsOnlyThatJobsLog(t *testing.T) {
	f := runFixtureForge(t)
	_, report, err := ReadFailedRun(f, RunSelector{Run: 35375346271}, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if report.Jobs != 1 || len(report.Timeouts) != 1 || len(report.Failures) != 0 {
		t.Errorf("report = jobs %d, timeouts %d, failures %d", report.Jobs, len(report.Timeouts), len(report.Failures))
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "log ") && c != "log 4" {
			t.Errorf("read a job the filter excluded: %q", c)
		}
	}
}

// (12a) A --job that matched nothing is a refusal naming the jobs that did fail, never a
// green report: the caller asked about a job this run does not have.
func TestAJobFilterThatMatchesNothingSaysWhichJobsFailed(t *testing.T) {
	f := runFixtureForge(t)
	_, _, err := ReadFailedRun(f, RunSelector{Run: 35375346271}, "zzz")
	if err == nil {
		t.Fatal("a filter matching no failing job was reported as a green run")
	}
	for _, want := range []string{`"zzz"`, `"test-windows-pr (0)"`, "run 35375346271"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %s", err, want)
		}
	}
	green := &fakeFailForge{jobs: []FailedJob{{ID: 1, Name: "test", Conclusion: "success"}}}
	_, _, err = ReadFailedRun(green, RunSelector{Run: 7}, "test")
	if err == nil || !strings.Contains(err.Error(), "no job of run 7 failed") {
		t.Errorf("error = %v, want it to say no job of the run failed", err)
	}
}

// (13) The selector reaches the forge in the caller's own words, so --pr --merge-group is
// one question to the forge rather than a branch this tool guessed.
func TestTheSelectorReachesTheForgeUntouched(t *testing.T) {
	f := &fakeFailForge{run: 42, logs: map[int64]string{}}
	if _, _, err := ReadFailedRun(f, RunSelector{PR: 1370, MergeGroup: true}, ""); err != nil {
		t.Fatal(err)
	}
	if f.calls[0] != "resolve run=0 pr=1370 branch= mergeGroup=true" {
		t.Errorf("first call = %q", f.calls[0])
	}
}

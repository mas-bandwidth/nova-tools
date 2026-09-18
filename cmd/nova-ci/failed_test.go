package main

// failed_test.go runs the `failed` verb end to end through run(), with a fake forge in
// place of gh. Nothing here reaches a network and nothing here runs gh: the seam is the
// newForge function cmdFailed takes, and the fixtures are internal/ci's four real job
// logs, reached by their path so there is one copy of them in the tree.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
)

// forgeFixture reads one of internal/ci's job-log fixtures.
func forgeFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "ci", "testdata", "failed", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stubForge is the forge the tests hand the verb.
type stubForge struct {
	jobs []ci.FailedJob
	logs map[int64]string
	err  error
}

func (s *stubForge) ResolveRun(sel ci.RunSelector) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	if sel.Run > 0 {
		return sel.Run, nil
	}
	return 35375346271, nil
}
func (s *stubForge) Jobs(int64) ([]ci.FailedJob, error) { return s.jobs, nil }
func (s *stubForge) JobLog(id int64) (string, error) {
	log, ok := s.logs[id]
	if !ok {
		return "", fmt.Errorf("no log for job %d", id)
	}
	return log, nil
}

// runFailed runs the verb with a forge the test supplies.
func runFailed(t *testing.T, forge ci.FailForge, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cmdFailed(args, &stdout, &stderr, func(string, string, time.Duration) ci.FailForge { return forge })
	return code, stdout.String(), stderr.String()
}

// (a) The whole verb over the real logs of one run: a FAILED block per failing test, the
// TIMEOUT, and the summary, exit 1.
func TestFailedPrintsABlockPerFailingTestAndExitsOne(t *testing.T) {
	forge := &stubForge{
		jobs: []ci.FailedJob{
			{ID: 2, Name: "test-windows-pr (0)", Conclusion: "failure"},
			{ID: 4, Name: "test-hosted-merge (darwin, 1)", Conclusion: "failure"},
			{ID: 9, Name: "lint", Conclusion: "success"},
		},
		logs: map[int64]string{
			2: forgeFixture(t, "windows-sandbox.log"),
			4: forgeFixture(t, "merge-darwin-timeout.log"),
		},
	}
	code, stdout, stderr := runFailed(t, forge, "--repo", "owner/name", "--run", "35375346271")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", code, stderr)
	}
	want := []string{
		"FAILED job=\"test-windows-pr (0)\" pkg=github.com/mas-bandwidth/nova-tools/cmd/nova-sandbox test=TestDenialsInsideTheAllowedSetAreNotReported at=denied_test.go:73",
		"TIMEOUT job=\"test-hosted-merge (darwin, 1)\" pkg=github.com/mas-bandwidth/nova-tools/cmd/nova-merge running=",
		"FAILED OK jobs=2 tests=5",
	}
	for _, w := range want {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout has no %q:\n%s", w, stdout)
		}
	}
	if strings.Contains(stdout, "goroutine 2790") {
		t.Errorf("the goroutine dump reached stdout; the point of the verb is that it does not:\n%s", stdout)
	}
	if last := lastLine(stdout); !strings.HasPrefix(last, "FAILED OK ") {
		t.Errorf("last line = %q, want the summary", last)
	}
}

// (b) A run with nothing red is the one summary line at exit 0.
func TestFailedOnAGreenRunIsOneLineAtExitZero(t *testing.T) {
	forge := &stubForge{jobs: []ci.FailedJob{{ID: 1, Name: "test", Conclusion: "success"}}}
	code, stdout, stderr := runFailed(t, forge, "--repo", "owner/name", "--run", "1")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
	}
	if stdout != "FAILED OK jobs=0 tests=0\n" {
		t.Errorf("stdout = %q", stdout)
	}
}

// (c) --max-lines bounds a test's own words and counts the rest.
func TestFailedBoundsTheMessageLines(t *testing.T) {
	forge := &stubForge{
		jobs: []ci.FailedJob{{ID: 2, Name: "test-windows-pr (0)", Conclusion: "failure"}},
		logs: map[int64]string{2: forgeFixture(t, "windows-sandbox.log")},
	}
	_, wide, _ := runFailed(t, forge, "--repo", "owner/name", "--run", "1", "--max-lines", "40")
	code, narrow, _ := runFailed(t, forge, "--repo", "owner/name", "--run", "1", "--max-lines", "1")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if len(strings.Split(narrow, "\n")) >= len(strings.Split(wide, "\n")) {
		t.Errorf("--max-lines 1 printed no fewer lines than --max-lines 40")
	}
	if !strings.Contains(narrow, " more lines") {
		t.Errorf("the dropped lines were not counted:\n%s", narrow)
	}
	if !strings.Contains(narrow, "FAILED OK jobs=1 tests=5") {
		t.Errorf("the count is the truth whether or not the lines printed:\n%s", narrow)
	}
}

// (d) A cancelled sibling is its own line, read from the job's steps.
func TestFailedPrintsACancelledStep(t *testing.T) {
	start := time.Date(2026, 9, 18, 16, 25, 10, 0, time.UTC)
	forge := &stubForge{
		jobs: []ci.FailedJob{{ID: 7, Name: "test (2/4 studio)", Conclusion: "cancelled", Steps: []ci.FailedStep{
			{Name: "test", Conclusion: "cancelled", Started: start, Completed: start.Add(195 * time.Second)},
		}}},
		logs: map[int64]string{7: "2026-09-18T16:28:26.4376290Z Cleaning up orphan processes\n"},
	}
	code, stdout, stderr := runFailed(t, forge, "--repo", "owner/name", "--run", "1")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, `CANCELLED job="test (2/4 studio)" step="test" after=3m15s`) {
		t.Errorf("stdout = %q", stdout)
	}
}

// (e) The refusals. Each names what the input WANTS, exits 2, writes nothing on stdout
// and ends at the door.
func TestFailedRefusals(t *testing.T) {
	forge := &stubForge{}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no repo", []string{"--run", "1"}, "<owner>/<name>"},
		{"no run named", []string{"--repo", "owner/name"}, "name exactly one run"},
		{"two runs named", []string{"--repo", "owner/name", "--run", "1", "--branch", "dev"}, "name exactly one run"},
		{"merge-group without pr", []string{"--repo", "owner/name", "--branch", "dev", "--merge-group"}, "--merge-group"},
		{"max-lines zero", []string{"--repo", "owner/name", "--run", "1", "--max-lines", "0"}, "greater than zero"},
		{"timeout zero", []string{"--repo", "owner/name", "--run", "1", "--timeout", "0"}, "positive duration"},
		{"stray argument", []string{"--repo", "owner/name", "--run", "1", "extra"}, "unexpected argument"},
		{"unknown flag", []string{"--repo", "owner/name", "--run", "1", "--nope"}, "nope"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := runFailed(t, forge, c.args...)
			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			if stdout != "" {
				t.Errorf("stdout = %q; a refusal belongs on stderr", stdout)
			}
			if !strings.Contains(stderr, c.want) {
				t.Errorf("stderr = %q, want it to name %q", stderr, c.want)
			}
			if !strings.Contains(stderr, "run: nova-ci help") {
				t.Errorf("stderr = %q, want it to end at the door", stderr)
			}
			if n := len(strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")); n > 2 {
				t.Errorf("stderr printed %d lines, want at most 2:\n%s", n, stderr)
			}
		})
	}
}

// (e2) A log the forge will not give is a NOLOG line and an unread= count, not a refusal:
// the run's other failing jobs still report. Found by running this verb on its own PR.
func TestFailedPrintsNologForALogTheForgeWillNotGive(t *testing.T) {
	forge := &stubForge{
		jobs: []ci.FailedJob{
			{ID: 2, Name: "test-windows-pr (0)", Conclusion: "failure"},
			{ID: 8, Name: "e2e", Conclusion: "cancelled"},
		},
		logs: map[int64]string{2: forgeFixture(t, "windows-sandbox.log")},
	}
	code, stdout, stderr := runFailed(t, forge, "--repo", "owner/name", "--run", "1", "--max-lines", "1")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, `NOLOG job="e2e" reason="`) {
		t.Errorf("no NOLOG line for the job whose log was gone:\n%s", stdout)
	}
	if !strings.Contains(stdout, "FAILED OK jobs=2 tests=5 unread=1") {
		t.Errorf("the summary does not count the unread log:\n%s", stdout)
	}
	if !strings.Contains(stdout, "test=TestDenialsInsideTheAllowedSetAreNotReported") {
		t.Errorf("one missing log sank the other job's report:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q; a missing log is a line, not a refusal", stderr)
	}
}

// (f) A forge that cannot answer is a refusal in one line, not a stack trace.
func TestAForgeThatCannotAnswerIsOneLine(t *testing.T) {
	forge := &stubForge{err: fmt.Errorf("no merge_group run for pull request 1370 of owner/name")}
	code, stdout, stderr := runFailed(t, forge, "--repo", "owner/name", "--pr", "1370", "--merge-group")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "no merge_group run") {
		t.Errorf("stderr = %q, want the forge's own words", stderr)
	}
}

// (g) `failed --help` opens the verb's own door on stdout at exit 0, without asking a
// forge anything -- the shape every subverb takes.
func TestFailedHelpIsItsOwnDoor(t *testing.T) {
	for _, arg := range []string{"--help", "-h", "help"} {
		var stdout, stderr bytes.Buffer
		code := cmdFailed([]string{arg}, &stdout, &stderr, func(string, string, time.Duration) ci.FailForge {
			t.Errorf("%s asked the forge a question", arg)
			return &stubForge{}
		})
		if code != 0 {
			t.Errorf("`failed %s` exit = %d, want 0", arg, code)
		}
		if !strings.Contains(stdout.String(), "nova-ci failed --repo") {
			t.Errorf("`failed %s` printed no usage:\n%s", arg, stdout.String())
		}
		if stderr.String() != "" {
			t.Errorf("`failed %s` wrote to stderr: %q", arg, stderr.String())
		}
	}
}

// (h) The verb is reachable from the dispatch, and the banner names it.
func TestFailedIsReachableFromTheDispatch(t *testing.T) {
	code, stdout, stderr := runCI(t, []string{"failed", "--help"}, "")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "usage:") {
		t.Errorf("stdout = %q", stdout)
	}
	_, banner, _ := runCI(t, []string{"help"}, "")
	if !strings.Contains(banner, "nova-ci failed --repo") {
		t.Errorf("the banner does not name the failed verb:\n%s", banner)
	}
}

// lastLine is the last non-empty line of some output.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

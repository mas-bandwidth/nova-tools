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
		"FAILED RED jobs=2 failed=2 tests=5",
	}
	for _, w := range want {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout has no %q:\n%s", w, stdout)
		}
	}
	if strings.Contains(stdout, "goroutine 2790") {
		t.Errorf("the goroutine dump reached stdout; the point of the verb is that it does not:\n%s", stdout)
	}
	if last := lastLine(stdout); !strings.HasPrefix(last, "FAILED RED ") {
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
	if !strings.Contains(narrow, "FAILED RED jobs=1 failed=1 tests=5") {
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

// (d2) The whole verb over run 35329874611 of mas-bandwidth/schema: one job red of its
// own inside `make test`, with no test event in its log, and seven cancelled out from
// under it. The red job gets a line naming it and the step, the summary splits the eight,
// and the exit is 1 -- it used to be counted in jobs= and then never mentioned.
func TestFailedNamesAJobThatWentRedWithNoTestEvent(t *testing.T) {
	start := time.Date(2026, 9, 18, 9, 31, 14, 0, time.UTC)
	jobs := []ci.FailedJob{{
		ID: 11, Name: "inline-gate (ubuntu-latest, go)", Conclusion: "failure",
		Steps: []ci.FailedStep{{Name: "make test", Conclusion: "failure", Started: start, Completed: start.Add(51 * time.Minute)}},
	}}
	logs := map[int64]string{11: forgeFixture(t, "inline-gate-werror.log")}
	for i := 0; i < 7; i++ {
		id := int64(20 + i)
		jobs = append(jobs, ci.FailedJob{
			ID: id, Name: fmt.Sprintf("inline-gate (macos-latest, %d)", i), Conclusion: "cancelled",
			Steps: []ci.FailedStep{{Name: "make test", Conclusion: "cancelled", Started: start, Completed: start.Add(59 * time.Minute)}},
		})
		logs[id] = "2026-09-18T10:30:19.3899906Z Cleaning up orphan processes\n"
	}
	code, stdout, stderr := runFailed(t, &stubForge{jobs: jobs, logs: logs}, "--repo", "owner/name", "--run", "35329874611")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; stderr: %s", code, stderr)
	}
	want := []string{
		`NOTEST job="inline-gate (ubuntu-latest, go)" step="make test" tests=none`,
		"error: 'back.grade' may be used uninitialized [-Werror=maybe-uninitialized]",
		"FAILED RED jobs=8 failed=1 cancelled=7 tests=0",
	}
	for _, w := range want {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout has no %q:\n%s", w, stdout)
		}
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
	if !strings.Contains(stdout, "FAILED RED jobs=2 failed=1 cancelled=1 tests=5 unread=1") {
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

// (i) `failed --decide` reads a red into its class and grants at most one licensed rerun.
// The negative control is first and it is the point: a cancelled leg beside a `--- FAIL`
// is a named-test, and a named failing test is never licensed however many reruns are
// left. A rerunnable class is licensed only at zero reruns -- the second red at one sha is
// a finding -- and a flake row past its expiry matches nothing, so the red falls back to a
// named-test. These are the mechanical rows of SPEC-DECIDE reading 6, "one licensed rerun,
// or a finding"; the reading never reruns anything itself.
func TestFailedDecideClassesTheRedAndLicensesAtMostOneRerun(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	flakes := write("flakes.tsv", "TestFlaky\t#1\t2099-01-01\n")
	expired := write("expired.tsv", "TestFlaky\t#1\t2026-09-18\n")
	infra := write("infra.txt", "Set up job\n")
	flakyLog := "--- FAIL: TestFlaky (0.01s)\n"

	cases := []struct {
		name  string
		forge *stubForge
		args  []string
		want  string
	}{
		{
			name: "a cancelled leg beside a failing test is a named-test",
			forge: &stubForge{
				jobs: []ci.FailedJob{
					{ID: 2, Name: "test (1/4)", Conclusion: "failure", Attempt: 1, HeadSHA: decideSHA},
					{ID: 3, Name: "test (2/4)", Conclusion: "cancelled", Attempt: 1, HeadSHA: decideSHA},
				},
				logs: map[int64]string{2: forgeFixture(t, "windows-sandbox.log"), 3: "cleanup\n"},
			},
			args: []string{"--decide"},
			want: "red=named-test rerun=no finding=yes reruns=0 attempt=1",
		},
		{
			name: "a cancelled-only run licenses one rerun",
			forge: &stubForge{
				jobs: []ci.FailedJob{{ID: 3, Name: "e2e", Conclusion: "cancelled", Attempt: 1, HeadSHA: decideSHA}},
				logs: map[int64]string{3: "cleanup\n"},
			},
			args: []string{"--decide"},
			want: "red=cancelled-leg rerun=licensed finding=no reruns=0 attempt=1",
		},
		{
			name: "the second red at one sha is a finding",
			forge: &stubForge{
				jobs: []ci.FailedJob{{ID: 3, Name: "e2e", Conclusion: "cancelled", Attempt: 2, HeadSHA: decideSHA}},
				logs: map[int64]string{3: "cleanup\n"},
			},
			args: []string{"--decide"},
			want: "red=cancelled-leg rerun=no finding=yes reruns=1 attempt=2",
		},
		{
			name: "an unexpired flake row licenses the rerun",
			forge: &stubForge{
				jobs: []ci.FailedJob{{ID: 4, Name: "test (3/4)", Conclusion: "failure", Attempt: 1, HeadSHA: decideSHA}},
				logs: map[int64]string{4: flakyLog},
			},
			args: []string{"--decide", "--flakes", flakes, "--now", "2026-09-19"},
			want: "red=known-flake rerun=licensed finding=no reruns=0 attempt=1",
		},
		{
			name: "an expired flake row matches nothing",
			forge: &stubForge{
				jobs: []ci.FailedJob{{ID: 4, Name: "test (3/4)", Conclusion: "failure", Attempt: 1, HeadSHA: decideSHA}},
				logs: map[int64]string{4: flakyLog},
			},
			args: []string{"--decide", "--flakes", expired, "--now", "2026-09-19"},
			want: "red=named-test rerun=no finding=yes reruns=0 attempt=1",
		},
		{
			name: "an infra step in the table is a rerunnable class",
			forge: &stubForge{
				jobs: []ci.FailedJob{{ID: 5, Name: "test (4/4)", Conclusion: "failure", Attempt: 1, HeadSHA: decideSHA, Steps: []ci.FailedStep{{Name: "Set up job", Conclusion: "failure"}}}},
				logs: map[int64]string{5: "no test event here\n"},
			},
			args: []string{"--decide", "--infra-steps", infra},
			want: "red=infra rerun=licensed finding=no reruns=0 attempt=1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"--repo", "owner/name", "--run", "1"}, c.args...)
			code, stdout, stderr := runFailed(t, c.forge, args...)
			if code != 1 {
				t.Fatalf("exit = %d, want 1; `nova-ci failed --decide` did not classify the red: %s", code, stderr)
			}
			if !strings.Contains(lastLine(stdout), c.want) {
				t.Errorf("closing line = %q, want it to carry %q", lastLine(stdout), c.want)
			}
		})
	}
}

// (j) THE COUNT IS THE FORGE'S. The one licensed rerun is worth nothing if the caller can
// say how many reruns have been spent, because the caller is the party that wants the
// rerun: `--reruns 0` on a second attempt would license a red that has already been rerun
// once, which is the manufactured green rule 4 forbids. So the count is read from the
// forge's own attempt number for the red jobs at this sha -- attempt N means N-1 reruns
// spent -- and the flag is a FLOOR on that count: it can raise the spent count and never
// lower it. The negative control is the point of this test: attempt 2 of the same job at
// the same sha is NOT licensed, with the default flags and with `--reruns 0` attempting to
// reset it.
func TestFailedDecideCountsRerunsFromTheForgeAttemptAndNotTheCaller(t *testing.T) {
	cancelledLeg := func(attempt int) *stubForge {
		return &stubForge{
			jobs: []ci.FailedJob{{ID: 3, Name: "e2e", Conclusion: "cancelled", Attempt: attempt, HeadSHA: decideSHA}},
			logs: map[int64]string{3: "cleanup\n"},
		}
	}
	noTestRed := func(attempt int) *stubForge {
		return &stubForge{
			jobs: []ci.FailedJob{{ID: 5, Name: "test (4/4)", Conclusion: "timed_out", Attempt: attempt, HeadSHA: decideSHA}},
			logs: map[int64]string{5: "no test event here\n"},
		}
	}
	cases := []struct {
		name  string
		forge *stubForge
		args  []string
		want  string
	}{
		{
			name:  "attempt 1 and a no-test red is the one licence",
			forge: noTestRed(1),
			args:  []string{"--decide"},
			want:  "red=infra rerun=licensed finding=no reruns=0 attempt=1",
		},
		{
			name:  "attempt 2 of the same job at the same sha is not licensed",
			forge: noTestRed(2),
			args:  []string{"--decide"},
			want:  "red=infra rerun=no finding=yes reruns=1 attempt=2",
		},
		{
			name:  "attempt 2 is not licensed however the caller sets --reruns 0",
			forge: noTestRed(2),
			args:  []string{"--decide", "--reruns", "0"},
			want:  "red=infra rerun=no finding=yes reruns=1 attempt=2",
		},
		{
			name:  "a cancelled leg on attempt 2 is a finding too",
			forge: cancelledLeg(2),
			args:  []string{"--decide", "--reruns", "0"},
			want:  "red=cancelled-leg rerun=no finding=yes reruns=1 attempt=2",
		},
		{
			name:  "the flag is a floor, so it can still withhold a licence at attempt 1",
			forge: cancelledLeg(1),
			args:  []string{"--decide", "--reruns", "1"},
			want:  "red=cancelled-leg rerun=no finding=yes reruns=1 attempt=1",
		},
		{
			name:  "a fifth attempt counts four spent reruns",
			forge: cancelledLeg(5),
			args:  []string{"--decide"},
			want:  "red=cancelled-leg rerun=no finding=yes reruns=4 attempt=5",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"--repo", "owner/name", "--run", "1"}, c.args...)
			code, stdout, stderr := runFailed(t, c.forge, args...)
			if code != 1 {
				t.Fatalf("exit = %d, want 1; stderr: %s", code, stderr)
			}
			if !strings.Contains(lastLine(stdout), c.want) {
				t.Errorf("closing line = %q, want it to carry %q", lastLine(stdout), c.want)
			}
		})
	}
}

// (k) NO ATTEMPT, NO LICENCE. The count has to come from somewhere, and when the forge
// will not say -- no attempt number on a red job, no sha to hold it against, or red jobs
// that disagree with each other -- the verb refuses at exit 2 rather than reading the
// absence as a first red. A refusal names what was missing, and prints no licence at all:
// a `rerun=` on stdout here would be a decision made on a fact nobody had.
func TestFailedDecideFailsClosedWhenTheForgeWillNotNameTheAttempt(t *testing.T) {
	other := "1111111111111111111111111111111111111111"
	cases := []struct {
		name  string
		forge *stubForge
		want  string
	}{
		{
			name: "no attempt number",
			forge: &stubForge{
				jobs: []ci.FailedJob{{ID: 3, Name: "e2e", Conclusion: "cancelled", HeadSHA: decideSHA}},
				logs: map[int64]string{3: "cleanup\n"},
			},
			want: "named no attempt number for the red job \"e2e\"",
		},
		{
			name: "no head sha",
			forge: &stubForge{
				jobs: []ci.FailedJob{{ID: 3, Name: "e2e", Conclusion: "cancelled", Attempt: 1}},
				logs: map[int64]string{3: "cleanup\n"},
			},
			want: "named no head sha for the red job \"e2e\"",
		},
		{
			name: "the red jobs disagree about the attempt",
			forge: &stubForge{
				jobs: []ci.FailedJob{
					{ID: 3, Name: "e2e", Conclusion: "cancelled", Attempt: 1, HeadSHA: decideSHA},
					{ID: 4, Name: "e2e (2)", Conclusion: "cancelled", Attempt: 2, HeadSHA: decideSHA},
				},
				logs: map[int64]string{3: "cleanup\n", 4: "cleanup\n"},
			},
			want: "attempt count disagrees with itself",
		},
		{
			name: "the red jobs disagree about the sha",
			forge: &stubForge{
				jobs: []ci.FailedJob{
					{ID: 3, Name: "e2e", Conclusion: "cancelled", Attempt: 1, HeadSHA: decideSHA},
					{ID: 4, Name: "e2e (2)", Conclusion: "cancelled", Attempt: 1, HeadSHA: other},
				},
				logs: map[int64]string{3: "cleanup\n", 4: "cleanup\n"},
			},
			want: "disagree about the sha",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := runFailed(t, c.forge, "--repo", "owner/name", "--run", "1", "--decide")
			if code != 2 {
				t.Fatalf("exit = %d, want 2; a licence decided without the forge's attempt is the one thing this verb must not do.\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stderr, c.want) {
				t.Errorf("stderr = %q, want it to name %q", stderr, c.want)
			}
			if strings.Contains(stdout, "rerun=") {
				t.Errorf("stdout carries a licence field after a refusal:\n%s", stdout)
			}
		})
	}
}

// (l) A REREAD OF ONE IMMUTABLE ATTEMPT SAYS THE SAME THING. This verb is a read-only
// classifier: it consumes nothing and reruns nothing, so reading the same attempt of the
// same job at the same sha twice is not a second red and must give the same advisory
// answer both times. The licence is spent by the caller's own rerun command, which the
// forge then reports as attempt 2 -- and the third read below, at that attempt, is the one
// that stops licensing.
func TestFailedDecideRereadingOneAttemptRepeatsTheSameAnswer(t *testing.T) {
	forge := func(attempt int) *stubForge {
		return &stubForge{
			jobs: []ci.FailedJob{{ID: 3, Name: "e2e", Conclusion: "cancelled", Attempt: attempt, HeadSHA: decideSHA}},
			logs: map[int64]string{3: "cleanup\n"},
		}
	}
	read := func(t *testing.T, attempt int) string {
		t.Helper()
		code, stdout, stderr := runFailed(t, forge(attempt), "--repo", "owner/name", "--run", "1", "--decide")
		if code != 1 {
			t.Fatalf("exit = %d, want 1; stderr: %s", code, stderr)
		}
		return lastLine(stdout)
	}
	first, second := read(t, 1), read(t, 1)
	if first != second {
		t.Errorf("two reads of attempt 1 answered differently:\n first  = %q\n second = %q", first, second)
	}
	if !strings.Contains(first, "rerun=licensed finding=no reruns=0 attempt=1") {
		t.Errorf("the first read of attempt 1 = %q, want the licence", first)
	}
	if third := read(t, 2); !strings.Contains(third, "rerun=no finding=yes reruns=1 attempt=2") {
		t.Errorf("the read after the rerun = %q, want no second licence", third)
	}
}

// decideSHA is the one commit the --decide tests' jobs ran at: the attempt count is only
// meaningful for one job at one sha, so the fake forge names it everywhere it is not the
// thing under test.
const decideSHA = "38a1affdb43d4cae9cb35a83c2cd3bcf3de2713d"

// lastLine is the last non-empty line of some output.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

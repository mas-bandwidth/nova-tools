package main

// fleet survey (issue #880 item 13): `nova-pulse fleet survey --benches <file>` runs
// tools/bench-standard.sh on every bench over ssh and folds the answers into one FLEET
// <name> line per bench. What the survey owes is that fold and the exit it votes for; the
// script's own findings are bench_standard_test.go's subject, and the real ssh child is
// one bounded probe at the bottom of this file.
//
// These tests wire a Go fake into fleetNewSurveyRunner and a fixed instant into fleetNow:
// no process starts, no shell runs, nothing waits on a clock.
//
// They used to build a shell world instead -- a #!/bin/sh fake ssh running `bash -s` under
// a fake HOME of nineteen freshly written #!/bin/sh bins per bench -- and drive the real
// tools/bench-standard.sh through it inside the survey's real-time deadline. On Linux a
// first exec is about a millisecond, so the survey was a blink. On macOS every first exec
// of a newly written file goes through the notarisation scan: ~240 ms each measured on an
// idle batman, and unbounded under load. One test burned nine seconds of wall clock idle,
// and when the scan queue stretched past the deadline, exec.CommandContext killed ssh
// mid-script. The survey then saw output with neither STANDARD OK nor a DRIFT line, which
// it reports as UNREACHABLE voting 3 -- "fleet survey drift exit = 3, want 2" on batman,
// "FLEET alpha UNREACHABLE signal: killed" on a loaded Studio, and the unreachable test's
// missing "Connection refused" (the reason carried was the kill). The verdict was a
// function of how busy the host was. Nothing below can be.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const fleetTestWant = "v9.9.9-fleet-test"

// The three answers a bench gives, in the exact shapes tools/bench-standard.sh prints.
const (
	fleetStandardOK    = "STANDARD OK go=go1.26.5 bins=" + fleetTestWant + " harness=ok seats=1\n"
	fleetStandardDrift = "DRIFT nova-bus version [v0.0.0-other] want " + fleetTestWant + "\n" +
		"STANDARD DRIFT (see lines above)\n"
)

// fleetRefused is what ssh itself says about a host it cannot reach: the message on the
// combined output and a non-zero exit.
func fleetRefused(target string) fleetSurveyAnswer {
	return fleetSurveyAnswer{
		out: "ssh: connect to host " + target + " port 22: Connection refused\n",
		err: errors.New("exit status 255"),
	}
}

// fleetSurveyAnswer is one bench's canned answer.
type fleetSurveyAnswer struct {
	out string
	err error
}

// fleetSurveyCall is one remote step as the fake saw it.
type fleetSurveyCall struct {
	target      string
	script      string
	deadline    time.Time
	hasDeadline bool
}

// fakeSurveyRunner answers every target from a table and records the call. It starts
// nothing and waits for nothing, so a survey of any number of benches finishes in the time
// it takes to copy a string.
type fakeSurveyRunner struct {
	answers map[string]fleetSurveyAnswer

	mu    sync.Mutex
	calls []fleetSurveyCall
}

func (f *fakeSurveyRunner) Run(ctx context.Context, target, script string) (string, error) {
	deadline, ok := ctx.Deadline()
	f.mu.Lock()
	f.calls = append(f.calls, fleetSurveyCall{target: target, script: script, deadline: deadline, hasDeadline: ok})
	f.mu.Unlock()
	answer, found := f.answers[target]
	if !found {
		answer = fleetRefused(target)
	}
	return answer.out, answer.err
}

func (f *fakeSurveyRunner) seen() []fleetSurveyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fleetSurveyCall(nil), f.calls...)
}

// fleetSurveyFixture writes the benches file and wires the fake runner in for this test.
// The benches are given in file order as name -> answer; a name with no answer is a bench
// the fake refuses the way ssh refuses a host it cannot reach.
func fleetSurveyFixture(t *testing.T, names []string, answers map[string]fleetSurveyAnswer) (benchesPath string, runner *fakeSurveyRunner) {
	t.Helper()
	byTarget := map[string]fleetSurveyAnswer{}
	var b strings.Builder
	for _, name := range names {
		target := "fake-" + name
		if answer, ok := answers[name]; ok {
			byTarget[target] = answer
		}
		b.WriteString(name + "\t" + target + "\t/fake/home/" + name + "\tmock\n")
	}
	runner = &fakeSurveyRunner{answers: byTarget}
	prev := fleetNewSurveyRunner
	fleetNewSurveyRunner = func(string) fleetSurveyRunner { return runner }
	t.Cleanup(func() { fleetNewSurveyRunner = prev })

	benchesPath = filepath.Join(t.TempDir(), "benches.tsv")
	if err := os.WriteFile(benchesPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return benchesPath, runner
}

// fleetSurveyRun runs the verb and returns its exit, stdout and stderr.
func fleetSurveyRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"fleet", "survey"}, args...), &out, &errb, time.Now().UTC())
	return code, out.String(), errb.String()
}

func TestFleetSurveyTwoOKBenchesExitZero(t *testing.T) {
	benches, _ := fleetSurveyFixture(t, []string{"alpha", "beta"}, map[string]fleetSurveyAnswer{
		"alpha": {out: fleetStandardOK},
		"beta":  {out: fleetStandardOK},
	})
	code, out, errb := fleetSurveyRun(t, "--benches", benches)
	if code != 0 {
		t.Fatalf("fleet survey exit = %d, want 0; stderr=%q out=%q", code, errb, out)
	}
	for _, name := range []string{"alpha", "beta"} {
		if !strings.Contains(out, "FLEET "+name+" STANDARD OK") {
			t.Errorf("output missing FLEET %s STANDARD OK:\n%s", name, out)
		}
	}
}

func TestFleetSurveyDriftBenchNamesItExitTwo(t *testing.T) {
	benches, _ := fleetSurveyFixture(t, []string{"alpha", "beta"}, map[string]fleetSurveyAnswer{
		"alpha": {out: fleetStandardOK},
		// the script exits 1 on drift, and that exit is not an unreachable bench
		"beta": {out: fleetStandardDrift, err: errors.New("exit status 1")},
	})
	code, out, errb := fleetSurveyRun(t, "--benches", benches)
	if code != 2 {
		t.Fatalf("fleet survey drift exit = %d, want 2; stderr=%q out=%q", code, errb, out)
	}
	if !strings.Contains(out, "FLEET beta DRIFT nova-bus") {
		t.Fatalf("output missing FLEET beta DRIFT naming nova-bus:\n%s", out)
	}
	if !strings.Contains(out, "FLEET beta STANDARD DRIFT") {
		t.Fatalf("output missing FLEET beta STANDARD DRIFT:\n%s", out)
	}
	if !strings.Contains(out, "FLEET alpha STANDARD OK") {
		t.Fatalf("one bench drifting lost the other bench's line:\n%s", out)
	}
}

func TestFleetSurveyUnreachableBenchNamesItExitThree(t *testing.T) {
	// gamma has no answer: the fake refuses it the way ssh refuses a host it cannot reach.
	benches, _ := fleetSurveyFixture(t, []string{"alpha", "gamma"}, map[string]fleetSurveyAnswer{
		"alpha": {out: fleetStandardOK},
	})
	code, out, errb := fleetSurveyRun(t, "--benches", benches)
	if code != 3 {
		t.Fatalf("fleet survey unreachable exit = %d, want 3; stderr=%q out=%q", code, errb, out)
	}
	if !strings.Contains(out, "FLEET gamma UNREACHABLE") {
		t.Fatalf("output missing FLEET gamma UNREACHABLE:\n%s", out)
	}
	if !strings.Contains(out, "Connection refused") {
		t.Fatalf("output does not carry the ssh error:\n%s", out)
	}
}

// An unreachable bench outvotes a drifting one: the fleet is not merely drifting when a
// bench did not answer at all.
func TestFleetSurveyUnreachableOutvotesDrift(t *testing.T) {
	benches, _ := fleetSurveyFixture(t, []string{"alpha", "beta"}, map[string]fleetSurveyAnswer{
		"alpha": {out: fleetStandardDrift, err: errors.New("exit status 1")},
	})
	code, out, _ := fleetSurveyRun(t, "--benches", benches)
	if code != 3 {
		t.Fatalf("drift plus unreachable exit = %d, want 3:\n%s", code, out)
	}
}

func TestFleetSurveyMaxCapsLines(t *testing.T) {
	benches, _ := fleetSurveyFixture(t, []string{"alpha", "beta"}, map[string]fleetSurveyAnswer{
		"alpha": {out: fleetStandardDrift, err: errors.New("exit status 1")},
		"beta":  {out: fleetStandardDrift, err: errors.New("exit status 1")},
	})
	_, out, _ := fleetSurveyRun(t, "--benches", benches, "--max", "1")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "FLEET ") {
		t.Fatalf("--max 1 printed %d lines, want 1 FLEET line:\n%s", len(lines), out)
	}
}

// Every bench is asked the standard itself, not a paraphrase of it.
func TestFleetSurveySendsTheStandardScriptToEveryBench(t *testing.T) {
	benches, runner := fleetSurveyFixture(t, []string{"alpha", "beta"}, map[string]fleetSurveyAnswer{
		"alpha": {out: fleetStandardOK},
		"beta":  {out: fleetStandardOK},
	})
	if code, out, errb := fleetSurveyRun(t, "--benches", benches); code != 0 {
		t.Fatalf("fleet survey exit = %d, want 0; stderr=%q out=%q", code, errb, out)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "tools", "bench-standard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	calls := runner.seen()
	if len(calls) != 2 {
		t.Fatalf("the survey made %d remote steps, want one per bench", len(calls))
	}
	for _, call := range calls {
		if call.script != string(want) {
			t.Fatalf("bench %s was sent %d bytes, want tools/bench-standard.sh whole", call.target, len(call.script))
		}
	}
}

// --timeout is the budget each bench gets, measured from the survey's clock. The clock is
// a fixed instant here, so the assertion is exact and nothing waits for a deadline.
func TestFleetSurveyBoundsEachBenchByTheTimeoutFlag(t *testing.T) {
	fixed := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	prev := fleetNow
	fleetNow = func() time.Time { return fixed }
	t.Cleanup(func() { fleetNow = prev })

	benches, runner := fleetSurveyFixture(t, []string{"alpha", "beta"}, map[string]fleetSurveyAnswer{
		"alpha": {out: fleetStandardOK},
		"beta":  {out: fleetStandardOK},
	})
	if code, out, errb := fleetSurveyRun(t, "--benches", benches, "--timeout", "7"); code != 0 {
		t.Fatalf("fleet survey exit = %d, want 0; stderr=%q out=%q", code, errb, out)
	}
	calls := runner.seen()
	if len(calls) != 2 {
		t.Fatalf("the survey made %d remote steps, want one per bench", len(calls))
	}
	want := fixed.Add(7 * time.Second)
	for _, call := range calls {
		if !call.hasDeadline {
			t.Fatalf("bench %s was given no deadline", call.target)
		}
		if !call.deadline.Equal(want) {
			t.Fatalf("bench %s deadline = %s, want %s", call.target, call.deadline, want)
		}
	}
}

// The one OS-level step, kept to itself and bounded: the real runner starts the program it
// was given with the target as its argument and the script on its stdin. It execs exactly
// one small program, and -short skips it.
func TestFleetSSHRunnerStartsTheProgramWithTheScriptOnStdin(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: the real ssh child is an OS-level step")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the fleet is a linux bench; the probe's fake ssh is a shell script")
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	ssh := filepath.Join(dir, "ssh")
	body := "#!/bin/sh\necho \"argv: $*\" > " + record + "\ncat >> " + record + "\necho '" + strings.TrimRight(fleetStandardOK, "\n") + "'\n"
	if err := os.WriteFile(ssh, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := fleetSSHRunner{Program: ssh}.Run(ctx, "fake-alpha", "echo hello\n")
	if err != nil {
		t.Fatalf("the real runner: %v\n%s", err, out)
	}
	if !strings.Contains(out, "STANDARD OK") {
		t.Fatalf("the runner did not return the child's output:\n%s", out)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); !strings.Contains(got, "argv: fake-alpha bash -s") || !strings.Contains(got, "echo hello") {
		t.Fatalf("the child saw %q, want the target, `bash -s` and the script on stdin", got)
	}
}

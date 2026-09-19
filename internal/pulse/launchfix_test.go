package pulse

// The five friction points a 2026-09-19 dogfood of `nova-pulse launch` measured against a
// real card (nova-tools #1760, #1761), each as the test that was red before the fix.
//
//	F1  docs/CLI.md and the binary named different flag sets   -> internal/docs, class test
//	F2  no --runner: the runner had to be on PATH              -> TestLaunchRunnerPath*
//	F3  the F2 remedy shadowed nova-swarm with a stale binary  -> TestLaunchRefusesSwarm*
//	F4  `PULSE REFUSED: exit status 3` named nothing           -> TestLaunchRefusalNames*
//	F5  a start-time provider 5xx killed the whole pulse       -> TestLaunchRetries*
//
// Every test here drives Launch through the fake nova-swarm on PATH, and the ones that need
// a job tree write the files the layers underneath would have written -- the same
// `harness.log` and `harness-output.log` the dogfood read by hand, transcribed from it.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// theHour is this file's clock: a fixed time, so an id is the only thing that varies.
func theHour() time.Time { return time.Date(2026, 9, 19, 16, 0, 0, 0, time.UTC) }

// swarmVersionRule is the arm a fake nova-swarm answers `version` with. See the cmd-side
// twin in cmd/nova-pulse/fake_test.go: the real binary prints one line of four tokens.
func swarmVersionRule(v string) fakeRule {
	return fakeRule{Arg: 1, Equals: "version", Stdout: "nova-swarm " + v + " " + runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version()}
}

// fakeSwarmSaying puts a fake nova-swarm on PATH with the caller's rules, and returns the
// directory it lives in so a test can name the binary outright with --swarm.
func fakeSwarmSaying(t *testing.T, argvLog string, rules []fakeRule, def fakeRule) string {
	t.Helper()
	specs := fakePATH(t)
	fakeTool(t, specs, "nova-swarm", fakeSpec{Log: argvLog, Rules: rules, Default: def})
	return fakeBins(t)
}

// writeJobTree writes what `nova-swarm batch` and its runner leave behind for one card:
// <root>/<slot>/jobs/<label>/harness.log with the runner's NATIVE line, and
// harness-output.log with the harness's own capture. Both lines are the dogfood's, verbatim
// bar the paths (nova-tools #1761).
func writeJobTree(t *testing.T, root, slot, label, native, capture string) string {
	t.Helper()
	job := filepath.Join(root, slot, "jobs", label)
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if native != "" {
		if err := os.WriteFile(filepath.Join(job, "harness.log"), []byte(native), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if capture != "" {
		if err := os.WriteFile(filepath.Join(job, "harness-output.log"), []byte(capture), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return job
}

const dogfoodNative = "NATIVE OK label=probe job=/x/jobs/probe rc=1 wall=3.10s sandbox=sandbox-exec " +
	"card_sha256=6a75f636 harness=ok usage=none reason=no-rows\n"

const dogfoodCapture = `Error: { "name": "UnknownError",
         "data": { "message": "Unexpected server error. Check server logs for details.",
                   "ref": "err_95331ad0" } }
`

// F3, the half that was measured. The stale nova-swarm the dogfood's PATH remedy put in
// front answered `nova-swarm: unknown subcommand "version"` and exited non-zero. Launch
// must refuse BY NAME before it hands that binary a batch, because what the stale one says
// about the batch ("flag provided but not defined: -id") reads exactly like launch building
// a bad call, and is not.
func TestLaunchRefusesASwarmThatCannotSayItsVersion(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarmSaying(t, argvLog, []fakeRule{
		{Arg: 1, Equals: "version", Stderr: `nova-swarm: unknown subcommand "version"; run: nova-swarm help`, Exit: 2},
	}, fakeRule{})
	cards, _ := writeCards(t, root, 1)

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Version: "v0.16.0-dev.0f7ed3b5",
		Now:     theHour,
	})

	if code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%s", code, errb)
	}
	if out != "" {
		t.Fatalf("stdout=%q, want nothing", out)
	}
	for _, want := range []string{"PULSE REFUSED SWARM-VERSION", "nova-swarm", `unknown subcommand`, "--swarm"} {
		if !strings.Contains(errb, want) {
			t.Fatalf("refusal %q does not name %q", errb, want)
		}
	}
	// And it never reached the batch: the refusal is BEFORE the card is handed over.
	if raw, err := os.ReadFile(argvLog); err == nil && strings.Contains(string(raw), " batch ") {
		t.Fatalf("a swarm that cannot say its version was still handed a batch: %s", raw)
	}
}

// F3, the sharper half. Two builds of the SAME release were the shadow this fleet actually
// carried -- c839379e against 0f7ed3b5, both `v0.16.0-dev`, the older missing a flag the
// newer passes. So the comparison is exact, not by release prefix.
func TestLaunchRefusesASwarmOfADifferentBuild(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarmSaying(t, argvLog, []fakeRule{swarmVersionRule("v0.16.0-dev.c839379e")}, fakeRule{})
	cards, _ := writeCards(t, root, 1)

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Version: "v0.16.0-dev.0f7ed3b5",
		Now:     theHour,
	})

	if code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%s", code, errb)
	}
	for _, want := range []string{"PULSE REFUSED SWARM-VERSION", "v0.16.0-dev.c839379e", "v0.16.0-dev.0f7ed3b5", "--swarm"} {
		if !strings.Contains(errb, want) {
			t.Fatalf("refusal %q does not name %q", errb, want)
		}
	}
}

// The same build passes, and the version probe leaves the batch call itself alone.
func TestLaunchAcceptsTheSwarmOfItsOwnBuild(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarmSaying(t, argvLog, []fakeRule{swarmVersionRule("v0.16.0-dev.0f7ed3b5")}, fakeRule{})
	cards, _ := writeCards(t, root, 1)

	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Version: "v0.16.0-dev.0f7ed3b5",
		Now:     theHour,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	if !strings.HasPrefix(out, "PULSE OK ") {
		t.Fatalf("stdout=%q", out)
	}
}

// F3's door: --swarm names the binary outright, and THAT is the binary the batch runs,
// whatever PATH holds. The dogfood had no such flag, so the only remedy available was to
// mutate PATH -- which is what created the shadow.
func TestLaunchRunsTheSwarmNamedByFlag(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	bin := fakeSwarmSaying(t, argvLog, []fakeRule{swarmVersionRule("v0.16.0-dev.0f7ed3b5")}, fakeRule{})
	named := filepath.Join(bin, "nova-swarm"+exeSuffix())
	cards, _ := writeCards(t, root, 1)

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Swarm: named, Version: "v0.16.0-dev.0f7ed3b5",
		Now: theHour,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "nova-swarm batch ") {
		t.Fatalf("the named binary ran no batch: %s", raw)
	}
}

// A --swarm that is not there is refused by the flag that named it.
func TestLaunchRefusesASwarmPathThatIsNotThere(t *testing.T) {
	root := t.TempDir()
	fakeSwarm(t, filepath.Join(root, "argv.log"))
	cards, _ := writeCards(t, root, 1)
	missing := filepath.Join(root, "no-such-nova-swarm")

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Swarm: missing, Version: "v0.16.0-dev.0f7ed3b5",
		Now: theHour,
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%s", code, errb)
	}
	if !strings.Contains(errb, "PULSE REFUSED SWARM-LOOKUP") || !strings.Contains(errb, missing) {
		t.Fatalf("refusal %q does not name the path it was given", errb)
	}
}

// F2. The runner had to be on PATH and no flag could act on the refusal. Now the flag
// exists and the path it names is what reaches `nova-swarm batch --runner`.
func TestLaunchRunnerPathReachesTheBatch(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 1)
	runner := filepath.Join(root, "nova-native-runner.sh")
	if err := os.WriteFile(runner, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Runner: runner, Now: theHour,
	})
	if code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "--runner "+runner) {
		t.Fatalf("batch argv lacks --runner %s: %s", runner, raw)
	}
}

// F2's refusal: a --runner path that is not there is named, with the flag that fixes it,
// before a slot is taken.
func TestLaunchRefusesARunnerPathThatIsNotThere(t *testing.T) {
	root := t.TempDir()
	fakeSwarm(t, filepath.Join(root, "argv.log"))
	cards, _ := writeCards(t, root, 1)
	missing := filepath.Join(root, "bin", "nova-native-runner.sh")

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Runner: missing, Now: theHour,
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%s", code, errb)
	}
	if !strings.Contains(errb, "PULSE REFUSED RUNNER") || !strings.Contains(errb, missing) || !strings.Contains(errb, "--runner") {
		t.Fatalf("refusal %q does not name the runner and the door", errb)
	}
}

// A launch that names no runner still passes the bare PATH name it always has: the default
// is unchanged, so no deployment moves because this flag now exists.
func TestLaunchWithoutRunnerFlagKeepsTheBareName(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 1)

	if code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120", Now: theHour,
	}); code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%s", code, errb)
	}
	raw, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "--runner nova-native-runner.sh") {
		t.Fatalf("batch argv lost the default runner: %s", raw)
	}
}

// F4. `PULSE REFUSED: exit status 3` was the ENTIRE operator-visible output while the
// label, the rc, the wall clock, the reason token and the provider's own error reference
// were all already on disk one directory away. The refusal must carry them, and the door.
func TestLaunchRefusalNamesTheCardTheRcAndTheHarnessError(t *testing.T) {
	root := t.TempDir()
	cards, _ := writeCardsNamed(t, root, "probe")
	job := writeJobTree(t, root, "1", "probe", dogfoodNative, dogfoodCapture)
	fakeSwarmSaying(t, filepath.Join(root, "argv.log"), nil, fakeRule{Exit: 3})

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Attempts: 1, Now: theHour,
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2; stderr=%s", code, errb)
	}
	line := firstSaidLine(errb)
	for _, want := range []string{
		"card=probe",
		"rc=1",
		"wall=3.10s",
		"reason=no-rows",
		"err_95331ad0",
		job,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("the refusal line does not name %q:\n%s", want, line)
		}
	}
	if !strings.Contains(line, "exit status 3") {
		t.Fatalf("the refusal dropped the child's own exit: %s", line)
	}
}

// A batch that refuses before any job directory exists keeps the swarm's own line, which
// the dogfood called exemplary. Nothing is invented for a run that left nothing.
func TestLaunchRelaysASwarmRefusalWithNoJobTreeUnchanged(t *testing.T) {
	root := t.TempDir()
	cards, _ := writeCardsNamed(t, root, "probe")
	fakeSwarmSaying(t, filepath.Join(root, "argv.log"), nil, fakeRule{
		Stderr: "nova-swarm batch: runner nova-native-runner.sh could not start for probe: executable file not found in $PATH",
		Exit:   2,
	})

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120", Attempts: 1, Now: theHour,
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
	want := "PULSE REFUSED: nova-swarm batch: runner nova-native-runner.sh could not start for probe: executable file not found in $PATH\n"
	if got := errb[:len(want)]; got != want {
		t.Fatalf("stderr=%q, want it to open with %q", errb, want)
	}
	if strings.Contains(errb, "card=") {
		t.Fatalf("a refusal with no job tree invented a card diagnosis: %q", errb)
	}
}

// F5. One transient provider 5xx on the first call ended the whole pulse, while the shell
// launcher this replaces retried exactly that signature. The retry is bounded, it backs
// off, and the failed attempt's job directory is MOVED aside rather than deleted.
func TestLaunchRetriesAStartTimeProviderFailure(t *testing.T) {
	root := t.TempDir()
	cards, _ := writeCardsNamed(t, root, "probe")
	// One job tree per attempt, under a different slot each time, which is what a real
	// batch leaves: the previous attempt's slot is no longer free, so the next one takes
	// the next slot. Three trees means all three attempts fail the same way, which is
	// what the bound is for.
	jobs := []string{
		writeJobTree(t, root, "1", "probe", dogfoodNative, dogfoodCapture),
		writeJobTree(t, root, "2", "probe", dogfoodNative, dogfoodCapture),
		writeJobTree(t, root, "3", "probe", dogfoodNative, dogfoodCapture),
	}
	fakeSwarmSaying(t, filepath.Join(root, "argv.log"), nil, fakeRule{Exit: 3})

	var slept []time.Duration
	code, out, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Attempts: 3, Sleep: func(d time.Duration) { slept = append(slept, d) },
		Now: theHour,
	})

	if code != 2 {
		t.Fatalf("exit=%d, want 2 (every attempt failed); stderr=%s", code, errb)
	}
	if out != "" {
		t.Fatalf("stdout=%q, want nothing", out)
	}
	if n := strings.Count(errb, "PULSE RETRY "); n != 2 {
		t.Fatalf("want 2 PULSE RETRY lines under --attempts 3, got %d:\n%s", n, errb)
	}
	if !strings.Contains(errb, "attempts=3 of 3") {
		t.Fatalf("the final refusal does not say the bound was reached:\n%s", errb)
	}
	if len(slept) != 2 || slept[0] != 15*time.Second || slept[1] != 25*time.Second {
		t.Fatalf("backoff=%v, want [15s 25s]", slept)
	}
	// The evidence of each retried failure survived the retry: parked, never deleted.
	for i, park := range []string{jobs[0] + ".attempt1", jobs[1] + ".attempt2"} {
		raw, err := os.ReadFile(filepath.Join(park, "harness-output.log"))
		if err != nil {
			t.Fatalf("attempt %d's job directory was not parked with its capture: %v", i+1, err)
		}
		if !strings.Contains(string(raw), "err_95331ad0") {
			t.Fatalf("the parked job lost its capture: %q", raw)
		}
	}
	// The last attempt's tree is where it was: a refusal leaves the evidence in place.
	if _, err := os.Stat(jobs[2]); err != nil {
		t.Fatalf("the final attempt's job directory was moved: %v", err)
	}
}

// The bound is a bound: --attempts 1 is the behaviour before this fix, no retry at all.
func TestLaunchAttemptsOneDoesNotRetry(t *testing.T) {
	root := t.TempDir()
	cards, _ := writeCardsNamed(t, root, "probe")
	writeJobTree(t, root, "1", "probe", dogfoodNative, dogfoodCapture)
	fakeSwarmSaying(t, filepath.Join(root, "argv.log"), nil, fakeRule{Exit: 3})

	var slept []time.Duration
	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Attempts: 1, Sleep: func(d time.Duration) { slept = append(slept, d) },
		Now: theHour,
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
	if strings.Contains(errb, "PULSE RETRY") || len(slept) != 0 {
		t.Fatalf("--attempts 1 retried anyway: %q %v", errb, slept)
	}
}

// A failure that is NOT the provider's is not retried, whatever it cost: a card that ran
// for a minute and came back red did work, and re-running it pays for that work twice.
func TestLaunchDoesNotRetryAFailureThatIsNotTheProvider(t *testing.T) {
	root := t.TempDir()
	cards, _ := writeCardsNamed(t, root, "probe")
	writeJobTree(t, root, "1", "probe",
		"NATIVE OK label=probe job=/x/jobs/probe rc=1 wall=91.40s sandbox=sandbox-exec harness=ok reason=red\n",
		"the tests failed: internal/pulse TestSomething\n")
	fakeSwarmSaying(t, filepath.Join(root, "argv.log"), nil, fakeRule{Exit: 3})

	var slept []time.Duration
	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Attempts: 3, Sleep: func(d time.Duration) { slept = append(slept, d) },
		Now: theHour,
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
	if len(slept) != 0 || strings.Contains(errb, "PULSE RETRY") {
		t.Fatalf("a red card was retried as if it were a 5xx: %q %v", errb, slept)
	}
	if !strings.Contains(errb, "reason=red") || !strings.Contains(errb, "wall=91.40s") {
		t.Fatalf("the refusal still has to say what happened: %q", errb)
	}
}

// A dead API key is not a transient failure, whatever the shell launcher does with it: it
// is dead on the third call too, and retrying it reaches the same refusal three times
// slower. Named because the launcher this replaces DOES retry it, so the difference is a
// decision and not an oversight.
func TestLaunchDoesNotRetryADeadKey(t *testing.T) {
	root := t.TempDir()
	cards, _ := writeCardsNamed(t, root, "probe")
	writeJobTree(t, root, "1", "probe",
		"NATIVE OK label=probe job=/x/jobs/probe rc=1 wall=1.20s harness=ok reason=no-rows\n",
		"Error: Authentication Fails, Your api key: ****1a8a is invalid\n")
	fakeSwarmSaying(t, filepath.Join(root, "argv.log"), nil, fakeRule{Exit: 3})

	var slept []time.Duration
	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 4, Deadline: "120",
		Attempts: 3, Sleep: func(d time.Duration) { slept = append(slept, d) },
		Now: theHour,
	})
	if code != 2 {
		t.Fatalf("exit=%d, want 2", code)
	}
	if len(slept) != 0 {
		t.Fatalf("a dead key was retried: %v", slept)
	}
	if !strings.Contains(errb, "api key") {
		t.Fatalf("the refusal does not name what the harness said: %q", errb)
	}
}

// writeCardsNamed is writeCards with the caller's label, because a refusal that names the
// card has to be tested against a label the test chose.
func writeCardsNamed(t *testing.T, root, label string) (string, string) {
	t.Helper()
	dir := filepath.Join(root, "src")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(dir, label+".md")
	if err := os.WriteFile(card, []byte("RESULT "+label+" sha=000000000000\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cards := filepath.Join(root, "cards.tsv")
	if err := os.WriteFile(cards, []byte(label+"\t-\tpro\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cards, card
}

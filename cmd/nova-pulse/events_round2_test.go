package main

// The two pulse verbs of round 2 at the BINARY: the flags a bench actually types, the sink
// they open, and the refusal each makes when it cannot. The per-line assertions live beside
// the work in internal/pulse; these are the wiring -- a flag that reaches no emitter is a
// verb that logs nothing however good the emitter is.
//
// Seen red first: before the flags existed both runs exited 2 on an unknown flag.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cmdEvent is one structured line as this package reads it back.
type cmdEvent struct {
	Level  string `json:"level"`
	Source string `json:"source"`
	Bench  string `json:"bench"`
	Verb   string `json:"verb"`
	Event  string `json:"event"`
	Msg    string `json:"msg"`
	TS     string `json:"ts"`
	GUID   string `json:"guid"`
}

func readCmdEvents(t *testing.T, path string) []cmdEvent {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the event log was not written: %v", err)
	}
	var out []cmdEvent
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e cmdEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("a logged line is not one JSON object: %v\n%s", err, line)
		}
		out = append(out, e)
	}
	return out
}

func cmdEventsOfKind(lines []cmdEvent, event string) []cmdEvent {
	var out []cmdEvent
	for _, l := range lines {
		if l.Event == event {
			out = append(out, l)
		}
	}
	return out
}

// hygiene-run-emits-start-delete-disk-free-and-done: one pass over a fake bench tree writes
// the timer's spine and one event per action, with the rule that decided each deletion --
// which is SPEC-LOGS.md Part 3's "what did hygiene delete in the last hour, and why" as one
// query instead of an ssh and a grep of ~/hygiene.log.
func TestHygieneRunEmitsItsPassIntoTheEventLog(t *testing.T) {
	now := time.Date(2026, 9, 17, 18, 0, 0, 0, time.UTC)
	home := t.TempDir()
	root1 := filepath.Join(home, "rowan-swarm-root")

	dead := filepath.Join(root1, "dead")
	hygieneMkdir(t, filepath.Join(dead, "data"))
	oldJob := filepath.Join(dead, "jobs", "card-old")
	hygieneMkdir(t, filepath.Join(oldJob, "scratch"))
	hygieneWrite(t, filepath.Join(oldJob, "harness-output.log"), "old\n", now.Add(-7*time.Hour))
	hygieneMkdir(t, filepath.Join(root1, "empty"))

	defer swapHygieneEnv(hygieneFakeProcs{}, hygieneFakeDisk{freeGB: 40, free: "40G", sizeGB: 0})()

	eventLog := filepath.Join(home, "nova-events.log")
	code, out, errb := hygieneRun(t, now, "run", "--home", home, "--hostname", "bench",
		"--label", "hulk", "--event-log", eventLog)
	if code != 0 {
		t.Fatalf("hygiene run exit = %d, stderr=%s", code, errb)
	}
	if !strings.Contains(out, "HYGIENE bench") {
		t.Fatalf("the human line is untouched and still first: %q", out)
	}

	lines := readCmdEvents(t, eventLog)
	for _, l := range lines {
		if l.Source != "nova-pulse" || l.Verb != "hygiene" || l.Bench != "hulk" {
			t.Fatalf("a line is missing its labels: source=%q verb=%q bench=%q", l.Source, l.Verb, l.Bench)
		}
		if l.TS == "" || l.GUID == "" {
			t.Fatalf("ts and guid are never absent: %+v", l)
		}
	}
	if n := len(cmdEventsOfKind(lines, "start")); n != 1 {
		t.Fatalf("one start per pass, got %d", n)
	}
	deletes := cmdEventsOfKind(lines, "delete")
	if len(deletes) == 0 {
		t.Fatalf("a pass that deleted directories wrote no delete event:\n%s", out)
	}
	for _, d := range deletes {
		if !strings.Contains(d.Msg, "rule=") {
			t.Fatalf("a delete event names the rule that decided it: %q", d.Msg)
		}
		// THE PATH, OR THE MARK THAT ATE IT. The emitter's redaction (internal/log,
		// #1326) treats a long mixed-class run over the credential alphabet as a
		// credential, and "/" and "=" are in that alphabet -- so on a host whose
		// temporary directory carries three upper-case letters, three lower-case and
		// three digits in one run (macOS names one after the test), the whole
		// `path=/...` token is replaced by the mark. A Linux bench's
		// /home/<user>/rowan-swarm-root/... has no upper case and always survives, which
		// is where this verb runs. The assertion is therefore "the path is named, or the
		// redaction says why it is not", and the narrowing of that rule is a finding for
		// the emitter's own PR, not a change made from this lane.
		if !strings.Contains(d.Msg, "path=") && !strings.Contains(d.Msg, "[redacted]") {
			t.Fatalf("a delete event names the path it removed: %q", d.Msg)
		}
	}
	free := cmdEventsOfKind(lines, "disk-free")
	if len(free) != 1 {
		t.Fatalf("one disk-free event per pass, got %d", len(free))
	}
	if !strings.Contains(free[0].Msg, "free_gb=40") {
		t.Fatalf("the free disk is a field the alert reads: %q", free[0].Msg)
	}
	done := cmdEventsOfKind(lines, "done")
	if len(done) != 1 {
		t.Fatalf("one done per pass, got %d", len(done))
	}
	for _, want := range []string{"slots=", "reaped=", "jobs-deleted=", "slots-deleted=", "free_gb=40"} {
		if !strings.Contains(done[0].Msg, want) {
			t.Fatalf("the done line carries the HYGIENE counts (%s): %q", want, done[0].Msg)
		}
	}
}

// A bench whose lines can never be written says so at the START, before it deletes
// anything: the whole point of the stream is that a removal has a record.
func TestHygieneRefusesAnEventLogItCannotOpen(t *testing.T) {
	home := t.TempDir()
	defer swapHygieneEnv(hygieneFakeProcs{}, hygieneFakeDisk{freeGB: 40, free: "40G", sizeGB: 0})()
	code, out, errb := hygieneRun(t, time.Now().UTC(), "run", "--home", home,
		"--event-log", filepath.Join(home, "no-such-directory", "nova-events.log"))
	if code != 2 {
		t.Fatalf("an --event-log that cannot be opened is exit 2, got %d; stderr=%s", code, errb)
	}
	if !strings.Contains(errb, "--event-log") {
		t.Fatalf("the refusal names the flag: %q", errb)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("a refused pass printed a HYGIENE line: %q", out)
	}
}

// The same edge on the fold: the sink is opened before the first push, so a --log nobody
// can write costs nothing rather than a fold whose lines went nowhere.
func TestHarvestRefusesALogItCannotOpen(t *testing.T) {
	dir := t.TempDir()
	var out, errb strings.Builder
	code := cmdHarvest([]string{
		"--id", "p1", "--root", dir,
		"--log", filepath.Join(dir, "no-such-directory", "nova-events.log"),
	}, &out, &errb)
	if code != 2 {
		t.Fatalf("a --log that cannot be opened is exit 2, got %d; stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--log") {
		t.Fatalf("the refusal names the flag: %q", errb.String())
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("a refused fold wrote a verdict on stdout: %q", out.String())
	}
}

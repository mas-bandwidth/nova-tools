package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const askUnits = `{"units":[
  {"id":"u1","title":"retire the child shell","owner":"Emma",
   "needs":["read the thirteen scripts"],
   "acceptance":["one line per script in scratch/notes.md"],
   "branch":"rowan/lane-friends"},
  {"id":"u2","title":"a unit with no owner",
   "needs":["n"],"acceptance":["a"]}
]}`

// fakeSender is the send seam in the tests: nothing is started and nothing is pushed.
type fakeSender struct {
	notes []string
	id    string
	err   error
}

func (f *fakeSender) Send(note string) (string, error) {
	f.notes = append(f.notes, note)
	return f.id, f.err
}
func (f *fakeSender) Where() string { return "(fake bus)" }

func unitsFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "units.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func runAsk(t *testing.T, f *fakeSender, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := askWith(args, &stdout, &stderr, f)
	return code, stdout.String(), stderr.String()
}

func TestAskSendsTheUnitToItsFriendAndRecordsTheAskID(t *testing.T) {
	p := unitsFile(t, askUnits)
	f := &fakeSender{id: "2026-09-18T1200Z-ask-abcdef"}
	code, stdout, stderr := runAsk(t, f,
		"--owner", "Emma", "--unit", "u1", "--units", p,
		"--deadline", "2026-09-18T20:00:00Z", "--bus", "/bus", "--as", "Rowan",
		"--remote", "origin", "--branch", "main", "--reply-branch", "rowan/lane-friends", "--now", "2026-09-18T12:00:00Z")
	if code != 0 {
		t.Fatalf("ask exit = %d, stderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "ASK OK id=2026-09-18T1200Z-ask-abcdef owner=Emma unit=u1 kind=work") {
		t.Fatalf("the one line is wrong: %q", stdout)
	}
	if n := strings.Count(strings.TrimSpace(stdout), "\n"); n != 0 {
		t.Fatalf("ask prints ONE line on stdout, got %d extra", n)
	}
	if len(f.notes) != 1 {
		t.Fatalf("the sender saw %d notes, want 1", len(f.notes))
	}
	for _, want := range []string{"To: Emma", "Subject: ask work: retire the child shell", "Deadline: 2026-09-18T20:00:00Z", "Reply on branch: rowan/lane-friends"} {
		if !strings.Contains(f.notes[0], want) {
			t.Errorf("the note does not carry %q:\n%s", want, f.notes[0])
		}
	}
	// progress over 0.1s goes on stderr, and stderr is not where the answer lives
	if !strings.Contains(stderr, "Emma") {
		t.Errorf("ask must say what it is doing on stderr, got %q", stderr)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "2026-09-18T1200Z-ask-abcdef") {
		t.Fatalf("the ask id was not recorded back on the unit:\n%s", raw)
	}
}

func TestAskKindReadIsTheReadCardForAFriend(t *testing.T) {
	p := unitsFile(t, askUnits)
	f := &fakeSender{id: "id1"}
	code, stdout, stderr := runAsk(t, f,
		"--owner", "Emma", "--unit", "u1", "--units", p, "--kind", "read",
		"--deadline", "2026-09-18T20:00:00Z", "--bus", "/bus", "--as", "Rowan",
		"--remote", "origin", "--branch", "main", "--reply-branch", "rowan/lane-friends", "--now", "2026-09-18T12:00:00Z")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "kind=read") {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(f.notes[0], "Subject: ask read: ") {
		t.Fatalf("the read ask's subject is wrong:\n%s", f.notes[0])
	}
}

func TestAskRefusesWhatItCannotRun(t *testing.T) {
	p := unitsFile(t, askUnits)
	full := []string{"--owner", "Emma", "--unit", "u1", "--units", p,
		"--deadline", "2026-09-18T20:00:00Z", "--bus", "/bus", "--as", "Rowan",
		"--remote", "origin", "--branch", "main", "--reply-branch", "rowan/lane-friends", "--now", "2026-09-18T12:00:00Z"}
	drop := func(flag string) []string {
		out := []string{}
		for i := 0; i < len(full); i++ {
			if full[i] == flag {
				i++
				continue
			}
			out = append(out, full[i])
		}
		return out
	}
	for _, flag := range []string{"--owner", "--unit", "--units", "--deadline", "--bus", "--as"} {
		f := &fakeSender{id: "x"}
		code, _, stderr := runAsk(t, f, drop(flag)...)
		if code != 2 {
			t.Errorf("a missing %s must be exit 2, got %d", flag, code)
		}
		if !strings.Contains(stderr, strings.TrimPrefix(flag, "--")) {
			t.Errorf("the refusal for %s must name it, got %q", flag, stderr)
		}
		if len(f.notes) != 0 {
			t.Errorf("a refused ask must send nothing, %s sent %d", flag, len(f.notes))
		}
	}
	// an unknown unit, an unknown kind and a deadline already past are all refusals
	for _, bad := range [][]string{
		append(drop("--unit"), "--unit", "nope"),
		append(drop("--kind"), "--kind", "sideways"),
		append(drop("--deadline"), "--deadline", "2026-09-18T11:00:00Z"),
		append(drop("--deadline"), "--deadline", "tomorrow"),
	} {
		f := &fakeSender{id: "x"}
		code, _, stderr := runAsk(t, f, bad...)
		if code != 2 {
			t.Errorf("%v must be exit 2, got %d (stderr %q)", bad, code, stderr)
		}
		if len(f.notes) != 0 {
			t.Errorf("%v sent a note it should have refused", bad)
		}
	}
}

func TestAskDoesNotRecordWhenTheSendFailed(t *testing.T) {
	p := unitsFile(t, askUnits)
	f := &fakeSender{err: errSendRefused}
	code, _, stderr := runAsk(t, f,
		"--owner", "Emma", "--unit", "u1", "--units", p,
		"--deadline", "2026-09-18T20:00:00Z", "--bus", "/bus", "--as", "Rowan",
		"--remote", "origin", "--branch", "main", "--reply-branch", "rowan/lane-friends", "--now", "2026-09-18T12:00:00Z")
	if code != 1 {
		t.Fatalf("a send that failed is exit 1, got %d", code)
	}
	if !strings.HasPrefix(stderr[strings.LastIndex(stderr, "ASK FAIL"):], "ASK FAIL") {
		t.Fatalf("stderr must end with an ASK FAIL line, got %q", stderr)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "\"asks\"") {
		t.Fatalf("nothing may be recorded when the note did not land:\n%s", raw)
	}
}

func TestAsksListsOpenAsksWithAgeAndDeadlineAndFlagsOverdue(t *testing.T) {
	p := unitsFile(t, `{"units":[
      {"id":"u1","title":"t","asks":[
        {"id":"a1","owner":"Emma","kind":"work","unit":"u1","sent":"2026-09-18T11:30:00Z","deadline":"2026-09-18T13:00:00Z","by":"Rowan"},
        {"id":"a2","owner":"Stella","kind":"read","unit":"u1","sent":"2026-09-18T09:00:00Z","deadline":"2026-09-18T11:00:00Z","by":"Rowan"},
        {"id":"a3","owner":"Emma","kind":"work","unit":"u1","sent":"2026-09-18T11:00:00Z","deadline":"2026-09-18T13:00:00Z","by":"Rowan","answered":true}
      ]}]}`)
	var stdout, stderr bytes.Buffer
	code := cmdAsks([]string{"--units", p, "--now", "2026-09-18T12:00:00Z"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("asks exit = %d, stderr: %s", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want two rows and one ASKS line, got:\n%s", stdout.String())
	}
	// oldest first, so the overdue one leads
	if !strings.Contains(lines[0], "id=a2") || !strings.Contains(lines[0], "state=overdue") {
		t.Errorf("row 0 = %q", lines[0])
	}
	if !strings.Contains(lines[1], "id=a1") || !strings.Contains(lines[1], "age=30m") || !strings.Contains(lines[1], "state=open") {
		t.Errorf("row 1 = %q", lines[1])
	}
	if lines[2] != "ASKS n=2 open=1 overdue=1" {
		t.Errorf("the count line = %q", lines[2])
	}
}

func TestAsksFiltersByOwnerAndBoundsItsOutput(t *testing.T) {
	p := unitsFile(t, `{"units":[
      {"id":"u1","title":"t","asks":[
        {"id":"a1","owner":"Emma","kind":"work","unit":"u1","sent":"2026-09-18T11:30:00Z","deadline":"2026-09-18T13:00:00Z"},
        {"id":"a2","owner":"Stella","kind":"read","unit":"u1","sent":"2026-09-18T11:00:00Z","deadline":"2026-09-18T13:00:00Z"}
      ]}]}`)
	var stdout, stderr bytes.Buffer
	if code := cmdAsks([]string{"--units", p, "--now", "2026-09-18T12:00:00Z", "--owner", "Stella"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "id=a1") || !strings.Contains(out, "id=a2") {
		t.Fatalf("--owner did not filter: %q", out)
	}

	stdout.Reset()
	if code := cmdAsks([]string{"--units", p, "--now", "2026-09-18T12:00:00Z", "--max", "1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "MORE") {
		t.Fatalf("--max must print one MORE line rather than every row: %q", stdout.String())
	}
}

func TestAsksRefusesWithoutUnits(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cmdAsks(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "units") {
		t.Fatalf("the refusal must name --units: %q", stderr.String())
	}
}

func TestHelpNamesTheAskAndAsksVerbs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("help exit %d", code)
	}
	for _, want := range []string{"nova-work ask ", "nova-work asks "} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("help does not list %q", want)
		}
	}
}

func TestAskVerbsAreReachableFromTheTopLevel(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"asks"}, &stdout, &stderr, ""); code != 2 {
		t.Fatalf("`nova-work asks` with no flags must be exit 2, got %d", code)
	}
	if strings.Contains(stderr.String(), "unknown verb") {
		t.Fatalf("asks is not wired into the top level: %q", stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"ask"}, &stdout, &stderr, ""); code != 2 {
		t.Fatalf("`nova-work ask` with no flags must be exit 2, got %d", code)
	}
	if strings.Contains(stderr.String(), "unknown verb") {
		t.Fatalf("ask is not wired into the top level: %q", stderr.String())
	}
}

// The deadline is a fact about the world and never a default: the tool refuses to guess one.
func TestAskHasNoDefaultDeadline(t *testing.T) {
	src, err := os.ReadFile("ask.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `"deadline", ""`) {
		t.Fatal("--deadline must have an empty default")
	}
	_ = time.Now
}

// ---------------------------------------------------------------- the dogfood edges

func TestAskReadsTheLispWorkSetAndRecordsNowhereWithoutARecordFile(t *testing.T) {
	f := &fakeSender{id: "rowan-93d3cbffc3d0"}
	code, stdout, stderr := runAsk(t, f,
		"--owner", "Stella", "--unit", "pull:queue",
		"--units", "../../internal/friends/testdata/work-set.lisp",
		"--bus", "/bus", "--as", "Rowan", "--now", "2026-09-18T12:00:00Z")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr)
	}
	// edge 3: the unit's OWN :deadline stands in for --deadline
	if !strings.Contains(stdout, "deadline=2026-09-18T18:00:00Z") {
		t.Errorf("the unit's own deadline must be used when --deadline is absent: %q", stdout)
	}
	if !strings.Contains(stdout, "lane=work") {
		t.Errorf("the ASK OK line must carry the lane: %q", stdout)
	}
	// edge 2: a note, not a refusal, when the unit carries no :acceptance
	if !strings.Contains(stderr, "acceptance") {
		t.Errorf("one note must say the unit carries no acceptance: %q", stderr)
	}
	if !strings.Contains(f.notes[0], "Acceptance: as titled") {
		t.Errorf("the note:\n%s", f.notes[0])
	}
	// a Lisp work set is not rewritten: the bus note is the record
	if !strings.Contains(stderr, "the bus note is the record") {
		t.Errorf("ask must say where the record went: %q", stderr)
	}
}

func TestAskRecordsALispWorkSetsAskIntoTheRecordFile(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "asks.json")
	f := &fakeSender{id: "rowan-93d3cbffc3d0"}
	code, _, stderr := runAsk(t, f,
		"--owner", "Stella", "--unit", "pull:queue",
		"--units", "../../internal/friends/testdata/work-set.lisp", "--record", rec,
		"--bus", "/bus", "--as", "Rowan", "--now", "2026-09-18T12:00:00Z")
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr)
	}
	raw, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "rowan-93d3cbffc3d0") || !strings.Contains(string(raw), `"lane": "work"`) {
		t.Fatalf("the record file does not carry the ask and its lane:\n%s", raw)
	}
}

func TestAskStillRefusesWhenNeitherTheFlagNorTheUnitHasADeadline(t *testing.T) {
	f := &fakeSender{id: "x"}
	code, _, stderr := runAsk(t, f,
		"--owner", "Emma", "--unit", "verb:hygiene",
		"--units", "../../internal/friends/testdata/work-set.lisp",
		"--bus", "/bus", "--as", "Rowan", "--now", "2026-09-18T12:00:00Z")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr, "deadline") {
		t.Errorf("the refusal must name the deadline: %q", stderr)
	}
	if len(f.notes) != 0 {
		t.Error("nothing may be sent without a deadline")
	}
}

func TestAsksReadsTheBusWhenGivenOne(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cmdAsks([]string{"--bus", "../../internal/friends/testdata/bus", "--as", "Ada",
		"--owner", "Bo", "--now", "2026-09-18T12:00:00Z"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "id=ada-bbbbbbbbbbbb") || !strings.Contains(out, "id=ada-aaaaaaaaaaaa") {
		t.Fatalf("the bus's own asks are missing:\n%s", out)
	}
	if strings.Contains(out, "id=ada-cccccccccccc") {
		t.Fatalf("an answered ask is not open:\n%s", out)
	}
	if !strings.Contains(out, "ASKS n=2 open=1 overdue=1") {
		t.Fatalf("the count line is wrong:\n%s", out)
	}
	if !strings.Contains(out, "src=bus") {
		t.Fatalf("a row must say where it was read: %s", out)
	}
}

func TestAsksTakesEitherSourceAndRefusesNeither(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cmdAsks([]string{"--now", "2026-09-18T12:00:00Z"}, &stdout, &stderr); code != 2 {
		t.Fatalf("neither --units nor --bus must be exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "units") || !strings.Contains(stderr.String(), "bus") {
		t.Fatalf("the refusal must name both sources: %q", stderr.String())
	}
	stderr.Reset()
	if code := cmdAsks([]string{"--bus", "../../internal/friends/testdata/bus", "--now", "2026-09-18T12:00:00Z"}, &stdout, &stderr); code != 2 {
		t.Fatalf("--bus without --as must be exit 2, got %d", code)
	}
}

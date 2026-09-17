package main

// The three verbs of class G at the command line: the sequence a friend actually types
// (Glenn 2026-09-15, "we must test": every tool card adds a round trip of the real verb
// sequence). Nothing here reaches the network: run with no seams wired starts no child, and
// triage and status read files.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func invokePulse(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	exit := run(args, &out, &errs, time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC))
	return exit, out.String(), errs.String()
}

// TestRunOnceIsOneWidthLineAndOneVerdict: the shape of the coordinator's whole reading of a
// tick, and the proof that the verbs behind the seams are the shipped ones. `run` wires
// gate, harvest, sweep, reap, refill and launch itself (the card that made the loop real),
// so a tick here runs all six -- against the fakes on PATH, never the network -- and the
// console still carries one WIDTH line and one verdict, with each verb's own line in
// <queue>/pulse.log where the hand loop wrote it.
func TestRunOnceIsOneWidthLineAndOneVerdict(t *testing.T) {
	specs := fakePATH(t)
	// One green ci push run for the gate -- the one run it reads (issue #879); the same
	// body is harmless to the refill's listings, which find no number they are allowed
	// to touch.
	fakeTool(t, specs, "gh", fakeSpec{Default: fakeRule{
		Stdout: `[{"databaseId":77,"status":"completed","conclusion":"success","headSha":"0123456789abcdef","workflowName":"ci","event":"push"}]`,
	}})
	fakeTool(t, specs, "nova-swarm", fakeSpec{Default: fakeRule{Stdout: "BATCH OK\n"}})

	queue, root := t.TempDir(), t.TempDir()
	write(t, filepath.Join(queue, "pulse.toml"), "[slots]\nstudio = 2\nspace = 0\nlocal = 0\n")
	write(t, filepath.Join(queue, "pending", "card-5.md"),
		"RESULT: CARD-5 nova-tools #777 fixed with its red test first: a card waiting\nSTEP 1. do the thing\n")

	exit, out, errs := invokePulse(t, "run", "--queue", queue, "--roots", root,
		"--repo", "mas-bandwidth/nova-tools", "--branch", "dev", "--once",
		"--temp-glob", filepath.Join(t.TempDir(), "*swarmtest*"))
	if exit != 0 {
		t.Fatalf("exit %d: %s%s", exit, out, errs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout is %d lines, want 2:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "PULSE WIDTH tick=1 ") || !strings.HasPrefix(lines[1], "RUN OK ticks=1 ") {
		t.Errorf("the two lines are\n%s", out)
	}
	// Every step is wired: a seam nobody wired names itself on stderr, once, and none does.
	if strings.Contains(errs, "seam=") {
		t.Errorf("a step of the tick is still a stub:\n%s", errs)
	}
	log, err := os.ReadFile(filepath.Join(queue, "pulse.log"))
	if err != nil {
		t.Fatalf("the tick wrote no log: %v", err)
	}
	for _, want := range []string{"GATE GREEN", "SWEEP repo=", "REAP roots=", "REFILL ", "PULSE OK"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("pulse.log does not carry %q:\n%s", want, log)
		}
	}
	// The card went to the bench, and it is in exactly one place.
	if got := strings.Contains(string(log), "PULSE OK"); !got {
		t.Errorf("no batch was admitted:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(queue, "launched", "card-5.md")); err != nil {
		t.Errorf("the waiting card did not launch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(queue, "pending", "card-5.md")); err == nil {
		t.Errorf("the launched card is still pending: a card is in one place")
	}
}

// write is a file and its directories, for a fixture.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRunRefusesWithoutHoursOrQueue: --hours is required unless --once says one tick.
func TestRunRefusesWithoutHoursOrQueue(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "--roots", "x", "--hours", "1"}, "--queue is required"},
		{[]string{"run", "--queue", "x", "--hours", "1"}, "--roots is required"},
		{[]string{"run", "--queue", "x", "--roots", "y"}, "--hours is required"},
		{[]string{"run", "--queue", "x", "--roots", "y", "--once", "--tick", "0"}, "--tick wants"},
	} {
		exit, _, errs := invokePulse(t, c.args...)
		if exit != 2 {
			t.Errorf("%v exited %d, want 2", c.args, exit)
		}
		if !strings.Contains(errs, c.want) {
			t.Errorf("%v: stderr does not say %q:\n%s", c.args, c.want, errs)
		}
	}
}

// TestTriageVerbCutsAPacketAndStatusReadsTheDay is the round trip: a queue with a rule
// table and an undecided case, a packet cut for the text route, and a one-line status a
// fresh window could start from.
func TestTriageVerbCutsAPacketAndStatusReadsTheDay(t *testing.T) {
	queue := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(queue, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("RULES.tsv", "nosha\tline1-has-no-sha\tRETRY\tpending\n")
	write("UNDECIDED/nosha.txt", "RESULT card-892\nABSTAIN reason=nosha\nREFUSED: line 1 carries no sha\n")
	write("PITSTOP", "pit stop 3: class G\n")

	card := filepath.Join(t.TempDir(), "triage.md")
	exit, out, errs := invokePulse(t, "triage", "--case", "nosha", "--queue", queue, "--out", card, "--ref", "card-892")
	if exit != 0 {
		t.Fatalf("triage exit %d: %s%s", exit, out, errs)
	}
	if !strings.HasPrefix(out, "TRIAGE OK case=nosha") || strings.Count(out, "\n") != 1 {
		t.Errorf("triage printed\n%s", out)
	}
	body, err := os.ReadFile(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"line1-has-no-sha", "line 1 carries no sha", "TRIAGE nosha <verdict>"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the packet does not carry %q:\n%s", want, body)
		}
	}

	exit, out, errs = invokePulse(t, "status", "--queue", queue, "--roots", t.TempDir(), "--oneline", "--day", "2026-09-16")
	if exit != 0 {
		t.Fatalf("status exit %d: %s%s", exit, out, errs)
	}
	line := strings.TrimSuffix(out, "\n")
	if strings.Contains(line, "\n") || len(line) >= 400 {
		t.Errorf("status --oneline is %d bytes over %d lines:\n%s", len(line), strings.Count(out, "\n"), out)
	}
	if !strings.Contains(line, "pitstop=pit") {
		t.Errorf("the line does not carry the open pit stop:\n%s", line)
	}

	// Without --oneline the eight-line report is exactly as it was.
	exit, out, _ = invokePulse(t, "status", "--queue", queue, "--roots", t.TempDir(), "--day", "2026-09-16")
	if exit != 0 || !strings.Contains(out, "STATUS QUEUE ") {
		t.Errorf("the default status changed: exit %d\n%s", exit, out)
	}
}

// TestHelpNamesTheThreeVerbsOfClassG: a verb a reader cannot find is a verb behind the
// source.
func TestHelpNamesTheThreeVerbsOfClassG(t *testing.T) {
	exit, out, _ := invokePulse(t, "help")
	if exit != 0 {
		t.Fatalf("help exit %d", exit)
	}
	for _, want := range []string{
		"nova-pulse run     --queue <dir> --roots <dirs> --repo <o/n> --branch <b> --hours <n>",
		"nova-pulse triage  --case <kind> --queue <dir> --out <card>",
		"[--oneline]",
		"TRIAGE <case> <verdict> <rule-row-or-NEW>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not carry %q", want)
		}
	}
}

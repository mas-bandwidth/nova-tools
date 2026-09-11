package main

import (
	"strings"
	"testing"
)

// The lessons that are code, each with the hurt it came from.

// Lessons 75, 76 and 172: A DEADLINE PINNED FROM ONE SIDE ONLY IS NOT PINNED. --timeout
// and --hours had a floor and no ceiling, and --loop had no floor: `--timeout 86400` is a
// tool that has stopped saying anything, `--hours 1000` is a loop nobody outlives, and
// `--loop 1ms` is a pass per millisecond against a host with a rate limit.
func TestEveryDeadlineIsPinnedFromBothSides(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "--lane", l.lane, "--timeout", "100000"}, "--timeout"},
		{[]string{"run", "--lane", l.lane, "--once", "--timeout", "100000"}, "--timeout"},
		{[]string{"run", "--lane", l.lane, "--loop", "1ms", "--hours", "1"}, "--loop"},
		{[]string{"run", "--lane", l.lane, "--loop", "5m", "--hours", "1000"}, "--hours"},
	} {
		t.Run(strings.Join(c.args[len(c.args)-2:], " "), func(t *testing.T) {
			exit, stdout, stderr := l.run(c.args...)
			if exit != 2 {
				t.Fatalf("a bound with no ceiling is a bound nobody set: exit %d\n%s\n%s", exit, stdout, stderr)
			}
			contains(t, stderr, c.want)
		})
	}
}

// Lesson 48: a lane's --base is stored and later handed to git as an argument. A value
// starting with a dash is a flag to whatever reads it, and a value holding a space or a
// control character is a refname git will refuse later, far from the person who typed it.
func TestARefNameIsCheckedWhereItIsTyped(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	for _, c := range []struct{ flag, value string }{
		{"--base", "--upload-pack=touch /evil"},
		{"--base", "a branch with spaces"},
		{"--lane-branch", "-x"},
	} {
		t.Run(c.flag+c.value, func(t *testing.T) {
			args := []string{"init", "--lane", l.dir + "/l", "--repo", "o/n", "--base", "main", "--lane-branch", "nova-merge/lane"}
			for i := range args {
				if args[i] == c.flag {
					args[i+1] = c.value
				}
			}
			exit, stdout, stderr := l.run(args...)
			if exit != 2 {
				t.Fatalf("a refname is checked where it is typed: exit %d\n%s\n%s", exit, stdout, stderr)
			}
			contains(t, stderr, c.flag)
		})
	}
}

// Lessons 3, 4 and 33, and the spec in its own words: "`status`, `dry-run` and `packet`
// REPORT and exit 0 whatever the lane holds." dry-run exited 1 on a malformed record, so a
// caller could not tell a report from a wall.
func TestDryRunReportsAndExitsZeroWhateverTheLaneHolds(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	l.putRecord("reads/README.json", "{not a record\n")
	exit, stdout, stderr := l.run("dry-run", "--lane", l.lane)
	if exit != 0 {
		t.Fatalf("dry-run REPORTS: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "malformed_record")
	// And it still prints its closing line, so a reader never has to work out whether
	// the report ended or died.
	contains(t, stdout, "DRY OK")
}

// Lesson 87: a pass that refused returned before the walk, so the closing line with the
// counts never printed on the one kind of pass a reader most wants counted.
func TestARefusedPassStillPrintsItsClosingLine(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", false)
	l.host.SetChecks(l.baseSHA(), 0, 0, "tables-java-versioning")
	exit, stdout, stderr := l.run("run", "--lane", l.lane, "--once")
	if exit != 1 {
		t.Fatalf("a red base stops the lane: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "RUN STOPPED base=main")
	// The closing line is printed on EVERY pass, so a reader of the log never has to
	// work out whether a pass ended or died.
	contains(t, stdout, "RUN OK lane=1 merged=0 dropped=0 blocked=0 waiting=0")
	contains(t, stdout, "RUN NOTE")
}

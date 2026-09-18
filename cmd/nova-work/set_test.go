package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setFixture copies one of internal/worklang's fixtures -- cut verbatim from
// work/pitstop-2026-09-17.lisp -- into a temp dir, so the command's tests read the
// same bytes the language's tests do rather than a second copy that can drift.
func setFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "worklang", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func write(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The two exit codes say different things and are never blurred: exit 1 is a file
// read whole whose content is wrong, with one SET line per finding and the summary
// still printed.
func TestSetCheckFindingsExitOneAndPrintEveryOne(t *testing.T) {
	code, stdout, stderr := runCLI(t, "set", "check", "--file", setFixture(t, "pitstop-cut.lisp"))
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (findings)\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Errorf("findings are the verb's answer, not a refusal, yet stderr = %q", stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("printed %d lines, want 2 findings and the summary:\n%s", len(lines), stdout)
	}
	for _, want := range []string{
		"SET NEEDS unit=stack:redis-live need=repair:spec-1208-1209",
		"SET NEEDS unit=batch:verb need=merge:simulate",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not carry %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(lines[len(lines)-1], "SET OK units=14") {
		t.Errorf("the summary line is %q, want it last and naming the units", lines[len(lines)-1])
	}
	// Every finding carries the byte it lives at and a remedy: a checker that only
	// said something was wrong would leave the reader to find it.
	for _, line := range lines[:len(lines)-1] {
		if !strings.Contains(line, " at=") || !strings.Contains(line, " remedy=") {
			t.Errorf("finding %q carries no byte or no remedy", line)
		}
	}
}

// A clean set is exit 0 and one line.
func TestSetCheckCleanSetIsOneLine(t *testing.T) {
	path := write(t, "clean.lisp", `(work-set "clean" :units (
	  (unit "a" :status "closed" :title "landed")
	  (unit "b" :needs ("a") :owner "Emma" :lane "work" :deadline "2026-09-19T12:00Z" :title "ready")
	  (unit "c" :needs ("b") :title "blocked")))`)
	code, stdout, stderr := runCLI(t, "set", "check", "--file", path)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "SET OK units=3 ready=1 blocked=1 owned=1" {
		t.Errorf("summary = %q", got)
	}
}

// --ready prints the mechanical ready set, and --done settles a need from outside
// the document.
func TestSetCheckReadyListsTheReadySet(t *testing.T) {
	path := write(t, "chain.lisp", `(work-set "chain" :units (
	  (unit "a" :title "needs nothing")
	  (unit "b" :needs ("a") :owner "Stella" :lane "work" :title "needs a")
	  (unit "c" :needs ("b") :title "needs b")))`)
	code, stdout, _ := runCLI(t, "set", "check", "--file", path, "--ready")
	if code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, stdout)
	}
	if !strings.Contains(stdout, "SET READY unit=a owner=- lane=- deadline=-") {
		t.Errorf("ready set does not hold a with its empty fields spelled `-`:\n%s", stdout)
	}
	if strings.Contains(stdout, "unit=b") {
		t.Errorf("b needs a, which is not done, yet it is ready:\n%s", stdout)
	}
	code, stdout, _ = runCLI(t, "set", "check", "--file", path, "--ready", "--done", "a")
	if code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, stdout)
	}
	if !strings.Contains(stdout, "SET READY unit=b owner=Stella lane=work") {
		t.Errorf("with a done, b is not in the ready set:\n%s", stdout)
	}
	if !strings.Contains(stdout, "SET OK units=3 ready=1 blocked=1 owned=1") {
		t.Errorf("the summary did not move with the ready set:\n%s", stdout)
	}
}

// --minds reads either JSON registry or a plain list, and the shape is READ rather
// than guessed at from the file's name.
func TestSetCheckMindsReadsEitherRegistry(t *testing.T) {
	set := write(t, "owners.lisp", `(work-set "o" :units (
	  (unit "a" :owner "Emma" :title "a known mind")
	  (unit "b" :owner "Nobody" :title "a mind nothing names")))`)
	ladder := write(t, "registry.json", `{"minds":[{"name":"emma","height":3},{"name":"flash"}]}`)
	roster := write(t, "participants.json", `{"participants":[{"name":"Emma"},{"name":"Rowan"}]}`)
	list := write(t, "minds.txt", "# the minds\nEmma\tthe code lane\nFlash\n")
	for _, minds := range []string{ladder, roster, list} {
		code, stdout, stderr := runCLI(t, "set", "check", "--file", set, "--minds", minds)
		if code != 1 {
			t.Fatalf("--minds %s exit = %d, want 1\nstdout: %s\nstderr: %s", minds, code, stdout, stderr)
		}
		if !strings.Contains(stdout, "SET OWNER unit=b owner=Nobody") {
			t.Errorf("--minds %s did not report the unknown owner:\n%s", minds, stdout)
		}
		if strings.Contains(stdout, "unit=a") {
			t.Errorf("--minds %s reported a known owner (the match folds case):\n%s", minds, stdout)
		}
	}
}

// --lanes is the lanes file SPEC-WORKLANG already fixes: <name>\t<path prefixes>.
func TestSetCheckLanesUsesTheLanesTable(t *testing.T) {
	set := write(t, "lanes.lisp", `(work-set "l" :units (
	  (unit "a" :lane "merge" :title "a lane the table names")
	  (unit "b" :lane "nowhere" :title "a lane it does not")))`)
	lanes := write(t, "lanes.tsv", "# lane\tprefixes\nmerge\tinternal/merge\nwork\tinternal/worklang\n")
	code, stdout, stderr := runCLI(t, "set", "check", "--file", set, "--lanes", lanes)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "SET LANE unit=b lane=nowhere") {
		t.Errorf("the unknown lane was not reported:\n%s", stdout)
	}
	if strings.Contains(stdout, "unit=a") {
		t.Errorf("a lane the table names was reported:\n%s", stdout)
	}
}

// Exit 2 is what could not run at all: a missing flag, a missing file, a file that
// is not a work set, a registry that is not one. Each is one line on stderr naming
// the door, and none of them writes to stdout.
func TestSetCheckRefusalsExitTwo(t *testing.T) {
	good := write(t, "good.lisp", `(work-set "g" :units ((unit "a" :title "t")))`)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no sub-verb", []string{"set"}, "set check --file"},
		{"unknown sub-verb", []string{"set", "walk"}, "set check --file"},
		{"no --file", []string{"set", "check"}, "--file is required"},
		// The refusal names the PATH, not the operating system's wording for what
		// went wrong with it: Windows says "The system cannot find the file
		// specified" where Unix says "no such file", and a test that pinned either
		// spelling would be red on the other platform. The windows leg found this.
		{"a missing file", []string{"set", "check", "--file", filepath.Join(t.TempDir(), "nope.lisp")}, "nope.lisp"},
		{"an unexpected argument", []string{"set", "check", "--file", good, "extra"}, "unexpected argument"},
		{"a plan is not a work set", []string{"set", "check", "--file", write(t, "p.work", `(:plan :version 1)`)}, "not a work set"},
		{"an empty registry", []string{"set", "check", "--file", good, "--minds", write(t, "empty.json", `{"comment":"no rows"}`)}, "names no minds"},
		{"an empty lanes file", []string{"set", "check", "--file", good, "--lanes", write(t, "empty.tsv", "# only a comment\n")}, "names no lanes"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, c.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if stdout != "" {
				t.Errorf("a refusal wrote to stdout: %q", stdout)
			}
			if !strings.Contains(stderr, c.want) {
				t.Errorf("refusal %q does not name %q", stderr, c.want)
			}
			if !strings.Contains(stderr, "run: nova-work help") {
				t.Errorf("refusal %q names no door", stderr)
			}
			if lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n"); len(lines) != 1 {
				t.Errorf("a refusal printed %d lines, want 1: %q", len(lines), stderr)
			}
		})
	}
}

// The bounds still hold on this form: a work set past --max-bytes is refused before
// a byte of it is parsed, and a dispatch macro is refused at the byte that owes it.
func TestSetCheckKeepsTheReadersBounds(t *testing.T) {
	good := write(t, "good.lisp", `(work-set "g" :units ((unit "a" :title "t")))`)
	code, _, stderr := runCLI(t, "set", "check", "--file", good, "--max-bytes", "8")
	if code != 2 || !strings.Contains(stderr, "--max-bytes") {
		t.Errorf("past the byte bound: exit %d, stderr %q", code, stderr)
	}
	evil := write(t, "evil.lisp", "(work-set \"g\" :units ((unit \"a\" :title #.(rm -rf))))")
	code, _, stderr = runCLI(t, "set", "check", "--file", evil)
	if code != 2 || !strings.Contains(stderr, "dispatch macro") {
		t.Errorf("a dispatch macro: exit %d, stderr %q", code, stderr)
	}
}

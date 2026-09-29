package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// THE RED TESTS FOR `nova-swarm doctor` (2026-09-17 shadowed-binary incident).
//
// ~/go/bin held an old nova-swarm for 25 minutes while ~/.local/bin held the rebuilt one,
// because PATH put ~/go/bin first; every card launched in that window ran the stale binary
// with a private build cache and nothing said so. The verb this file turns red is the one
// that reads the stamp of the nova-swarm first on PATH and the stamp of the one at
// ~/.local/bin and REFUSES when the two disagree.
//
// NOTHING HERE EXECUTES A DISCOVERED PATH. The version reader and the PATH resolver are
// package vars a test replaces, so a unit test hands over two fixed stamps and never runs a
// binary it went looking for.

// doctorFake is the environment a test hands the doctor: where nova-swarm is looked up on
// PATH, where home is, and what a binary at a path reports for `version`. It is a value the
// test owns, so the tests that use it run in parallel.
func doctorFake(lines map[string]string, lookPath func(string) (string, error), home string) doctorEnv {
	return doctorEnv{
		lookPath: lookPath,
		homeDir:  func() (string, error) { return home, nil },
		read: func(path string) (string, error) {
			if line, ok := lines[path]; ok {
				return line, nil
			}
			return "", errDoctorNotFound
		},
	}
}

// doctorFakeGlobal swaps the three package seams for the life of one test, for the one test
// that reaches the doctor through the dispatcher and so cannot be handed an environment.
func doctorFakeGlobal(t *testing.T, lines map[string]string, lookPath func(string) (string, error), home string) {
	t.Helper()
	env := doctorFake(lines, lookPath, home)
	savedLookPath, savedHome, savedRead := doctorLookPath, doctorHomeDir, doctorReadVersion
	doctorLookPath, doctorHomeDir, doctorReadVersion = env.lookPath, env.homeDir, env.read
	t.Cleanup(func() {
		doctorLookPath, doctorHomeDir, doctorReadVersion = savedLookPath, savedHome, savedRead
	})
}

func noPath(name string) (string, error) { return "", errors.New("not found: " + name) }

func doctorLocal(home string) string { return filepath.Join(home, ".local", "bin", "nova-swarm") }

// A pair of stamps that differ, the shape the incident produced: same tool, same platform,
// a different build identity in field two.
const (
	doctorStaleLine   = "nova-swarm 20260917010203-aaaaaaaaaaaa linux/amd64 go1.26.5"
	doctorRebuiltLine = "nova-swarm 20260917174700-bbbbbbbbbbbb linux/amd64 go1.26.5"
)

// MATCHING STAMPS ARE OK, one line, exit 0. This is the answer on a healthy bench and the
// whole point of the OK line: a launch's own refusal has to be able to say it looked.
func TestDoctorOKWhenBothStampsMatch(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{
			"/opt/go/bin/nova-swarm":         doctorRebuiltLine,
			"/home/me/.local/bin/nova-swarm": doctorRebuiltLine,
		},
		noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", "/opt/go/bin/nova-swarm", "--local", "/home/me/.local/bin/nova-swarm"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("a matching pair wrote to stderr: %q", errOut.String())
	}
	if want := "DOCTOR OK stamp=" + doctorRebuiltLine + "\n"; out.String() != want {
		t.Errorf("OK line:\n got %q\nwant %q", out.String(), want)
	}
}

// A DIFFERENT STAMP IS A REFUSAL, exit 2, with BOTH full version lines printed and one
// remedy. The stale stamp must not be truncated to forty characters the way the operator
// script did: the revision is the part a person compares, and it is at the end.
func TestDoctorRefusesWhenThePATHBinaryIsShadowed(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{
			"/opt/go/bin/nova-swarm":         doctorStaleLine,
			"/home/me/.local/bin/nova-swarm": doctorRebuiltLine,
		},
		noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", "/opt/go/bin/nova-swarm", "--local", "/home/me/.local/bin/nova-swarm"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	both := out.String() + errOut.String()
	if !strings.Contains(both, doctorStaleLine) {
		t.Errorf("the PATH binary's full line is not printed:\n%s", both)
	}
	if !strings.Contains(both, doctorRebuiltLine) {
		t.Errorf("the ~/.local/bin binary's full line is not printed:\n%s", both)
	}
	if !strings.Contains(both, "copy") || !strings.Contains(both, ".local/bin") {
		t.Errorf("the refusal names no fix:\n%s", both)
	}
}

// THE PATH RESOLVER IS THE SEAM, and this test drives it: `--path` is left off, the fake
// resolver answers the PATH question, and the fake reader answers both stamps. Nothing is
// executed.
func TestDoctorResolvesNovaSwarmOnPATH(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{
			"/usr/local/bin/nova-swarm": doctorRebuiltLine,
			doctorLocal("/home/me"):     doctorRebuiltLine,
		},
		func(name string) (string, error) {
			if name != "nova-swarm" {
				t.Errorf("the resolver was asked for %q, want nova-swarm", name)
			}
			return "/usr/local/bin/nova-swarm", nil
		}, "/home/me")

	var out, errOut bytes.Buffer
	if code := env.cmdDoctor(nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "DOCTOR OK stamp=") {
		t.Errorf("not the OK line: %q", out.String())
	}
}

// PATH's nova-swarm IS ~/.local/bin/nova-swarm: there is no second binary and so nothing to
// compare. This is the healthy machine whose PATH already prefers the rebuilt install.
func TestDoctorOKWhenPATHResolvesToTheLocalBinary(t *testing.T) {
	t.Parallel()
	env := doctorFake(map[string]string{doctorLocal("/home/me"): doctorRebuiltLine}, noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", doctorLocal("/home/me"), "--local", doctorLocal("/home/me")}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("wrote to stderr: %q", errOut.String())
	}
	if want := "DOCTOR OK stamp=" + doctorRebuiltLine + "\n"; out.String() != want {
		t.Errorf("OK line:\n got %q\nwant %q", out.String(), want)
	}
}

// The one binary a launch would run must answer even when there is no second one to compare
// it with.
func TestDoctorRefusesWhenTheOneBinaryCannotBeRead(t *testing.T) {
	t.Parallel()
	env := doctorFake(map[string]string{}, noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", doctorLocal("/home/me"), "--local", doctorLocal("/home/me")}, &out, &errOut)
	if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "DOCTOR UNREADABLE") {
		t.Fatalf("exit %d, want 2 with DOCTOR UNREADABLE on stderr\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
}

// NO ~/.local/bin COPY means there is nothing that could be shadowed: the guard cannot
// invent a mismatch, so it is OK and reports the stamp it did read.
func TestDoctorOKWhenTheLocalBinaryIsAbsent(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{"/opt/go/bin/nova-swarm": doctorStaleLine},
		noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", "/opt/go/bin/nova-swarm"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	if want := "DOCTOR OK stamp=" + doctorStaleLine + "\n"; out.String() != want {
		t.Errorf("OK line:\n got %q\nwant %q", out.String(), want)
	}
}

// THE LAUNCH SEAM. `batch` and `native` are the verbs that start a card, and the preflight is
// what main calls before the dispatcher: a shadowed pair stops the launch with exit 2
// before anything is spent. A verb that starts nothing is untouched.
func TestPreflightRefusesALaunchUnderAShadowedBinary(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{
			"/opt/go/bin/nova-swarm": doctorStaleLine,
			doctorLocal("/home/me"):  doctorRebuiltLine,
		},
		func(string) (string, error) { return "/opt/go/bin/nova-swarm", nil }, "/home/me")

	var errOut bytes.Buffer
	code, stop := env.preflight([]string{"native", "--dir", "d"}, &errOut)
	if !stop || code != 2 {
		t.Fatalf("preflight(exit=%d, stop=%v), want (2, true)", code, stop)
	}
	both := errOut.String()
	if !strings.Contains(both, doctorStaleLine) || !strings.Contains(both, doctorRebuiltLine) {
		t.Errorf("the preflight prints both stamps:\n%s", both)
	}

	// A flag value spelled -h (like `batch --id -h` or `native --card -h`) is NOT a help request:
	// under a shadowed binary, the preflight must refuse it with exit 2 rather than stand aside.
	errOut.Reset()
	if code, stop := env.preflight([]string{"batch", "--id", "-h"}, &errOut); !stop || code != 2 {
		t.Errorf("batch --id -h: preflight(exit=%d, stop=%v), want (2, true)", code, stop)
	}
	errOut.Reset()
	if code, stop := env.preflight([]string{"native", "--card", "-h"}, &errOut); !stop || code != 2 {
		t.Errorf("native --card -h: preflight(exit=%d, stop=%v), want (2, true)", code, stop)
	}
}

func TestPreflightLeavesNonLaunchVerbsAlone(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{
			"/opt/go/bin/nova-swarm": doctorStaleLine,
			doctorLocal("/home/me"):  doctorRebuiltLine,
		},
		func(string) (string, error) { return "/opt/go/bin/nova-swarm", nil }, "/home/me")

	var errOut bytes.Buffer
	if code, stop := env.preflight([]string{"template", "--name", "read-pr"}, &errOut); stop || code != 0 {
		t.Fatalf("template: preflight(exit=%d, stop=%v), want (0, false)", code, stop)
	}
	if errOut.Len() != 0 {
		t.Errorf("a verb that starts nothing was refused: %q", errOut.String())
	}
}

// The verb is reachable from the dispatcher, and `doctor` is not a launch verb itself, so
// running it does not recurse.
func TestDoctorVerbIsReachableFromTheDispatch(t *testing.T) {
	doctorFakeGlobal(t,
		map[string]string{
			"/opt/go/bin/nova-swarm":         doctorRebuiltLine,
			"/home/me/.local/bin/nova-swarm": doctorRebuiltLine,
		},
		noPath, "/home/me")
	var out, errOut bytes.Buffer
	if code := run([]string{"doctor", "--path", "/opt/go/bin/nova-swarm", "--local", "/home/me/.local/bin/nova-swarm"}, strings.NewReader(""), &out, &errOut, time.Now().UTC()); code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "DOCTOR OK stamp=") {
		t.Errorf("not the OK line: %q", out.String())
	}
}

// A PATH binary that is named and cannot be read is a refusal, not an OK with the other
// binary's stamp: the line names the binary, the cause, and what the other one reported.
func TestDoctorRefusesAnUnreadablePATHBinary(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{"/home/me/.local/bin/nova-swarm": doctorRebuiltLine},
		noPath, "/home/me")
	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", "/opt/go/bin/gone", "--local", "/home/me/.local/bin/nova-swarm"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit %d, want 2\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	got := errOut.String()
	for _, want := range []string{"DOCTOR UNREADABLE", "/opt/go/bin/gone", "not found", doctorRebuiltLine, "by hand"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not contain %q:\n%s", want, got)
		}
	}
	if out.Len() != 0 {
		t.Errorf("a refusal wrote a DOCTOR OK line: %q", out.String())
	}
}

// Preflight stands aside for -h and --help: asking for help is not a launch.
func TestPreflightDoctorStandsAsideForHelp(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	for _, verb := range []string{"batch", "native"} {
		for _, flag := range []string{"-h", "--help", "-help", "--h"} {
			if code, stop := preflightDoctor([]string{verb, flag}, &errOut); stop || code != 0 {
				t.Errorf("%s %s: preflight(exit=%d, stop=%v), want (0, false)", verb, flag, code, stop)
			}
		}
	}
	// Flag value followed by actual help stands aside
	if code, stop := preflightDoctor([]string{"batch", "--id", "-h", "-h"}, &errOut); stop || code != 0 {
		t.Errorf("batch --id -h -h: preflight(exit=%d, stop=%v), want (0, false)", code, stop)
	}
	if code, stop := preflightDoctor([]string{"native", "--card", "-h", "-h"}, &errOut); stop || code != 0 {
		t.Errorf("native --card -h -h: preflight(exit=%d, stop=%v), want (0, false)", code, stop)
	}
}

// The version reader answers under a deadline: a binary that answers is read, and one that
// hangs is killed with the cause "timed out after <deadline>". The deadline is injected, so
// the hung case ends at a fraction of a second.
func TestReadVersionLineWithinKillsAHungBinary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	answers := filepath.Join(dir, "answers")
	if err := testbin.WriteExecutable(answers, []byte("#!/bin/sh\necho 'nova-swarm v1 stamp'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	line, err := readVersionLineWithin(answers, doctorGoodDeadline, time.Second, doctorVersionLineMax)
	if err != nil || line != "nova-swarm v1 stamp" {
		t.Fatalf("a binary that answers: got (%q, %v)", line, err)
	}

	hangs := filepath.Join(dir, "hangs")
	if err := testbin.WriteExecutable(hangs, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	line, err = readVersionLineWithin(hangs, doctorHungDeadline, 50*time.Millisecond, doctorVersionLineMax)
	if err == nil || err.Error() != "timed out after 100ms" || line != "" {
		t.Errorf("a hung binary: got (%q, %v), want an empty line and \"timed out after 100ms\"", line, err)
	}
}

// Production's reader carries a bounded deadline.
func TestDoctorVersionDeadlineIsBounded(t *testing.T) {
	t.Parallel()
	if doctorVersionDeadline <= 0 || doctorVersionDeadline > 30*time.Second {
		t.Errorf("doctorVersionDeadline = %s, want a few seconds", doctorVersionDeadline)
	}
}

// doctorGoodDeadline is the deadline a stub that answers is read under: no test requires a
// child to produce output within a short one, so a loaded machine cannot fail it.
const doctorGoodDeadline = 5 * time.Second

// doctorHungDeadline is the injected deadline a stub that hangs is read under.
const doctorHungDeadline = 100 * time.Millisecond

// doctorGoodGrace is the grace a stub that leaves no child behind is read under: the copy of
// its output gets that long to finish after it exits, so a stall of the machine cannot drop a
// stamp that was printed. doctorPipeGrace is the grace for the one stub that leaves a child
// holding the output pipe, which the reader must wait out.
const (
	doctorGoodGrace = 5 * time.Second
	doctorPipeGrace = 250 * time.Millisecond
)

// doctorStubs lays out a PATH binary and a ~/.local/bin copy as real scripts and returns an
// environment that reads them with the real reader under a short injected deadline. An empty
// script leaves that binary out: an empty pathScript makes PATH answer with a path that is
// not there, and an empty localScript installs no copy.
//
// A stub that answers is read under a generous deadline, so a loaded machine cannot turn a
// good answer into a timeout; a stub that hangs (named in hung, "path" and/or "local") is
// read under the short doctorHungDeadline, so the test does not wait for it.
func doctorStubs(t *testing.T, pathScript, localScript, hung string) (env doctorEnv, pathBin, localBin string) {
	return doctorStubsGrace(t, pathScript, localScript, hung, doctorGoodGrace)
}

// doctorStubsGrace is doctorStubs with the grace named.
func doctorStubsGrace(t *testing.T, pathScript, localScript, hung string, grace time.Duration) (env doctorEnv, pathBin, localBin string) {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	pathBin = filepath.Join(dir, "bin", "nova-swarm")
	localBin = filepath.Join(home, ".local", "bin", "nova-swarm")
	for bin, script := range map[string]string{pathBin: pathScript, localBin: localScript} {
		if script == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := testbin.WriteExecutable(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env = doctorEnv{
		lookPath: func(string) (string, error) { return pathBin, nil },
		homeDir:  func() (string, error) { return home, nil },
		read: func(p string) (string, error) {
			deadline := doctorGoodDeadline
			if (p == pathBin && strings.Contains(hung, "path")) || (p == localBin && strings.Contains(hung, "local")) {
				deadline = doctorHungDeadline
			}
			return readVersionLineWithin(p, deadline, grace, doctorVersionLineMax)
		},
	}
	return env, pathBin, localBin
}

// THE WHOLE PREFLIGHT, with real stubs: a binary the doctor compares that cannot be read
// refuses the launch with one line naming the binary, the cause, what the other one
// reported, and the next action; a stamp printed before the failure is still compared; and
// the doctor verb reports the same and exits non-zero.
func TestPreflightRefusesAnUnreadableBinary(t *testing.T) {
	t.Parallel()
	const good = "echo 'nova-swarm good-stamp'"
	cases := []struct {
		name        string
		pathScript  string
		localScript string
		hung        string // which stubs hang: "path", "local"
		wantExit    int
		contains    []string
		absent      []string
	}{
		{"hang", "exec sleep 30", good, "path", 2,
			[]string{"DOCTOR UNREADABLE", "path=", "timed out after 100ms", "the other binary, ", ", reported stamp=nova-swarm good-stamp;", "by hand"},
			[]string{"DOCTOR DRIFT", "shadows"}},
		{"print then exit 3: the stamp is compared and the exit reported", "echo 'nova-swarm stale-stamp'; exit 3", good, "", 2,
			[]string{"DOCTOR DRIFT path=", "stale-stamp", "shadows", "DOCTOR UNREADABLE", "exited 3"}, nil},
		{"print then exit 3 with the same stamp", "echo 'nova-swarm good-stamp'; exit 3", good, "", 2,
			[]string{"DOCTOR UNREADABLE", "exited 3"}, []string{"DOCTOR DRIFT"}},
		{"printed nothing", "exit 0", good, "", 2,
			[]string{"DOCTOR UNREADABLE", "printed nothing", "stamp=nova-swarm good-stamp"}, nil},
		{"missing where PATH names it", "", good, "", 2,
			[]string{"DOCTOR UNREADABLE", "not found", "stamp=nova-swarm good-stamp"}, nil},
		{"the local copy hangs", good, "exec sleep 30", "local", 2,
			[]string{"DOCTOR UNREADABLE", "local=", "timed out after 100ms", "stamp=nova-swarm good-stamp"}, []string{"DOCTOR DRIFT"}},
		{"both hang: each is named", "exec sleep 30", "exec sleep 30", "path,local", 2,
			[]string{"DOCTOR UNREADABLE reading the version of path=", "DOCTOR UNREADABLE reading the version of local=", "timed out after 100ms", "could not be read either: timed out after 100ms"}, []string{"DOCTOR DRIFT", "reported nothing", "no other binary"}},
		{"the PATH binary hangs and no local copy is installed", "exec sleep 30", "", "path", 2,
			[]string{"DOCTOR UNREADABLE", "timed out after 100ms", "the other binary, ", "is not installed"}, []string{"reported nothing", "no other binary"}},
		{"both answer and agree", good, good, "", 0, nil, []string{"DOCTOR"}},
		{"no local copy is tolerated", good, "", "", 0, nil, []string{"DOCTOR"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			env, _, _ := doctorStubs(t, c.pathScript, c.localScript, c.hung)
			// The launch and the doctor verb read the same stubs at the same time, so a hang
			// is waited for once.
			var out, derr, errOut bytes.Buffer
			var dcode int
			done := make(chan struct{})
			go func() {
				defer close(done)
				dcode = env.cmdDoctor(nil, &out, &derr)
			}()
			code, stop := env.preflight([]string{"batch", "--tokens", "unmetered"}, &errOut)
			<-done
			if want := c.wantExit != 0; stop != want || code != c.wantExit {
				t.Fatalf("preflight(exit=%d, stop=%v), want exit %d\n%s", code, stop, c.wantExit, errOut.String())
			}
			got := errOut.String()
			for _, want := range c.contains {
				if !strings.Contains(got, want) {
					t.Errorf("stderr lacks %q:\n%s", want, got)
				}
			}
			for _, no := range c.absent {
				if strings.Contains(got, no) {
					t.Errorf("stderr holds %q:\n%s", no, got)
				}
			}

			// The doctor verb says the same as a finding, and exits non-zero.
			if dcode != c.wantExit {
				t.Errorf("doctor exit %d, want %d\nstdout: %s\nstderr: %s", dcode, c.wantExit, out.String(), derr.String())
			}
			if c.wantExit != 0 && (out.Len() != 0 || derr.String() != got) {
				t.Errorf("doctor's finding differs from the preflight's\nstdout: %q\nstderr: %q\nwant stderr: %q", out.String(), derr.String(), got)
			}
			if c.wantExit == 0 && !strings.HasPrefix(out.String(), "DOCTOR OK stamp=") {
				t.Errorf("doctor: not the OK line: %q", out.String())
			}
		})
	}
}

// The first-line writer keeps the first line and nothing else, whatever streams through it:
// a hundred megabytes in 64 KiB writes leaves it holding what one line may hold, and every
// write is still accepted in full so the copy never stalls.
func TestFirstLineWriterRetainsOnlyTheFirstLineUpToTheLimit(t *testing.T) {
	t.Parallel()
	chunk := bytes.Repeat([]byte("y\n"), 32*1024)

	w := &firstLineWriter{limit: 4096}
	for i := 0; i < 1600; i++ {
		if n, err := w.Write(chunk); n != len(chunk) || err != nil {
			t.Fatalf("write %d: (%d, %v), want the whole chunk accepted", i, n, err)
		}
	}
	if line, over := w.result(); line != "y" || over || len(w.line) != 1 {
		t.Errorf("a stream of short lines: kept %q (overflow %v, %d bytes), want just %q", line, over, len(w.line), "y")
	}

	fired := 0
	w = &firstLineWriter{limit: 4096, onOverflow: func() { fired++ }}
	long := bytes.Repeat([]byte("a"), 64*1024)
	for i := 0; i < 1600; i++ {
		_, _ = w.Write(long)
	}
	line, over := w.result()
	if !over || len(line) != 4096 || len(w.line) != 4096 || fired != 1 {
		t.Errorf("a line that never ends: kept %d bytes (overflow %v, notified %d times), want 4096, true, 1", len(line), over, fired)
	}

	// A first line of exactly the limit is a line; one byte more is not.
	w = &firstLineWriter{limit: 8}
	_, _ = w.Write([]byte("12345678\nrest"))
	if line, over := w.result(); line != "12345678" || over {
		t.Errorf("a line of exactly the limit: got %q overflow %v", line, over)
	}
	w = &firstLineWriter{limit: 8}
	_, _ = w.Write([]byte("123456789\n"))
	if line, over := w.result(); !over || len(line) != 8 {
		t.Errorf("a line one byte over: got %q overflow %v", line, over)
	}
}

// A binary that streams forever is cut off at the deadline holding at most its first line,
// and one whose first line never ends is killed at the limit, with its own cause and no line
// to compare; the bytes retained are asserted, not the process's memory.
func TestReadVersionLineWithinBoundsWhatItKeeps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := testbin.WriteExecutable(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// A binary that streams twenty megabytes after its first line: the stub prints the line and
	// then a bounded stream and exits, and it is read under a generous deadline, so nothing
	// here depends on how fast a shell starts. The bytes retained are the first line alone.
	streams := write("streams", "echo y; yes | head -c 20000000")
	out, timedOut, err := runVersion(context.Background(), streams, doctorGoodDeadline, 50*time.Millisecond, 4096)
	if err != nil || timedOut || string(out.line) != "y" || len(out.line) != 1 || out.overflowed {
		t.Errorf("a binary that streams: kept %q (%d bytes, overflow %v), run error %v, timed out %v; want just \"y\" and a clean exit", out.line, len(out.line), out.overflowed, err, timedOut)
	}
	if line, err := readVersionLineWithin(streams, doctorGoodDeadline, 50*time.Millisecond, 4096); line != "y" || err != nil {
		t.Errorf("a binary that streams: got (%q, %v), want its first line and no error", line, err)
	}

	endless := write("endless", "while :; do printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; done")
	line, err := readVersionLineWithin(endless, doctorGoodDeadline, 50*time.Millisecond, 64)
	if line != "" || err == nil || err.Error() != "printed a line longer than 64 bytes" {
		t.Errorf("a first line that never ends: got (%q, %v), want no line and the limit named", line, err)
	}
}

// A stamp the doctor prints is a bounded, escaped excerpt: a very long stamp is compared
// whole and printed short, on the OK line, in the drift lines and in the unreadable line.
func TestDoctorPrintsABoundedExcerptOfAStamp(t *testing.T) {
	t.Parallel()
	long := "nova-swarm " + strings.Repeat("x", 3000)
	env := doctorFake(map[string]string{"/opt/nova-swarm": long, "/home/me/.local/bin/nova-swarm": doctorRebuiltLine}, noPath, "/home/me")

	var out, errOut bytes.Buffer
	if code := env.cmdDoctor([]string{"--path", "/opt/nova-swarm"}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if len(errOut.String()) > 4*doctorStampExcerpt+2000 || strings.Contains(errOut.String(), strings.Repeat("x", doctorStampExcerpt+50)) {
		t.Errorf("the refusal printed the whole stamp (%d bytes)", errOut.Len())
	}
	if !strings.Contains(errOut.String(), "...+") {
		t.Errorf("the excerpt does not say it was cut:\n%.400s", errOut.String())
	}

	out.Reset()
	env = doctorFake(map[string]string{"/opt/nova-swarm": long}, noPath, "/home/me")
	if code := env.cmdDoctor([]string{"--path", "/opt/nova-swarm"}, &out, &errOut); code != 0 || out.Len() > doctorStampExcerpt+100 {
		t.Errorf("OK line: exit %d, %d bytes", code, out.Len())
	}
}

// The two binaries are read at the same time: each read waits until the other has started,
// so a comparison that read them one after the other never gets past the first (the run
// ends at the test timeout, which is the failure). Two hung
// binaries therefore cost one deadline, not two.
func TestCompareBinariesReadsTheTwoAtTheSameTime(t *testing.T) {
	t.Parallel()
	started := map[string]chan struct{}{"/a/nova-swarm": make(chan struct{}), "/b/nova-swarm": make(chan struct{})}
	other := map[string]string{"/a/nova-swarm": "/b/nova-swarm", "/b/nova-swarm": "/a/nova-swarm"}
	env := doctorEnv{
		read: func(path string) (string, error) {
			close(started[path])
			<-started[other[path]] // a sequential comparison waits here for good
			return "nova-swarm " + path, nil
		},
	}
	r := env.compareBinaries("/a/nova-swarm", "/b/nova-swarm")
	if len(r.unreadable) != 0 {
		t.Fatalf("the reads were not concurrent: %+v", r.unreadable)
	}
	if !r.shadowed || r.pathLine != "nova-swarm /a/nova-swarm" || r.localLine != "nova-swarm /b/nova-swarm" {
		t.Errorf("the comparison lost a stamp: %+v", r)
	}
}

// The preflight reads its arguments the way the dispatcher does: --seat comes out first,
// wherever it stands. A help request with a --seat in it is still help, and a launch with
// --seat before the verb is still a launch. The PATH binary here cannot be read, so a launch
// is refused and help is not.
func TestPreflightReadsArgumentsAfterTheGlobalFlagsAreStripped(t *testing.T) {
	t.Parallel()
	env := doctorFake(map[string]string{doctorLocal("/home/me"): doctorRebuiltLine}, func(string) (string, error) {
		return "/opt/go/bin/nova-swarm", nil
	}, "/home/me")

	cases := []struct {
		name string
		args []string
		stop bool
	}{
		{"help after --seat", []string{"batch", "--seat", "foo", "-h"}, false},
		{"help after --seat=", []string{"native", "--seat=foo", "--help"}, false},
		{"help before --seat", []string{"batch", "-h", "--seat", "foo"}, false},
		{"--seat before the verb, help", []string{"--seat", "foo", "batch", "-h"}, false},
		{"--seat before the verb is a launch", []string{"--seat", "foo", "batch", "--tokens", "1"}, true},
		{"--seat=name before the verb is a launch", []string{"--seat=foo", "native", "--card", "c"}, true},
		{"--seat after the verb is a launch", []string{"batch", "--seat", "foo", "--tokens", "1"}, true},
		{"a value spelled -h is still a value", []string{"--seat", "foo", "batch", "--id", "-h"}, true},
		{"no seat name is the dispatcher's refusal", []string{"batch", "--seat"}, false},
		{"another verb is untouched", []string{"--seat", "foo", "template", "--name", "read-pr"}, false},
	}
	for _, c := range cases {
		var errOut bytes.Buffer
		code, stop := env.preflight(c.args, &errOut)
		if stop != c.stop || (stop && code != 2) || (!stop && code != 0) {
			t.Errorf("%s: preflight(%v) = (exit=%d, stop=%v), want stop=%v\n%s", c.name, c.args, code, stop, c.stop, errOut.String())
		}
	}
}

// The other binary's part of an unreadable line is a plain sentence in each of its cases:
// there is no other binary, it could not be read either, it is not installed, it reported a
// stamp. It is never "no other binary reported nothing".
func TestDoctorUnreadableLineSaysWhatTheOtherBinaryCameTo(t *testing.T) {
	t.Parallel()
	failing := func(path string) (string, error) { return "", errors.New("exited 3") }
	env := doctorEnv{
		lookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
		homeDir:  func() (string, error) { return "/home/me", nil },
		read: func(path string) (string, error) {
			switch path {
			case "/p/one":
				return "", errors.New("timed out after 5s")
			case "/l/one":
				return doctorRebuiltLine, nil
			case "/l/gone":
				return "", errDoctorNotFound
			}
			return failing(path)
		},
	}
	cases := []struct {
		name, path, local string
		want              string
		not               []string
	}{
		{"the other binary reported a stamp", "/p/one", "/l/one", ", reported stamp=" + doctorRebuiltLine, nil},
		{"there is no other binary: one file", "/p/one", "/p/one", "there is no other binary: PATH resolves to the local copy", []string{"reported"}},
		{"there is no other binary: nothing resolved", "/p/one", "", "there is no other binary to compare with", []string{"reported"}},
		{"the other binary is not installed", "/p/one", "/l/gone", ", is not installed", []string{"reported", "no other binary"}},
		{"the other binary could not be read either", "/p/one", "/l/two", ", could not be read either: exited 3", []string{"reported", "no other binary"}},
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		args := []string{"--path", c.path}
		if c.local != "" {
			args = append(args, "--local", c.local)
		} else {
			env.homeDir = func() (string, error) { return "", errors.New("no home") }
		}
		if code := env.cmdDoctor(args, &out, &errOut); code != 2 {
			t.Errorf("%s: exit %d, want 2\n%s", c.name, code, errOut.String())
			continue
		}
		got := errOut.String()
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: the line lacks %q:\n%s", c.name, c.want, got)
		}
		for _, no := range c.not {
			if strings.Contains(got, no) {
				t.Errorf("%s: the line holds %q:\n%s", c.name, no, got)
			}
		}
		if strings.Contains(got, "no other binary reported") {
			t.Errorf("%s: the line reads as its opposite:\n%s", c.name, got)
		}
	}
}

// With nothing on PATH and no local copy, nothing is read, and the doctor says that instead
// of reporting a stamp: exit 0, no refusal.
func TestDoctorSaysWhenThereIsNothingToCompare(t *testing.T) {
	t.Parallel()
	env := doctorFake(map[string]string{}, noPath, "/home/me")
	var out, errOut bytes.Buffer
	if code := env.cmdDoctor(nil, &out, &errOut); code != 0 || errOut.Len() != 0 {
		t.Fatalf("exit %d, stderr %q, want 0 and silence", code, errOut.String())
	}
	if want := "DOCTOR OK nothing to compare: no nova-swarm on PATH and none under the local directory\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
	if strings.Contains(out.String(), "devel") || strings.Contains(out.String(), "stamp=") {
		t.Errorf("the line reports a stamp nobody read: %q", out.String())
	}
	if code, stop := env.preflight([]string{"batch", "--tokens", "1"}, &errOut); code != 0 || stop {
		t.Errorf("the launch is refused with nothing to compare: (%d, %v)", code, stop)
	}
}

// The spec and the CLI reference list exactly the lines the doctor prints, and the causes
// those lines can carry: every line the code prints below starts with one of these, both
// documents carry each one, and so does each cause (checkDoctorCausesAgainstDocs).
func TestDoctorLinesAreTheOnesTheDocsList(t *testing.T) {
	t.Parallel()
	prefixes := []string{
		"DOCTOR OK stamp=",
		"DOCTOR OK nothing to compare: no nova-swarm on PATH and none under the local directory",
		"DOCTOR DRIFT path=",
		"DOCTOR DRIFT local=",
		"DOCTOR REFUSED ",
		"DOCTOR UNREADABLE reading the version of ",
	}
	printed := map[string]bool{}
	note := func(text string) {
		for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			if line == "" {
				continue
			}
			known := false
			for _, p := range prefixes {
				if strings.HasPrefix(line, p) {
					printed[p], known = true, true
				}
			}
			if !known {
				t.Errorf("the doctor printed a line the docs do not list: %q", line)
			}
		}
	}
	run := func(env doctorEnv, args ...string) {
		var out, errOut bytes.Buffer
		env.cmdDoctor(args, &out, &errOut)
		note(out.String())
		note(errOut.String())
	}
	both := doctorFake(map[string]string{"/p": doctorRebuiltLine, "/l": doctorRebuiltLine}, noPath, "/home/me")
	run(both, "--path", "/p", "--local", "/l")
	run(doctorFake(nil, noPath, "/home/me"))
	run(doctorFake(map[string]string{"/p": doctorStaleLine, "/l": doctorRebuiltLine}, noPath, "/home/me"), "--path", "/p", "--local", "/l")
	run(doctorFake(map[string]string{"/l": doctorRebuiltLine}, noPath, "/home/me"), "--path", "/p", "--local", "/l")
	for _, p := range prefixes {
		if !printed[p] {
			t.Errorf("no run printed a line starting %q", p)
		}
	}

	checkDoctorCausesAgainstDocs(t)

	for _, doc := range []string{"SPEC-SWARM.md", "CLI.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "docs", doc))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range prefixes {
			if !strings.Contains(string(raw), p) {
				t.Errorf("docs/%s does not list %q", doc, p)
			}
		}
	}
}

// checkDoctorCausesAgainstDocs holds the causes to the documents too: every cause the reader
// can put in a DOCTOR UNREADABLE line is produced below by a real run, starts with one of the
// listed forms, and both documents carry every form.
func checkDoctorCausesAgainstDocs(t *testing.T) {
	t.Helper()
	forms := []string{
		"timed out after ",
		"exited ",
		"was killed (",
		"printed nothing",
		"printed a line longer than ",
		"not found",
		"fork/exec ",
	}
	dir := t.TempDir()
	write := func(name, body string, perm os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := testbin.WriteExecutable(p, []byte(body), perm); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stubs := map[string]string{
		"timed out after ":            write("hang", "#!/bin/sh\nexec sleep 30\n", 0o755),
		"exited ":                     write("exit3", "#!/bin/sh\nexit 3\n", 0o755),
		"was killed (":                write("killed", "#!/bin/sh\nkill -9 $$\n", 0o755),
		"printed nothing":             write("silent", "#!/bin/sh\nexit 0\n", 0o755),
		"printed a line longer than ": write("long", "#!/bin/sh\nwhile :; do printf 'aaaaaaaaaaaaaaaa'; done\n", 0o755),
		"not found":                   filepath.Join(dir, "absent"),
		"fork/exec ":                  write("plain-file", "#!/bin/sh\necho x\n", 0o644),
	}
	seen := map[string]bool{}
	for form, stub := range stubs {
		deadline := doctorGoodDeadline
		if form == "timed out after " {
			deadline = doctorHungDeadline
		}
		_, err := readVersionLineWithin(stub, deadline, 50*time.Millisecond, 64)
		if err == nil {
			t.Errorf("%s: the run gave no cause", form)
			continue
		}
		if !strings.HasPrefix(err.Error(), form) {
			t.Errorf("a run expected to give %q gave %q", form, err)
		}
		seen[form] = true
	}
	for _, form := range forms {
		if !seen[form] {
			t.Errorf("no run produced a cause starting %q", form)
		}
	}
	for _, doc := range []string{"SPEC-SWARM.md", "CLI.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "docs", doc))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"`timed out after <deadline>`", "`exited <n>`", "`was killed (<signal>)`", "`printed nothing`",
			"`printed a line longer than <n> bytes`", "`not found`", "`fork/exec <path>: permission denied`",
		} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("docs/%s does not list the cause %s", doc, want)
			}
		}
	}
}

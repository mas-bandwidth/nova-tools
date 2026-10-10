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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// THE RED TESTS FOR `nova-worker doctor` (2026-09-17 shadowed-binary incident).
//
// ~/go/bin held an old nova-worker for 25 minutes while ~/.local/bin held the rebuilt one,
// because PATH put ~/go/bin first; every card launched in that window ran the stale binary
// with a private build cache and nothing said so. The verb this file turns red is the one
// that reads the stamp of the nova-worker first on PATH and the stamp of the one at
// ~/.local/bin and REFUSES when the two disagree.
//
// NOTHING HERE EXECUTES A DISCOVERED PATH. The version reader and the PATH resolver are
// package vars a test replaces, so a unit test hands over two fixed stamps and never runs a
// binary it went looking for.

// doctorFake is the environment a test hands the doctor: where nova-worker is looked up on
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

func doctorLocal(home string) string { return filepath.Join(home, ".local", "bin", "nova-worker") }

// A pair of stamps that differ, the shape the incident produced: same tool, same platform,
// a different build identity in field two.
const (
	doctorStaleLine   = "nova-worker 20260917010203-aaaaaaaaaaaa linux/amd64 go1.26.5"
	doctorRebuiltLine = "nova-worker 20260917174700-bbbbbbbbbbbb linux/amd64 go1.26.5"
)

// MATCHING STAMPS ARE OK, one line, exit 0. This is the answer on a healthy bench and the
// whole point of the OK line: a launch's own refusal has to be able to say it looked.
func TestDoctorOKWhenBothStampsMatch(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{
			"/opt/go/bin/nova-worker":         doctorRebuiltLine,
			"/home/me/.local/bin/nova-worker": doctorRebuiltLine,
		},
		noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", "/opt/go/bin/nova-worker", "--local", "/home/me/.local/bin/nova-worker"}, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
	assert.Zero(t, errOut.Len(), "a matching pair wrote to stderr: %q", errOut.String())
	want := "DOCTOR OK stamp=" + doctorRebuiltLine + "\n"
	assert.Equal(t, want, out.String(), "OK line:\n got %q\nwant %q", out.String(), want)
}

// A DIFFERENT STAMP IS A REFUSAL, exit 2, with BOTH full version lines printed and one
// remedy. The stale stamp must not be truncated to forty characters the way the operator
// script did: the revision is the part a person compares, and it is at the end.
func TestDoctorRefusesWhenThePATHBinaryIsShadowed(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{
			"/opt/go/bin/nova-worker":         doctorStaleLine,
			"/home/me/.local/bin/nova-worker": doctorRebuiltLine,
		},
		noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", "/opt/go/bin/nova-worker", "--local", "/home/me/.local/bin/nova-worker"}, &out, &errOut)
	require.Equal(t, 2, code, "exit %d, want 2\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	both := out.String() + errOut.String()
	assert.Contains(t, both, doctorStaleLine, "the PATH binary's full line is not printed:\n%s", both)
	assert.Contains(t, both, doctorRebuiltLine, "the ~/.local/bin binary's full line is not printed:\n%s", both)
	assert.Contains(t, both, "copy", "the refusal names no fix:\n%s", both)
	assert.Contains(t, both, ".local/bin", "the refusal names no fix:\n%s", both)
}

// THE PATH RESOLVER IS THE SEAM, and this test drives it: `--path` is left off, the fake
// resolver answers the PATH question, and the fake reader answers both stamps. Nothing is
// executed.
func TestDoctorResolvesNovaSwarmOnPATH(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{
			"/usr/local/bin/nova-worker": doctorRebuiltLine,
			doctorLocal("/home/me"):     doctorRebuiltLine,
		},
		func(name string) (string, error) {
			assert.Equal(t, "nova-worker", name, "the resolver was asked for %q, want nova-worker", name)
			return "/usr/local/bin/nova-worker", nil
		}, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor(nil, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
	assert.True(t, strings.HasPrefix(out.String(), "DOCTOR OK stamp="), "not the OK line: %q", out.String())
}

// PATH's nova-worker IS ~/.local/bin/nova-worker: there is no second binary and so nothing to
// compare. This is the healthy machine whose PATH already prefers the rebuilt install.
func TestDoctorOKWhenPATHResolvesToTheLocalBinary(t *testing.T) {
	t.Parallel()
	env := doctorFake(map[string]string{doctorLocal("/home/me"): doctorRebuiltLine}, noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", doctorLocal("/home/me"), "--local", doctorLocal("/home/me")}, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
	assert.Zero(t, errOut.Len(), "wrote to stderr: %q", errOut.String())
	want := "DOCTOR OK stamp=" + doctorRebuiltLine + "\n"
	assert.Equal(t, want, out.String(), "OK line:\n got %q\nwant %q", out.String(), want)
}

// The one binary a launch would run must answer even when there is no second one to compare
// it with.
func TestDoctorRefusesWhenTheOneBinaryCannotBeRead(t *testing.T) {
	t.Parallel()
	env := doctorFake(map[string]string{}, noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", doctorLocal("/home/me"), "--local", doctorLocal("/home/me")}, &out, &errOut)
	require.Equal(t, 2, code, "exit %d, want 2 with DOCTOR UNREADABLE on stderr\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	require.Equal(t, 0, out.Len(), "exit %d, want 2 with DOCTOR UNREADABLE on stderr\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	require.Contains(t, errOut.String(), "DOCTOR UNREADABLE", "exit %d, want 2 with DOCTOR UNREADABLE on stderr\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
}

// NO ~/.local/bin COPY means there is nothing that could be shadowed: the guard cannot
// invent a mismatch, so it is OK and reports the stamp it did read.
func TestDoctorOKWhenTheLocalBinaryIsAbsent(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{"/opt/go/bin/nova-worker": doctorStaleLine},
		noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", "/opt/go/bin/nova-worker"}, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
	want := "DOCTOR OK stamp=" + doctorStaleLine + "\n"
	assert.Equal(t, want, out.String(), "OK line:\n got %q\nwant %q", out.String(), want)
}

// THE LAUNCH SEAM. `native` is the verb that starts a card, and the preflight is
// what main calls before the dispatcher: a shadowed pair stops the launch with exit 2
// before anything is spent. A verb that starts nothing is untouched.
func TestPreflightRefusesALaunchUnderAShadowedBinary(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{
			"/opt/go/bin/nova-worker": doctorStaleLine,
			doctorLocal("/home/me"):  doctorRebuiltLine,
		},
		func(string) (string, error) { return "/opt/go/bin/nova-worker", nil }, "/home/me")

	var errOut bytes.Buffer
	code, stop := env.preflight([]string{"native", "--dir", "d"}, &errOut)
	require.True(t, stop, "preflight(exit=%d, stop=%v), want (2, true)", code, stop)
	require.Equal(t, 2, code, "preflight(exit=%d, stop=%v), want (2, true)", code, stop)
	both := errOut.String()
	assert.Contains(t, both, doctorStaleLine, "the preflight prints both stamps:\n%s", both)
	assert.Contains(t, both, doctorRebuiltLine, "the preflight prints both stamps:\n%s", both)

	// A flag value spelled -h (like `native --label -h` or `native --card -h`) is NOT a help request:
	// under a shadowed binary, the preflight must refuse it with exit 2 rather than stand aside.
	errOut.Reset()
	code, stop = env.preflight([]string{"native", "--label", "-h"}, &errOut)
	assert.True(t, stop, "native --label -h: preflight(exit=%d, stop=%v), want (2, true)", code, stop)
	assert.Equal(t, 2, code, "native --label -h: preflight(exit=%d, stop=%v), want (2, true)", code, stop)
	errOut.Reset()
	code, stop = env.preflight([]string{"native", "--card", "-h"}, &errOut)
	assert.True(t, stop, "native --card -h: preflight(exit=%d, stop=%v), want (2, true)", code, stop)
	assert.Equal(t, 2, code, "native --card -h: preflight(exit=%d, stop=%v), want (2, true)", code, stop)
}

func TestPreflightLeavesNonLaunchVerbsAlone(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{
			"/opt/go/bin/nova-worker": doctorStaleLine,
			doctorLocal("/home/me"):  doctorRebuiltLine,
		},
		func(string) (string, error) { return "/opt/go/bin/nova-worker", nil }, "/home/me")

	var errOut bytes.Buffer
	code, stop := env.preflight([]string{"template", "--name", "read-pr"}, &errOut)
	require.False(t, stop, "template: preflight(exit=%d, stop=%v), want (0, false)", code, stop)
	require.Equal(t, 0, code, "template: preflight(exit=%d, stop=%v), want (0, false)", code, stop)
	assert.Zero(t, errOut.Len(), "a verb that starts nothing was refused: %q", errOut.String())
}

// The verb is reachable from the dispatcher, and `doctor` is not a launch verb itself, so
// running it does not recurse.
func TestDoctorVerbIsReachableFromTheDispatch(t *testing.T) {
	doctorFakeGlobal(t,
		map[string]string{
			"/opt/go/bin/nova-worker":         doctorRebuiltLine,
			"/home/me/.local/bin/nova-worker": doctorRebuiltLine,
		},
		noPath, "/home/me")
	var out, errOut bytes.Buffer
	code := run([]string{"doctor", "--path", "/opt/go/bin/nova-worker", "--local", "/home/me/.local/bin/nova-worker"}, strings.NewReader(""), &out, &errOut, time.Now().UTC())
	require.Equal(t, 0, code, "exit %d, want 0\nstderr: %s", code, errOut.String())
	assert.True(t, strings.HasPrefix(out.String(), "DOCTOR OK stamp="), "not the OK line: %q", out.String())
}

// A PATH binary that is named and cannot be read is a refusal, not an OK with the other
// binary's stamp: the line names the binary, the cause, and what the other one reported.
func TestDoctorRefusesAnUnreadablePATHBinary(t *testing.T) {
	t.Parallel()
	env := doctorFake(
		map[string]string{"/home/me/.local/bin/nova-worker": doctorRebuiltLine},
		noPath, "/home/me")
	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", "/opt/go/bin/gone", "--local", "/home/me/.local/bin/nova-worker"}, &out, &errOut)
	require.Equal(t, 2, code, "exit %d, want 2\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	got := errOut.String()
	for _, want := range []string{"DOCTOR UNREADABLE", "/opt/go/bin/gone", "not found", doctorRebuiltLine, "by hand"} {
		assert.Contains(t, got, want, "the refusal does not contain %q:\n%s", want, got)
	}
	assert.Zero(t, out.Len(), "a refusal wrote a DOCTOR OK line: %q", out.String())
}

// Preflight stands aside for -h and --help: asking for help is not a launch.
func TestPreflightDoctorStandsAsideForHelp(t *testing.T) {
	t.Parallel()
	var errOut bytes.Buffer
	for _, verb := range []string{"native"} {
		for _, flag := range []string{"-h", "--help", "-help", "--h"} {
			code, stop := preflightDoctor([]string{verb, flag}, &errOut)
			assert.False(t, stop, "%s %s: preflight(exit=%d, stop=%v), want (0, false)", verb, flag, code, stop)
			assert.Equal(t, 0, code, "%s %s: preflight(exit=%d, stop=%v), want (0, false)", verb, flag, code, stop)
		}
	}
	// Flag value followed by actual help stands aside
	code, stop := preflightDoctor([]string{"native", "--label", "-h", "-h"}, &errOut)
	assert.False(t, stop, "native --label -h -h: preflight(exit=%d, stop=%v), want (0, false)", code, stop)
	assert.Equal(t, 0, code, "native --label -h -h: preflight(exit=%d, stop=%v), want (0, false)", code, stop)
	code, stop = preflightDoctor([]string{"native", "--card", "-h", "-h"}, &errOut)
	assert.False(t, stop, "native --card -h -h: preflight(exit=%d, stop=%v), want (0, false)", code, stop)
	assert.Equal(t, 0, code, "native --card -h -h: preflight(exit=%d, stop=%v), want (0, false)", code, stop)
}

// The version reader answers under a deadline: a binary that answers is read, and one that
// hangs is killed with the cause "timed out after <deadline>". The deadline is injected, so
// the hung case ends at a fraction of a second.
func TestReadVersionLineWithinKillsAHungBinary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	answers := filepath.Join(dir, "answers")
	require.NoError(t, testbin.WriteExecutable(answers, []byte("#!/bin/sh\necho 'nova-worker v1 stamp'\n"), 0o755))
	line, err := readVersionLineWithin(answers, doctorGoodDeadline, time.Second, doctorVersionLineMax)
	require.NoError(t, err, "a binary that answers: got (%q, %v)", line, err)
	require.Equal(t, "nova-worker v1 stamp", line, "a binary that answers: got (%q, %v)", line, err)

	hangs := filepath.Join(dir, "hangs")
	require.NoError(t, testbin.WriteExecutable(hangs, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755))
	line, err = readVersionLineWithin(hangs, doctorHungDeadline, 50*time.Millisecond, doctorVersionLineMax)
	assert.EqualError(t, err, "timed out after 100ms", "a hung binary: got (%q, %v), want an empty line and \"timed out after 100ms\"", line, err)
	assert.Equal(t, "", line, "a hung binary: got (%q, %v), want an empty line and \"timed out after 100ms\"", line, err)
}

// Production's reader carries a bounded deadline.
func TestDoctorVersionDeadlineIsBounded(t *testing.T) {
	t.Parallel()
	assert.Greater(t, doctorVersionDeadline, time.Duration(0), "doctorVersionDeadline = %s, want a few seconds", doctorVersionDeadline)
	assert.LessOrEqual(t, doctorVersionDeadline, 30*time.Second, "doctorVersionDeadline = %s, want a few seconds", doctorVersionDeadline)
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
	pathBin = filepath.Join(dir, "bin", "nova-worker")
	localBin = filepath.Join(home, ".local", "bin", "nova-worker")
	for bin, script := range map[string]string{pathBin: pathScript, localBin: localScript} {
		if script == "" {
			continue
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
		require.NoError(t, testbin.WriteExecutable(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755))
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
	const good = "echo 'nova-worker good-stamp'"
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
			[]string{"DOCTOR UNREADABLE", "path=", "timed out after 100ms", "the other binary, ", ", reported stamp=nova-worker good-stamp;", "by hand"},
			[]string{"DOCTOR DRIFT", "shadows"}},
		{"print then exit 3: the stamp is compared and the exit reported", "echo 'nova-worker stale-stamp'; exit 3", good, "", 2,
			[]string{"DOCTOR DRIFT path=", "stale-stamp", "shadows", "DOCTOR UNREADABLE", "exited 3"}, nil},
		{"print then exit 3 with the same stamp", "echo 'nova-worker good-stamp'; exit 3", good, "", 2,
			[]string{"DOCTOR UNREADABLE", "exited 3"}, []string{"DOCTOR DRIFT"}},
		{"printed nothing", "exit 0", good, "", 2,
			[]string{"DOCTOR UNREADABLE", "printed nothing", "stamp=nova-worker good-stamp"}, nil},
		{"missing where PATH names it", "", good, "", 2,
			[]string{"DOCTOR UNREADABLE", "not found", "stamp=nova-worker good-stamp"}, nil},
		{"the local copy hangs", good, "exec sleep 30", "local", 2,
			[]string{"DOCTOR UNREADABLE", "local=", "timed out after 100ms", "stamp=nova-worker good-stamp"}, []string{"DOCTOR DRIFT"}},
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
			code, stop := env.preflight([]string{"native", "--tokens", "unmetered"}, &errOut)
			<-done
			want := c.wantExit != 0
			require.Equal(t, want, stop, "preflight(exit=%d, stop=%v), want exit %d\n%s", code, stop, c.wantExit, errOut.String())
			require.Equal(t, c.wantExit, code, "preflight(exit=%d, stop=%v), want exit %d\n%s", code, stop, c.wantExit, errOut.String())
			got := errOut.String()
			for _, want := range c.contains {
				assert.Contains(t, got, want, "stderr lacks %q:\n%s", want, got)
			}
			for _, no := range c.absent {
				assert.NotContains(t, got, no, "stderr holds %q:\n%s", no, got)
			}

			// The doctor verb says the same as a finding, and exits non-zero.
			assert.Equal(t, c.wantExit, dcode, "doctor exit %d, want %d\nstdout: %s\nstderr: %s", dcode, c.wantExit, out.String(), derr.String())
			assert.True(t, c.wantExit == 0 || (out.Len() == 0 && derr.String() == got), "doctor's finding differs from the preflight's\nstdout: %q\nstderr: %q\nwant stderr: %q", out.String(), derr.String(), got)
			assert.False(t, c.wantExit == 0 && !strings.HasPrefix(out.String(), "DOCTOR OK stamp="), "doctor: not the OK line: %q", out.String())
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
		n, err := w.Write(chunk)
		require.Equal(t, len(chunk), n, "write %d: (%d, %v), want the whole chunk accepted", i, n, err)
		require.NoError(t, err, "write %d: (%d, %v), want the whole chunk accepted", i, n, err)
	}
	line, over := w.result()
	assert.Equal(t, "y", line, "a stream of short lines: kept %q (overflow %v, %d bytes), want just %q", line, over, len(w.line), "y")
	assert.False(t, over, "a stream of short lines: kept %q (overflow %v, %d bytes), want just %q", line, over, len(w.line), "y")
	assert.Len(t, w.line, 1, "a stream of short lines: kept %q (overflow %v, %d bytes), want just %q", line, over, len(w.line), "y")

	fired := 0
	w = &firstLineWriter{limit: 4096, onOverflow: func() { fired++ }}
	long := bytes.Repeat([]byte("a"), 64*1024)
	for i := 0; i < 1600; i++ {
		_, _ = w.Write(long)
	}
	line, over = w.result()
	assert.True(t, over, "a line that never ends: kept %d bytes (overflow %v, notified %d times), want 4096, true, 1", len(line), over, fired)
	assert.Len(t, line, 4096, "a line that never ends: kept %d bytes (overflow %v, notified %d times), want 4096, true, 1", len(line), over, fired)
	assert.Len(t, w.line, 4096, "a line that never ends: kept %d bytes (overflow %v, notified %d times), want 4096, true, 1", len(line), over, fired)
	assert.Equal(t, 1, fired, "a line that never ends: kept %d bytes (overflow %v, notified %d times), want 4096, true, 1", len(line), over, fired)

	// A first line of exactly the limit is a line; one byte more is not.
	w = &firstLineWriter{limit: 8}
	_, _ = w.Write([]byte("12345678\nrest"))
	line, over = w.result()
	assert.Equal(t, "12345678", line, "a line of exactly the limit: got %q overflow %v", line, over)
	assert.False(t, over, "a line of exactly the limit: got %q overflow %v", line, over)
	w = &firstLineWriter{limit: 8}
	_, _ = w.Write([]byte("123456789\n"))
	line, over = w.result()
	assert.True(t, over, "a line one byte over: got %q overflow %v", line, over)
	assert.Len(t, line, 8, "a line one byte over: got %q overflow %v", line, over)
}

// A binary that streams forever is cut off at the deadline holding at most its first line,
// and one whose first line never ends is killed at the limit, with its own cause and no line
// to compare; the bytes retained are asserted, not the process's memory.
func TestReadVersionLineWithinBoundsWhatItKeeps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, testbin.WriteExecutable(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755))
		return p
	}

	// A binary that streams twenty megabytes after its first line: the stub prints the line and
	// then a bounded stream and exits, and it is read under a generous deadline, so nothing
	// here depends on how fast a shell starts. The bytes retained are the first line alone.
	streams := write("streams", "echo y; yes | head -c 20000000")
	out, timedOut, err := runVersion(context.Background(), streams, doctorGoodDeadline, 50*time.Millisecond, 4096)
	assert.NoError(t, err, "a binary that streams: kept %q (%d bytes, overflow %v), run error %v, timed out %v; want just \"y\" and a clean exit", out.line, len(out.line), out.overflowed, err, timedOut)
	assert.False(t, timedOut, "a binary that streams: kept %q (%d bytes, overflow %v), run error %v, timed out %v; want just \"y\" and a clean exit", out.line, len(out.line), out.overflowed, err, timedOut)
	assert.Equal(t, "y", string(out.line), "a binary that streams: kept %q (%d bytes, overflow %v), run error %v, timed out %v; want just \"y\" and a clean exit", out.line, len(out.line), out.overflowed, err, timedOut)
	assert.Len(t, out.line, 1, "a binary that streams: kept %q (%d bytes, overflow %v), run error %v, timed out %v; want just \"y\" and a clean exit", out.line, len(out.line), out.overflowed, err, timedOut)
	assert.False(t, out.overflowed, "a binary that streams: kept %q (%d bytes, overflow %v), run error %v, timed out %v; want just \"y\" and a clean exit", out.line, len(out.line), out.overflowed, err, timedOut)
	line, err := readVersionLineWithin(streams, doctorGoodDeadline, 50*time.Millisecond, 4096)
	assert.Equal(t, "y", line, "a binary that streams: got (%q, %v), want its first line and no error", line, err)
	assert.NoError(t, err, "a binary that streams: got (%q, %v), want its first line and no error", line, err)

	endless := write("endless", "while :; do printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; done")
	line, err = readVersionLineWithin(endless, doctorGoodDeadline, 50*time.Millisecond, 64)
	assert.Equal(t, "", line, "a first line that never ends: got (%q, %v), want no line and the limit named", line, err)
	assert.EqualError(t, err, "printed a line longer than 64 bytes", "a first line that never ends: got (%q, %v), want no line and the limit named", line, err)
}

// A stamp the doctor prints is a bounded, escaped excerpt: a very long stamp is compared
// whole and printed short, on the OK line, in the drift lines and in the unreadable line.
func TestDoctorPrintsABoundedExcerptOfAStamp(t *testing.T) {
	t.Parallel()
	long := "nova-worker " + strings.Repeat("x", 3000)
	env := doctorFake(map[string]string{"/opt/nova-worker": long, "/home/me/.local/bin/nova-worker": doctorRebuiltLine}, noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := env.cmdDoctor([]string{"--path", "/opt/nova-worker"}, &out, &errOut)
	require.Equal(t, 2, code, "exit %d, want 2", code)
	assert.LessOrEqual(t, len(errOut.String()), 4*doctorStampExcerpt+2000, "the refusal printed the whole stamp (%d bytes)", errOut.Len())
	assert.NotContains(t, errOut.String(), strings.Repeat("x", doctorStampExcerpt+50), "the refusal printed the whole stamp (%d bytes)", errOut.Len())
	assert.Contains(t, errOut.String(), "...+", "the excerpt does not say it was cut:\n%.400s", errOut.String())

	out.Reset()
	env = doctorFake(map[string]string{"/opt/nova-worker": long}, noPath, "/home/me")
	code = env.cmdDoctor([]string{"--path", "/opt/nova-worker"}, &out, &errOut)
	assert.Equal(t, 0, code, "OK line: exit %d, %d bytes", code, out.Len())
	assert.LessOrEqual(t, out.Len(), doctorStampExcerpt+100, "OK line: exit %d, %d bytes", code, out.Len())
}

// The two binaries are read at the same time: each read waits until the other has started,
// so a comparison that read them one after the other never gets past the first (the run
// ends at the test timeout, which is the failure). Two hung
// binaries therefore cost one deadline, not two.
func TestCompareBinariesReadsTheTwoAtTheSameTime(t *testing.T) {
	t.Parallel()
	started := map[string]chan struct{}{"/a/nova-worker": make(chan struct{}), "/b/nova-worker": make(chan struct{})}
	other := map[string]string{"/a/nova-worker": "/b/nova-worker", "/b/nova-worker": "/a/nova-worker"}
	env := doctorEnv{
		read: func(path string) (string, error) {
			close(started[path])
			<-started[other[path]] // a sequential comparison waits here for good
			return "nova-worker " + path, nil
		},
	}
	r := env.compareBinaries("/a/nova-worker", "/b/nova-worker")
	require.Empty(t, r.unreadable, "the reads were not concurrent: %+v", r.unreadable)
	assert.True(t, r.shadowed, "the comparison lost a stamp: %+v", r)
	assert.Equal(t, "nova-worker /a/nova-worker", r.pathLine, "the comparison lost a stamp: %+v", r)
	assert.Equal(t, "nova-worker /b/nova-worker", r.localLine, "the comparison lost a stamp: %+v", r)
}

// The preflight reads its arguments the way the dispatcher does: --seat comes out first,
// wherever it stands. A help request with a --seat in it is still help, and a launch with
// --seat before the verb is still a launch. The PATH binary here cannot be read, so a launch
// is refused and help is not.
func TestPreflightReadsArgumentsAfterTheGlobalFlagsAreStripped(t *testing.T) {
	t.Parallel()
	env := doctorFake(map[string]string{doctorLocal("/home/me"): doctorRebuiltLine}, func(string) (string, error) {
		return "/opt/go/bin/nova-worker", nil
	}, "/home/me")

	cases := []struct {
		name string
		args []string
		stop bool
	}{
		{"help after --seat", []string{"native", "--seat", "foo", "-h"}, false},
		{"help after --seat=", []string{"native", "--seat=foo", "--help"}, false},
		{"help before --seat", []string{"native", "-h", "--seat", "foo"}, false},
		{"--seat before the verb, help", []string{"--seat", "foo", "native", "-h"}, false},
		{"--seat before the verb is a launch", []string{"--seat", "foo", "native", "--tokens", "1"}, true},
		{"--seat=name before the verb is a launch", []string{"--seat=foo", "native", "--card", "c"}, true},
		{"--seat after the verb is a launch", []string{"native", "--seat", "foo", "--tokens", "1"}, true},
		{"a value spelled -h is still a value", []string{"--seat", "foo", "native", "--label", "-h"}, true},
		{"no seat name is the dispatcher's refusal", []string{"native", "--seat"}, false},
		{"another verb is untouched", []string{"--seat", "foo", "template", "--name", "read-pr"}, false},
	}
	for _, c := range cases {
		var errOut bytes.Buffer
		code, stop := env.preflight(c.args, &errOut)
		assert.Equal(t, c.stop, stop, "%s: preflight(%v) = (exit=%d, stop=%v), want stop=%v\n%s", c.name, c.args, code, stop, c.stop, errOut.String())
		assert.True(t, !stop || code == 2, "%s: preflight(%v) = (exit=%d, stop=%v), want stop=%v\n%s", c.name, c.args, code, stop, c.stop, errOut.String())
		assert.True(t, stop || code == 0, "%s: preflight(%v) = (exit=%d, stop=%v), want stop=%v\n%s", c.name, c.args, code, stop, c.stop, errOut.String())
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
		code := env.cmdDoctor(args, &out, &errOut)
		if !assert.Equal(t, 2, code, "%s: exit %d, want 2\n%s", c.name, code, errOut.String()) {
			continue
		}
		got := errOut.String()
		assert.Contains(t, got, c.want, "%s: the line lacks %q:\n%s", c.name, c.want, got)
		for _, no := range c.not {
			assert.NotContains(t, got, no, "%s: the line holds %q:\n%s", c.name, no, got)
		}
		assert.NotContains(t, got, "no other binary reported", "%s: the line reads as its opposite:\n%s", c.name, got)
	}
}

// With nothing on PATH and no local copy, nothing is read, and the doctor says that instead
// of reporting a stamp: exit 0, no refusal.
func TestDoctorSaysWhenThereIsNothingToCompare(t *testing.T) {
	t.Parallel()
	env := doctorFake(map[string]string{}, noPath, "/home/me")
	var out, errOut bytes.Buffer
	code := env.cmdDoctor(nil, &out, &errOut)
	require.Equal(t, 0, code, "exit %d, stderr %q, want 0 and silence", code, errOut.String())
	require.Equal(t, 0, errOut.Len(), "exit %d, stderr %q, want 0 and silence", code, errOut.String())
	want := "DOCTOR OK nothing to compare: no nova-worker on PATH and none under the local directory\n"
	assert.Equal(t, want, out.String(), "got %q, want %q", out.String(), want)
	assert.NotContains(t, out.String(), "devel", "the line reports a stamp nobody read: %q", out.String())
	assert.NotContains(t, out.String(), "stamp=", "the line reports a stamp nobody read: %q", out.String())
	code, stop := env.preflight([]string{"native", "--tokens", "1"}, &errOut)
	assert.Equal(t, 0, code, "the launch is refused with nothing to compare: (%d, %v)", code, stop)
	assert.False(t, stop, "the launch is refused with nothing to compare: (%d, %v)", code, stop)
}

// The spec and the CLI reference list exactly the lines the doctor prints, and the causes
// those lines can carry: every line the code prints below starts with one of these, both
// documents carry each one, and so does each cause (checkDoctorCausesAgainstDocs).
func TestDoctorLinesAreTheOnesTheDocsList(t *testing.T) {
	t.Parallel()
	prefixes := []string{
		"DOCTOR OK stamp=",
		"DOCTOR OK nothing to compare: no nova-worker on PATH and none under the local directory",
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
			assert.True(t, known, "the doctor printed a line the docs do not list: %q", line)
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
		assert.True(t, printed[p], "no run printed a line starting %q", p)
	}

	checkDoctorCausesAgainstDocs(t)

	for _, doc := range []string{"SPEC-WORKER.md", "CLI.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "docs", doc))
		require.NoError(t, err)
		for _, p := range prefixes {
			assert.Contains(t, string(raw), p, "docs/%s does not list %q", doc, p)
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
		require.NoError(t, testbin.WriteExecutable(p, []byte(body), perm))
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
		if !assert.Error(t, err, "%s: the run gave no cause", form) {
			continue
		}
		assert.True(t, strings.HasPrefix(err.Error(), form), "a run expected to give %q gave %q", form, err)
		seen[form] = true
	}
	for _, form := range forms {
		assert.True(t, seen[form], "no run produced a cause starting %q", form)
	}
	for _, doc := range []string{"SPEC-WORKER.md", "CLI.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "docs", doc))
		require.NoError(t, err)
		for _, want := range []string{
			"`timed out after <deadline>`", "`exited <n>`", "`was killed (<signal>)`", "`printed nothing`",
			"`printed a line longer than <n> bytes`", "`not found`", "`fork/exec <path>: permission denied`",
		} {
			assert.Contains(t, string(raw), want, "docs/%s does not list the cause %s", doc, want)
		}
	}
}

// TestSpecSwarmSaysTheTokenBudgetIsAdvisoryUnderAHostileHarness pins
// security#66 finding 4: the token and usd budgets are read from the child's
// writable data home, so under a hostile harness they are advisory and the
// deadline is the enforced bound; rule 11 must say so.
func TestSpecSwarmSaysTheTokenBudgetIsAdvisoryUnderAHostileHarness(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-WORKER.md"))
	require.NoError(t, err)
	spec := string(raw)
	_, after, ok := strings.Cut(spec, "11. **The swarm's own tokens are budgeted per job.**")
	require.True(t, ok, "docs/SPEC-WORKER.md has no rule 11 paragraph")
	if i := strings.Index(after, "\n\n"); i >= 0 {
		after = after[:i]
	}
	for _, word := range []string{"advisory", "deadline", "hostile"} {
		assert.Contains(t, after, word, "rule 11 does not say %q", word)
	}
}

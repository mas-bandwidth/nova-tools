package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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
	line, err := readVersionLineWithin(answers, 30*time.Second, time.Second)
	if err != nil || line != "nova-swarm v1 stamp" {
		t.Fatalf("a binary that answers: got (%q, %v)", line, err)
	}

	hangs := filepath.Join(dir, "hangs")
	if err := testbin.WriteExecutable(hangs, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	line, err = readVersionLineWithin(hangs, 200*time.Millisecond, 50*time.Millisecond)
	if err == nil || err.Error() != "timed out after 200ms" || line != "" {
		t.Errorf("a hung binary: got (%q, %v), want an empty line and \"timed out after 200ms\"", line, err)
	}
}

// Production's reader carries a bounded deadline.
func TestDoctorVersionDeadlineIsBounded(t *testing.T) {
	t.Parallel()
	if doctorVersionDeadline <= 0 || doctorVersionDeadline > 30*time.Second {
		t.Errorf("doctorVersionDeadline = %s, want a few seconds", doctorVersionDeadline)
	}
}

// doctorStubs lays out a PATH binary and a ~/.local/bin copy as real scripts and returns an
// environment that reads them with the real reader under a short injected deadline. An empty
// script leaves that binary out: an empty pathScript makes PATH answer with a path that is
// not there, and an empty localScript installs no copy.
func doctorStubs(t *testing.T, pathScript, localScript string) (env doctorEnv, pathBin, localBin string) {
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
			return readVersionLineWithin(p, time.Second, 100*time.Millisecond)
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
		wantExit    int
		contains    []string
		absent      []string
	}{
		{"hang", "exec sleep 30", good, 2,
			[]string{"DOCTOR UNREADABLE", "path=", "timed out after 1s", "stamp=nova-swarm good-stamp", "by hand"},
			[]string{"DOCTOR DRIFT", "shadows"}},
		{"print then hang: the stamp is compared and the hang reported", "echo 'nova-swarm stale-stamp'; exec sleep 30", good, 2,
			[]string{"DOCTOR DRIFT path=", "stale-stamp", "shadows", "DOCTOR UNREADABLE", "timed out after 1s"}, nil},
		{"print then exit 3: the stamp is compared and the exit reported", "echo 'nova-swarm stale-stamp'; exit 3", good, 2,
			[]string{"DOCTOR DRIFT path=", "stale-stamp", "shadows", "DOCTOR UNREADABLE", "exited 3"}, nil},
		{"print then exit 3 with the same stamp", "echo 'nova-swarm good-stamp'; exit 3", good, 2,
			[]string{"DOCTOR UNREADABLE", "exited 3"}, []string{"DOCTOR DRIFT"}},
		{"printed nothing", "exit 0", good, 2,
			[]string{"DOCTOR UNREADABLE", "printed nothing", "stamp=nova-swarm good-stamp"}, nil},
		{"missing where PATH names it", "", good, 2,
			[]string{"DOCTOR UNREADABLE", "not found", "stamp=nova-swarm good-stamp"}, nil},
		{"the local copy hangs", good, "exec sleep 30", 2,
			[]string{"DOCTOR UNREADABLE", "local=", "timed out after 1s", "stamp=nova-swarm good-stamp"}, []string{"DOCTOR DRIFT"}},
		{"both answer and agree", good, good, 0, nil, []string{"DOCTOR"}},
		{"no local copy is tolerated", good, "", 0, nil, []string{"DOCTOR"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			env, _, _ := doctorStubs(t, c.pathScript, c.localScript)
			var errOut bytes.Buffer
			code, stop := env.preflight([]string{"batch", "--tokens", "unmetered"}, &errOut)
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
			var out, derr bytes.Buffer
			dcode := env.cmdDoctor(nil, &out, &derr)
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

// reapRecordedChildren ends every process a stub recorded in pidFile (one pid per line, one
// line per run of the stub) and fails the test if any of them is still alive afterwards. The
// stub's children are orphans, so they cannot be waited for; a process that is gone answers a
// signal-0 with ESRCH, and the check spins on that under a bound rather than sleeping.
func reapRecordedChildren(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Errorf("the stub recorded no child pid: %v", err)
		return
	}
	var pids []int
	for _, field := range strings.Fields(string(raw)) {
		pid, err := strconv.Atoi(field)
		if err != nil || pid <= 1 {
			t.Errorf("the pid file holds %q, want one pid per line", field)
			continue
		}
		pids = append(pids, pid)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, pid := range pids {
		for syscall.Kill(pid, 0) == nil && ctx.Err() == nil {
			runtime.Gosched()
		}
		if syscall.Kill(pid, 0) == nil {
			t.Errorf("the stub's background child %d is still alive after it was killed", pid)
		}
	}
}

// A binary that prints its stamp, exits 0 and leaves a background child holding the output
// pipe has answered: the wait for the pipe gives up after the grace and the stamp is read.
// The stub runs once for the preflight and once for the doctor verb, and each run appends
// its child's pid, so the cleanup ends every child, not the last; the child's own sleep is
// short, so a test process that dies before its cleanup leaves nothing for long.
func TestPreflightReadsABinaryWhoseChildHoldsThePipe(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "children.pid")
	env, _, _ := doctorStubs(t,
		"echo 'nova-swarm good-stamp'; sleep 5 & echo $! >> "+pidFile,
		"echo 'nova-swarm good-stamp'")
	t.Cleanup(func() { reapRecordedChildren(t, pidFile) })
	var errOut bytes.Buffer
	if code, stop := env.preflight([]string{"native", "--card", "c"}, &errOut); stop || code != 0 {
		t.Fatalf("preflight(exit=%d, stop=%v), want (0, false)\n%s", code, stop, errOut.String())
	}
	var out, derr bytes.Buffer
	if code := env.cmdDoctor(nil, &out, &derr); code != 0 || out.String() != "DOCTOR OK stamp=nova-swarm good-stamp\n" {
		t.Errorf("doctor: exit %d, stdout %q, stderr %q", code, out.String(), derr.String())
	}
	if raw, err := os.ReadFile(pidFile); err != nil || len(strings.Fields(string(raw))) != 2 {
		t.Errorf("the stub ran twice and should have recorded two children, got %q (%v)", raw, err)
	}
}

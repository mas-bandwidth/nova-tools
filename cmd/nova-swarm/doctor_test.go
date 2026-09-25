package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// doctorFake swaps the three seams for the life of one test: where nova-swarm is looked up
// on PATH, where home is, and what a binary at a path reports for `version`.
func doctorFake(t *testing.T, lines map[string]string, lookPath func(string) (string, error), home string) {
	t.Helper()
	savedLookPath, savedHome, savedRead := doctorLookPath, doctorHomeDir, doctorReadVersion
	doctorLookPath = lookPath
	doctorHomeDir = func() (string, error) { return home, nil }
	doctorReadVersion = func(path string) (string, error) {
		if line, ok := lines[path]; ok {
			return line, nil
		}
		return "", errors.New("no such binary")
	}
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
	doctorFake(t,
		map[string]string{
			"/opt/go/bin/nova-swarm":         doctorRebuiltLine,
			"/home/me/.local/bin/nova-swarm": doctorRebuiltLine,
		},
		noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := cmdDoctor([]string{"--path", "/opt/go/bin/nova-swarm", "--local", "/home/me/.local/bin/nova-swarm"}, &out, &errOut)
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
	doctorFake(t,
		map[string]string{
			"/opt/go/bin/nova-swarm":         doctorStaleLine,
			"/home/me/.local/bin/nova-swarm": doctorRebuiltLine,
		},
		noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := cmdDoctor([]string{"--path", "/opt/go/bin/nova-swarm", "--local", "/home/me/.local/bin/nova-swarm"}, &out, &errOut)
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
	doctorFake(t,
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
	if code := cmdDoctor(nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "DOCTOR OK stamp=") {
		t.Errorf("not the OK line: %q", out.String())
	}
}

// PATH's nova-swarm IS ~/.local/bin/nova-swarm: there is no second binary and so nothing to
// compare, even when no version can be read. This is the healthy machine whose PATH already
// prefers the rebuilt install.
func TestDoctorOKWhenPATHResolvesToTheLocalBinary(t *testing.T) {
	doctorFake(t, map[string]string{}, noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := cmdDoctor([]string{"--path", "/home/me/.local/bin/nova-swarm", "--local", "/home/me/.local/bin/nova-swarm"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("wrote to stderr: %q", errOut.String())
	}
}

// NO ~/.local/bin COPY means there is nothing that could be shadowed: the guard cannot
// invent a mismatch, so it is OK and reports the stamp it did read.
func TestDoctorOKWhenTheLocalBinaryIsAbsent(t *testing.T) {
	doctorFake(t,
		map[string]string{"/opt/go/bin/nova-swarm": doctorStaleLine},
		noPath, "/home/me")

	var out, errOut bytes.Buffer
	code := cmdDoctor([]string{"--path", "/opt/go/bin/nova-swarm"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
	if want := "DOCTOR OK stamp=" + doctorStaleLine + "\n"; out.String() != want {
		t.Errorf("OK line:\n got %q\nwant %q", out.String(), want)
	}
}

// THE LAUNCH SEAM. `run` and `native` are the verbs that start a card, and the preflight is
// what main calls before the dispatcher: a shadowed pair stops the launch with exit 2
// before anything is spent. A verb that starts nothing is untouched.
func TestPreflightRefusesALaunchUnderAShadowedBinary(t *testing.T) {
	doctorFake(t,
		map[string]string{
			"/opt/go/bin/nova-swarm": doctorStaleLine,
			doctorLocal("/home/me"):  doctorRebuiltLine,
		},
		func(string) (string, error) { return "/opt/go/bin/nova-swarm", nil }, "/home/me")

	var errOut bytes.Buffer
	code, stop := preflightDoctor([]string{"run", "--pool", "p", "--workers", "1"}, &errOut)
	if !stop || code != 2 {
		t.Fatalf("preflight(exit=%d, stop=%v), want (2, true)", code, stop)
	}
	both := errOut.String()
	if !strings.Contains(both, doctorStaleLine) || !strings.Contains(both, doctorRebuiltLine) {
		t.Errorf("the preflight prints both stamps:\n%s", both)
	}
}

func TestPreflightLeavesNonLaunchVerbsAlone(t *testing.T) {
	doctorFake(t,
		map[string]string{
			"/opt/go/bin/nova-swarm": doctorStaleLine,
			doctorLocal("/home/me"):  doctorRebuiltLine,
		},
		func(string) (string, error) { return "/opt/go/bin/nova-swarm", nil }, "/home/me")

	var errOut bytes.Buffer
	if code, stop := preflightDoctor([]string{"status", "--pool", "p"}, &errOut); stop || code != 0 {
		t.Fatalf("status: preflight(exit=%d, stop=%v), want (0, false)", code, stop)
	}
	if errOut.Len() != 0 {
		t.Errorf("a verb that starts nothing was refused: %q", errOut.String())
	}
}

// The verb is reachable from the dispatcher, and `doctor` is not a launch verb itself, so
// running it does not recurse.
func TestDoctorVerbIsReachableFromTheDispatch(t *testing.T) {
	doctorFake(t,
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

// An unreadable override is not silently ignored: `--path`/`--local` name binaries and a
// path that does not exist when the pair is compared is OK only because there is nothing to
// shadow, which the local-absent test covers. This one pins that a broken PATH reader
// cannot crash the verb.
func TestDoctorSurvivesAnUnreadablePATHBinary(t *testing.T) {
	doctorFake(t,
		map[string]string{"/home/me/.local/bin/nova-swarm": doctorRebuiltLine},
		noPath, "/home/me")
	var out, errOut bytes.Buffer
	code := cmdDoctor([]string{"--path", "/opt/go/bin/gone", "--local", "/home/me/.local/bin/nova-swarm"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, errOut.String())
	}
}

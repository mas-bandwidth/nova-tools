package main

import (
	"bytes"
	"errors"
	"flag"
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

// THE LAUNCH SEAM. `batch` and `native` are the verbs that start a card, and the preflight is
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
	code, stop := preflightDoctor([]string{"native", "--dir", "d"}, &errOut)
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
	if code, stop := preflightDoctor([]string{"batch", "--id", "-h"}, &errOut); !stop || code != 2 {
		t.Errorf("batch --id -h: preflight(exit=%d, stop=%v), want (2, true)", code, stop)
	}
	errOut.Reset()
	if code, stop := preflightDoctor([]string{"native", "--card", "-h"}, &errOut); !stop || code != 2 {
		t.Errorf("native --card -h: preflight(exit=%d, stop=%v), want (2, true)", code, stop)
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
	if code, stop := preflightDoctor([]string{"template", "--name", "read-pr"}, &errOut); stop || code != 0 {
		t.Fatalf("template: preflight(exit=%d, stop=%v), want (0, false)", code, stop)
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

// TestLaunchFlagSetParityWithBatchAndNative permanently enforces that launchFlagSet's
// mirror for batch and native matches the real FlagSets declared by the commands.
func TestLaunchFlagSetParityWithBatchAndNative(t *testing.T) {
	t.Parallel()

	checkParity := func(verb string, realFS *flag.FlagSet) {
		t.Helper()
		mirrorFS := launchFlagSet(verb)

		realFlags := make(map[string]bool)
		realFS.VisitAll(func(f *flag.Flag) {
			realFlags[f.Name] = true
		})

		mirrorFlags := make(map[string]bool)
		mirrorFS.VisitAll(func(f *flag.Flag) {
			mirrorFlags[f.Name] = true
		})

		isBool := func(fs *flag.FlagSet, name string) bool {
			fl := fs.Lookup(name)
			if fl == nil {
				return false
			}
			bf, ok := fl.Value.(interface{ IsBoolFlag() bool })
			return ok && bf.IsBoolFlag()
		}

		for name := range realFlags {
			if !mirrorFlags[name] {
				t.Errorf("%s: real flag %q missing from launchFlagSet mirror", verb, name)
				continue
			}
			if isBool(realFS, name) != isBool(mirrorFS, name) {
				t.Errorf("%s: flag %q boolean status mismatch: real isBool=%v, mirror isBool=%v", verb, name, isBool(realFS, name), isBool(mirrorFS, name))
			}
		}
		for name := range mirrorFlags {
			if !realFlags[name] {
				t.Errorf("%s: mirror flag %q not present in real FlagSet", verb, name)
			}
		}
	}

	bFlags, _ := batchFlagSet()
	checkParity("batch", bFlags.fs)

	nFlags, _ := nativeFlagSet()
	checkParity("native", nFlags.fs)
}


package main

// THE FLAGS THE DOGFOOD COULD NOT FIND.
//
// On 2026-09-18 a non-author drove nova-pulse against a private copy of the queue and wrote
// down every flag the tool refused: `fill --dry-run`, `fill --ssh`, `manager --once`,
// `run --gh-config` -- each of them "flag provided but not defined", each of them a door the
// script had. A flag is not shipped until the CLI takes it, so these ask the CLI.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// refusedFlag says whether the CLI turned an invocation away over the flag itself, which is
// the one failure these tests are about. Everything else -- a missing file, a bench that is
// not there -- is the verb running, which is what we want.
func refusedFlag(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	run(args, &out, &errb, time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC))
	if strings.Contains(errb.String(), "flag provided but not defined") {
		return errb.String()
	}
	return ""
}

// TestTheFlagsTheDogfoodAskedFor: every flag the manager dogfood found missing is defined.
func TestTheFlagsTheDogfoodAskedFor(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"fill", "--ready", dir, "--launched", dir, "--machines", dir, "--dry-run", "--once"},
		{"fill", "--ready", dir, "--launched", dir, "--machines", dir, "--ssh", "/usr/bin/ssh", "--once"},
		{"fill", "--ready", dir, "--launched", dir, "--machines", dir, "--gh-config", dir, "--once"},
		{"fill", "--ready", dir, "--launched", dir, "--machines", dir, "--repo", "owner/name", "--queue", dir, "--once"},
		{"manager", "--policy", dir, "--queue", dir, "--roots", dir, "--bus", dir, "--as", "Rowan", "--once"},
		{"manager", "--policy", dir, "--queue", dir, "--roots", dir, "--bus", dir, "--as", "Rowan", "--once", "--dry-run"},
		{"run", "--queue", dir, "--roots", dir, "--once", "--gh-config", dir},
		{"run", "--queue", dir, "--roots", dir, "--once", "--machines", dir, "--lanes", dir},
		{"loop", "--queue", dir, "--machines", dir, "--lanes", dir, "--roots", dir, "--once", "--dry-run", "--gh-config", dir},
	} {
		if said := refusedFlag(t, args...); said != "" {
			t.Errorf("nova-pulse %s: %s", strings.Join(args, " "), said)
		}
	}
}

// TestLoopVerbIsAVerb: `nova-pulse loop` runs, refuses the tables it may not guess, and
// appears in the help. A verb the help lists must run, and a verb that runs must be listed.
func TestLoopVerbIsAVerb(t *testing.T) {
	var help bytes.Buffer
	if code := run([]string{"help"}, &help, &bytes.Buffer{}, time.Now()); code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	if !strings.Contains(help.String(), "nova-pulse loop") {
		t.Fatalf("the help does not list the loop verb")
	}

	dir := t.TempDir()
	var out, errb bytes.Buffer
	// No --machines: the registry is the one thing the loop may not guess, because it is
	// what tells a bench from a CI runner host.
	code := run([]string{"loop", "--queue", dir, "--lanes", dir, "--roots", dir, "--once"}, &out, &errb, time.Now())
	if code != 2 {
		t.Fatalf("loop with no --machines exited %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--machines") {
		t.Fatalf("the refusal does not name --machines: %q", errb.String())
	}
}

// TestLoopGhConfigReachesTheChildren: --gh-config is line 4 of bin/pulse-loop.sh. gh answers
// as whoever GH_CONFIG_DIR says, so a loop started from a service manager with a bare
// environment pushed and merged as nobody in particular.
func TestLoopGhConfigReachesTheChildren(t *testing.T) {
	before, had := os.LookupEnv("GH_CONFIG_DIR")
	t.Cleanup(func() {
		if had {
			os.Setenv("GH_CONFIG_DIR", before)
			return
		}
		os.Unsetenv("GH_CONFIG_DIR")
	})
	want := filepath.Join(t.TempDir(), "gh-rowan")
	if err := applyGhConfig(want); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("GH_CONFIG_DIR"); got != want {
		t.Fatalf("GH_CONFIG_DIR = %q, want %q", got, want)
	}
	// Left out, the caller's own identity goes through untouched.
	if err := applyGhConfig(""); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("GH_CONFIG_DIR"); got != want {
		t.Fatalf("an empty --gh-config overwrote the caller's own identity: %q", got)
	}
}

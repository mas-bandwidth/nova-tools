package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The onboarding standard (ONBOARDING.md), pinned for this binary: the usage banner's
// examples are RUN rather than read, the refusals a first run hits say what the flag WANTS
// and name every independent problem in one go, and the README transcript's shape is
// compared against what the tool actually prints. Guidance nothing checks rots into a claim
// about a message that has since moved.

func runSwarm(t *testing.T, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit = run(args, strings.NewReader(""), &out, &errb, time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC))
	return exit, out.String(), errb.String()
}

// localize points an example or a transcript command at a pool this test makes, so what is
// under test is the command's SHAPE and not the reader's directory layout. Nothing here
// reaches outside t.TempDir().
func localize(t *testing.T, pool string, args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		if a == "./pool" {
			out[i] = pool
		}
	}
	return out
}

func examples(t *testing.T) []string {
	t.Helper()
	exit, stdout, stderr := runSwarm(t, "help")
	if exit != 0 {
		t.Fatalf("`nova-swarm help` must print the usage and exit 0, got %d; stderr: %s", exit, stderr)
	}
	lines, err := onboarding.ExampleLines(stdout, "nova-swarm")
	if err != nil {
		t.Fatalf("%s\n\n%s", err, stdout)
	}
	return lines
}

// (a) The usage banner ends in an `example:` block of lines that actually run. They are run
// here: an example that has drifted out of the flag set teaches the wrong invocation to
// exactly the reader who cannot tell.
func TestUsageBannerExamplesRun(t *testing.T) {
	pool := filepath.Join(t.TempDir(), "pool")
	for _, ex := range examples(t) {
		exit, stdout, stderr := runSwarm(t, localize(t, pool, strings.Fields(ex)[1:])...)
		if exit == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr)
			continue
		}
		if exit != 0 {
			t.Errorf("the usage example %q ran but said NO (exit %d)\nstderr: %s", ex, exit, stderr)
		}
		if stdout == "" {
			t.Errorf("the usage example %q printed nothing on stdout", ex)
		}
	}
}

// (b) A bare invocation costs ONE line and names the door, rather than 60 lines of banner
// on every flag typo.
func TestABareInvocationCostsOneLineAndNamesTheDoor(t *testing.T) {
	exit, stdout, stderr := runSwarm(t)
	if exit != 2 {
		t.Errorf("a bare nova-swarm exits %d, want 2", exit)
	}
	if stdout != "" {
		t.Errorf("a refusal belongs on stderr, got stdout: %q", stdout)
	}
	if lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n"); len(lines) != 1 {
		t.Errorf("a bare nova-swarm printed %d lines, want 1:\n%s", len(lines), stderr)
	}
	if !strings.Contains(stderr, "run: nova-swarm help") {
		t.Errorf("a bare nova-swarm names no door:\n%s", stderr)
	}
}

// (c) The README transcript is compared against what the tool prints -- the event prefixes
// and the field names, never the values, so the transcript stays a document rather than
// becoming a fixture.
func TestTheReadmeTranscriptIsWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-swarm")
	if err != nil {
		t.Fatal(err)
	}
	pool := filepath.Join(t.TempDir(), "pool")
	var want []string
	var printed []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "$ "):
			args := localize(t, pool, strings.Fields(strings.TrimPrefix(line, "$ "))[1:])
			exit, stdout, stderr := runSwarm(t, args...)
			if exit != 0 {
				t.Fatalf("the transcript's `%s` exited %d: %s", line, exit, stderr)
			}
			for _, out := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
				if shape := onboarding.Shape(out); shape != "" {
					printed = append(printed, shape)
				}
			}
		default:
			if shape := onboarding.Shape(line); shape != "" {
				want = append(want, shape)
			}
		}
	}
	if len(want) == 0 {
		t.Fatal("the transcript holds no event line")
	}
	if strings.Join(want, "\n") != strings.Join(printed, "\n") {
		t.Errorf("the README transcript and the tool disagree.\ntranscript:\n%s\n\nprinted:\n%s",
			strings.Join(want, "\n"), strings.Join(printed, "\n"))
	}
}

package main

// The wait verb's command line. These are the cases Go's flag package would have got wrong
// -- a condition with arguments followed by more flags, and a command after a bare -- --
// which is why the parsing is by hand.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runWaitCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"wait"}, args...), &out, &errb, time.Now().UTC())
	return code, out.String(), errb.String()
}

// The shape Go's flag package cannot parse: --until takes a condition AND its arguments,
// and --every comes after them. The flag package stops at the first non-flag word, so
// --every would have been a positional argument nobody read and the verb would have polled
// at its default for thirty minutes without saying so.
func TestWaitReadsFlagsAfterTheConditionsArguments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := filepath.Join(dir, "harvest.log")
	if err := os.WriteFile(log, []byte("LANDED #1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errb := runWaitCLI(t, "--until", "file-has", log, `LANDED #[0-9]+`, "--every", "1s", "--timeout", "5s")
	if code != 0 {
		t.Fatalf("want 0, got %d: %s", code, errb)
	}
	if !strings.Contains(out, "WAIT HELD until=file-has") || !strings.Contains(out, "LANDED #1234") {
		t.Fatalf("receipt: %s", out)
	}
}

// The same line with a timeout that must actually be READ. The assertion is the RECEIPT,
// not the clock: the receipt prints back the bound the verb is using, so `timeout=1s` says
// the flag after the condition's argument was read, where a wall-clock measurement would
// only say this machine was quick.
func TestWaitHonoursATimeoutGivenAfterTheCondition(t *testing.T) {
	t.Parallel()
	code, _, errb := runWaitCLI(t, "--until", "file-exists", filepath.Join(t.TempDir(), "never"), "--every", "100ms", "--timeout", "1s")
	if code != 2 {
		t.Fatalf("want 2, got %d", code)
	}
	if !strings.Contains(errb, "WAIT TIMEOUT") || !strings.Contains(errb, "timeout=1s") {
		t.Fatalf("the receipt must carry the bound the verb used, not the default 30m: %s", errb)
	}
}

// Everything after a bare -- is the command, verbatim, including its own flags.
func TestWaitPassesTheCommandAfterTheSeparatorThrough(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	there := filepath.Join(dir, "there")
	if err := os.WriteFile(there, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := filepath.Join(dir, "ran")
	code, out, errb := runWaitCLI(t, "--until", "file-exists", there, "--timeout", "5s",
		"--", "/bin/sh", "-c", "echo ran > "+stamp)
	if code != 0 {
		t.Fatalf("want the command's 0, got %d: %s", code, errb)
	}
	if _, err := os.Stat(stamp); err != nil {
		t.Fatalf("the command after -- did not run: %v (%s)", err, out)
	}
}

// The command's exit status is the verb's.
func TestWaitCarriesTheCommandsExitStatus(t *testing.T) {
	t.Parallel()
	there := filepath.Join(t.TempDir(), "there")
	if err := os.WriteFile(there, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ := runWaitCLI(t, "--until", "file-exists", there, "--timeout", "5s", "--", "/bin/sh", "-c", "exit 7")
	if code != 7 {
		t.Fatalf("a command that exited 7 must make the verb exit 7, got %d", code)
	}
}

func TestWaitCommandLineRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		says string
	}{
		{"no condition at all", nil, "--until is required"},
		{"an unknown flag is named, not swallowed", []string{"--until", "file-exists", "/x", "--evry", "5s"}, "unknown flag --evry"},
		{"a positional argument before --until", []string{"file-exists", "/x"}, "takes no positional arguments"},
		{"--every with no value", []string{"--until", "file-exists", "/x", "--every"}, "--every wants a value"},
		{"--every that is not a duration", []string{"--until", "file-exists", "/x", "--every", "soon"}, "--every"},
		{"a bare -- with nothing after it", []string{"--until", "file-exists", "/x", "--"}, "names no command"},
		{"an unknown condition", []string{"--until", "whenever", "x"}, "is not a condition"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, out, errb := runWaitCLI(t, tc.args...)
			if code != 2 {
				t.Fatalf("want 2, got %d (%s)", code, out)
			}
			if !strings.Contains(errb, tc.says) {
				t.Fatalf("the refusal must say %q, got: %s", tc.says, errb)
			}
		})
	}
}

// --every=5s is the same flag as --every 5s.
func TestWaitTakesTheInlineFlagForm(t *testing.T) {
	t.Parallel()
	code, _, errb := runWaitCLI(t, "--until=file-exists", filepath.Join(t.TempDir(), "never"), "--every=100ms", "--timeout=300ms")
	if code != 2 {
		t.Fatalf("want a timeout, got %d: %s", code, errb)
	}
	if !strings.Contains(errb, "WAIT TIMEOUT until=file-exists") {
		t.Fatalf("the inline form named the condition: %s", errb)
	}
}

// The help lists the verb, and every condition it lists is one the verb accepts.
func TestWaitIsInTheHelpAndItsConditionsAreReal(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("help exit %d", code)
	}
	help := out.String()
	if !strings.Contains(help, "nova-pulse wait ") {
		t.Fatal("the help does not list wait; a verb that runs and is not in the help is a verb nobody finds")
	}
	for _, cond := range []string{"process-gone", "file-has", "file-exists", "pr-check", "redis-key", "bus-note"} {
		if !strings.Contains(help, cond) {
			t.Errorf("the help does not name the %s condition", cond)
		}
	}
}

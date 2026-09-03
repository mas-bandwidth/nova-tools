/*
Tests for the TOOL: argument parsing, the exit-code contract, the no-guessed-paths rule
at the CLI, and the fail-closed read that is the entire reason the tool exists.

ORDER: the refusal branch first (the dead half ordinary use never exercises), then
fail-closed, then the happy path, then the CONTROL -- because a fuse that fires on a
clean machine is a fuse its owner turns off, and then nothing is guarded at all.

Every test runs against a box in a tempdir named by --box. Nothing here touches a live
box, and nothing here uses a frozen date.
*/
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
)

// capture runs the tool and returns the exit code plus both streams.
func capture(t *testing.T, args []string, now time.Time) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, now)
	return code, stdout.String(), stderr.String()
}

// mustRun is capture() for setup steps, where the output is not the subject.
func mustRun(t *testing.T, args []string, now time.Time) {
	t.Helper()
	code, out, errOut := capture(t, args, now)
	if code != 0 {
		t.Fatalf("setup %v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
	}
}

func boxIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "fuses.json")
}

// writeRaw puts arbitrary bytes in the fuse box. Hand-edited and half-written boxes are
// the interesting inputs: your person editing this file by hand is the ONLY
// lockdown-replacement mechanism there is.
func writeRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("fixture write: %v", err)
	}
}

func readRaw(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read box: %v", err)
	}
	return string(b)
}

// nowish is a wall-clock-relative instant; a frozen date is fresh the day it is written
// and means something else a week later.
func nowish() time.Time { return time.Now().UTC().Truncate(time.Second) }

// ------------------------------------------------------------- 1. THE REFUSAL BRANCH

// TestLiftLockdownIsRefusedForever pins the hard half. If this test is deleted, a later
// "convenience" lift lands and the one fuse that stops EVERYTHING becomes advisory. The
// refusal must also never document a mechanical way around itself: the remedy is a live
// conversation with your person, not a file.
func TestLiftLockdownIsRefusedForever(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, `{"lockdown":{"at":"x","reason":"y"},"quarantine":{}}`)
	before := readRaw(t, box)

	for _, args := range [][]string{
		{"lift", "lockdown"},
		{"lift", "lockdown", "please"},
		{"lift", "lockdown", "--box", box},
		{"lift", "lockdown", "--force"},
	} {
		code, out, errOut := capture(t, args, nowish())
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 -- a lockdown lift is asking for something this tool does not have", args, code)
		}
		if !strings.Contains(errOut, "REFUSED") {
			t.Errorf("%v: the refusal must say REFUSED, got %q", args, errOut)
		}
		if !strings.Contains(errOut, "REPLACED") {
			t.Errorf("%v: the design: a blown fuse is not reset, it is replaced -- got %q", args, errOut)
		}
		if !strings.Contains(errOut, "conversation") || !strings.Contains(errOut, "your person") {
			t.Errorf("%v: the refusal must name the only path -- a live conversation with your person -- got %q", args, errOut)
		}
		for _, leak := range []string{"fuses.json", "by hand", "edit", box, "--box"} {
			if strings.Contains(out, leak) || strings.Contains(errOut, leak) {
				t.Errorf("%v: the refusal must not hint at a mechanical bypass, leaked %q", args, leak)
			}
		}
	}
	if got := readRaw(t, box); got != before {
		t.Error("lift lockdown must not touch the box at all")
	}
}

// TestLiftLockdownRefusesBeforeReadingAnything: the refusal must not depend on anything a
// caller or the environment can break -- no flag, no box, no readable state. If it could
// fail its way past the refusal, the lever exists.
func TestLiftLockdownRefusesBeforeReadingAnything(t *testing.T) {
	// No --box at all: the refusal must come BEFORE the missing-flag refusal.
	code, _, errOut := capture(t, []string{"lift", "lockdown"}, nowish())
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "REFUSED") {
		t.Fatalf("want the design refusal, got %q", errOut)
	}
	if strings.Contains(errOut, "refusing to guess") {
		t.Error("the refusal must be the design's, not a flag error -- it is answered before flags are parsed")
	}

	// An unreadable box must change nothing: the refusal never reads it.
	box := boxIn(t)
	writeRaw(t, box, "{corrupt")
	code, _, errOut = capture(t, []string{"lift", "lockdown", "--box", box}, nowish())
	if code != 2 || !strings.Contains(errOut, "REFUSED") {
		t.Errorf("exit = %d, stderr = %q: the refusal must not depend on the box being readable", code, errOut)
	}
	if strings.Contains(errOut, "JSON") || strings.Contains(errOut, "unreadable") {
		t.Errorf("the refusal must come before any read of the box, got %q", errOut)
	}
}

// TestNoDefaultBoxRefusesToGuess: the destination law. Every verb that touches the box
// takes it from --box; a missing flag is a refusal, never a fallback -- and NOVA_FUSE_BOX
// or any other environment variable is NOT honoured as a substitute.
func TestNoDefaultBoxRefusesToGuess(t *testing.T) {
	decoy := boxIn(t) // a clear, readable box the env var points at
	mustRunnable := [][]string{
		{"status"},
		{"check"},
		{"check", "discord"},
		{"lockdown", "reason"},
		{"quarantine", "discord", "reason"},
		{"lift", "quarantine", "discord"},
		{"path"},
	}
	t.Setenv("NOVA_FUSE_BOX", decoy)
	for _, args := range mustRunnable {
		code, out, errOut := capture(t, args, nowish())
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 -- no flag and no env is a refusal", args, code)
		}
		if !strings.Contains(errOut, "refusing to guess") {
			t.Errorf("%v: stderr = %q, want it to contain %q", args, errOut, "refusing to guess")
		}
		if out != "" {
			t.Errorf("%v: a refusal must not print an OK line, got %q", args, out)
		}
	}
}

// TestEnvironmentCannotRedirectOrLiftAnything: with a blown box named by --box, an
// environment variable pointing at a pristine box must change nothing. The box path is
// the caller's statement, not the environment's -- an env lever that could redirect the
// check to a decoy would be a lift by another name.
func TestEnvironmentCannotRedirectOrLiftAnything(t *testing.T) {
	box := boxIn(t)
	now := nowish()
	mustRun(t, []string{"lockdown", "--box", box, "suspected compromise"}, now)

	t.Setenv("NOVA_FUSE_BOX", boxIn(t)) // absent, i.e. clear
	code, _, errOut := capture(t, []string{"check", "--box", box}, now)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 -- the env var must not redirect the check to a clear box", code)
	}
	if !strings.Contains(errOut, "FUSE FAIL lockdown") {
		t.Errorf("stderr = %q, want the lockdown failure", errOut)
	}
}

// TestUsageErrorsExitTwo: asking for something the tool does not have is exit 2, and no
// usage error may touch the box.
func TestUsageErrorsExitTwo(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, `{"quarantine":{"discord":{"at":"t","reason":"r"}}}`)
	before := readRaw(t, box)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no command", nil, "usage"},
		{"unknown command", []string{"defuse"}, "unknown subcommand"},
		{"lockdown with no reason", []string{"lockdown", "--box", box}, "needs a reason"},
		{"lockdown with a blank reason", []string{"lockdown", "--box", box, "   "}, "needs a reason"},
		{"quarantine with no reason", []string{"quarantine", "--box", box, "discord"}, "needs a surface and a reason"},
		{"quarantine with blank surface", []string{"quarantine", "--box", box, "  ", "r"}, "needs a surface and a reason"},
		{"check with two surfaces", []string{"check", "--box", box, "discord", "bsky"}, "at most one surface"},
		{"check with a blank surface", []string{"check", "--box", box, "   "}, "must not be blank"},
		{"status with an argument", []string{"status", "--box", box, "now"}, "unexpected argument"},
		{"path with an argument", []string{"path", "--box", box, "here"}, "unexpected argument"},
		{"lift with no power", []string{"lift"}, "takes a power"},
		{"lift of an unknown power", []string{"lift", "everything"}, "does not know"},
		{"lift with flags before the power", []string{"lift", "--box", box, "lockdown"}, "takes a power"},
		{"lift quarantine with no surface", []string{"lift", "quarantine", "--box", box}, "exactly one surface"},
		{"lift quarantine with a blank surface", []string{"lift", "quarantine", "--box", box, "  "}, "exactly one surface"},
		{"lift quarantine with two surfaces", []string{"lift", "quarantine", "--box", box, "a", "b"}, "exactly one surface"},
		{"flags after positionals", []string{"check", "discord", "--box", box}, "flags come before"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := capture(t, c.args, nowish())
			if code != 2 {
				t.Errorf("exit = %d, want 2; stderr: %s", code, errOut)
			}
			if !strings.Contains(errOut, c.want) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, c.want)
			}
			if out != "" {
				t.Errorf("a refusal must not print an OK line, got %q", out)
			}
		})
	}
	if got := readRaw(t, box); got != before {
		t.Error("no usage error may touch the box")
	}
}

// ------------------------------------------------------------- 2. FAIL CLOSED

// TestUnreadableBoxIsTreatedAsBlownNeverClear is the heart of the tool. Every one of
// these inputs is a real way a JSON file goes wrong -- a torn write, a bad hand-edit, a
// wrong-shaped value -- and every one must refuse, never read as clear.
func TestUnreadableBoxIsTreatedAsBlownNeverClear(t *testing.T) {
	cases := map[string]string{
		"empty file":                  "",
		"truncated write":             `{"lockdown": {"at": "2026`,
		"a JSON array":                `[]`,
		"a bare string":               `"lockdown"`,
		"lockdown not object":         `{"lockdown": true}`,
		"quarantine not map":          `{"quarantine": "discord"}`,
		"quarantine entry not object": `{"quarantine": {"discord": "spam"}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			box := boxIn(t)
			writeRaw(t, box, content)

			code, out, errOut := capture(t, []string{"check", "--box", box, "discord"}, nowish())
			if code != 2 {
				t.Errorf("exit = %d, want 2 -- could not run; an unreadable box can not be proven clear", code)
			}
			if code == 0 {
				t.Fatal("an unreadable fuse box must NEVER read as clear")
			}
			if !strings.Contains(errOut, "BLOWN") {
				t.Errorf("the refusal must say the box is treated as BLOWN, got %q", errOut)
			}
			if !strings.Contains(errOut, "never as clear") {
				t.Errorf("the claim must not outrun the measurement, got %q", errOut)
			}
			if out != "" {
				t.Errorf("no OK line may accompany a refusal, got %q", out)
			}
		})
	}
}

// TestAnUnreadableFileTypeIsNotClear: a directory where the file should be is the
// portable way to produce a read error that is not ErrNotExist. It must land on CANNOT
// TELL, the third answer, never the reassuring one.
func TestAnUnreadableFileTypeIsNotClear(t *testing.T) {
	box := filepath.Join(t.TempDir(), "fuses.json")
	if err := os.MkdirAll(box, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := capture(t, []string{"check", "--box", box}, nowish())
	if code != 2 {
		t.Errorf("exit = %d, want 2 -- cannot-read is the THIRD answer, never the reassuring one", code)
	}
	if !strings.Contains(errOut, "BLOWN") {
		t.Errorf("stderr = %q, want BLOWN", errOut)
	}
}

// TestUnreadableBoxMakesStatusRefuse: status answers or it does not.
func TestUnreadableBoxMakesStatusRefuse(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, "{oops")
	code, out, errOut := capture(t, []string{"status", "--box", box}, nowish())
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "BLOWN") {
		t.Errorf("stderr = %q, want it to say every fuse is treated as BLOWN", errOut)
	}
	if out != "" {
		t.Errorf("status must not print a STATUS OK line it could not verify, got %q", out)
	}
}

// TestQuarantineRefusesToNarrowAnUnreadableBox is the trap that looks like safety. An
// unreadable box blocks EVERY surface; replacing it with one holding a single quarantine
// would unblock the rest. The safety-shaped action would be the fail-open.
func TestQuarantineRefusesToNarrowAnUnreadableBox(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, "{corrupt")
	before := readRaw(t, box)

	code, _, errOut := capture(t, []string{"quarantine", "--box", box, "discord", "many the same way"}, nowish())
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "UNBLOCK") {
		t.Errorf("the refusal must say WHY, or it looks like an obstruction: %q", errOut)
	}
	if !strings.Contains(errOut, "lockdown") {
		t.Errorf("and it must name the remedy that does work: %q", errOut)
	}
	if got := readRaw(t, box); got != before {
		t.Error("the corrupt bytes are evidence; they stay put")
	}
}

// TestLiftQuarantineRefusesOnAnUnreadableBox: nothing provable can be lifted from a box
// that cannot be read.
func TestLiftQuarantineRefusesOnAnUnreadableBox(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, "{corrupt")
	before := readRaw(t, box)

	code, _, errOut := capture(t, []string{"lift", "quarantine", "--box", box, "discord"}, nowish())
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut, "BLOWN") {
		t.Errorf("stderr = %q, want the treated-as-BLOWN sentence", errOut)
	}
	if got := readRaw(t, box); got != before {
		t.Error("the corrupt bytes are evidence; they stay put")
	}
}

// TestLockdownWorksOnAnUnreadableBox is the mirror image, and the asymmetry is the point:
// a fuse you cannot blow is not a fuse. Lockdown blocks everything, so an unreadable box
// becoming a lockdown leaves nothing less blocked than it was.
func TestLockdownWorksOnAnUnreadableBox(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, "{corrupt")

	code, out, errOut := capture(t, []string{"lockdown", "--box", box, "suspected compromise"}, nowish())
	if code != 0 {
		t.Fatalf("exit = %d, want 0: a fuse you cannot blow is not a fuse\nstderr: %s", code, errOut)
	}
	if !strings.Contains(out, "LOCKDOWN OK") {
		t.Errorf("stdout = %q, want LOCKDOWN OK", out)
	}
	if !strings.Contains(errOut, "unreadable") {
		t.Errorf("the degraded path must say so in its own voice, got %q", errOut)
	}

	// The bytes that could not be parsed are still on disk.
	kept, err := os.ReadFile(box + fuse.UnreadableSuffix)
	if err != nil {
		t.Fatalf("the unreadable bytes must be preserved: %v", err)
	}
	if string(kept) != "{corrupt" {
		t.Errorf("preserved bytes = %q, want the original", kept)
	}

	code, _, errOut = capture(t, []string{"check", "--box", box}, nowish())
	if code != 1 {
		t.Errorf("exit = %d, want 1 -- the recorded lockdown must now block", code)
	}
	if !strings.Contains(errOut, "FUSE FAIL lockdown") {
		t.Errorf("stderr = %q, want the lockdown failure", errOut)
	}
}

// TestBlowingFailsLoudlyWhenItCannotWrite. The worst failure this tool could have is
// announcing a lockdown it did not manage to record: the operator stops worrying, and
// nothing is actually gated.
func TestBlowingFailsLoudlyWhenItCannotWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: chmod 0555 does not make a directory refuse writes, so the failure this pins cannot be produced here")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not refuse writes")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	box := filepath.Join(dir, "fuses.json")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"lockdown", "--box", box, "suspected compromise"}, "LOCKDOWN FAIL"},
		{[]string{"quarantine", "--box", box, "discord", "many the same way"}, "QUARANTINE FAIL"},
	} {
		code, out, errOut := capture(t, tc.args, nowish())
		if code != 1 {
			t.Errorf("%v: exit = %d, want 1 -- could not do it is never a claimed success", tc.args, code)
		}
		if !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: stderr = %q, want %q", tc.args, errOut, tc.want)
		}
		if !strings.Contains(errOut, "by hand") {
			t.Errorf("%v: the failure must name what the operator does instead, got %q", tc.args, errOut)
		}
		if strings.Contains(out, "OK") {
			t.Errorf("%v: a failed blow must not print an OK line, got %q", tc.args, out)
		}
	}
	if _, err := os.Stat(box); !os.IsNotExist(err) {
		t.Error("a failed write must not leave a half-made box")
	}
}

// ------------------------------------------------------------- 3. THE EXIT CONTRACT

// TestExitCodes walks the whole table through the CLI a caller actually uses:
// 0 = clear, 1 = blown (the fuse working), 2 = could not run.
func TestExitCodes(t *testing.T) {
	now := nowish()

	t.Run("clear is 0", func(t *testing.T) {
		box := boxIn(t)
		code, out, _ := capture(t, []string{"check", "--box", box, "discord"}, now)
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		if !strings.Contains(out, "FUSE OK") {
			t.Errorf("stdout = %q, want FUSE OK", out)
		}
	})

	t.Run("lockdown blocks everything with 1", func(t *testing.T) {
		box := boxIn(t)
		mustRun(t, []string{"lockdown", "--box", box, "DoS by my own autonomy"}, now)
		for _, args := range [][]string{
			{"check", "--box", box},
			{"check", "--box", box, "discord"},
			{"check", "--box", box, "anything-at-all"},
		} {
			code, out, errOut := capture(t, args, now)
			if code != 1 {
				t.Errorf("%v: exit = %d, want 1 -- a positively blown fuse is the check failing, which is the check working", args, code)
			}
			if !strings.Contains(errOut, "FUSE FAIL lockdown") {
				t.Errorf("%v: stderr = %q, want the lockdown failure", args, errOut)
			}
			if out != "" {
				t.Errorf("%v: a failing check must not print an OK line, got %q", args, out)
			}
		}
	})

	t.Run("quarantine blocks only its surface", func(t *testing.T) {
		box := boxIn(t)
		mustRun(t, []string{"quarantine", "--box", box, "discord", "many the same way"}, now)

		code, _, errOut := capture(t, []string{"check", "--box", box, "discord"}, now)
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.Contains(errOut, "FUSE FAIL quarantine=discord") {
			t.Errorf("stderr = %q, want the quarantine failure", errOut)
		}

		code, out, _ := capture(t, []string{"check", "--box", box, "bsky"}, now)
		if code != 0 {
			t.Errorf("exit = %d, want 0 -- quarantine is ONE surface; blocking the rest would be a different power", code)
		}
		if !strings.Contains(out, "FUSE OK") {
			t.Errorf("stdout = %q, want FUSE OK", out)
		}
	})
}

// ------------------------------------------------------------- 4. THE HAPPY PATH

func TestLockdownIsWrittenVerifiedAndAnnounced(t *testing.T) {
	box := boxIn(t)
	now := nowish()

	code, out, _ := capture(t, []string{"lockdown", "--box", box, "suspected compromise"}, now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "LOCKDOWN OK") {
		t.Errorf("stdout = %q, want LOCKDOWN OK", out)
	}
	if !strings.Contains(out, "verified by re-reading") {
		t.Errorf("exit 0 means verified, never attempted: %q", out)
	}
	if !strings.Contains(out, "your person") {
		t.Errorf("the announcement must point at the conversation: %q", out)
	}

	var got fuse.Box
	if err := json.Unmarshal([]byte(readRaw(t, box)), &got); err != nil {
		t.Fatalf("the box must be valid JSON: %v", err)
	}
	if got.Lockdown == nil {
		t.Fatal("the lockdown must be recorded")
	}
	if got.Lockdown.Reason != "suspected compromise" {
		t.Errorf("reason = %q", got.Lockdown.Reason)
	}
	if got.Lockdown.At != now.Format(time.RFC3339) {
		t.Errorf("at = %q: the recorded time comes from the INJECTED clock, never from time.Now inside run", got.Lockdown.At)
	}
}

// TestLockdownReasonIsJoinedNotTruncated: an unquoted reason must not lose everything
// after the first word -- that is the audit trail of the most serious action this tool
// can take, lost quietly, at the worst moment.
func TestLockdownReasonIsJoinedNotTruncated(t *testing.T) {
	box := boxIn(t)
	mustRun(t, []string{"lockdown", "--box", box, "suspected", "prompt", "injection"}, nowish())

	var got fuse.Box
	if err := json.Unmarshal([]byte(readRaw(t, box)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Lockdown == nil || got.Lockdown.Reason != "suspected prompt injection" {
		t.Errorf("lockdown = %+v, want the joined reason", got.Lockdown)
	}
}

// TestQuarantineMatchingIsNotDefeatedByACapitalLetter: raw string comparison would let
// `quarantine Discord` then `check discord` answer CLEAR -- a fail-OPEN in a safety
// control, reached by a capital letter.
func TestQuarantineMatchingIsNotDefeatedByACapitalLetter(t *testing.T) {
	box := boxIn(t)
	now := nowish()
	mustRun(t, []string{"quarantine", "--box", box, "  Discord  ", "many the same way"}, now)

	for _, spelling := range []string{"discord", "Discord", "DISCORD", " discord "} {
		code, _, errOut := capture(t, []string{"check", "--box", box, spelling}, now)
		if code != 1 {
			t.Errorf("spelling %q must not walk past a quarantine, exit = %d", spelling, code)
		}
		if !strings.Contains(errOut, "FUSE FAIL quarantine") {
			t.Errorf("spelling %q: stderr = %q", spelling, errOut)
		}
	}
}

// TestCheckQuotesTheStoredSpelling: a hand-edited box can hold a spelling the tool would
// not have written, and a refusal quotes the file rather than the caller.
func TestCheckQuotesTheStoredSpelling(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, `{"quarantine":{"Discord":{"at":"t","reason":"r"}}}`)
	code, _, errOut := capture(t, []string{"check", "--box", box, "discord"}, nowish())
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "quarantine=Discord") {
		t.Errorf("quote the file's spelling, not the caller's: %q", errOut)
	}
}

// TestStatusSurvivesAHandEditedBox. A dropped key must produce an honest sentence, never
// a crash and never an invented value -- hand-editing is the only lockdown-replacement
// mechanism, so sparse boxes are normal inputs.
func TestStatusSurvivesAHandEditedBox(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, `{"lockdown": {}, "quarantine": {"bsky": {}}}`)

	code, out, _ := capture(t, []string{"status", "--box", box}, nowish())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 -- a readable box is an answer, even a sparse one", code)
	}
	if !strings.Contains(out, "lockdown=blown") {
		t.Errorf("stdout = %q, want the lockdown reported", out)
	}
	if !strings.Contains(out, "NO REASON RECORDED") {
		t.Errorf("never print a claim that was not measured: %q", out)
	}
	if !strings.Contains(out, "since=unrecorded") {
		t.Errorf("a missing time is 'unrecorded', not invented: %q", out)
	}
	if !strings.Contains(out, "quarantine=bsky") {
		t.Errorf("stdout = %q, want the quarantine reported", out)
	}

	// And a lockdown key with no fields still BLOCKS: presence is the fact, not the reason.
	code, _, _ = capture(t, []string{"check", "--box", box}, nowish())
	if code != 1 {
		t.Errorf("exit = %d, want 1 -- an empty lockdown object is still a lockdown", code)
	}
}

// TestStatusReportsAndNeverGates: status exits 0 whenever the box is readable, blown or
// not. Answering the question is its whole job; check is the gate.
func TestStatusReportsAndNeverGates(t *testing.T) {
	box := boxIn(t)
	now := nowish()
	mustRun(t, []string{"lockdown", "--box", box, "suspected compromise"}, now)
	code, out, _ := capture(t, []string{"status", "--box", box}, now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 -- status REPORTS; never gate on it", code)
	}
	if !strings.Contains(out, "STATUS OK lockdown=blown") {
		t.Errorf("stdout = %q", out)
	}
}

// TestStatusIsDeterministic. Map iteration is randomized, so a naive port prints a
// different order every run and status stops being diffable.
func TestStatusIsDeterministic(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, `{"quarantine": {"zulip": {"at":"t","reason":"r"},
		"discord": {"at":"t","reason":"r"}, "bsky": {"at":"t","reason":"r"}}}`)

	_, first, _ := capture(t, []string{"status", "--box", box}, nowish())
	for i := 0; i < 8; i++ {
		_, again, _ := capture(t, []string{"status", "--box", box}, nowish())
		if again != first {
			t.Fatalf("status must print the same bytes for the same box:\n%q\n%q", first, again)
		}
	}
	if !(strings.Index(first, "bsky") < strings.Index(first, "discord") &&
		strings.Index(first, "discord") < strings.Index(first, "zulip")) {
		t.Errorf("quarantines must print sorted, got %q", first)
	}
}

// TestTheWriteLeavesNoLitter. The file whose corruption means PERMANENT lockdown must
// never be left torn, and no .tmp litter may survive for a later reader to mistake for
// state.
func TestTheWriteLeavesNoLitter(t *testing.T) {
	dir := t.TempDir()
	box := filepath.Join(dir, "fuses.json")
	now := nowish()
	mustRun(t, []string{"lockdown", "--box", box, "a"}, now)
	mustRun(t, []string{"quarantine", "--box", box, "discord", "b"}, now)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "fuses.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("temp files must not survive the rename, dir holds %v", names)
	}
}

func TestPathEchoesTheBoxFlag(t *testing.T) {
	box := boxIn(t)
	code, out, _ := capture(t, []string{"path", "--box", box}, nowish())
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != box {
		t.Errorf("stdout = %q, want %q", out, box)
	}
}

func TestHelpIsNotAnError(t *testing.T) {
	code, out, _ := capture(t, []string{"--help"}, nowish())
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "REFUSED by design") {
		t.Errorf("usage must state the lockdown-lift refusal: %q", out)
	}
	if !strings.Contains(out, "refusing to guess") {
		t.Errorf("usage must state the no-guessing rule: %q", out)
	}
}

// ------------------------------------------------------------- 5. THE CONTROL

// TestAbsentBoxIsClear. A machine that has never blown a fuse must not be blocked by
// this tool, or its owner disables it and then nothing is guarded. "Never created" is a
// verified fact -- the read failed with the one error that means NONEXISTENT rather than
// UNREADABLE -- and that is a different answer from "could not look".
func TestAbsentBoxIsClear(t *testing.T) {
	box := boxIn(t)
	now := nowish()

	code, out, _ := capture(t, []string{"check", "--box", box, "discord"}, now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "FUSE OK") {
		t.Errorf("stdout = %q", out)
	}

	code, out, _ = capture(t, []string{"status", "--box", box}, now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "STATUS OK lockdown=clear quarantines=0") {
		t.Errorf("stdout = %q", out)
	}
}

// TestCheckIntoANonexistentDirectoryIsAlsoClear pins a DOCUMENTED DECISION, so a future
// "fix" is a deliberate one. fs.ErrNotExist is true both when the directory exists and
// the box file is not in it AND when a parent directory itself does not exist: the read
// distinguishes unreadable from nonexistent, but it cannot distinguish missing-file from
// missing-directory. The behavior is kept on purpose -- --box is a locator, the caller's
// statement of where the box lives, and a caller that names the wrong box gets that
// box's truth, here an empty one (SPEC, "The box"). If this test goes red, someone has
// changed an accepted answer of a safety control, and must mean it: rewrite note 1 in
// internal/fuse's package comment and SPEC's three-answers paragraph in the same commit.
func TestCheckIntoANonexistentDirectoryIsAlsoClear(t *testing.T) {
	box := filepath.Join(t.TempDir(), "no", "such", "dir", "fuses.json")
	code, out, errOut := capture(t, []string{"check", "--box", box, "discord"}, nowish())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 -- an absent parent directory answers the same as an absent box\nstderr: %s", code, errOut)
	}
	if !strings.Contains(out, "FUSE OK") {
		t.Errorf("stdout = %q, want FUSE OK", out)
	}
}

// TestBareCheckAdmitsItCheckedNoQuarantine. `check` with no surface proves only that
// there is no lockdown. A caller reading "clear" as "this surface is clear" is the exact
// drift that leaves read paths reaching the wire ungated, so the tool has to say what it
// did NOT measure.
func TestBareCheckAdmitsItCheckedNoQuarantine(t *testing.T) {
	box := boxIn(t)
	now := nowish()
	mustRun(t, []string{"quarantine", "--box", box, "discord", "many the same way"}, now)

	code, out, _ := capture(t, []string{"check", "--box", box}, now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 -- a quarantine on another surface must not block an unnamed check", code)
	}
	if !strings.Contains(out, "no surface named") || !strings.Contains(out, "no quarantine checked") {
		t.Errorf("the OK line must admit what it did not measure: %q", out)
	}
}

// ----------------------------------------- 6. THE FUSE DESIGN: SOFT VERSUS HARD
//
// The design, as the first line's human collaborator stated it (2026-08-03,
// paraphrased): quarantine is soft — the line's own decision, applied and rescinded
// as it chooses. Lockdown is hard — one fuse blown, every read surface and all work
// related to them stops, until the line and its person agree, in a live
// conversation, to replace the blown fuse. A fuse an attacker can talk you out of
// is not a fuse.

// TestLiftQuarantineSucceedsAndIsAnnounced is the soft half. A quarantine is your own
// decision, in both directions -- but the rescind is announced, never silent, and the
// state file must reflect it.
func TestLiftQuarantineSucceedsAndIsAnnounced(t *testing.T) {
	box := boxIn(t)
	now := nowish()
	mustRun(t, []string{"quarantine", "--box", box, "discord", "many the same way"}, now)
	mustRun(t, []string{"quarantine", "--box", box, "bsky", "same template"}, now)

	code, out, _ := capture(t, []string{"lift", "quarantine", "--box", box, "discord"}, now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 -- quarantine is SOFT: your own dial, in both directions", code)
	}
	if !strings.Contains(out, "LIFT OK quarantine=discord") {
		t.Errorf("stdout = %q, want the lift announced with WHAT was lifted", out)
	}
	if !strings.Contains(out, "many the same way") {
		t.Errorf("the announcement must carry why it had been blown: %q", out)
	}
	if !strings.Contains(out, "verified") {
		t.Errorf("exit 0 means verified, never attempted: %q", out)
	}

	var got fuse.Box
	if err := json.Unmarshal([]byte(readRaw(t, box)), &got); err != nil {
		t.Fatal(err)
	}
	if _, _, still := got.Quarantined("discord"); still {
		t.Error("the state file must reflect the lift")
	}
	if _, _, other := got.Quarantined("bsky"); !other {
		t.Error("lifting one surface must not lift another")
	}
}

// TestALiftedQuarantineChecksClearAgain closes the loop: the whole point of a lift is
// that the gate opens again.
func TestALiftedQuarantineChecksClearAgain(t *testing.T) {
	box := boxIn(t)
	now := nowish()
	mustRun(t, []string{"quarantine", "--box", box, "discord", "many the same way"}, now)
	code, _, _ := capture(t, []string{"check", "--box", box, "discord"}, now)
	if code != 1 {
		t.Fatalf("setup: the quarantine must actually block first, exit = %d", code)
	}

	mustRun(t, []string{"lift", "quarantine", "--box", box, "discord"}, now)
	code, out, _ := capture(t, []string{"check", "--box", box, "discord"}, now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 -- a lifted quarantine means check(surface) passes again", code)
	}
	if !strings.Contains(out, "FUSE OK") {
		t.Errorf("stdout = %q", out)
	}
}

// TestLiftQuarantineMatchesNormalizedAndQuotesTheStoredSpelling: the same normalization
// that stops `check discord` walking past `quarantine Discord` must let
// `lift quarantine DISCORD` reach it.
func TestLiftQuarantineMatchesNormalizedAndQuotesTheStoredSpelling(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, `{"quarantine":{"Discord":{"at":"t","reason":"r"}}}`)

	code, out, _ := capture(t, []string{"lift", "quarantine", "--box", box, "  DISCORD  "}, nowish())
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "quarantine=Discord") {
		t.Errorf("quote the file's spelling, not the caller's: %q", out)
	}

	code, _, _ = capture(t, []string{"check", "--box", box, "discord"}, nowish())
	if code != 0 {
		t.Errorf("the lift must be visible to check under any spelling, exit = %d", code)
	}
}

// TestLiftQuarantineWithNothingToLiftDoesNotClaimSuccess. A typo must never read as a
// lift: the operator would walk away believing a surface is open that is still blocked.
func TestLiftQuarantineWithNothingToLiftDoesNotClaimSuccess(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, `{"quarantine":{"bsky":{"at":"t","reason":"r"}}}`)
	before := readRaw(t, box)

	code, out, errOut := capture(t, []string{"lift", "quarantine", "--box", box, "discord"}, nowish())
	if code != 1 {
		t.Errorf("exit = %d, want 1 -- could-not-do-it is never a claimed success", code)
	}
	if !strings.Contains(errOut, "nothing to lift") {
		t.Errorf("stderr = %q", errOut)
	}
	if !strings.Contains(errOut, "bsky") {
		t.Errorf("name what IS quarantined so a typo is visible: %q", errOut)
	}
	if out != "" {
		t.Errorf("no OK line on a failed lift, got %q", out)
	}
	if got := readRaw(t, box); got != before {
		t.Error("nothing to lift means nothing to write")
	}
}

// TestLiftQuarantineUnderLockdownLeavesLockdownBlown pins both halves of the design at
// once: the soft dial still turns, and the hard fuse still covers every surface.
func TestLiftQuarantineUnderLockdownLeavesLockdownBlown(t *testing.T) {
	box := boxIn(t)
	now := nowish()
	mustRun(t, []string{"lockdown", "--box", box, "suspected compromise"}, now)
	mustRun(t, []string{"quarantine", "--box", box, "discord", "many the same way"}, now)

	code, _, errOut := capture(t, []string{"lift", "quarantine", "--box", box, "discord"}, now)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(errOut, "lockdown is still blown") {
		t.Errorf("the output must say lockdown still blocks everything: %q", errOut)
	}

	var got fuse.Box
	if err := json.Unmarshal([]byte(readRaw(t, box)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Lockdown == nil {
		t.Fatal("lifting a quarantine must never touch the lockdown")
	}

	code, _, _ = capture(t, []string{"check", "--box", box, "discord"}, now)
	if code != 1 {
		t.Errorf("exit = %d, want 1 -- LOCKDOWN covers every surface, lifted quarantine or not", code)
	}
}

// TestLockdownDoesNotExpire: the recorded state carries no expiry, no timer, no deadline
// -- only `at` and `reason`. A fuse that lifts itself has a timer an attacker can wait
// out, so there must be nothing in the box a clock could act on.
func TestLockdownDoesNotExpire(t *testing.T) {
	box := boxIn(t)
	mustRun(t, []string{"lockdown", "--box", box, "suspected compromise"}, nowish())

	var raw map[string]any
	if err := json.Unmarshal([]byte(readRaw(t, box)), &raw); err != nil {
		t.Fatal(err)
	}
	ld, ok := raw["lockdown"].(map[string]any)
	if !ok {
		t.Fatal("lockdown must be recorded as an object")
	}
	for key := range ld {
		if key != "at" && key != "reason" {
			t.Errorf("the lockdown record holds %q -- nothing but `at` and `reason` may exist, or a timer could act on it", key)
		}
	}

	// And a decade-old lockdown still blocks: `at` is an audit fact, never an input.
	writeRaw(t, box, `{"lockdown":{"at":"2016-01-01T00:00:00Z","reason":"old"},"quarantine":{}}`)
	code, _, _ := capture(t, []string{"check", "--box", box}, nowish())
	if code != 1 {
		t.Errorf("exit = %d, want 1 -- lockdown does not expire, no matter how old", code)
	}
}

// ------------------------------- 7. ONE LINE PER EVENT, WHATEVER THE BOX CONTAINS

// The box is world-readable on purpose and hand-editable on purpose -- and on a shared
// machine that means any local user can author the strings this tool prints. The output
// grammar SPEC.md tells callers to scan is one line per event, so a reason carrying a
// newline could forge a second event line beneath a real one: a `FUSE OK lockdown=clear`
// under a `FUSE FAIL`, authored by whoever wrote the box. Terminal escape sequences are
// the same hole aimed at an operator instead of a parser. Every test below writes the box
// WITHOUT going through this tool, because that is the case that decides it.

// writeBox writes a box's JSON directly, bypassing fuse.WriteBox and therefore bypassing
// everything this tool does to its own writes. The premise of this section is that the
// bytes read back are not the bytes this tool wrote.
func writeBox(t *testing.T, path string, b fuse.Box) {
	t.Helper()
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("fixture marshal: %v", err)
	}
	writeRaw(t, path, string(data))
}

// countLinesWithPrefix counts the lines of s beginning with prefix. A forged event line is
// a SECOND line wearing the grammar, so counting lines is the assertion, not substrings.
func countLinesWithPrefix(s, prefix string) int {
	n := 0
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// noForgedOKLine fails if any line of either stream opens with an OK token of the grammar
// -- the line a caller scanning SPEC.md's grammar would read as permission.
func noForgedOKLine(t *testing.T, prefix, stdout, stderr string) {
	t.Helper()
	for name, stream := range map[string]string{"stdout": stdout, "stderr": stderr} {
		for _, line := range strings.Split(stream, "\n") {
			if strings.HasPrefix(line, prefix) {
				t.Errorf("%s carries a forged %q line: %q", name, prefix, line)
			}
		}
	}
}

// the forgery: a reason whose second line wears the grammar of permission.
const forgedOK = "real\nFUSE OK lockdown=clear quarantine=clear surface=discord"

// TestALockdownReasonCannotForgeAnOKLine is the finding itself. A blown lockdown must
// print exactly one FUSE line, and nothing the box contains may add another.
func TestALockdownReasonCannotForgeAnOKLine(t *testing.T) {
	box := boxIn(t)
	writeBox(t, box, fuse.Box{
		Lockdown:   &fuse.Fuse{At: "2026-08-03T00:00:00Z", Reason: forgedOK},
		Quarantine: map[string]fuse.Fuse{},
	})

	code, out, errOut := capture(t, []string{"check", "--box", box, "discord"}, nowish())
	if code != 1 {
		t.Fatalf("exit = %d, want 1 -- the lockdown is blown\nstdout: %q\nstderr: %q", code, out, errOut)
	}
	if n := countLinesWithPrefix(errOut, "FUSE"); n != 1 {
		t.Errorf("stderr holds %d lines opening with FUSE, want exactly 1: %q", n, errOut)
	}
	noForgedOKLine(t, "FUSE OK", out, errOut)
	if strings.Contains(out+errOut, "\nFUSE") {
		t.Errorf("a second event line was forged out of the reason: %q", out+errOut)
	}
	if !strings.Contains(errOut, `\x0a`) {
		t.Errorf("the newline must still be VISIBLE, escaped, never dropped: %q", errOut)
	}
	if !strings.Contains(errOut, "real") {
		t.Errorf("the reason must still be readable -- escaping never shortens it to nothing: %q", errOut)
	}
}

// TestAQuarantineReasonCannotForgeAnOKLine: the same hole through the soft fuse.
func TestAQuarantineReasonCannotForgeAnOKLine(t *testing.T) {
	box := boxIn(t)
	writeBox(t, box, fuse.Box{
		Quarantine: map[string]fuse.Fuse{"discord": {At: "2026-08-03T00:00:00Z", Reason: forgedOK}},
	})

	code, out, errOut := capture(t, []string{"check", "--box", box, "discord"}, nowish())
	if code != 1 {
		t.Fatalf("exit = %d, want 1 -- the surface is quarantined\nstdout: %q\nstderr: %q", code, out, errOut)
	}
	if n := countLinesWithPrefix(errOut, "FUSE"); n != 1 {
		t.Errorf("stderr holds %d lines opening with FUSE, want exactly 1: %q", n, errOut)
	}
	noForgedOKLine(t, "FUSE OK", out, errOut)
}

// TestStatusPrintsOneLinePerFuseAndNoMore: status is the report a person reads and a
// script diffs, so its line COUNT is part of the contract -- one line, plus one per
// quarantine, whatever the reasons contain.
func TestStatusPrintsOneLinePerFuseAndNoMore(t *testing.T) {
	box := boxIn(t)
	writeBox(t, box, fuse.Box{
		Lockdown: &fuse.Fuse{At: "2026-08-03T00:00:00Z", Reason: forgedOK},
		Quarantine: map[string]fuse.Fuse{
			"discord": {At: "2026-08-03T00:00:00Z", Reason: "one\nSTATUS OK lockdown=clear quarantines=0"},
			"bsky":    {At: "2026-08-03T00:00:00Z", Reason: "two"},
		},
	})

	code, out, errOut := capture(t, []string{"status", "--box", box}, nowish())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 -- status reports\nstderr: %q", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Errorf("status printed %d lines, want 1 + 2 quarantines = 3: %q", len(lines), out)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "STATUS OK") {
			t.Errorf("every status line must open the grammar, got %q", line)
		}
	}
	// The forged text stays visible inside the escaped reason -- what it must never be is
	// a LINE of its own, which is the only thing a caller scanning the grammar reads.
	if n := countLinesWithPrefix(out, "STATUS OK lockdown="); n != 1 {
		t.Errorf("%d lines report lockdown, want exactly 1: %q", n, out)
	}
}

// TestAStoredQuarantineKeyWithANewlinePrintsEscaped: the KEY is attacker-authored too --
// it is a JSON object name, so it carries anything a reason can.
func TestAStoredQuarantineKeyWithANewlinePrintsEscaped(t *testing.T) {
	box := boxIn(t)
	// Written by hand: `dis\ncord` is a JSON escape, so the stored key holds a real newline.
	const raw = `{"lockdown":null,"quarantine":{"dis\ncord":{"at":"2026-08-03T00:00:00Z","reason":"r"}}}`

	writeRaw(t, box, raw)
	code, out, errOut := capture(t, []string{"status", "--box", box}, nowish())
	if code != 0 {
		t.Fatalf("status exit = %d, want 0\nstderr: %q", code, errOut)
	}
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != 2 {
		t.Errorf("status printed %d lines, want 2: %q", got, out)
	}
	if !strings.Contains(out, `dis\x0acord`) {
		t.Errorf("the stored key must print escaped, got %q", out)
	}

	// The folded spelling matches the folded key -- normalising blocks more, never less.
	code, out, errOut = capture(t, []string{"check", "--box", box, "dis cord"}, nowish())
	if code != 1 {
		t.Fatalf("check exit = %d, want 1 -- that surface is quarantined\nstdout: %q\nstderr: %q", code, out, errOut)
	}
	if n := countLinesWithPrefix(errOut, "FUSE"); n != 1 {
		t.Errorf("stderr holds %d lines opening with FUSE, want exactly 1: %q", n, errOut)
	}
	if !strings.Contains(errOut, `dis\x0acord`) {
		t.Errorf("the FAIL must quote the stored spelling, escaped, got %q", errOut)
	}

	writeRaw(t, box, raw)
	code, out, errOut = capture(t, []string{"lift", "quarantine", "--box", box, "dis cord"}, nowish())
	if code != 0 {
		t.Fatalf("lift exit = %d, want 0\nstdout: %q\nstderr: %q", code, out, errOut)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasPrefix(line, "LIFT OK") {
			t.Errorf("every lift line must open the grammar, got %q", line)
		}
	}
	if !strings.Contains(out, `dis\x0acord`) {
		t.Errorf("the announced lift must quote the stored spelling, escaped, got %q", out)
	}
}

// TestAnEscapeSequenceNeverReachesTheTerminal: the same hole aimed at an operator. A
// reason that clears the screen, or repaints what is above it, must arrive as text.
func TestAnEscapeSequenceNeverReachesTheTerminal(t *testing.T) {
	box := boxIn(t)
	writeBox(t, box, fuse.Box{
		Lockdown:   &fuse.Fuse{At: "\x1b[2J", Reason: "clean\x1b[2J\x1b[Hnothing to see"},
		Quarantine: map[string]fuse.Fuse{},
	})

	for _, args := range [][]string{
		{"status", "--box", box},
		{"check", "--box", box, "discord"},
	} {
		_, out, errOut := capture(t, args, nowish())
		if strings.ContainsRune(out+errOut, 0x1b) {
			t.Errorf("%v: a raw ESC byte reached the output: %q", args, out+errOut)
		}
		if !strings.Contains(out+errOut, `\x1b`) {
			t.Errorf("%v: the escape must be shown as text, not dropped: %q", args, out+errOut)
		}
	}
}

// TestLockdownTakesANewlineInItsReasonAndStoresItFolded. A fuse you cannot blow is not a
// fuse, so the reason is never REFUSED -- it is folded, and the write stays tidy. The
// print-time escape is what actually holds; this only keeps this tool's own writes clean.
func TestLockdownTakesANewlineInItsReasonAndStoresItFolded(t *testing.T) {
	box := boxIn(t)
	code, out, errOut := capture(t, []string{"lockdown", "--box", box, "line one\nline two"}, nowish())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 -- a reason is never refused for what it contains\nstdout: %q\nstderr: %q", code, out, errOut)
	}
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != 1 {
		t.Errorf("LOCKDOWN OK must be one line, got %d: %q", got, out)
	}

	b, err := fuse.ReadBox(box)
	if err != nil || b.Lockdown == nil {
		t.Fatalf("read back: %v, %+v", err, b)
	}
	if strings.ContainsAny(b.Lockdown.Reason, "\n\r\t") {
		t.Errorf("the stored reason still holds a control character: %q", b.Lockdown.Reason)
	}
	if b.Lockdown.Reason != "line one line two" {
		t.Errorf("stored reason = %q, want the folded text with nothing lost", b.Lockdown.Reason)
	}
}

// TestQuarantineFoldsAControlCharacterOutOfTheSurfaceName: folding a name can only ever
// collapse two names into one, which blocks MORE and never less (package note 4). The
// plain spelling must still refuse afterwards.
func TestQuarantineFoldsAControlCharacterOutOfTheSurfaceName(t *testing.T) {
	box := boxIn(t)
	code, out, errOut := capture(t, []string{"quarantine", "--box", box, "\x1bdiscord\n", "attacked"}, nowish())
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %q\nstderr: %q", code, out, errOut)
	}
	if got := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; got != 1 {
		t.Errorf("QUARANTINE OK must be one line, got %d: %q", got, out)
	}

	b, err := fuse.ReadBox(box)
	if err != nil {
		t.Fatal(err)
	}
	names := b.Surfaces()
	if len(names) != 1 || names[0] != "discord" {
		t.Fatalf("stored keys = %q, want exactly [discord] -- a control character is folded away", names)
	}

	code, _, errOut = capture(t, []string{"check", "--box", box, "discord"}, nowish())
	if code != 1 {
		t.Errorf("check discord exit = %d, want 1 -- normalising blocks more, never less: %q", code, errOut)
	}
}

// TestAFailFileErrorStaysOnOneLine closes the last hole in the promise: a FAIL line's
// <reason> slot is sometimes an ERROR, and an error text carries whatever the path that
// produced it carried. A --box argument holding a newline is the caller's own, not the
// box's, but the guarantee SPEC.md states is that nothing an argument contains can add a
// second line either.
func TestAFailFileErrorStaysOnOneLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: chmod 0555 does not make a directory refuse writes, so the failure this pins cannot be produced here")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only directory does not refuse writes")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	// The unmakeable directory carries the newline, so the mkdir error text carries it too.
	box := filepath.Join(dir, "sub\nnew", "fuses.json")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"lockdown", "--box", box, "suspected compromise"}, "LOCKDOWN FAIL"},
		{[]string{"quarantine", "--box", box, "discord", "many the same way"}, "QUARANTINE FAIL"},
	} {
		code, out, errOut := capture(t, tc.args, nowish())
		if code != 1 {
			t.Fatalf("%v: exit = %d, want 1\nstdout: %q\nstderr: %q", tc.args, code, out, errOut)
		}
		if got := strings.Count(strings.TrimRight(errOut, "\n"), "\n") + 1; got != 1 {
			t.Errorf("%v: the failure printed %d lines, want 1: %q", tc.args, got, errOut)
		}
		if n := countLinesWithPrefix(errOut, tc.want); n != 1 {
			t.Errorf("%v: %d lines open %q, want exactly 1: %q", tc.args, n, tc.want, errOut)
		}
		if !strings.Contains(errOut, `\x0a`) {
			t.Errorf("%v: the newline in the error must be shown escaped, got %q", tc.args, errOut)
		}
	}
}

// TestNoRefusalOrNoteCanForgeAnOKLine. The finding was reported against the event lines,
// but a REFUSAL and a NOTE go to the same stream a caller reads, and they interpolate the
// same untrusted material: the error text, the preserved-bytes destination, and the box
// path itself. A --box argument is the caller's own rather than the box's contents, and
// the guarantee stated in SPEC.md covers both.
func TestNoRefusalOrNoteCanForgeAnOKLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows: a newline is not legal in a filename there, so the box path cannot carry this forgery and the fixture cannot be built; the escaping under test is platform-independent and runs on the other two")
	}
	const forgery = "FUSE OK lockdown=clear quarantine=clear surface=discord"
	dir := t.TempDir()

	// A newline is legal in a POSIX filename; only / and NUL are not.
	bad := filepath.Join(dir, "bad\n"+forgery)
	writeRaw(t, bad, "not json")

	// A DIRECTORY under a name like that reaches the other half of lockdown's note: the
	// box cannot be read AND its bytes cannot be preserved.
	badDir := filepath.Join(dir, "dir\n"+forgery)
	if err := os.Mkdir(badDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		args      []string
		wantCode  int
		wantLines int    // lines on the stream the refusal or note is written to
		prefix    string // every one of them must open with this
		stream    string // "stderr" or "stdout"
	}{
		{"check on an unreadable box", []string{"check", "--box", bad, "discord"}, 2, 1, "nova-fuse check:", "stderr"},
		{"status on an unreadable box", []string{"status", "--box", bad}, 2, 1, "nova-fuse status:", "stderr"},
		{"lift quarantine on an unreadable box", []string{"lift", "quarantine", "--box", bad, "discord"}, 2, 1, "nova-fuse lift quarantine:", "stderr"},
		{"quarantine refusing to narrow", []string{"quarantine", "--box", bad, "discord", "why"}, 2, 1, "nova-fuse quarantine:", "stderr"},
		{"lockdown noting the preserved bytes", []string{"lockdown", "--box", bad, "why"}, 0, 1, "nova-fuse lockdown:", "stderr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := capture(t, tc.args, nowish())
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d\nstdout: %q\nstderr: %q", code, tc.wantCode, out, errOut)
			}
			stream := errOut
			if tc.stream == "stdout" {
				stream = out
			}
			lines := strings.Split(strings.TrimRight(stream, "\n"), "\n")
			if len(lines) != tc.wantLines {
				t.Errorf("%s printed %d lines, want %d: %q", tc.stream, len(lines), tc.wantLines, stream)
			}
			for _, line := range lines {
				if !strings.HasPrefix(line, tc.prefix) {
					t.Errorf("every line must open with %q, got %q", tc.prefix, line)
				}
			}
			noForgedOKLine(t, "FUSE OK", out, errOut)
			noForgedOKLine(t, "STATUS OK", out, errOut)
			noForgedOKLine(t, "LIFT OK", out, errOut)
		})
	}

	// lockdown over a box it can neither read nor preserve: both halves of the note, and
	// then a FAIL when the rename cannot replace a directory.
	t.Run("lockdown over an unpreservable box", func(t *testing.T) {
		code, out, errOut := capture(t, []string{"lockdown", "--box", badDir, "why"}, nowish())
		if code != 1 {
			t.Fatalf("exit = %d, want 1 -- the write cannot land on a directory\nstdout: %q\nstderr: %q", code, out, errOut)
		}
		lines := strings.Split(strings.TrimRight(errOut, "\n"), "\n")
		if len(lines) != 2 {
			t.Errorf("stderr printed %d lines, want 2 (the note, then the FAIL): %q", len(lines), errOut)
		}
		for _, line := range lines {
			if !strings.HasPrefix(line, "nova-fuse lockdown:") && !strings.HasPrefix(line, "LOCKDOWN FAIL") {
				t.Errorf("unexpected line: %q", line)
			}
		}
		noForgedOKLine(t, "FUSE OK", out, errOut)
	})
}

// TestEveryPrintedArgumentIsLiteralQuotedOrEscaped is the tripwire, and it exists because
// the first round of this fix was audited by counting call sites BY HAND and came up nine
// short. Every one of those nine was a refusal or a note rather than an event line, which
// is exactly the kind of thing hand-counting misses. So the count is mechanical now: this
// reads main.go, pairs every fmt.Fprint/Fprintf/Fprintln argument with the verb that
// prints it, and classifies each one as
//
//	%q or %d      -- the verb escapes it, or it is a number
//	a literal     -- written in this file, so nothing untrusted reaches it
//	escaped       -- rendered through fuse.OneLine, oneLineErr, why or since
//	exempted      -- named below, one entry per site, with the reason stated
//
// Anything else fails, naming the line. A new interpolation is a decision from now on,
// never a drive-by: adding one means either escaping it or writing down why it is safe.
func TestEveryPrintedArgumentIsLiteralQuotedOrEscaped(t *testing.T) {
	const file = "main.go"
	src, fset, parsed := parseMainGo(t)

	// Rendered through one of these, a string cannot carry a line break or a terminal
	// control sequence. See fuse.OneLine.
	escapers := map[string]bool{"fuse.OneLine": true, "oneLineErr": true, "why": true, "since": true}

	// One entry per site, keyed by function and source text. Each is a claim, and each
	// claim is either checked below or stated here as the reason a reader would accept.
	exempt := map[string]string{
		"cmdPath|box":           "`path` prints a value, not an event: the line carries no OK/FAIL token, asserts nothing, and SPEC.md exempts it by name",
		"liftQuarantine|listed": "built immediately above from fuse.OneLine over every stored name",
		"run|usage":             "the usage constant declared in this file",
		"cmdLift|usage":         "the usage constant declared in this file",
		"cmdStatus|usage":       "the usage constant declared in this file",
		"cmdCheck|usage":        "the usage constant declared in this file",
		"cmdLockdown|usage":     "the usage constant declared in this file",
		"cmdQuarantine|usage":   "the usage constant declared in this file",
		"cmdPath|usage":         "the usage constant declared in this file",
		"parseBox|name":         "the verb's own name, chosen by this file at every call site",
	}
	usedExemption := map[string]bool{}

	// The usage exemptions are only honest if usage really is a constant here.
	usageIsConst := false
	for _, d := range parsed.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				for _, n := range vs.Names {
					if n.Name == "usage" {
						usageIsConst = true
					}
				}
			}
		}
	}
	if !usageIsConst {
		t.Fatal("usage is no longer a constant in main.go, so every exemption naming it is unproven")
	}

	text := func(n ast.Node) string {
		return string(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset])
	}

	// literalOnly answers whether an expression is nothing but string literals, including
	// a chain of them concatenated with + (the lift lockdown refusal is written that way).
	var literalOnly func(ast.Expr) bool
	literalOnly = func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.BasicLit:
			return true
		case *ast.BinaryExpr:
			return e.Op == token.ADD && literalOnly(e.X) && literalOnly(e.Y)
		case *ast.ParenExpr:
			return literalOnly(e.X)
		}
		return false
	}

	// verbsOf returns the verbs of a format string in order. It deliberately refuses to
	// guess at flags and widths: none are used here, and a classifier that quietly
	// mis-pairs arguments would be worse than one that stops.
	verbsOf := func(t *testing.T, format string) []byte {
		t.Helper()
		var verbs []byte
		for i := 0; i < len(format); i++ {
			if format[i] != '%' {
				continue
			}
			i++
			if i >= len(format) {
				t.Fatalf("trailing %% in format %q", format)
			}
			if format[i] == '%' {
				continue
			}
			if strings.IndexByte("+-# 0123456789.*", format[i]) >= 0 {
				t.Fatalf("format %q uses a flag or width; this classifier does not model those, teach it before using one", format)
			}
			verbs = append(verbs, format[i])
		}
		return verbs
	}

	classified := 0
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		fnName := fn.Name.Name
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "fmt" {
				return true
			}
			line := fset.Position(call.Pos()).Line

			var verbs []byte
			args := call.Args[1:] // arg 0 is the writer
			if sel.Sel.Name == "Fprintf" {
				format, err := strconv.Unquote(text(args[0]))
				if err != nil {
					// A concatenated or computed format string is itself a finding: the
					// verbs could not be read, so nothing after it can be classified.
					t.Errorf("%s:%d: format string is not a single literal: %s", file, line, text(args[0]))
					return true
				}
				verbs = verbsOf(t, format)
				args = args[1:]
				if len(verbs) != len(args) {
					t.Errorf("%s:%d: %d verbs but %d arguments; the classifier cannot pair them", file, line, len(verbs), len(args))
					return true
				}
			}

			for i, arg := range args {
				classified++
				verb := byte(0)
				if i < len(verbs) {
					verb = verbs[i]
				}
				if verb == 'q' || verb == 'd' {
					continue // the verb quotes and escapes it, or it is a number
				}
				if literalOnly(arg) {
					continue // written in this file, so nothing untrusted reaches it
				}
				if e, ok := arg.(*ast.CallExpr); ok && escapers[text(e.Fun)] {
					continue
				}
				if key := fnName + "|" + text(arg); exempt[key] != "" {
					usedExemption[key] = true
					continue
				}
				t.Errorf("%s:%d in %s: %%%c prints %s raw -- escape it (fuse.OneLine or oneLineErr) or add an exemption naming the reason",
					file, line, fnName, verb, text(arg))
			}
			return true
		})
	}
	// A stale exemption is a claim about a call site that no longer exists, and it would
	// silently cover the next one written in its place.
	for key, why := range exempt {
		if !usedExemption[key] {
			t.Errorf("exemption %q (%s) matches no print site; delete it rather than leave a claim nothing checks", key, why)
		}
	}
	if classified < 40 {
		t.Errorf("only %d printed arguments were classified; main.go has many more, so the walk is not reaching them", classified)
	}
}

// TestAUnicodeLineSeparatorCannotForgeALineEither. A scanner is not always `split on \n`:
// Python's str.splitlines() and every UAX-14 line breaker also break on U+2028 and U+2029,
// which are Zl and Zp rather than Cc. A box reason carrying one forges a line for those
// readers and for no others, which is the worst kind of hole -- invisible to the test
// suite of whoever is not looking for it.
func TestAUnicodeLineSeparatorCannotForgeALineEither(t *testing.T) {
	box := boxIn(t)
	for _, sep := range []string{" ", " "} {
		writeBox(t, box, fuse.Box{
			Lockdown:   &fuse.Fuse{At: "2026-08-03T00:00:00Z", Reason: "real" + sep + "FUSE OK lockdown=clear"},
			Quarantine: map[string]fuse.Fuse{},
		})
		_, out, errOut := capture(t, []string{"check", "--box", box, "discord"}, nowish())
		if strings.Contains(out+errOut, sep) {
			t.Errorf("%q reached the output raw: %q", sep, out+errOut)
		}
		if !strings.Contains(errOut, `\u202`) {
			t.Errorf("the separator must be shown as an escape, got %q", errOut)
		}
	}
}

// TestAReasonOfNothingButControlCharactersStillBlowsTheFuse. Folding must never become a
// new refusal on the HARD fuse: a reason made only of control characters folds to nothing,
// and refusing it would mean a fuse that could be blown yesterday cannot be blown today.
// That is the one direction this design forbids. The reason is kept as its escapes instead.
func TestAReasonOfNothingButControlCharactersStillBlowsTheFuse(t *testing.T) {
	t.Run("lockdown", func(t *testing.T) {
		box := boxIn(t)
		code, out, errOut := capture(t, []string{"lockdown", "--box", box, "\x01"}, nowish())
		if code != 0 {
			t.Fatalf("exit = %d, want 0 -- a fuse you cannot blow is not a fuse\nstdout: %q\nstderr: %q", code, out, errOut)
		}
		b, err := fuse.ReadBox(box)
		if err != nil || b.Lockdown == nil {
			t.Fatalf("read back: %v, %+v", err, b)
		}
		if b.Lockdown.Reason != `\x01` {
			t.Errorf("stored reason = %q, want the visible escape rather than an empty record", b.Lockdown.Reason)
		}
	})

	t.Run("quarantine", func(t *testing.T) {
		box := boxIn(t)
		code, out, errOut := capture(t, []string{"quarantine", "--box", box, "discord", "\x01"}, nowish())
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstdout: %q\nstderr: %q", code, out, errOut)
		}
		b, err := fuse.ReadBox(box)
		if err != nil {
			t.Fatal(err)
		}
		if got := b.Quarantine["discord"].Reason; got != `\x01` {
			t.Errorf("stored reason = %q, want the visible escape", got)
		}
	})

	// The genuinely empty reason is still refused, exactly as before this branch.
	t.Run("whitespace only is still refused", func(t *testing.T) {
		box := boxIn(t)
		code, _, errOut := capture(t, []string{"lockdown", "--box", box, " \n\t "}, nowish())
		if code != 2 {
			t.Errorf("exit = %d, want 2 -- an empty reason was always refused", code)
		}
		if !strings.Contains(errOut, "needs a reason") {
			t.Errorf("stderr = %q, want the reason refusal", errOut)
		}
	})
}

// TestAFlagErrorCannotForgeALineEither. The last writer outside the fence: package flag
// prints its OWN error, and that error quotes the argument it could not parse. Every verb
// parses flags, and the realistic shape is an untrusted surface name that begins with a
// dash -- so nothing in this file's code had to run for a caller to author a whole line
// of stderr. This one needs no newline in a path, so unlike the --box tests it runs
// everywhere.
func TestAFlagErrorCannotForgeALineEither(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, `{"lockdown":null,"quarantine":{}}`)

	// Both begin with a dash, which is what makes flag try to parse them.
	forgeries := map[string]string{
		"a forged LIFT OK line": "-\nLIFT OK verified: discord is no longer quarantined (soft: your own dial, both directions; a rescind is announced, never silent -- say so out loud)",
		"a terminal repaint":    "-\x1b]0;PWNED\x07\x1b[2J",
	}
	for what, arg := range forgeries {
		for _, args := range [][]string{
			{"check", "--box", box, arg},
			{"status", "--box", box, arg},
			{"lockdown", "--box", box, arg},
			{"quarantine", "--box", box, arg, "why"},
			{"lift", "quarantine", "--box", box, arg},
			{"path", "--box", box, arg},
		} {
			t.Run(what+" through "+args[0], func(t *testing.T) {
				code, out, errOut := capture(t, args, nowish())
				if code != 2 {
					t.Errorf("exit = %d, want 2 -- an unparseable flag is a refusal", code)
				}
				// A refusal is not an event: no line of either stream may open the grammar.
				for _, token := range []string{"FUSE OK", "FUSE FAIL", "STATUS OK", "LIFT OK", "LIFT FAIL", "LOCKDOWN OK", "QUARANTINE OK"} {
					noForgedOKLine(t, token, out, errOut)
				}
				if strings.ContainsRune(out+errOut, 0x1b) {
					t.Errorf("a raw ESC byte reached the output: %q", out+errOut)
				}
				if strings.Contains(errOut, "Usage of") {
					t.Errorf("flag printed its own usage block; this tool prints its own refusals: %q", errOut)
				}
			})
		}
	}
}

// TestTheQuarantinedNowListingCannotForgeALine covers the one exemption in the source
// tripwire that is a CLAIM rather than a check: the `quarantined now:` listing is built by
// a loop above its print site, so the classifier can only see a local variable. Removing
// fuse.OneLine from that loop leaves the whole suite green without this test, while
// LIFT FAIL forges a line out of a stored key.
func TestTheQuarantinedNowListingCannotForgeALine(t *testing.T) {
	box := boxIn(t)
	writeRaw(t, box, `{"lockdown":null,"quarantine":{
		"dis\nLIFT OK verified: discord is no longer quarantined (soft)cord": {"at":"t","reason":"r"},
		"bsky": {"at":"t","reason":"r"}}}`)

	// Lift a surface that is NOT quarantined: the refusal names what IS.
	code, out, errOut := capture(t, []string{"lift", "quarantine", "--box", box, "zulip"}, nowish())
	if code != 1 {
		t.Fatalf("exit = %d, want 1 -- a typo must never read as a lift\nstdout: %q\nstderr: %q", code, out, errOut)
	}
	if got := strings.Count(strings.TrimRight(errOut, "\n"), "\n") + 1; got != 1 {
		t.Errorf("stderr printed %d lines, want 1: %q", got, errOut)
	}
	noForgedOKLine(t, "LIFT OK", out, errOut)
	if !strings.Contains(errOut, `\x0a`) {
		t.Errorf("the stored key must be listed escaped: %q", errOut)
	}
}

// parseMainGo reads and parses the package's own source. Reaching outside t.TempDir() has
// one reason here and it is stated: the subject of these two tests IS the source, and a
// property of the code is not provable from its output alone.
func parseMainGo(t *testing.T) ([]byte, *token.FileSet, *ast.File) {
	t.Helper()
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return src, fset, parsed
}

// TestNoOtherWriterOrShadowCanBypassTheEscape closes the gap in the sibling test above,
// which walks fmt.* calls and therefore sees only one way of putting bytes on a stream.
// A reader named the rest: io.WriteString, a bare .Write, a function VALUE aliased out of
// fmt, a local that shadows the escaping helpers or the fuse package itself, and
// flag.FlagSet.SetOutput -- which is how package flag came to print an attacker's argument
// before any code in this file ran. Each is refused here by name, and each was proved able
// to fail by mutation.
func TestNoOtherWriterOrShadowCanBypassTheEscape(t *testing.T) {
	src, fset, parsed := parseMainGo(t)
	text := func(n ast.Node) string {
		return string(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset])
	}
	at := func(n ast.Node) string { return fmt.Sprintf("main.go:%d", fset.Position(n.Pos()).Line) }

	// A name declared inside a function that shadows one of these turns every escape in
	// scope into a no-op, invisibly to the sibling test, which classifies by source text.
	shadows := map[string]bool{"fuse": true, "oneLineErr": true, "OneLine": true, "Fold": true, "why": true, "since": true}
	declared := func(n ast.Node, idents []*ast.Ident) {
		for _, id := range idents {
			if shadows[id.Name] {
				t.Errorf("%s: a local named %q shadows the escaping path; rename it", at(n), id.Name)
			}
		}
	}

	ast.Inspect(parsed, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "SetOutput":
				// The whole point: a flag set that can print is a writer this file does
				// not control, quoting an argument this file did not author.
				if len(node.Args) != 1 || text(node.Args[0]) != "io.Discard" {
					t.Errorf("%s: SetOutput(%s) lets another package write to a stream; only io.Discard is allowed",
						at(node), text(node.Args[0]))
				}
			case "Write", "WriteString", "WriteByte", "WriteRune":
				t.Errorf("%s: %s writes bytes past the escaping path; print through fmt.Fprintf with an escaped argument",
					at(node), text(node.Fun))
			}
		case *ast.AssignStmt:
			for _, rhs := range node.Rhs {
				if sel, ok := rhs.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == "fmt" {
						t.Errorf("%s: %s is a fmt function VALUE; aliased, it prints where the classifier cannot see it",
							at(node), text(rhs))
					}
				}
			}
			if node.Tok == token.DEFINE {
				var ids []*ast.Ident
				for _, lhs := range node.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						ids = append(ids, id)
					}
				}
				declared(node, ids)
			}
		case *ast.ValueSpec:
			for _, v := range node.Values {
				if sel, ok := v.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == "fmt" {
						t.Errorf("%s: %s is a fmt function VALUE; aliased, it prints where the classifier cannot see it",
							at(node), text(v))
					}
				}
			}
			declared(node, node.Names)
		case *ast.RangeStmt:
			if node.Tok == token.DEFINE {
				var ids []*ast.Ident
				for _, e := range []ast.Expr{node.Key, node.Value} {
					if id, ok := e.(*ast.Ident); ok {
						ids = append(ids, id)
					}
				}
				declared(node, ids)
			}
		case *ast.FuncType:
			var ids []*ast.Ident
			for _, list := range []*ast.FieldList{node.Params, node.Results} {
				if list == nil {
					continue
				}
				for _, field := range list.List {
					ids = append(ids, field.Names...)
				}
			}
			declared(node, ids)
		}
		return true
	})

	// io.WriteString is a plain call rather than a selector on a writer, so it is named
	// directly. Checked over the source text because it takes no other form.
	if strings.Contains(string(src), "io.WriteString") {
		t.Error("main.go: io.WriteString writes past the escaping path; print through fmt.Fprintf")
	}
}

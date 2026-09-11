package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 10. serve wakes a harness from outside it, once per note, and spends nothing
// while idle.

// fakeNote builds the fake receiver and returns the command line to hand
// --on-note and the directory it records into.
func fakeNote(t *testing.T) (command, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin := filepath.Join(t.TempDir(), "on-note")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/fakenote")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fake receiver: %v\n%s", err, raw)
	}
	t.Setenv("NOVA_WAKE_FAKE_NOTE", dir)
	return bin, dir
}

func TestServeSurfacesTheUncertainAndWakesOnlyTo(t *testing.T) {
	busDir, _ := fakes(t)
	// Two notes To: the name, one Cc: only, and one for another name -- which
	// nova-bus does not list for this reader at all, and is here as the line
	// this tool must not dispatch even when it sees it.
	write(t, filepath.Join(busDir, "out"), strings.Join([]string{
		"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first",
		"INBOX NOTE id=bbb222 from=Johnny addr=to at=2026-09-11T11:01:00Z path=from-johnny/b.md: the second",
		"INBOX NOTE id=ccc333 from=Emma addr=cc at=2026-09-11T11:02:00Z path=from-emma/c.md: a broadcast",
		"INBOX OK as=Rowan carrying=3 open=3 notes=3 receipts=0",
	}, "\n")+"\n")
	note, noteDir := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")
	args := []string{"serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "30s", "--state", state, "--hours", "0.02"}

	r := wakeRun(t, args...)
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	got := calls(t, noteDir)
	if len(got) != 1 {
		t.Fatalf("the receiver was started %d times, want 1: every note queued at the moment the command is not running is handed to ONE invocation\n%v", len(got), got)
	}
	if got[0] != "aaa111 bbb222" {
		t.Errorf("the receiver was handed %q, want the two To: ids in bus order and nothing else", got[0])
	}
	if strings.Contains(strings.Join(got, " "), "ccc333") {
		t.Error("a Cc: note was dispatched; To means must act, cc means should know, and a broadcast to five is five turns")
	}
	if read(t, filepath.Join(noteDir, "stdin")) != "" {
		t.Error("something reached the receiver's stdin; the command receives note ids and nothing else")
	}
	for _, banned := range []string{"INBOX", "carrying=", "the first"} {
		if strings.Contains(strings.Join(got, " "), banned) {
			t.Errorf("the receiver was handed %q; never inbox's output, never the carrying line, never a body", banned)
		}
	}
	if !strings.Contains(r.stdout, "WAKE FIRED ids=2 first=aaa111 rc=0 redelivered=0") {
		t.Errorf("the fire line is missing or wrong:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "cc=1") {
		t.Errorf("the exit line does not count the Cc: note:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "WAKE SERVE fired=1 notes=3") {
		t.Errorf("the exit line is missing or wrong:\n%s", r.stdout)
	}
}

func TestServeFiresNothingOnAnEmptyHour(t *testing.T) {
	busDir, _ := fakes(t)
	write(t, filepath.Join(busDir, "out"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
	note, noteDir := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")
	r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "60s", "--state", state, "--hours", "1")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	if got := calls(t, noteDir); len(got) != 0 {
		t.Errorf("the receiver was started %d times on an hour with no note; an empty minute costs one fetch and ZERO tokens", len(got))
	}
	if !strings.Contains(r.stdout, "WAKE SERVE fired=0 notes=0") {
		t.Errorf("the exit line does not say fired=0:\n%s", r.stdout)
	}
	// The command starts for a note and for nothing else: never on an interval,
	// never to receipt, never to look.
	polls := 0
	for _, c := range calls(t, busDir) {
		if strings.HasPrefix(c, "inbox ") || strings.HasPrefix(c, "wait ") {
			polls++
		}
	}
	if polls != 60 {
		t.Errorf("the bus was fetched %d times in an hour at --interval 60s, want 60", polls)
	}
}

// A kill between `dispatching` and the spawn, and between the spawn and
// `delivered`, both leave the note UNCERTAIN: a command with no idempotency
// protocol may have acted, and a tool that ran it again would be choosing a
// duplicate action on the mind's behalf.
func TestServeAKilledDispatchIsUncertainAndBlocksTheQueue(t *testing.T) {
	for _, kill := range []string{"before-spawn", "before-delivered"} {
		t.Run(kill, func(t *testing.T) {
			busDir, _ := fakes(t)
			write(t, filepath.Join(busDir, "out"),
				"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first\n")
			note, noteDir := fakeNote(t)
			state := filepath.Join(t.TempDir(), "serve.state")
			args := []string{"serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
				"--interval", "30s", "--state", state, "--hours", "0.02"}

			serveKillPoint = kill
			wakeRun(t, args...)
			serveKillPoint = ""
			if !strings.Contains(read(t, state), "dispatching") {
				t.Fatalf("the kill point did not leave a dispatching record:\n%s", read(t, state))
			}
			before := len(calls(t, noteDir))

			// The restart runs NOTHING and says so, with the command a person
			// types to resolve it.
			r := wakeRun(t, args...)
			if !strings.Contains(r.stdout, "WAKE UNCERTAIN id=aaa111 attempt=1: dispatch interrupted") {
				t.Errorf("the restart does not surface the uncertain dispatch:\n%s", r.stdout)
			}
			if !strings.Contains(r.stdout, "--redeliver aaa111") {
				t.Errorf("the uncertain line does not name the remedy:\n%s", r.stdout)
			}
			if got := len(calls(t, noteDir)); got != before {
				t.Errorf("the receiver was started %d more times on the restart, want 0", got-before)
			}

			// A second note landing now WAITS: an unresolved uncertain entry
			// blocks this receiver's queue.
			write(t, filepath.Join(busDir, "out"),
				"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first\n"+
					"INBOX NOTE id=bbb222 from=Johnny addr=to at=2026-09-11T11:05:00Z path=from-johnny/b.md: the second\n")
			r = wakeRun(t, args...)
			if !strings.Contains(r.stdout, "WAKE BLOCKED as=Rowan uncertain=aaa111 queued=1") {
				t.Errorf("the queue was not blocked behind the uncertain dispatch:\n%s", r.stdout)
			}
			if got := len(calls(t, noteDir)); got != before {
				t.Errorf("a queued note was dispatched while a dispatch may still own this receiver")
			}
			if !strings.Contains(r.stdout, "uncertain=1 queued=1") {
				t.Errorf("the exit line does not carry what waited:\n%s", r.stdout)
			}

			// A person's act clears it, once, as attempt=2 redelivered=1.
			r = wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--state", state,
				"--redeliver", "aaa111", "--on-note", note)
			if r.exit != 0 {
				t.Fatalf("exit = %d; %s", r.exit, r.all())
			}
			if !strings.Contains(r.stdout, "WAKE FIRED ids=1 first=aaa111 rc=0 redelivered=1") {
				t.Errorf("the redelivery did not run it once more:\n%s", r.stdout)
			}
			if got := len(calls(t, noteDir)); got != before+1 {
				t.Errorf("the redelivery started the receiver %d times, want exactly 1", got-before)
			}
			// And now the queue drains, in its own invocation.
			r = wakeRun(t, args...)
			got := calls(t, noteDir)
			if got[len(got)-1] != "bbb222" {
				t.Errorf("the last invocation was %q, want bbb222 in an invocation of its own", got[len(got)-1])
			}

			// A redelivery of something that is not uncertain is refused.
			r = wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--state", state,
				"--redeliver", "bbb222", "--on-note", note)
			if r.exit != 2 || !strings.Contains(r.stderr, "not uncertain") {
				t.Errorf("exit %d; a redelivery is for a dispatch this tool could not prove had finished, and nothing else:\n%s", r.exit, r.stderr)
			}
		})
	}
}

// A kill AFTER `delivered` fires nothing more: exit 0 is the one acceptance
// boundary an arbitrary command offers.
func TestServeAKillAfterDeliveredFiresNothingMore(t *testing.T) {
	busDir, _ := fakes(t)
	write(t, filepath.Join(busDir, "out"),
		"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first\n")
	note, noteDir := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")
	args := []string{"serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "30s", "--state", state, "--hours", "0.02"}
	serveKillPoint = "after-delivered"
	wakeRun(t, args...)
	serveKillPoint = ""
	before := len(calls(t, noteDir))
	r := wakeRun(t, args...)
	if got := len(calls(t, noteDir)); got != before {
		t.Errorf("the restart fired %d more, want 0:\n%s", got-before, r.all())
	}
	if strings.Contains(r.stdout, "WAKE UNCERTAIN") {
		t.Errorf("a delivered note is not uncertain:\n%s", r.stdout)
	}
}

// --on-note-idempotent is the caller's declaration of a STRONGER RECEIVER
// CONTRACT, never an assumption about an arbitrary command: the interrupted
// attempt is run once more as attempt=2 redelivered=1, and a retry that exits
// non-zero has not acknowledged the original dispatch and is uncertain again.
func TestServeTheIdempotentRetryAndItsCompletionBoundary(t *testing.T) {
	t.Run("the retry returns 0", func(t *testing.T) {
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "out"),
			"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first\n"+
				"INBOX NOTE id=bbb222 from=Johnny addr=to at=2026-09-11T11:05:00Z path=from-johnny/b.md: the second\n")
		note, noteDir := fakeNote(t)
		state := filepath.Join(t.TempDir(), "serve.state")
		args := []string{"serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
			"--interval", "30s", "--state", state, "--hours", "0.02", "--on-note-idempotent"}
		serveKillPoint = "before-spawn"
		wakeRun(t, args...)
		serveKillPoint = ""

		r := wakeRun(t, args...)
		got := calls(t, noteDir)
		if len(got) < 1 || !strings.Contains(got[0], "aaa111") {
			t.Fatalf("the interrupted attempt was not run once more before anything queued: %v", got)
		}
		if !strings.Contains(r.stdout, "redelivered=1") {
			t.Errorf("the retry is not marked redelivered:\n%s", r.stdout)
		}
		if !strings.Contains(read(t, state), "delivered") {
			t.Errorf("the completion boundary is the retry's delivered rc=0:\n%s", read(t, state))
		}
	})

	t.Run("the retry answers already-accepted with a non-zero exit", func(t *testing.T) {
		busDir, _ := fakes(t)
		// A alone at the kill, so that B lands while A's dispatch is the one
		// the restart has to resolve rather than being in the same batch.
		write(t, filepath.Join(busDir, "out"),
			"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first\n")
		note, noteDir := fakeNote(t)
		state := filepath.Join(t.TempDir(), "serve.state")
		args := []string{"serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
			"--interval", "30s", "--state", state, "--hours", "0.02", "--on-note-idempotent"}
		serveKillPoint = "before-spawn"
		wakeRun(t, args...)
		serveKillPoint = ""
		write(t, filepath.Join(busDir, "out"),
			"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first\n"+
				"INBOX NOTE id=bbb222 from=Johnny addr=to at=2026-09-11T11:05:00Z path=from-johnny/b.md: the second\n")
		// The duplicate answers immediately with "already accepted" and exit 75
		// while the original still runs. "Already accepted" says nothing about
		// idleness, and idleness is what the queue behind it needs.
		write(t, filepath.Join(noteDir, "rc"), "75")
		before := len(calls(t, noteDir))

		r := wakeRun(t, args...)
		if !strings.Contains(r.stdout, "WAKE UNCERTAIN id=aaa111 attempt=2 rc=75: retry not terminal") {
			t.Errorf("a non-zero retry must be uncertain again and a person's:\n%s", r.stdout)
		}
		if !strings.Contains(r.stdout, "WAKE BLOCKED") && !strings.Contains(r.stdout, "queued=1") {
			t.Errorf("the queue must stay blocked behind it:\n%s", r.stdout)
		}
		fired := calls(t, noteDir)[before:]
		for _, c := range fired {
			if strings.Contains(c, "bbb222") {
				t.Errorf("a different queued id was dispatched past a retry that never acknowledged: %q", c)
			}
		}
		// A further restart runs no third attempt on its own.
		before = len(calls(t, noteDir))
		wakeRun(t, args...)
		if got := len(calls(t, noteDir)); got != before {
			t.Errorf("a third automatic attempt was run; nothing fires forever and nothing goes quiet")
		}
	})
}

// --batch-max, and the receipt that is sent only after `delivered rc=0`.
func TestServeBatchesAndReceiptsAfterDelivered(t *testing.T) {
	busDir, _ := fakes(t)
	var lines []string
	for i := 0; i < 3; i++ {
		lines = append(lines, fmt.Sprintf(
			"INBOX NOTE id=n%d from=Stella addr=to at=2026-09-11T11:0%d:00Z path=from-stella/n%d.md: note %d", i, i, i, i))
	}
	write(t, filepath.Join(busDir, "out"), strings.Join(lines, "\n")+"\n")
	note, noteDir := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")

	r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "30s", "--state", state, "--hours", "0.02", "--batch-max", "2",
		"--receipt", "--remote", "origin", "--branch", "main")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	got := calls(t, noteDir)
	if len(got) != 2 || got[0] != "n0 n1" || got[1] != "n2" {
		t.Fatalf("--batch-max 2 fired %v, want two then one", got)
	}
	receipts := 0
	for _, c := range calls(t, busDir) {
		if strings.HasPrefix(c, "receipt ") {
			receipts++
			if !strings.Contains(c, "--note") {
				t.Errorf("a receipt names no note: %q", c)
			}
		}
	}
	if receipts != 2 {
		t.Errorf("%d receipt calls, want one per batch (per id inside it)", receipts)
	}

	t.Run("a command that fails gets no receipt", func(t *testing.T) {
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "out"),
			"INBOX NOTE id=zzz from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/z.md: one\n")
		note, noteDir := fakeNote(t)
		write(t, filepath.Join(noteDir, "rc"), "3")
		state := filepath.Join(t.TempDir(), "serve.state")
		r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
			"--interval", "30s", "--state", state, "--hours", "0.02",
			"--receipt", "--remote", "origin", "--branch", "main")
		if !strings.Contains(r.stdout, "WAKE FIRED ids=1 first=zzz rc=3") {
			t.Errorf("the exit code is not recorded:\n%s", r.stdout)
		}
		for _, c := range calls(t, busDir) {
			if strings.HasPrefix(c, "receipt ") {
				t.Errorf("a receipt was sent for a command that exited 3: a note whose command failed stays on the open list unreceipted, which is where a note nobody has dealt with belongs")
			}
		}
	})
}

// The stop file ends it, and --hours ends it: a process with no end is the
// orphaned shell of 2026-09-09.
func TestServeEndsOnItsStopFile(t *testing.T) {
	busDir, _ := fakes(t)
	write(t, filepath.Join(busDir, "out"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
	note, _ := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")
	if err := os.WriteFile(state+".stop", []byte("stop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "60s", "--state", state, "--hours", "8")
	if r.exit != 0 || !strings.Contains(r.stdout, "WAKE SERVE fired=0") {
		t.Fatalf("exit %d; the stop file must end it at once:\n%s", r.exit, r.all())
	}
	if n := len(calls(t, busDir)); n > 1 {
		t.Errorf("%d bus calls after a stop file, want none but the version read", n)
	}
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// 10. serve wakes a harness from outside it, once per note, and spends nothing
// while idle.

// fakeNote builds the fake receiver and returns the command line to hand
// --on-note and the directory it records into.
func fakeNote(t *testing.T) (command, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin := install(t, t.TempDir(), "on-note")
	t.Setenv("NOVA_WAKE_FAKE_NOTE", dir)
	return bin, dir
}

func TestServeSurfacesTheUncertainAndWakesOnlyTo(t *testing.T) {
	busDir, _ := fakes(t)
	// Two notes To: the name, one Cc: only, and one for another name -- which
	// nova-bus does not list for this reader at all, and is here as the line
	// this tool must not dispatch even when it sees it.
	write(t, filepath.Join(busDir, "out"), strings.Join([]string{
		"INBOX NOTE id=zzz111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first",
		"INBOX NOTE id=bbb222 from=Johnny addr=to at=2026-09-11T11:01:00Z path=from-johnny/b.md: the second",
		"INBOX NOTE id=ccc333 from=Emma addr=cc at=2026-09-11T11:02:00Z path=from-emma/c.md: a broadcast",
		// And the fourth the spec's fixture names: one for ANOTHER NAME. A bus
		// does not list it for this reader, and a serve that dispatched
		// anything whose addr was merely not cc would start somebody's command
		// over a note nobody addressed to them.
		"INBOX NOTE id=ddd444 from=Freddy addr=other at=2026-09-11T11:03:00Z path=from-freddy/d.md: for somebody else",
		"INBOX OK as=Rowan carrying=4 open=4 notes=4 receipts=0",
	}, "\n")+"\n")
	note, noteDir := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")
	args := []string{"serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "30s", "--state", state, "--hours", "0.02",
		"--remote", "origin", "--branch", "main", "--receipt-max-words", "40"}

	r := wakeRun(t, args...)
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	got := calls(t, noteDir)
	if len(got) != 1 {
		t.Fatalf("the receiver was started %d times, want 1: every note queued at the moment the command is not running is handed to ONE invocation\n%v", len(got), got)
	}
	if got[0] != "zzz111 bbb222" {
		t.Errorf("the receiver was handed %q, want the two To: ids in BUS order (zzz111 first) and nothing else; a sorted order is not bus order", got[0])
	}
	if strings.Contains(strings.Join(got, " "), "ccc333") {
		t.Error("a Cc: note was dispatched; To means must act, cc means should know, and a broadcast to five is five turns")
	}
	if strings.Contains(strings.Join(got, " "), "ddd444") {
		t.Error("a note for another name was dispatched; only addr=to wakes this receiver")
	}
	if read(t, filepath.Join(noteDir, "stdin")) != "" {
		t.Error("something reached the receiver's stdin; the command receives note ids and nothing else")
	}
	for _, banned := range []string{"INBOX", "carrying=", "the first"} {
		if strings.Contains(strings.Join(got, " "), banned) {
			t.Errorf("the receiver was handed %q; never inbox's output, never the carrying line, never a body", banned)
		}
	}
	if !strings.Contains(r.stdout, "WAKE FIRED ids=2 first=zzz111 rc=0 redelivered=0") {
		t.Errorf("the fire line is missing or wrong:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "cc=1") {
		t.Errorf("the exit line must say cc=1 -- the Cc: note, and not the one addressed to another name:\n%s", r.stdout)
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
		"--interval", "60s", "--state", state, "--hours", "1",
		"--remote", "origin", "--branch", "main", "--receipt-max-words", "40")
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
				"--interval", "30s", "--state", state, "--hours", "0.02",
				"--remote", "origin", "--branch", "main", "--receipt-max-words", "40"}

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
		"--interval", "30s", "--state", state, "--hours", "0.02",
		"--remote", "origin", "--branch", "main", "--receipt-max-words", "40"}
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
			"--interval", "30s", "--state", state, "--hours", "0.02", "--on-note-idempotent",
			"--remote", "origin", "--branch", "main", "--receipt-max-words", "40"}
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
			"--interval", "30s", "--state", state, "--hours", "0.02", "--on-note-idempotent",
			"--remote", "origin", "--branch", "main", "--receipt-max-words", "40"}
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
		"--receipt", "--remote", "origin", "--branch", "main", "--receipt-max-words", "40")
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
			"--receipt", "--remote", "origin", "--branch", "main", "--receipt-max-words", "40")
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
		"--interval", "60s", "--state", state, "--hours", "8",
		"--remote", "origin", "--branch", "main", "--receipt-max-words", "40")
	if r.exit != 0 || !strings.Contains(r.stdout, "WAKE SERVE fired=0") {
		t.Fatalf("exit %d; the stop file must end it at once:\n%s", r.exit, r.all())
	}
	if n := len(calls(t, busDir)); n > 1 {
		t.Errorf("%d bus calls after a stop file, want none but the version read", n)
	}
}

// Rule 7 is not the watch verb's alone. `serve` reads the same bus with the
// same program, and a serve that kept only the tokens it knew about would be
// the 2026-09-10 hurt rebuilt in the other verb: an hour of confident quiet
// over a bus refusing every read, ending `fired=0` and exit 0.
//
// "Never filter the status line. Every line the bus source reads is classified
// as suppressed, relayed or standing, and every line is counted ... Nothing is
// dropped silently." (docs/SPEC-WAKE.md, rule 7; prototype item 21, "Lines read
// and never counted".)
func TestServeNeverFiltersTheStatusLine(t *testing.T) {
	busDir, _ := fakes(t)
	write(t, filepath.Join(busDir, "out"), "INBOX REFUSED the bus is not a git checkout\n")
	write(t, filepath.Join(busDir, "exit"), "2\n")
	note, noteDir := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")
	r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "60s", "--state", state, "--hours", "1",
		"--remote", "origin", "--branch", "main", "--receipt-max-words", "40")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	if len(calls(t, noteDir)) != 0 {
		t.Error("the receiver was started over a refusing bus; the command starts for a note and for nothing else")
	}
	// Shown every time, woken on once: the first sighting is relayed verbatim,
	// every later one stands.
	if !strings.Contains(r.stdout, "WAKE BUS LINE INBOX REFUSED the bus is not a git checkout") {
		t.Errorf("the REFUSED line was dropped; a line this tool cannot classify is a line this tool prints:\n%s", r.all())
	}
	if n := countLines(r.stdout, "WAKE BUS STANDING INBOX REFUSED"); n != 59 {
		t.Errorf("%d standing lines over 60 polls, want 59: shown every time, woken on once\n%s", n, r.stdout)
	}
	if !strings.Contains(r.stdout, "WAKE SOURCE bus read=60 suppressed=0 relayed=1 standing=59") {
		t.Errorf("the four counts are missing or do not add up; a bus that printed nothing and a bus that printed sixty REFUSED lines must not look the same:\n%s", r.stdout)
	}
	if !strings.Contains(r.stderr, "WAKE POLL bus") {
		t.Errorf("a nova-bus that exited non-zero was silent on stderr:\n%s", r.stderr)
	}
}

// Rule 10: serve "runs as its own process outside any session, FETCHES THE BUS
// EVERY --interval (a git fetch costs no tokens; the interval matches the
// latency a person will accept, never the second)". A plain `inbox` reads the
// checkout and never the remote, so a serve that did not fetch would sit on a
// standing checkout forever.
func TestServeFetchesEveryIntervalAndPinsTheVersion(t *testing.T) {
	busDir, _ := fakes(t)
	write(t, filepath.Join(busDir, "out"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
	note, _ := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")
	r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "30s", "--state", state, "--hours", "0.02",
		"--remote", "origin", "--branch", "main", "--receipt-max-words", "40")
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	polls, versions := 0, 0
	for _, c := range calls(t, busDir) {
		switch {
		case strings.HasPrefix(c, "version"):
			versions++
		case strings.HasPrefix(c, "wait "):
			polls++
			if advanced(c) {
				t.Errorf("a serve poll carried --advance; serve moves no cursor: %q", c)
			}
			if !strings.Contains(c, "--remote origin") || !strings.Contains(c, "--branch main") {
				t.Errorf("a serve poll did not fetch: %q", c)
			}
			// The timeout is derived from the interval this run was given and
			// is never a guessed second.
			if !strings.Contains(c, "--timeout 30s") {
				t.Errorf("the wait timeout is not the interval: %q", c)
			}
		case strings.HasPrefix(c, "inbox "):
			t.Errorf("a serve poll read the checkout without fetching: %q", c)
		}
	}
	if versions != 1 {
		t.Errorf("nova-bus version was read %d times, want once before anything else: the two-poll freshness promise is a property of the push", versions)
	}
	if polls == 0 {
		t.Error("serve never fetched")
	}

	t.Run("a serve with no remote is refused", func(t *testing.T) {
		fakes(t)
		r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
			"--interval", "30s", "--state", filepath.Join(t.TempDir(), "s"), "--hours", "1")
		if r.exit != 2 || !strings.Contains(r.stderr, "--remote") || !strings.Contains(r.stderr, "--branch") {
			t.Errorf("exit %d; a serve that cannot fetch is a serve that cannot see its mail:\n%s", r.exit, r.stderr)
		}
	})

	t.Run("a nova-bus other than the pin is refused", func(t *testing.T) {
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "version"), "nova-bus v0.10.4 darwin/arm64 go1.27.1\n")
		r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
			"--interval", "30s", "--state", filepath.Join(t.TempDir(), "s"), "--hours", "1",
			"--remote", "origin", "--branch", "main", "--receipt-max-words", "40")
		if r.exit != 2 || !strings.Contains(r.stderr, "v0.10.4") || !strings.Contains(r.stderr, "v0.10.3") {
			t.Errorf("exit %d; a nova-bus that stopped fetching inside its push leaves a serve that looks healthy and is blind:\n%s", r.exit, r.stderr)
		}
	})
}

// "An interrupted `attempt=2` is `uncertain` and blocks all the same, so
// nothing fires forever and nothing goes quiet" -- NEVER A THIRD AUTOMATIC RUN
// (docs/SPEC-WAKE.md, rule 10). The idempotent exception is one retry of an
// interrupted attempt=1 and is not a loop: a tool that kept running the command
// after every kill would be choosing duplicate actions nobody chose.
func TestServeRunsNoThirdAttemptOnItsOwn(t *testing.T) {
	busDir, _ := fakes(t)
	write(t, filepath.Join(busDir, "out"),
		"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first\n")
	note, noteDir := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")
	args := []string{"serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "30s", "--state", state, "--hours", "0.02", "--on-note-idempotent",
		"--remote", "origin", "--branch", "main", "--receipt-max-words", "40"}

	// The first kill interrupts attempt=1; the second interrupts the retry the
	// idempotent contract allows, leaving `dispatching attempt=2`.
	serveKillPoint = "before-spawn"
	wakeRun(t, args...)
	wakeRun(t, args...)
	serveKillPoint = ""
	if !strings.Contains(read(t, state), "dispatching") || !strings.Contains(read(t, state), "attempt=2") {
		t.Fatalf("the second kill did not leave an interrupted attempt=2:\n%s", read(t, state))
	}
	before := len(calls(t, noteDir))

	r := wakeRun(t, args...)
	if got := len(calls(t, noteDir)); got != before {
		t.Errorf("a third automatic attempt was run (%d more); an interrupted attempt=2 is uncertain and blocks all the same", got-before)
	}
	if !strings.Contains(r.stdout, "WAKE UNCERTAIN id=aaa111 attempt=2: dispatch interrupted") {
		t.Errorf("the interrupted attempt=2 was not surfaced as uncertain:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "--redeliver aaa111") {
		t.Errorf("the uncertain line does not name the person's remedy:\n%s", r.stdout)
	}
}

// Two halves of test 10 the first build named and did not assert.
//
// "Three notes landing while the command runs are `queued` and fired in ONE
// invocation carrying three ids after it returns, never beside it", and
// "`max_wait=` equals the injected clock's longest queued interval" -- the
// latency Stella's sixth idea asks to measure, which read 0s in every green run.
func TestServeCoalescesWhatWaitedAndMeasuresHowLongItWaited(t *testing.T) {
	t.Run("three notes that land while the command runs fire in one invocation", func(t *testing.T) {
		busDir, _ := fakes(t)
		// The first poll sees A alone; the second sees the three that landed
		// while A's command held the receiver.
		write(t, filepath.Join(busDir, "out.1"),
			"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first\n")
		write(t, filepath.Join(busDir, "out"), strings.Join([]string{
			"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first",
			"INBOX NOTE id=bbb222 from=Johnny addr=to at=2026-09-11T11:01:00Z path=from-johnny/b.md: the second",
			"INBOX NOTE id=ccc333 from=Emma addr=to at=2026-09-11T11:02:00Z path=from-emma/c.md: the third",
			"INBOX NOTE id=ddd444 from=Freddy addr=to at=2026-09-11T11:03:00Z path=from-freddy/d.md: the fourth",
		}, "\n")+"\n")
		note, noteDir := fakeNote(t)
		state := filepath.Join(t.TempDir(), "serve.state")
		r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
			"--interval", "30s", "--state", state, "--hours", "0.02",
			"--remote", "origin", "--branch", "main", "--receipt-max-words", "40")
		if r.exit != 0 {
			t.Fatalf("exit = %d; %s", r.exit, r.all())
		}
		got := calls(t, noteDir)
		if len(got) != 2 {
			t.Fatalf("the receiver was started %d times, want 2: one for A and ONE for the three that waited\n%v", len(got), got)
		}
		if got[1] != "bbb222 ccc333 ddd444" {
			t.Errorf("the second invocation carried %q, want the three queued ids in bus order in one invocation; one turn reads k notes rather than k turns reading one", got[1])
		}
		if !strings.Contains(r.stdout, "WAKE FIRED ids=3 first=bbb222") {
			t.Errorf("the fire line does not say three ids went in one batch:\n%s", r.stdout)
		}
	})

	t.Run("max_wait is the longest a note sat queued", func(t *testing.T) {
		busDir, _ := fakes(t)
		write(t, filepath.Join(busDir, "out"),
			"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first\n"+
				"INBOX NOTE id=bbb222 from=Johnny addr=to at=2026-09-11T11:01:00Z path=from-johnny/b.md: the second\n")
		note, _ := fakeNote(t)
		state := filepath.Join(t.TempDir(), "serve.state")
		// --batch-max 1 leaves B queued for exactly one interval of the
		// INJECTED clock, and the exit line must say so.
		r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
			"--interval", "30s", "--state", state, "--hours", "0.02", "--batch-max", "1",
			"--remote", "origin", "--branch", "main", "--receipt-max-words", "40")
		if !strings.Contains(r.stdout, "max_wait=30s") {
			t.Errorf("max_wait is not the longest a note sat queued before its dispatch; 0s in every run is a measurement that measures nothing:\n%s", r.stdout)
		}
	})
}

// Bus order has to survive a RESTART, which is the case the coalescing rule is
// really about: every note this receiver still carries already has a serve:<id>
// record, so nothing about it is new, and a dispatch that read its order off
// the state file would read SORTED order and call it bus order.
//
// "Dispatch is coalesced. Every note `queued` at the moment the command is not
// running is handed to one invocation, IN BUS ORDER, at most --batch-max ids"
// (docs/SPEC-WAKE.md, rule 10).
func TestServeKeepsBusOrderAcrossARestart(t *testing.T) {
	busDir, _ := fakes(t)
	// Bus order zzz111, yyy222, bbb333. Sorted order is bbb333, yyy222 -- so
	// the two orders disagree about the pair that is left queued.
	write(t, filepath.Join(busDir, "out"), strings.Join([]string{
		"INBOX NOTE id=zzz111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first",
		"INBOX NOTE id=yyy222 from=Johnny addr=to at=2026-09-11T11:01:00Z path=from-johnny/b.md: the second",
		"INBOX NOTE id=bbb333 from=Emma addr=to at=2026-09-11T11:02:00Z path=from-emma/c.md: the third",
	}, "\n")+"\n")
	note, noteDir := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")
	// One poll, one dispatch of one id: the other two are left queued in the
	// state file for the next process to find.
	args := []string{"serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "30s", "--state", state, "--hours", "0.005", "--batch-max", "1",
		"--remote", "origin", "--branch", "main", "--receipt-max-words", "40"}
	first := wakeRun(t, args...)
	if first.exit != 0 {
		t.Fatalf("exit = %d; %s", first.exit, first.all())
	}
	if got := calls(t, noteDir); len(got) != 1 || got[0] != "zzz111" {
		t.Fatalf("the first call handed %v, want one invocation carrying zzz111: bus order, --batch-max 1", got)
	}

	// A new process, with two carried notes it has records for.
	r := wakeRun(t, append(append([]string{}, args[:len(args)-4]...), "--remote", "origin", "--branch", "main", "--receipt-max-words", "40")...)
	if r.exit != 0 {
		t.Fatalf("exit = %d; %s", r.exit, r.all())
	}
	got := calls(t, noteDir)
	if len(got) < 2 {
		t.Fatalf("the restart dispatched nothing: %v", got)
	}
	if got[1] != "yyy222" {
		t.Errorf("the restart handed %q first, want yyy222: a restart reads sorted state order, and sorted order is not bus order", got[1])
	}
	if strings.Contains(strings.Join(got[1:], " "), "zzz111") {
		t.Errorf("a delivered note was dispatched again on the restart: %v", got)
	}
}

// Rule 5 and the grammar: "A relayed line is escaped and NEVER SHORTENED BELOW
// ITS OWN TAIL BUDGET; and no line is exempt from the cap". A bus line is
// somebody else's bytes, and one of them must not spend the whole of what a
// window learns from this return.
func TestASoLongBusLineIsCappedWhenServeRelaysIt(t *testing.T) {
	busDir, _ := fakes(t)
	long := "INBOX REFUSED " + strings.Repeat("the bus is not a git checkout ", 400)
	write(t, filepath.Join(busDir, "out"), long+"\n")
	note, _ := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")
	r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "30s", "--state", state, "--hours", "0.005",
		"--remote", "origin", "--branch", "main", "--receipt-max-words", "40")
	for _, line := range strings.Split(r.stdout, "\n") {
		if !strings.HasPrefix(line, "WAKE BUS LINE") {
			continue
		}
		if len(line) > 2*oneline.TailBytes {
			t.Errorf("a relayed bus line is %d bytes; no line is exempt from the cap, and a silent truncation is the prototype's cut160", len(line))
		}
		if !strings.Contains(line, "B") || !strings.Contains(line, "...+") {
			t.Errorf("the cut is not marked; a reader cannot tell a cut from an author's own ellipsis:\n%s", line)
		}
	}
	if !strings.Contains(r.stdout, "WAKE BUS LINE") {
		t.Fatalf("the line was not relayed at all:\n%s", r.all())
	}
}

// Exit 2 is for "a missing or MALFORMED flag". The watch verb refuses a
// non-positive --gh-timeout; serve took the same class of number and built a
// context that can never finish out of it, so every poll printed `nova-bus
// timed out after 0s` and the fetch never happened.
func TestServeRefusesABudgetThatCanNeverFinish(t *testing.T) {
	note, _ := fakeNote(t)
	for _, tc := range []struct{ name, flag, value string }{
		{"a zero git timeout", "--git-timeout", "0"},
		{"a negative git timeout", "--git-timeout", "-5"},
		{"a zero receipt word budget", "--receipt-max-words", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
				"--interval", "30s", "--state", filepath.Join(t.TempDir(), "s"), "--hours", "1",
				"--remote", "origin", "--branch", "main", "--receipt-max-words", "40", tc.flag, tc.value)
			if r.exit != 2 || !strings.Contains(r.stderr, tc.flag) {
				t.Errorf("exit %d; a budget of zero or less is not 'unlimited', it is a call that can never finish:\n%s", r.exit, r.stderr)
			}
		})
	}
}

// The process budget must cover the fetch it asked for. serve hands `wait` a
// --timeout of one interval and then wrapped the process in --git-timeout, so a
// `serve --interval 60s` at the default 45s killed its own fetch every poll and
// printed WAKE POLL for it: the fetch covered the first 45s of each interval
// and the rest of the minute was blind.
func TestServesPollBudgetCoversTheIntervalItFetchesFor(t *testing.T) {
	busDir, _ := fakes(t)
	write(t, filepath.Join(busDir, "out"), "INBOX OK as=Rowan carrying=0 open=0 notes=0 receipts=0\n")
	// A nova-bus that takes longer than --git-timeout and less than the
	// interval it was given, which is exactly what a blocking `wait` does.
	write(t, filepath.Join(busDir, "delay"), "1200\n")
	note, _ := fakeNote(t)
	state := filepath.Join(t.TempDir(), "serve.state")
	r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "5s", "--state", state, "--hours", "0.001",
		"--remote", "origin", "--branch", "main", "--receipt-max-words", "40", "--git-timeout", "1")
	if strings.Contains(r.stderr, "timed out") {
		t.Errorf("serve killed its own fetch: the budget for the process must cover the interval the wait was told to block for\n%s", r.stderr)
	}
}

// Test 10, the half that says what a person gets wrong: "--redeliver without
// --on-note is refused naming the flag" -- because the state stores no command,
// so a redelivery names its handler.
func TestServeRefusesARedeliveryThatNamesNoHandler(t *testing.T) {
	r := wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan",
		"--state", filepath.Join(t.TempDir(), "s"), "--redeliver", "aaa111")
	if r.exit != 2 {
		t.Fatalf("exit = %d, want 2:\n%s", r.exit, r.all())
	}
	if !strings.Contains(r.stderr, "--on-note") {
		t.Errorf("the refusal does not name the flag it wants:\n%s", r.stderr)
	}
}

// A first attempt that exited non-zero was written `delivered rc=7` -- so the
// note was never dispatched again, and --redeliver refused it as "delivered,
// not uncertain". A harness out of credits, a typo in the command, a crashed
// child: the note is gone, with no path back. The spec's promise is "never a
// silent duplicate, and NEVER A SILENT LOSS" (Known limits).
func TestServeDoesNotLoseANoteWhoseCommandFailed(t *testing.T) {
	busDir, _ := fakes(t)
	write(t, filepath.Join(busDir, "out"),
		"INBOX NOTE id=aaa111 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/a.md: the first\n")
	note, noteDir := fakeNote(t)
	write(t, filepath.Join(noteDir, "rc"), "7")
	state := filepath.Join(t.TempDir(), "serve.state")
	args := []string{"serve", "--bus", t.TempDir(), "--as", "Rowan", "--on-note", note,
		"--interval", "30s", "--state", state, "--hours", "0.005",
		"--remote", "origin", "--branch", "main", "--receipt-max-words", "40"}
	r := wakeRun(t, args...)
	if !strings.Contains(r.stdout, "WAKE FIRED ids=1 first=aaa111 rc=7") {
		t.Fatalf("the exit code is not recorded:\n%s", r.all())
	}
	if !strings.Contains(r.stdout, "WAKE UNCERTAIN id=aaa111 attempt=1 rc=7") {
		t.Errorf("a first attempt that never returned 0 was written off as delivered:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "failed=1") {
		t.Errorf("the exit line does not count the dispatch that failed; the count line prints on failure too:\n%s", r.stdout)
	}
	if strings.Contains(read(t, state), "delivered") {
		t.Errorf("exit 0 is the one acceptance boundary an arbitrary command offers:\n%s", read(t, state))
	}

	// And the path back exists: a person's --redeliver is accepted, not
	// refused, and the note runs again once the receiver is fixed.
	if err := os.Remove(filepath.Join(noteDir, "rc")); err != nil {
		t.Fatal(err)
	}
	before := len(calls(t, noteDir))
	r = wakeRun(t, "serve", "--bus", t.TempDir(), "--as", "Rowan", "--state", state,
		"--redeliver", "aaa111", "--on-note", note)
	if r.exit != 0 {
		t.Fatalf("a person cannot get the note back: exit %d\n%s", r.exit, r.all())
	}
	if got := len(calls(t, noteDir)); got != before+1 {
		t.Errorf("the redelivery started the receiver %d times, want 1", got-before)
	}
	if !strings.Contains(r.stdout, "redelivered=1") {
		t.Errorf("the redelivery is not marked:\n%s", r.stdout)
	}
}

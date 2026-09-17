package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// CARD-8510 / nova-tools #82, the efficiency card of 2026-09-12.
//
// The card is a MEASUREMENT, not a defect list: four `nova-bus inbox` spawns
// for four polls, one `git log` per `--line` per poll, a re-walk of the
// reports tree per poll, and a watch that costs one turn whatever it returns.
// The spec answers it by pricing one tick per source (docs/SPEC-WAKE.md, "What
// one tick costs") and by making a watch ONE blocking call whose polls are
// subprocesses and not turns (the first lesson of 2026-09-11). This test pins
// that contract and is GREEN on dev, because the card's work was absorbed by
// the 2026-09-13 amendment (#178) and the pin it quotes
// (`const PinnedBusVersion = "v0.10.3"`) is this build's own version now.
func TestWatchEfficiencyCardContract(t *testing.T) {
	busDir, _ := fakes(t)
	// One note, re-listed on EVERY poll because a watch without
	// --advance-cursor consumes nothing. The first call relays it and returns
	// on the change; the second, over the same state, finds it already printed
	// and suppresses it on every poll -- the card's "read=20 suppressed=20,
	// relayed=0" shape, and what makes a quiet watch run to its deadline.
	write(t, filepath.Join(busDir, "out"),
		"INBOX NOTE id=n1 from=Stella addr=to at=2026-09-11T11:00:00Z path=from-stella/one.md: hello\n"+
			"INBOX OK as=Rowan carrying=1 open=1 notes=1 receipts=0\n")

	// A reports tree the card names: walked every poll, reported on no poll
	// because the world did not move after the cold start recorded it.
	reports := t.TempDir()
	write(t, filepath.Join(reports, "job", "RESULT.md"), "# a finding\n")
	state := filepath.Join(t.TempDir(), "wake.state")

	args := []string{"watch", "--state", state, "--max", "20s", "--on-deadline", "quiet",
		"--interval", "5s", "--bus", t.TempDir(), "--as", "Rowan", "--receipt-max-words", "40",
		"--reports", reports}

	// The first call records the world as it prints the one note; the report
	// is cold and is not reported.
	first := wakeRun(t, args...)
	if first.exit != 0 {
		t.Fatalf("first call exit = %d, want 0; %s", first.exit, first.all())
	}
	if !strings.Contains(first.stdout, "WAKE CHANGE") {
		t.Fatalf("the first call must relay the note:\n%s", first.stdout)
	}
	if strings.Contains(first.stdout, "WAKE REPORT ") {
		t.Fatalf("a cold reports tree reported a file; the cold-start rule records and does not report:\n%s", first.stdout)
	}

	// The second call is the card's quiet watch: nothing moved, so it runs its
	// whole deadline, polling the bus four times and suppressing every read.
	r := wakeRun(t, args...)
	if r.exit != 0 {
		t.Fatalf("exit = %d, want 0 (a deadline is not an error); %s", r.exit, r.all())
	}

	// ONE call, ONE verdict line: the clock lives inside the tool, where a
	// poll costs a subprocess and not a turn.
	verdicts := 0
	for _, line := range strings.Split(r.stdout, "\n") {
		toks := strings.Fields(line)
		if len(toks) >= 2 && toks[0] == "WAKE" {
			switch toks[1] {
			case "CHANGE", "QUIET", "STOPPED", "BROKEN":
				verdicts++
			}
		}
	}
	if verdicts != 1 {
		t.Fatalf("%d verdict lines, want exactly 1: one call, one return, one turn per change\n%s", verdicts, r.stdout)
	}

	// One program start per poll, and `nova-bus version` exactly ONCE per call
	// before the opening line -- the card counted a whole nova-bus process per
	// poll, and that is the price the spec prices rather than a defect to
	// remove. Five inbox reads: one for the change, four for the quiet watch.
	polls, versions := 0, 0
	for _, c := range calls(t, busDir) {
		switch {
		case strings.HasPrefix(c, "inbox "):
			polls++
		case c == "version":
			versions++
		}
	}
	if polls != 5 {
		t.Errorf("the bus was polled %d times across the two calls, want 5 (one for the change + four for 20s / 5s)", polls)
	}
	if versions != 2 {
		t.Errorf("`nova-bus version` ran %d times, want exactly 2 (once per call, before the opening line)", versions)
	}

	// Rule 7's sum holds and the suppression is COUNTED, not printed.
	var read, suppressed, relayed, standing int
	for _, line := range strings.Split(r.stdout, "\n") {
		if !strings.HasPrefix(line, "WAKE SOURCE bus ") {
			continue
		}
		for _, tok := range strings.Fields(line) {
			switch {
			case strings.HasPrefix(tok, "read="):
				read = atoiTok(tok)
			case strings.HasPrefix(tok, "suppressed="):
				suppressed = atoiTok(tok)
			case strings.HasPrefix(tok, "relayed="):
				relayed = atoiTok(tok)
			case strings.HasPrefix(tok, "standing="):
				standing = atoiTok(tok)
			}
		}
	}
	if read != suppressed+relayed+standing {
		t.Errorf("bus read=%d, but suppressed+relayed+standing=%d: every line is classified and counted", read, suppressed+relayed+standing)
	}
	if relayed != 0 {
		t.Errorf("relayed=%d, want 0 (the note was printed by the first call)", relayed)
	}
	if suppressed < 4 {
		t.Errorf("suppressed=%d, want at least 4 (the same note re-listed over the quiet watch)", suppressed)
	}
}

// atoiTok reads the leading number after the `=` in a bare key=value token.
func atoiTok(tok string) int {
	_, v, _ := strings.Cut(tok, "=")
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

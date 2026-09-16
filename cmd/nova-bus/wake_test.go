package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The three verbs class K of the pit stop (#828) asks for, each with the failure it closes.
//
// K is "the bus is a mailbox read by minds": 1,937 open notes, receipts written as prose,
// and a wake that was a model reading INBOX OPEN to find out what it was for. The answers
// are a wake that prints AT MOST three lines and never a listing, a receipt that leaves a
// ROW a machine can read, and a close a daily job can run without computing an instant.

// dayBus is a bus the size of a day's traffic: the fixture plus n notes from Bo, oldest
// first, every one of them addressed to Ada and open. It is what the bound in
// TestWakeIsThreeLinesOnADaySizedBus is measured against -- a wake whose output grew with
// the backlog would be the failure class K names, and 200 notes is the day that was.
func dayBus(t *testing.T, checkout string, n int) {
	t.Helper()
	var index strings.Builder
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("bo-%012d", i)
		stamp := fmt.Sprintf("2026-09-08T%02d%02dZ", i/60, i%60)
		when := fmt.Sprintf("Tue Sep  8 %02d:%02d:00 UTC 2026", i/60, i%60)
		path := fmt.Sprintf("from-bo/%s-day-%s.md", stamp, id)
		writeFile(t, checkout, path, "From: Bo\nTo: Ada\nDate: "+when+"\nId: "+id+
			"\nSubject: Day note "+fmt.Sprint(i)+
			"\n\nOne of the day's notes, long enough not to read as a bare acknowledgement of anything at all.\n")
		index.WriteString(fmt.Sprintf("%s\t%s\t2026-09-08T%02d:%02d:00Z\tAda\t-\n", id, path, i/60, i%60))
	}
	appendFile(t, checkout, "from-bo/INDEX", index.String())
	commitAs(t, checkout, "Bo", "a day of notes")
}

// A wake is a pin and ONE note, whatever the backlog is. This is the bound: three lines and
// under 400 bytes over a bus carrying 202 open notes, and no OPEN listing anywhere in it.
func TestWakeIsThreeLinesOnADaySizedBus(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	dayBus(t, checkout, 200)

	// The newest note addressed to Ada, the one the wake must find, with a deadline in its
	// body and a Cc that must not be counted as a To.
	writeFile(t, checkout, "from-bo/2026-09-09T1000Z-the-one-999999999999.md",
		"From: Bo\nTo: Ada\nCc: Dana\nDate: Wed Sep  9 10:00:00 UTC 2026\nId: bo-999999999999\n"+
			"Subject: The gate on the merge queue\n\nRead the gate and say yes or no by deadline 2026-09-09T18:00:00Z, please.\n")
	appendFile(t, checkout, "from-bo/INDEX",
		"bo-999999999999\tfrom-bo/2026-09-09T1000Z-the-one-999999999999.md\t2026-09-09T10:00:00Z\tAda\t-\n")
	commitAs(t, checkout, "Bo", "the one")

	pin := filepath.Join(t.TempDir(), "pin.md")
	if err := os.WriteFile(pin, []byte("Rowan: finish the roadmap, merge nothing red.\nand a second line nobody wakes on\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := invoke(t, "", "wake", "--bus", checkout, "--as", "Ada", "--pin", pin,
		"--remote", "origin", "--branch", "main").mustCode(t, 0)

	lines := strings.Split(strings.TrimRight(r.stdout, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("a wake printed %d lines, want 3:\n%s", len(lines), r.stdout)
	}
	if len(r.stdout) > 400 {
		t.Fatalf("a wake printed %d bytes, want under 400:\n%s", len(r.stdout), r.stdout)
	}
	if strings.Contains(r.stdout, "OPEN") && !strings.Contains(r.stdout, "open_to=") {
		t.Fatalf("a wake listed the open notes:\n%s", r.stdout)
	}
	if !strings.HasPrefix(lines[0], "WAKE PIN Rowan: finish the roadmap") {
		t.Fatalf("the first line is not the pin's first line: %q", lines[0])
	}
	if strings.Contains(r.stdout, "second line nobody wakes on") {
		t.Fatalf("a wake printed more of the pin than its first line:\n%s", r.stdout)
	}
	for _, want := range []string{
		"WAKE NOTE id=bo-999999999999",
		"from=Bo",
		`subject="The gate on the merge queue"`,
		"deadline=2026-09-09T18:00:00Z",
		"path=from-bo/2026-09-09T1000Z-the-one-999999999999.md",
	} {
		if !strings.Contains(lines[1], want) {
			t.Fatalf("the note line does not carry %q: %q", want, lines[1])
		}
	}
	// 201 to (the day's 200 and the one), the fixture's question, and the fixture's bare
	// acknowledgement, which is a receipt and not work. Cc is counted apart.
	if !strings.HasPrefix(lines[2], "WAKE OK open_to=202 open_cc=0") {
		t.Fatalf("the counts line is wrong: %q", lines[2])
	}
}

// Nothing addressed to you is exit 3 and no note line: a pulse that has nothing to do says
// so in one line and costs its harness nothing. It is not a refusal -- the verb ran.
func TestWakeIsQuietWhenNothingIsAddressedToYou(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	// Bo's own lane: the fixture's notes are TO Ada, so Bo is addressed by nothing.
	r := invoke(t, "", "wake", "--bus", checkout, "--as", "Bo",
		"--remote", "origin", "--branch", "main").mustCode(t, 3)
	if strings.Contains(r.stdout, "WAKE NOTE") {
		t.Fatalf("a quiet wake printed a note:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "WAKE OK open_to=0 open_cc=0") {
		t.Fatalf("a quiet wake did not print its counts:\n%s", r.stdout)
	}
	if n := len(strings.Split(strings.TrimRight(r.stdout, "\n"), "\n")); n != 1 {
		t.Fatalf("a quiet wake with no pin printed %d lines, want 1:\n%s", n, r.stdout)
	}
}

// A note already receipted does not wake anybody a second time: the wake is the UNRECEIPTED
// To note, which is what makes a pulse that receipts what it did idempotent.
func TestWakeSkipsWhatThisLaneHasAlreadyHeard(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	first := invoke(t, "", "wake", "--bus", checkout, "--as", "Ada",
		"--remote", "origin", "--branch", "main").mustCode(t, 0)
	if !strings.Contains(first.stdout, "id=bo-abcdef012345") {
		t.Fatalf("the first wake did not name the fixture's question:\n%s", first.stdout)
	}
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-abcdef012345",
		"--verdict", "ANSWERED", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0)
	invoke(t, "", "wake", "--bus", checkout, "--as", "Ada",
		"--remote", "origin", "--branch", "main").mustCode(t, 3)
}

// The receipt ROW: a verdict a machine reads without opening a note, beside the receipt note
// that stays for compatibility. This is the round trip class K asks for.
func TestReceiptVerdictWritesARowThatReceiptsReadsBack(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	r := invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-abcdef012345",
		"--verdict", "APPROVE", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "RECEIPT OK recorded=1").
		mustContain(t, "stdout", "RECEIPT ROWS rows=1 path=receipts/ada.tsv verdict=APPROVE")
	_ = r

	// The row is a TSV line, and the receipt note is still there beside it.
	row := read(t, checkout, "receipts/ada.tsv")
	fields := strings.Split(strings.TrimRight(row, "\n"), "\t")
	if len(fields) != 4 {
		t.Fatalf("the row is not four fields: %q", row)
	}
	if fields[1] != "Ada" || fields[2] != "bo-abcdef012345" || fields[3] != "APPROVE" {
		t.Fatalf("the row's fields are wrong: %q", row)
	}
	if !strings.Contains(read(t, checkout, "from-ada/RECEIPTS"), "bo-abcdef012345") {
		t.Fatalf("the receipt note's RECEIPTS line is gone; it stays for compatibility")
	}

	back := invoke(t, "", "receipts", "--bus", checkout, "--note", "bo-abcdef012345").
		mustCode(t, 0).
		mustContain(t, "stdout", "as=Ada").
		mustContain(t, "stdout", "verdict=APPROVE").
		mustContain(t, "stdout", "RECEIPTS OK note=bo-abcdef012345 rows=1 listed=1")
	if n := len(strings.Split(strings.TrimRight(back.stdout, "\n"), "\n")); n != 2 {
		t.Fatalf("one row read back as %d lines:\n%s", n, back.stdout)
	}

	// A verdict word is one token: a value holding a tab would author a column.
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-111111111111",
		"--verdict", "not one word", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).mustContain(t, "stderr", "--verdict")

	// And the listing is bounded, like every other listing this tool prints.
	invoke(t, "", "receipts", "--bus", checkout, "--note", "bo-abcdef012345", "--max", "0").
		mustCode(t, 0).
		mustContain(t, "stdout", "RECEIPTS OK note=bo-abcdef012345 rows=1 listed=0")
}

// close --older-than is close --before with the instant worked out from the clock, so a
// daily job is one line in a crontab and never a shell computing a date.
func TestCloseOlderThanClosesExactlyTheNotesPastTheWindow(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	// The fixture's two notes are dated 2026-09-07; the clock these tests run on is
	// 2026-09-09T12:34:56Z, so a two-day window closes those two and keeps this one.
	writeFile(t, checkout, "from-bo/2026-09-09T1000Z-new-333333333333.md",
		"From: Bo\nTo: Ada\nDate: Wed Sep  9 10:00:00 UTC 2026\nId: bo-333333333333\nSubject: New\n\nStill open.\n")
	appendFile(t, checkout, "from-bo/INDEX",
		"bo-333333333333\tfrom-bo/2026-09-09T1000Z-new-333333333333.md\t2026-09-09T10:00:00Z\tAda\t-\n")
	commitAs(t, checkout, "Bo", "a new note")

	invoke(t, "", "close", "--bus", checkout, "--as", "Ada", "--older-than", "2d", "--dry-run").
		mustCode(t, 0).
		mustContain(t, "stdout", "CLOSE OK closed=2 kept=1 commit=-")
	if entries := mdFiles(t, checkout, "from-ada"); len(entries) != 0 {
		t.Fatalf("--dry-run wrote %d receipt notes, want 0", len(entries))
	}

	// A window nothing is older than closes nothing.
	invoke(t, "", "close", "--bus", checkout, "--as", "Ada", "--older-than", "30d", "--dry-run").
		mustCode(t, 0).
		mustContain(t, "stdout", "CLOSE OK closed=0 kept=3 commit=-")

	// The two flags are one decision: naming both is a bad invocation, naming neither too.
	invoke(t, "", "close", "--bus", checkout, "--as", "Ada", "--older-than", "2d",
		"--before", "2026-09-08T00:00:00Z", "--dry-run").
		mustCode(t, 2).mustContain(t, "stderr", "--before and --older-than")
	invoke(t, "", "close", "--bus", checkout, "--as", "Ada", "--dry-run").
		mustCode(t, 2).mustContain(t, "stderr", "--before")
	invoke(t, "", "close", "--bus", checkout, "--as", "Ada", "--older-than", "soon", "--dry-run").
		mustCode(t, 2).mustContain(t, "stderr", "--older-than")

	// And it writes what the dry run said it would.
	invoke(t, "", "close", "--bus", checkout, "--as", "Ada", "--older-than", "2d",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "CLOSE OK closed=2 kept=1")
	if entries := mdFiles(t, checkout, "from-ada"); len(entries) != 2 {
		t.Fatalf("close --older-than wrote %d receipt notes, want 2", len(entries))
	}
}

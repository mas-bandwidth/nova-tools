package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The INBOX OPEN line used to be two lines: one carrying the counts, and, past a size, a
// second carrying a whole pasted command. A reader parsing OPEN had to know the threshold
// to tell a small list from a large one. It is one line now, and `large=` plus `remedy=` say
// the sentence and the way out without a command the caller already built.
func TestInboxOpenIsOneLine(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("bo-c%011d", i)
		writeFile(t, checkout, fmt.Sprintf("from-bo/2026-09-09T12%02dZ-many-%s.md", i, id),
			fmt.Sprintf("From: Bo\nTo: Ada\nDate: Wed Sep  9 12:%02d:00 UTC 2026\nId: %s\nSubject: One of many %d\n\nOpen.\n", i, id, i))
	}
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "many notes")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	r := invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OPEN carrying=62 heard=0 large=true remedy=reply or receipt each note, or close --before <instant> as an explicit bulk cutoff")
	if n := strings.Count(r.stdout, "INBOX OPEN carrying=62 heard="); n != 1 {
		t.Fatalf("the backlog counts were not on exactly one line:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "is large") {
		t.Fatalf("the large-list sentence still sits on its own line:\n%s", r.stdout)
	}
}

// A large carrying set names reply/receipt as the normal path, not `--advance` alone: plain
// `inbox --advance` moves the read cursor while the OPEN entries stay carried, so it loops
// without resolving anything. `close --before` is named only as the explicit opt-in cutoff.
func TestInboxLargeRemedyNamesReplyOrReceiptNotAdvance(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("bo-r%011d", i)
		writeFile(t, checkout, fmt.Sprintf("from-bo/2026-09-09T12%02dZ-many-%s.md", i, id),
			fmt.Sprintf("From: Bo\nTo: Ada\nDate: Wed Sep  9 12:%02d:00 UTC 2026\nId: %s\nSubject: One of many %d\n\nOpen.\n", i, id, i))
	}
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "many notes")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	r := invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OPEN carrying=62 heard=0 large=true")
	if strings.Contains(r.stdout, "remedy=inbox --advance") {
		t.Fatalf("a large carrying set names '--advance' alone as its remedy, which loops without resolving:\n%s", r.stdout)
	}
	r.mustContain(t, "stdout", "reply or receipt each note").
		mustContain(t, "stdout", "close --before <instant>").
		mustContain(t, "stdout", "explicit bulk cutoff")
}

// close --before receipts every open note dated before the stamp, one receipt note per note,
// and leaves every note at or after it open. A receipt note carries a Re line to the note it
// closes, so the note leaves the open list; a newer note is left alone.
func TestCloseBeforeReceiptsOldNotesOnly(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	// The fixture leaves Ada carrying two notes dated 2026-09-07. One more arrives after the
	// stamp, and must be kept open.
	writeFile(t, checkout, "from-bo/2026-09-09T1000Z-new-333333333333.md",
		"From: Bo\nTo: Ada\nDate: Wed Sep  9 10:00:00 UTC 2026\nId: bo-333333333333\nSubject: New\n\nStill open.\n")
	appendFile(t, checkout, "from-bo/INDEX",
		"bo-333333333333\tfrom-bo/2026-09-09T1000Z-new-333333333333.md\t2026-09-09T10:00:00Z\tAda\t-\n")
	commitAs(t, checkout, "Bo", "a new note")

	// Dry run reports the split and writes nothing.
	invoke(t, "", "close", "--bus", checkout, "--as", "Ada",
		"--before", "2026-09-08T00:00:00Z", "--dry-run").
		mustCode(t, 0).
		mustContain(t, "stdout", "CLOSE OK closed=2 kept=1 commit=-")
	if entries := mdFiles(t, checkout, "from-ada"); len(entries) != 0 {
		t.Fatalf("--dry-run wrote %d receipt notes, want 0", len(entries))
	}

	r := invoke(t, "", "close", "--bus", checkout, "--as", "Ada",
		"--before", "2026-09-08T00:00:00Z",
		"--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "CLOSE OK closed=2 kept=1")
	if !strings.Contains(r.stdout, "commit=") || strings.Contains(r.stdout, "commit=-") {
		t.Fatalf("a writing close printed no commit:\n%s", r.stdout)
	}

	got := ""
	for _, name := range mdFiles(t, checkout, "from-ada") {
		got += read(t, checkout, "from-ada/"+name) + "\n"
	}
	for _, want := range []string{
		"Re: bo-abcdef012345",
		"Re: bo-111111111111",
		"closed: unanswered before 2026-09-08T00:00:00Z",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("receipt notes do not carry %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "bo-333333333333") {
		t.Fatalf("the note after the stamp was receipted:\n%s", got)
	}
	if n := strings.Count(got, "closed: unanswered before 2026-09-08T00:00:00Z"); n < 2 {
		t.Fatalf("one receipt note per old note, want 2 bodies, saw %d:\n%s", n, got)
	}
}

// mdFiles lists the .md files in a lane, which is where the receipt notes close writes.
func mdFiles(t *testing.T, checkout, lane string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(checkout, filepath.FromSlash(lane)))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, e.Name())
		}
	}
	return out
}

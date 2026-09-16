package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE TWO VERBS OF CLASS K (#828), which is "the bus is a mailbox read by minds".
//
// `wake` is the start of a turn: a pull, a pin, ONE note, two counts, and never a listing.
// `receipts` is the other side of the row a receipt now writes: what a note was answered
// with, read without opening a note.
//
// Both are additive. Every verb that was here before prints what it printed before, byte
// for byte; nothing below is reachable without naming one of these two words.

// wakePinMax and wakeSubjectMax are what a wake will print of a pin's first line and of a
// subject. The whole return is meant to fit in one glance and a few hundred bytes -- that
// is the property class K is measured on -- so both are cut with the one-line package's
// mark rather than silently.
const (
	wakePinMax     = 120
	wakeSubjectMax = 100
)

// cmdWake is one line's whole start-of-turn read.
//
// WHAT IT DELIBERATELY WILL NOT DO. It does not list the open notes, at any width, under
// any flag: the failure it closes is a model loading 1,937 of them to work out what it was
// for, and a flag that put the listing back would put the failure back. It does not move
// the cursor, because a wake is a read and a line that has not done the work has not read
// anything. It does not open a second note. The counts are the backlog, and `inbox` is
// still there for a person who wants to see it.
//
// Exit 3 is the QUIET wake: the verb ran, the bus was pulled, and nothing is addressed to
// you. It is not a refusal (2) and not a NO (1); it is the answer "nothing for you", which
// a harness gates on without parsing a line, and it is why a pulse that has nothing to do
// costs its window three lines and no judgment.
func cmdWake(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("wake")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	as := f.fs.String("as", "", "which participant you are (required)")
	pin := f.fs.String("pin", "", "a file whose FIRST line is printed above the note: your standing line for this turn")
	remote := f.fs.String("remote", "", "the git remote to pull from (required)")
	branch := f.fs.String("branch", "", "the branch the bus lives on (required)")
	gitSeconds := f.fs.Int("git-timeout", defaultGitTimeoutSeconds, "how long one git subprocess may take before this run gives up on it")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "as": as, "remote": remote, "branch": branch}) {
		return 2
	}
	if !f.gitTimeoutFlag(*gitSeconds, stderr) {
		return 2
	}
	if !f.gitArgs(*remote, *branch, stderr) {
		return 2
	}
	if err := bus.IsRepoRoot(*busDir); err != nil {
		fmt.Fprintf(stderr, "nova-bus wake: %s\n", oneline.Err(err))
		return 2
	}
	// The pin is read BEFORE the pull, so a pin that has moved is a refusal about the pin
	// rather than a wake that has already changed the checkout and then says so.
	pinLine := ""
	if strings.TrimSpace(*pin) != "" {
		line, err := firstLine(*pin)
		if err != nil {
			fmt.Fprintf(stderr, "nova-bus wake: --pin %s: %s\n", oneline.Field(*pin), oneline.Err(err))
			return 2
		}
		pinLine = line
	}
	release, code := lockCheckout("WAKE", *busDir, stderr)
	if release == nil {
		return code
	}
	defer release()
	if _, err := refreshCheckout(*busDir, *remote, *branch); err != nil {
		fmt.Fprintf(stderr, "WAKE REFUSED: %s\n", oneline.Err(err))
		return 1
	}
	t, ok := openBus("wake", *busDir, stderr)
	if !ok {
		return 2
	}
	me, found := t.Config.Lookup(*as)
	if !found {
		fmt.Fprintf(stderr, "nova-bus wake: --as %q names no one on this bus (known: %s)\n", *as, oneline.Escape(strings.Join(t.Config.KnownNames(), "; ")))
		return 2
	}
	w := bus.SelectWake(t, me)
	if pinLine != "" {
		fmt.Fprintf(stdout, "WAKE PIN %s\n", oneline.Escape(oneline.Cap(pinLine, wakePinMax)))
	}
	if w.Note != nil {
		fmt.Fprintf(stdout, "WAKE NOTE id=%s from=%s subject=%s deadline=%s path=%s\n",
			oneline.Field(orPlaceholder(w.Note.Header.ID, "-")),
			oneline.Field(w.From),
			oneline.Quote(oneline.Cap(w.Note.Header.Subject, wakeSubjectMax)),
			oneline.Field(orPlaceholder(w.Deadline, "-")),
			oneline.Field(w.Note.Path))
	}
	fmt.Fprintf(stdout, "WAKE OK open_to=%d open_cc=%d\n", w.OpenTo, w.OpenCc)
	if w.Note == nil {
		return 3
	}
	return 0
}

// firstLine reads a pin's first line with something on it. A pin whose top is blank lines
// or a markdown rule is a pin somebody wrote for a person to read, and the line they meant
// is the first one that says anything.
//
// It reads the whole file rather than streaming it because a pin is a page: the one this
// was built for is 40 lines, and a read that had to be clever about a file that size would
// be cleverness nobody asked for.
func firstLine(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line), nil
		}
	}
	return "", fmt.Errorf("the file has no line with anything on it")
}

// cmdReceipts prints the verdict rows recorded for one note, by any lane.
//
// It runs no git: the rows are files in the checkout, and a reader asking what a note was
// answered with is asking about what has already arrived. A caller that wants the newest
// answer pulls first, which `wake` does for them.
func cmdReceipts(args []string, stdout, stderr io.Writer) int {
	f := newFlags("receipts")
	busDir := f.fs.String("bus", "", "the bus's repository root (required)")
	note := f.fs.String("note", "", "the note to report, by id or by path (required)")
	max := f.fs.Int("max", defaultRowsMax, "how many rows to print before saying how many it did not")
	if !f.parse(args, stderr, map[string]*string{"bus": busDir, "note": note}) {
		return 2
	}
	if !f.atLeastZero("max", *max, stderr) {
		return 2
	}
	t, ok := openBus("receipts", *busDir, stderr)
	if !ok {
		return 2
	}
	// The two names one note answers to, so a row written against the path and a row
	// written against the id are both the same note's rows. A target the bus does not
	// know is matched literally: a note closed and removed still has rows, and a reader
	// asking about it should get them rather than a refusal.
	target := strings.TrimSpace(*note)
	keys := map[string]bool{target: true}
	if n, found := t.Resolve(target); found {
		keys[n.Path] = true
		if n.Header.ID != "" {
			keys[n.Header.ID] = true
			target = n.Header.ID
		} else {
			target = n.Path
		}
	}
	rows, err := bus.ReadVerdictRows(*busDir)
	if err != nil {
		fmt.Fprintf(stderr, "RECEIPTS FAIL %s: %s\n", oneline.Field(bus.RowsDir), oneline.Err(err))
		return 1
	}
	listed, total := 0, 0
	for _, r := range rows {
		if !keys[r.Note] {
			continue
		}
		total++
		if listed >= *max {
			continue
		}
		listed++
		fmt.Fprintf(stdout, "RECEIPTS ROW at=%s as=%s note=%s verdict=%s\n",
			oneline.Field(orPlaceholder(r.Stamp, "-")), oneline.Field(r.As),
			oneline.Field(r.Note), oneline.Field(r.Verdict))
	}
	fmt.Fprintf(stdout, "RECEIPTS OK note=%s rows=%d listed=%d\n", oneline.Field(target), total, listed)
	return 0
}

// defaultRowsMax is how many rows `receipts` prints without being asked to print more. It
// is the open list's own default, for the same reason: a listing whose size is the record's
// size is the cost this tool exists to remove.
const defaultRowsMax = 20

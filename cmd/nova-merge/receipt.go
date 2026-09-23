package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdReceipt reads the gate receipt a pull request's body quotes: the BATCH OK
// line `nova-merge batch` prints, which is the gate's own evidence that the
// merged tree built, vetted, tested and ran the lisp suite on the bench that
// ran it (#2693). A reader on another machine who can name the pull request
// can fetch the receipt with this verb -- the line a caller hands to
// `nova-merge land --receipt` -- without ssh and without the lander's word.
//
// IT DOES NOT WRITE A RECEIPT and it does not push one: the lander prints the
// BATCH OK line into the PR body when they open the pull request, and the
// receipt lives there. The verb is the read side of that contract.
//
// A PR whose body quotes no BATCH OK line is refused with a reason that names
// the absence, not a silent empty answer: a reader who names a PR the gate
// never built for is not handed a green receipt over nothing.
func cmdReceipt(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("receipt")
	repo := f.fs.String("repo", "", "")
	pr := f.fs.Int("pr", 0, "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	if *repo != "" {
		if err := validRepoSlug(*repo); err != nil {
			f.problem(fmt.Sprintf("--repo is <owner>/<name>, got %q: %s", *repo, oneline.Escape(err.Error())))
		}
	}
	if *pr < 1 {
		f.problem(fmt.Sprintf("--pr is the pull request whose body holds the receipt, got %d", *pr))
	}
	if *timeout < 1 || *timeout > maxTimeout {
		f.problem(fmt.Sprintf("--timeout is a number of seconds this tool waits for gh before saying so, from 1 to %d, got %d", maxTimeout, *timeout))
	}
	if !f.done(stderr) {
		return 2
	}
	host := deps.NewHost(*repo, time.Duration(*timeout)*time.Second)
	data, err := host.PR(*pr)
	if err != nil {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: pull request %d could not be read: %s\n",
			*pr, oneline.Escape(oneline.Cap(err.Error(), oneline.TailBytes)))
		return 2
	}
	line := receiptLineInBody(data.Body)
	if line == "" {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: pull request %d's body quotes no BATCH OK line; a receipt is the gate's own green line, not a hand-written summary\n",
			*pr)
		return 1
	}
	// The line is parsed so a caller is handed the SAME receipt `nova-merge land
	// --receipt` would accept: a truncated sha would match another commit and a
	// receipt whose members= is "none" lands the base. The PR body is the place
	// anyone can edit it, and a quoted line that won't parse is a quoted line
	// nobody should trust.
	rec, err := merge.ParseBatchReceipt(line)
	if err != nil {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: pull request %d's body quotes a BATCH OK line this tool cannot read: %s\n",
			*pr, oneline.Escape(err.Error()))
		return 1
	}
	if rec.Head != "" && data.HeadOID != "" && !strings.EqualFold(rec.Head, data.HeadOID) {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: pull request %d's receipt names head=%s but the pull request's head is %s; the receipt is for a tree nobody is landing\n",
			*pr, oneline.Field(rec.Head), oneline.Field(data.HeadOID))
		return 1
	}
	fmt.Fprintf(stdout, "%s\n", oneline.Escape(line))
	return 0
}

// receiptLineInBody is the BATCH OK line a pull request's body quotes, or the
// empty string when it quotes none. The body may carry many lines and the
// receipt may be preceded by prose; the line is the only thing worth printing.
//
// A line that begins with `BATCH OK ` and is followed by `name=<value>` is a
// receipt. Anything else is not -- a BATCH FAIL is not a receipt (it's a red
// gate, see merge.batchOKPrefix), and a hand-typed summary is not a receipt
// (parseBatchReceipt would refuse it on the field shape, but the body may carry
// both and we pick the receipt rather than the summary).
func receiptLineInBody(body string) string {
	for _, line := range strings.Split(body, "\n") {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "BATCH OK ") {
			continue
		}
		rest := strings.TrimPrefix(s, "BATCH OK ")
		if !strings.HasPrefix(rest, "name=") {
			continue
		}
		return s
	}
	return ""
}

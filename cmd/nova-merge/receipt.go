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
// line `nova-merge batch` prints (#2693). It is the READ SIDE ONLY. The body is
// editable by anyone who can edit the pull request, so the line printed here is
// a quote, not evidence bound to the gate run: the verb checks that it parses
// and that it names the pull request's current head, and it says on stderr
// (RECEIPT SOURCE pr-body) that the line was not fetched from a store the gate
// wrote. Retaining the gate's own artifact and fetching it from another machine
// is #3183; until then this verb is a convenience, not a verification.
//
// IT DOES NOT WRITE A RECEIPT and it does not push one.
//
// A PR whose body quotes no BATCH OK line is refused with a reason that names
// the absence, not a silent empty answer. A body that quotes several receipts
// (an earlier batch's line kept for history, say) is answered with the LAST one
// naming the pull request's current head; a body whose receipts all name other
// heads is refused and the refusal names them.
func cmdReceipt(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := newFlags("receipt")
	repo := f.fs.String("repo", "", "")
	pr := f.fs.Int("pr", 0, "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.require("repo", *repo, "the repository whose pull request body quotes the receipt, as <owner>/<name>")
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
	// The answer is the receipt naming the PR's current head. With no head there
	// is nothing to match against, so no receipt can be chosen: refuse rather than
	// hand back whichever line parsed (#2843, fail-open on an empty HeadOID).
	if strings.TrimSpace(data.HeadOID) == "" {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: pull request %d's head sha is missing from the forge's answer; a receipt is chosen by the head it names, so none can be matched\n",
			*pr)
		return 1
	}
	lines := receiptLinesInBody(data.Body)
	if len(lines) == 0 {
		fmt.Fprintf(stderr, "RECEIPT REFUSED: pull request %d's body quotes no BATCH OK line; a receipt is the gate's own green line, not a hand-written summary\n",
			*pr)
		return 1
	}
	// Every quoted line is parsed so a caller is handed the SAME receipt `nova-merge
	// land --receipt` would accept: a truncated sha would match another commit. A
	// line that won't parse is skipped only when another line answers; when none
	// does, the first parse error is the refusal.
	var firstErr error
	var heads []string
	answer := ""
	for _, line := range lines {
		rec, err := merge.ParseBatchReceipt(line)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		heads = append(heads, rec.Head)
		if strings.EqualFold(rec.Head, data.HeadOID) {
			answer = line
		}
	}
	if answer == "" {
		if len(heads) == 0 {
			fmt.Fprintf(stderr, "RECEIPT REFUSED: pull request %d's body quotes a BATCH OK line this tool cannot read: %s\n",
				*pr, oneline.Escape(firstErr.Error()))
			return 1
		}
		fmt.Fprintf(stderr, "RECEIPT REFUSED: pull request %d's receipts name head=%s but the pull request's head is %s; the receipt is for a tree nobody is landing\n",
			*pr, oneline.Field(strings.Join(heads, ",")), oneline.Field(data.HeadOID))
		return 1
	}
	fmt.Fprintf(stderr, "RECEIPT SOURCE pr-body pr=%d: quoted from the pull request body, which anyone who can edit the PR can write; not fetched from a store the gate wrote (#3183)\n", *pr)
	fmt.Fprintf(stdout, "%s\n", oneline.Escape(answer))
	return 0
}

// receiptLinesInBody is every BATCH OK line a pull request's body quotes, in
// order, or none. The body may carry many lines and prose between them. A BATCH
// FAIL is not a receipt (it's a red gate, see merge.batchOKPrefix). Field order
// inside the line is not checked here: merge.ParseBatchReceipt reads the line as
// key=value fields, never by position, and this finder agrees with it.
func receiptLinesInBody(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		s := strings.TrimSpace(line)
		if !strings.HasPrefix(s, "BATCH OK ") {
			continue
		}
		out = append(out, s)
	}
	return out
}

// validRepoSlug is the shape of <owner>/<name>.
func validRepoSlug(s string) error {
	owner, name, ok := strings.Cut(s, "/")
	if !ok || owner == "" || name == "" {
		return fmt.Errorf("a repository is <owner>/<name>")
	}
	if strings.ContainsAny(owner, " /") || strings.ContainsAny(name, " /") {
		return fmt.Errorf("a repository has no spaces and exactly one slash")
	}
	return nil
}

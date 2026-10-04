//go:build functional || slow || perf

// Helpers the functional, slow and perf tiers share. Every test that calls them starts
// git over a real bus checkout, so the unit tier builds none of them; the constraint is
// wider than functional because slow_test.go and timing_test.go call them too.

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// longBusOfNotes hands a test a checkout standing `commits` commits above the fixture's
// HEAD, carrying `notes` notes from Bo to Ada spread evenly over them, and the commit the
// fixture stood at -- which is where a stale cursor is put. It is one git process, for the
// reason fastImport is: a loop of `git commit` over a thousand commits is a thousand
// subprocesses and the package has a minute to run in.
func longBusOfNotes(t *testing.T, commits, notes int) (checkout, base string) {
	t.Helper()
	checkout, _ = busDir(t)
	base = strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))
	// The INDEX is REPLACED rather than appended to, because fast-import writes a whole
	// blob: the fixture's own two lines are carried into it or the notes they name leave the
	// catalogue. It is written once, in the last commit, because it is not a note and no
	// reader's listing depends on which commit carried it.
	index := read(t, checkout, "from-bo/INDEX")
	per := notes / commits
	var b strings.Builder
	prev := base
	for i := range commits {
		msg := fmt.Sprintf("bo: batch %d", i)
		fmt.Fprintf(&b, "commit refs/heads/main\n")
		fmt.Fprintf(&b, "mark :%d\n", i+1)
		fmt.Fprintf(&b, "author Bo <bo@example.com> %d +0000\n", 1757376000+i)
		fmt.Fprintf(&b, "committer Bo <bo@example.com> %d +0000\n", 1757376000+i)
		fmt.Fprintf(&b, "data %d\n%s\n", len(msg), msg)
		fmt.Fprintf(&b, "from %s\n", prev)
		for j := range per {
			n := i*per + j
			id := fmt.Sprintf("bo-%012x", n+0x200000)
			day := n%28 + 1
			path := fmt.Sprintf("from-bo/2026-08-%02dT%02d%02dZ-walk-%s.md", day, n/60%24, n%60, id[len(id)-12:])
			note := fmt.Sprintf(
				"From: Bo\nTo: Ada\nDate: Sat Aug %2d 00:00:00 UTC 2026\nId: %s\nSubject: walk %d\n\nA note in the history.\n",
				day, id, n)
			fmt.Fprintf(&b, "M 100644 inline %s\ndata %d\n%s", path, len(note), note)
			index += fmt.Sprintf("%s\t%s\t2026-08-%02dT00:00:00Z\tAda\t-\n", id, path, day)
		}
		if i == commits-1 {
			fmt.Fprintf(&b, "M 100644 inline from-bo/INDEX\ndata %d\n%s", len(index), index)
		}
		b.WriteString("\n")
		prev = fmt.Sprintf(":%d", i+1)
	}
	b.WriteString("done\n")
	cmd := exec.Command("git", "-C", checkout, "fast-import", "--quiet")
	cmd.Stdin = strings.NewReader(b.String())
	{
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git fast-import %d commits carrying %d notes: %v\n%s", commits, notes, err, out)
	}
	// fast-import moved the branch under the working tree; this is what puts the notes in
	// it, and it leaves the checkout clean, which inbox --advance requires.
	gitIn(t, checkout, "reset", "--hard", "-q", "refs/heads/main")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	return checkout, base
}

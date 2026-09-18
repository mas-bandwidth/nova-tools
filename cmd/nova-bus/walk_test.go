package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// TestInboxSinceWalkReportsProgressAndHonoursMaxCommits is card 9376.
//
// Glenn's hard rule: a program that takes longer than 0.1 s says what it is doing, on
// stderr. The failure this test is written against ran four minutes with no output on a
// bus whose cursor was 285 commits stale, then printed nothing new. So the since-walk
// says where it is while it runs -- `INBOX WALK commits=<n>/<total> notes=<n> elapsed=<s>`
// -- and a stale cursor is bounded by --max-commits (default 500), with one line naming
// the way out when the bound is hit and exit 0.
func TestInboxSinceWalkReportsProgressAndHonoursMaxCommits(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout := longBus(t, 1000)

	// A walk the bound allows finishes and reports progress at its end. The line is the
	// shape the rule asks for: commits walked over the total, notes parsed, elapsed.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--max-commits", "2000").
		mustCode(t, 0).
		mustContain(t, "stderr", "INBOX WALK commits=1000/1000 notes=0 elapsed=")

	// The default bound stops the same walk at 500 commits: no listing, one remedy, exit 0.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stderr", `INBOX WALK bounded commits=500 remedy="raise --max-commits or close --before <instant>"`)

	// A tighter bound stops at the number the caller gave, with the same remedy.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--max-commits", "100").
		mustCode(t, 0).
		mustContain(t, "stderr", `INBOX WALK bounded commits=100 remedy="raise --max-commits or close --before <instant>"`)
}

// longBus hands a test a checkout whose cursor stands `commits` empty commits behind
// HEAD. The fixture's HEAD is the stale cursor's commit and the commits above it carry
// no changes, so the notes and the working tree the fixture built are untouched.
func longBus(t *testing.T, commits int) string {
	t.Helper()
	checkout, _ := busDir(t)
	base := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))
	fastImport(t, checkout, base, commits)
	writeFile(t, checkout, "from-ada/CURSOR", base+" 2026-09-09T12:00:00Z open=0\n")
	return checkout
}

// fastImport writes n empty commits onto refs/heads/main with one git process. A loop of
// `git commit` over a thousand commits is a thousand subprocesses; fast-import is one, and
// the fixture stays hermetic and inside the package's time budget.
func fastImport(t *testing.T, dir, from string, n int) {
	t.Helper()
	var b strings.Builder
	prev := from
	for i := 0; i < n; i++ {
		msg := fmt.Sprintf("empty commit %d", i)
		fmt.Fprintf(&b, "commit refs/heads/main\n")
		fmt.Fprintf(&b, "mark :%d\n", i+1)
		fmt.Fprintf(&b, "author Bus <bus@example.com> %d +0000\n", 1700000000+i)
		fmt.Fprintf(&b, "committer Bus <bus@example.com> %d +0000\n", 1700000000+i)
		fmt.Fprintf(&b, "data %d\n%s\n", len(msg), msg)
		fmt.Fprintf(&b, "from %s\n", prev)
		b.WriteString("\n")
		prev = fmt.Sprintf(":%d", i+1)
	}
	b.WriteString("done\n")
	cmd := exec.Command("git", "-C", dir, "fast-import", "--quiet")
	cmd.Stdin = strings.NewReader(b.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import %d commits: %v\n%s", n, err, out)
	}
}

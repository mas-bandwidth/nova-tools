//go:build functional || slow || perf

// Helpers the functional, slow and perf tiers share. Every test that calls them starts
// git over a real bus checkout, so the unit tier builds none of them; the constraint is
// wider than functional because slow_test.go and timing_test.go call them too.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// advance is the flags that move a reader's cursor, since every test below does it.
//
// It carries --carry-history, because the fixture bus is dated two days before the fixed
// clock and a FIRST advance over notes older than today is refused unless the reader says
// what to do with them (see TestAFirstAdvanceOverOldNotesIsRefused). These tests mean the
// answer "carry them": they are about the cursor and the open list, and every count they
// assert is a count of notes carried. The flag is dropped when the caller draws a
// switch-day line of its own, which is the other answer and cannot be given with this one.
func advance(checkout, who string, extra ...string) []string {
	args := append([]string{
		"inbox", "--bus", checkout, "--as", who, "--receipt-max-words", "40",
		"--advance", "--remote", "origin", "--branch", "main", "--attempts", "3",
	}, extra...)
	for _, a := range extra {
		if a == "--legacy-before" || a == "--legacy-now" || a == "--carry-history" || strings.HasPrefix(a, "--legacy-before=") {
			return args
		}
	}
	return append(args, "--carry-history")
}

func read(t *testing.T, checkout, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(path)))
	require.NoErrorf(t, err, "reading %s: %v", path, err)
	return string(raw)
}

// bulkHistory puts `history` notes on the bus in ONE git process, of which the first
// `carried` are addressed to Ada and the rest are Bo's own business.
//
// It used to be a loop of `history` os.WriteFile calls followed by commitAs, which is
// `git add -A` over ten thousand new paths, a commit over them and a push. Measured on an
// idle Studio 2026-09-18: 0.9 s of file writes and 5.7 s of git, which is most of this
// test -- and windows-latest, where a file operation is expensive and the merge group's
// hosted leg runs, timed the shard out at 100 s with this test still running, twice.
//
// fast-import builds the same commit from a stream: one process, no working-tree scan, no
// index full of ten thousand untracked paths to hash, and the tree written once. The
// working tree is then materialized by a single `git reset --hard`, which is git writing
// the files instead of Go writing them one at a time. The BUS IS THE SAME: the same ten
// thousand notes, the same INDEX lines, the same one commit on main, pushed the same way.
// Nothing this test asserts is a fact about how the fixture was built.
func bulkHistory(t *testing.T, checkout string, history, carried int) {
	t.Helper()
	head := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))

	// The INDEX is REPLACED rather than appended to, because fast-import writes a whole
	// blob: the fixture's own two lines have to be carried into it or the notes they name
	// leave the index and the bus stops agreeing with itself.
	index := read(t, checkout, "from-bo/INDEX")

	var b strings.Builder
	const msg = "ten thousand notes"
	fmt.Fprintf(&b, "commit refs/heads/main\n")
	fmt.Fprintf(&b, "author Bo <bo@example.com> 1757376000 +0000\n")
	fmt.Fprintf(&b, "committer Bo <bo@example.com> 1757376000 +0000\n")
	fmt.Fprintf(&b, "data %d\n%s\n", len(msg), msg)
	fmt.Fprintf(&b, "from %s\n", head)
	for i := range history {
		id := fmt.Sprintf("bo-%012x", i+0x100000)
		path := fmt.Sprintf("from-bo/2026-08-%02dT%02d%02dZ-bulk-%s.md", i%28+1, i/60%24, i%60, id[len(id)-12:])
		to := "Bo"
		if i < carried {
			to = "Ada"
		}
		note := fmt.Sprintf(
			"From: Bo\nTo: %s\nDate: Sat Aug %2d 00:00:00 UTC 2026\nId: %s\nSubject: bulk %d\n\nA note in the history.\n",
			to, i%28+1, id, i)
		fmt.Fprintf(&b, "M 100644 inline %s\ndata %d\n%s", path, len(note), note)
		index += fmt.Sprintf("%s\t%s\t2026-08-%02dT00:00:00Z\t%s\t-\n", id, path, i%28+1, to)
	}
	fmt.Fprintf(&b, "M 100644 inline from-bo/INDEX\ndata %d\n%s", len(index), index)
	b.WriteString("\ndone\n")

	cmd := exec.Command("git", "-C", checkout, "fast-import", "--quiet")
	cmd.Stdin = strings.NewReader(b.String())
	{
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git fast-import %d notes: %v\n%s", history, err, out)
	}
	// fast-import moved the branch under the working tree; this is what puts the notes in
	// it. --hard against the branch it just wrote, so the tree, the index and HEAD agree
	// and the checkout is clean -- inbox --advance refuses a dirty one.
	gitIn(t, checkout, "reset", "--hard", "-q", "refs/heads/main")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
}

// commitAs commits everything in the checkout under a roster name's identity and pushes it,
// which is what a note arriving on the bus looks like from a test's side.
func commitAs(t *testing.T, checkout, who, message string) {
	t.Helper()
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name="+who, "-c", "user.email="+strings.ToLower(who)+"@example.com",
		"commit", "-q", "-m", message)
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
}

// field pulls one key=value out of an event line.
func field(t *testing.T, out, key string) string {
	t.Helper()
	i := strings.Index(out, key)
	require.Falsef(t, i < 0, "no %s in %q", key, out)
	rest := out[i+len(key):]
	if j := strings.IndexAny(rest, " \n"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func appendFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	require.NoError(t, err)
	defer f.Close()
	{
		_, err := f.WriteString(content)
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())
}

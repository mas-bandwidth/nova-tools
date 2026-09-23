// nova-tools #2021: cut --from <table> as a first-class verb for the three card families
// (fix per triaged issue, read per unreviewed PR, guard per dev commit). The fleet's true
// limit is the size of the queue's pending directory; what is missing is a generator that
// turns a spec's machine-readable work table into cards without a manager in the loop. This
// test pins the smallest version: --kind gates the layout, --from reads the rows, already
// -carded rows are skipped, and the verdict sits on stdout as one line per row plus one
// summary line.
//
// The test is hermetic: a fixture gh and git (cmd/nova-pulse/fake_test.go), a TempDir for
// state, no network, no model call (#1142, class on real network hosts).
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runCutKind drives `cut --kind` through the verb's own run(), so flag parsing, the dedupe
// loop and the dispatch to pulse.CutKind are under test from the same place every run.
func runCutKind(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"cut"}, args...), &out, &errb, time.Now().UTC())
	return code, out.String(), errb.String()
}

// tableFiles writes a kind-specific table for `cut --kind <kind> --from <file>`. The shape
// is the kind's own: fix rows are repo/issue/title, read rows repo/pr/head/title, guard
// rows repo/head. The first column is what the verb reads when no --kind is given.
func tableFiles(t *testing.T, dir string) (fixPath, readPath, guardPath string) {
	t.Helper()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	fix := "mas-bandwidth/nova-tools\t42\tsweep the ones the manager missed\n" +
		"mas-bandwidth/nova-tools\t43\tthe fix-card lane carries its own red\n"
	read := "mas-bandwidth/nova-tools\t812\tabc123def456\treap kills the orphans\n"
	guard := "mas-bandwidth/nova-tools\tddce356eabcd\n"
	return write("from-fix.tsv", fix), write("from-read.tsv", read), write("from-guard.tsv", guard)
}

// mkLaneDirs lays down the four queue subdirs cut/lanes expects, after a run (-pedding,
// /launched, /done, /failed). The harness's own dir is the queue, so a test can read the
// cards back and see the dedupe.
func mkLaneDirs(t *testing.T, queue string) {
	t.Helper()
	for _, sub := range []string{"pending", "launched", "done", "failed"} {
		if err := os.MkdirAll(filepath.Join(queue, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestIssue2021FromCutsOneCardPerRow pins the small end: a single fix family row in
// --from produces one card in --out, the kind's line 1 carries the issue number, and the
// summary prints CUT FROM kind=fix rows=1 cut=1 skipped=0.
func TestIssue2021FromCutsOneCardPerRow(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "")
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	mkLaneDirs(t, queue)
	out := filepath.Join(queue, "pending")

	fixPath, _, _ := tableFiles(t, dir)

	code, stdout, stderr := runCutKind(t, "--kind", "fix", "--from", fixPath,
		"--out", out, "--queue", queue, "--repo", dir)
	if code != 0 {
		t.Fatalf("--from fix: exit = %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "CUT FROM kind=fix rows=2 cut=2 skipped=0") {
		t.Fatalf("--from fix: stdout=%q, want CUT FROM kind=fix rows=2 cut=2 skipped=0", stdout)
	}
	names, err := filepath.Glob(filepath.Join(out, "card-*.md"))
	if err != nil || len(names) != 2 {
		t.Fatalf("--from fix: cards = %v, want exactly two (stderr=%q)", names, stderr)
	}
	var saw42, saw43 bool
	for _, p := range names {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		first := strings.SplitN(string(raw), "\n", 2)[0]
		switch {
		case strings.Contains(first, "#42 "):
			saw42 = true
		case strings.Contains(first, "#43 "):
			saw43 = true
		}
	}
	if !saw42 || !saw43 {
		t.Fatalf("--from fix: line 1 did not name issue 42 and 43; saw42=%v saw43=%v", saw42, saw43)
	}
}

// TestIssue2021FromSkipsAlreadyCarded pins the dedupe end: a row whose marker is in any of
// {pending, launched, done, failed} is skipped, the verdict is on stdout, no card is
// written. The marker the test plants is the format pulse.CutKind's own fix card carries
// (`<repo-short> #<issue> `, the same pattern internal/pulse/wire.go uses), so a real
// card placed under done/ is enough to read the dedupe without enumerating every kind
// shape here.
func TestIssue2021FromSkipsAlreadyCarded(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "")
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	mkLaneDirs(t, queue)
	out := filepath.Join(queue, "pending")

	// Plant a card already cut for issue 42, then run --from with two rows: issue 42 (the
	// pre-existing one, must skip), issue 43 (new, must cut).
	prior := filepath.Join(queue, "done", "card-1.md")
	if err := os.WriteFile(prior,
		[]byte("RESULT: CARD-1 sha=000000000001 nova-tools #42 fixed with its red test first: prior attempt\nSTEP 1. pwd\n"),
		0o644); err != nil {
		t.Fatal(err)
	}

	fixPath, _, _ := tableFiles(t, dir)

	code, stdout, stderr := runCutKind(t, "--kind", "fix", "--from", fixPath,
		"--out", out, "--queue", queue, "--repo", dir)
	// The convention is exit 0 on a clean cut and exit 1 when at least one row was
	// skipped because the queue already had its card (validated.go does the same for
	// header/separator rows; here, for already-carded rows). 2 is a refusal only.
	if code != 1 {
		t.Fatalf("--from fix with one pre-carded row: exit = %d, want 1 (skipped); stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "CUT FROM kind=fix rows=2 cut=1 skipped=1") {
		t.Fatalf("--from fix: stdout=%q, want CUT FROM kind=fix rows=2 cut=1 skipped=1 (stderr=%q)", stdout, stderr)
	}
	names, _ := filepath.Glob(filepath.Join(out, "card-*.md"))
	if len(names) != 1 {
		t.Fatalf("--from fix: cards = %v, want exactly one (issue 43 cut, issue 42 skipped)", names)
	}
	raw, err := os.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "#43 ") || strings.Contains(string(raw), "#42 ") {
		t.Fatalf("--from fix: the cut card carried the wrong issue:\n%s", raw)
	}
}

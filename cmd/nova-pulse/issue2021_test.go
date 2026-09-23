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

// mkLaneDirs lays down the queue's pending/launched/done/failed, so a test can plant a
// card and read the dedupe back.
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

// TestIssue2021FromSkipsRowsAlreadyOnOrigin pins the other half of #2021's dedupe (the
// hold on PR #2873, cut_from.go isCarded read queue card files only): with --dir <clone>,
// a fix row whose issue already has a branch on the clone's origin -- the open PR the
// hand-cut wave re-cut -- is skipped, reported apart from the rows already carded, and
// the origin is asked once per run with `git ls-remote --heads origin`, the same question
// internal/pulse/validated.go's check=branch asks. A branch for issue 430 does not stand
// for issue 43: the number is matched on a word boundary.
func TestIssue2021FromSkipsRowsAlreadyOnOrigin(t *testing.T) {
	specs := fakePATH(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "git.log")
	gitAnswers(t, specs, log, fakeRule{Arg: 3, Equals: "ls-remote",
		Stdout: "aaaa000000000000000000000000000000000000\trefs/heads/rowan/fix3-nova-tools-42\n" +
			"bbbb000000000000000000000000000000000000\trefs/heads/rowan/fix3-nova-tools-430\n" +
			"cccc000000000000000000000000000000000000\trefs/heads/dev\n"})
	queue := filepath.Join(dir, "queue")
	mkLaneDirs(t, queue)
	out := filepath.Join(queue, "pending")
	if err := os.WriteFile(filepath.Join(queue, "done", "card-1.md"),
		[]byte("RESULT: CARD-1 sha=000000000001 nova-tools #43 fixed with its red test first: prior attempt\nSTEP 1. pwd\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	table := filepath.Join(dir, "fix.tsv")
	if err := os.WriteFile(table, []byte(
		"mas-bandwidth/nova-tools\t42\talready a branch on origin\n"+
			"mas-bandwidth/nova-tools\t43\talready carded in done\n"+
			"mas-bandwidth/nova-tools\t44\tnew work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCutKind(t, "--kind", "fix", "--from", table,
		"--out", out, "--queue", queue, "--repo", "mas-bandwidth/nova-tools", "--dir", dir)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (rows skipped); stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{
		"CUT FROM SKIPPED kind=fix why=origin issue=42 branch=rowan/fix3-nova-tools-42\n",
		"CUT FROM SKIPPED kind=fix why=carded issue=43 mark=nova-tools\\x20#43\\x20\n",
		"CUT FROM kind=fix rows=3 cut=1 skipped=2 carded=1 origin=1\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout=%q, want the line %q (stderr=%q)", stdout, want, stderr)
		}
	}
	names, _ := filepath.Glob(filepath.Join(out, "card-*.md"))
	if len(names) != 1 {
		t.Fatalf("cards = %v, want exactly one (issue 44)", names)
	}
	raw, err := os.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	if first := strings.SplitN(string(raw), "\n", 2)[0]; !strings.Contains(first, "#44 ") {
		t.Fatalf("the cut card is not issue 44: %q", first)
	}
	calls, _ := os.ReadFile(log)
	if n := strings.Count(string(calls), "ls-remote"); n != 1 {
		t.Fatalf("git ls-remote ran %d times, want once per run:\n%s", n, calls)
	}
}

// TestIssue2021FromRefusesWhenOriginCannotBeRead pins the refusal: a --dir whose origin
// does not answer is CUT FROM REFUSED before any card is written, never a silent
// carded-only dedupe that re-cuts every open PR.
func TestIssue2021FromRefusesWhenOriginCannotBeRead(t *testing.T) {
	specs := fakePATH(t)
	gitAnswers(t, specs, "", fakeRule{Arg: 3, Equals: "ls-remote", Stderr: "fatal: 'origin' does not appear to be a git repository", Exit: 128})
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	mkLaneDirs(t, queue)
	out := filepath.Join(queue, "pending")
	fixPath, _, _ := tableFiles(t, dir)

	code, stdout, stderr := runCutKind(t, "--kind", "fix", "--from", fixPath,
		"--out", out, "--queue", queue, "--repo", "mas-bandwidth/nova-tools", "--dir", dir)
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "CUT FROM REFUSED: --dir "+dir+": git ls-remote --heads origin") {
		t.Fatalf("stderr=%q, want the ls-remote refusal naming --dir", stderr)
	}
	if names, _ := filepath.Glob(filepath.Join(out, "card-*.md")); len(names) != 0 {
		t.Fatalf("cards written despite the refusal: %v", names)
	}
}

package ci

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sleeps_class_test.go is the falling ratchet on the SLEEPS skips (nova-tools
// #4312, after #4221).
//
// On 2026-09-25 the per-commit run was over its two minutes, and 94 tests that
// waited on the wall clock were skipped in one change (#4221), each with the
// same first line -- `t.Skip("SLEEPS: needs a mocked clock or a functional
// test (nova-tools #4221)")` -- so the debt is countable. Glenn's rule is that
// a unit test never waits on the wall clock: it injects the clock and asserts
// the transition, or it becomes a functional test. So the count of SLEEPS
// skips only falls: testdata/sleeps_count.txt carries the number and the date
// it was measured, this test counts the `t.Skip("SLEEPS:` calls under cmd/ and
// internal/, and a count above the number is red. A count below it is red too,
// with the line to write, because a ratchet that is not tightened when the
// debt falls is a ratchet the next skip fits under.
//
// The reader walks the parsed tree, not the text: a call whose method is Skip
// or Skipf and whose first argument is a string literal starting with SLEEPS:
// is one skip; a comment, a doc line or a string in some other call is not.
const sleepsCountPath = "testdata/sleeps_count.txt"

// sleepsMarker is the first bytes of every SLEEPS skip's reason.
const sleepsMarker = "SLEEPS:"

func TestSleepsSkipsOnlyFall(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	count := 0
	for _, f := range tree.GoFilesUnder(true, "cmd", "internal") {
		if f.HasDirNamed("testdata") {
			continue
		}
		if f.ParseErr != nil {
			t.Fatal(f.ParseErr)
		}
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if isSleepsSkip(n) {
				count++
			}
			return true
		})
	}
	ceiling, date := readSleepsCount(t)
	today := time.Now().Format("2006-01-02")
	switch {
	case count > ceiling:
		t.Errorf("%d SLEEPS skips under cmd/ and internal/, over the %d measured %s in %s; a test that waits on the wall clock is not skipped, it gets an injected clock and asserts the transition, or it becomes a functional test (#4221). The count only falls",
			count, ceiling, date, sleepsCountPath)
	case count < ceiling:
		t.Errorf("%d SLEEPS skips under cmd/ and internal/, under the %d measured %s in %s; tighten the ratchet in the same change: write `%d %s` as the one number line of %s",
			count, ceiling, date, sleepsCountPath, count, today, sleepsCountPath)
	default:
		t.Logf("%d SLEEPS skips, at the ratchet measured %s", count, date)
	}
}

// isSleepsSkip reports whether n is `<x>.Skip("SLEEPS: ...")` or the Skipf form.
func isSleepsSkip(n ast.Node) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Skip" && sel.Sel.Name != "Skipf") {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	text, err := strconv.Unquote(lit.Value)
	return err == nil && strings.HasPrefix(text, sleepsMarker)
}

// readSleepsCount reads the one `<count> <YYYY-MM-DD>` line of the ratchet
// file; comment lines start with #.
func readSleepsCount(t *testing.T) (int, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(sleepsCountPath))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("%s carries %d non-comment lines, want one: `<count> <YYYY-MM-DD>`", sleepsCountPath, len(lines))
	}
	fields := strings.Fields(lines[0])
	if len(fields) != 2 {
		t.Fatalf("%s: %q is not `<count> <YYYY-MM-DD>`", sleepsCountPath, lines[0])
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil || n < 0 {
		t.Fatalf("%s: %q is not a count", sleepsCountPath, fields[0])
	}
	if _, err := time.Parse("2006-01-02", fields[1]); err != nil {
		t.Fatalf("%s: %q is not a YYYY-MM-DD date", sleepsCountPath, fields[1])
	}
	return n, fields[1]
}

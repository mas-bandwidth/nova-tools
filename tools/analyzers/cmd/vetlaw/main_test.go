package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func parse(t *testing.T, name, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return fset, f
}

// The fixture trips all three laws (the positive control).
func TestFixtureTripsEveryLaw(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../../fixtures/fixtures.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]string{
		"zero-byte":    checkZeroByte(fset, f),
		"help-refused": checkHelpRefused(fset, f),
		"argv-text":    checkArgvText(fset, f),
	} {
		if len(got) != 1 {
			t.Errorf("%s: want 1 diagnostic on the fixture, got %d: %q", name, len(got), got)
		}
	}
}

// Help in one branch and REFUSED in another is the normal shape of a verb
// (the #2724 HOLD 3 false positives); both switch and if forms stay clean.
const verbShape = `package p
import ("fmt"; "os")
func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "X REFUSED: no verb; run: x help")
		return 2
	}
	if args[0] == "--help" {
		fmt.Print("usage")
		return 0
	}
	switch args[0] {
	case "help", "-h":
		fmt.Print("usage")
		return 0
	default:
		fmt.Fprintln(os.Stderr, "X REFUSED: unknown verb")
		return 2
	}
}
`

func TestHelpAndRefusedInSeparateBranchesIsClean(t *testing.T) {
	fset, f := parse(t, "verb.go", verbShape)
	if got := checkHelpRefused(fset, f); len(got) != 0 {
		t.Fatalf("want no diagnostic, got %q", got)
	}
}

const helpRefusedCase = `package p
import ("fmt"; "os")
func run(args []string) int {
	switch args[0] {
	case "help":
		fmt.Fprintln(os.Stderr, "X REFUSED: no help")
		return 2
	}
	return 0
}
`

func TestHelpCaseAnsweringRefusedIsFlagged(t *testing.T) {
	fset, f := parse(t, "verb.go", helpRefusedCase)
	got := checkHelpRefused(fset, f)
	if len(got) != 1 || !strings.Contains(got[0], "verb.go:6:") {
		t.Fatalf("want one diagnostic at verb.go:6, got %q", got)
	}
}

func TestTestFilesAreNeverFlagged(t *testing.T) {
	fset, f := parse(t, "verb_test.go", helpRefusedCase)
	if got := checkHelpRefused(fset, f); len(got) != 0 {
		t.Fatalf("want no diagnostic in a _test.go file, got %q", got)
	}
}

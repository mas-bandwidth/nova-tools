package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
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

// refused runs vet and requires the #2724 HOLD 6 shape: exit 2, nothing on
// stdout, exactly one stderr line carrying every want fragment.
func refused(t *testing.T, args []string, want ...string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := vet(args, &out, &errb); code != 2 {
		t.Fatalf("vet(%q) = %d, want 2; stderr %q", args, code, errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	lines := strings.Split(strings.TrimRight(errb.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stderr has %d lines, want 1: %q", len(lines), errb.String())
	}
	for _, w := range want {
		if !strings.Contains(lines[0], w) {
			t.Errorf("stderr %q does not name %q", lines[0], w)
		}
	}
}

func TestConfigAbsentRefuses(t *testing.T) {
	refused(t, nil, "vet config", "absent")
	missing := filepath.Join(t.TempDir(), "no-such.cfg")
	refused(t, []string{missing}, missing, "unreadable")
}

func TestConfigUnreadableRefuses(t *testing.T) {
	dir := t.TempDir() // a directory: ReadFile fails for every user, root included
	refused(t, []string{dir}, dir, "unreadable")
}

func TestConfigInvalidJSONRefuses(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "vet.cfg")
	if err := os.WriteFile(cfg, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	refused(t, []string{cfg}, cfg, "invalid JSON")
}

func TestGoFileParseErrorRefuses(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(bad, []byte("package p\nfunc {"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "vet.cfg")
	if err := os.WriteFile(cfg, []byte(`{"GoFiles":[`+strconv.Quote(bad)+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	refused(t, []string{cfg}, bad, "does not parse")
}

func TestVetxOutputUnwritableRefuses(t *testing.T) {
	dir := t.TempDir()
	vetx := filepath.Join(dir, "missing-dir", "vet.out")
	cfg := filepath.Join(dir, "vet.cfg")
	if err := os.WriteFile(cfg, []byte(`{"VetxOnly":true,"VetxOutput":`+strconv.Quote(vetx)+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	refused(t, []string{cfg}, vetx, "unwritable")
}

// The good path still answers 0 with a clean file and 1 on the fixture.
func TestValidConfigRuns(t *testing.T) {
	dir := t.TempDir()
	clean := filepath.Join(dir, "clean.go")
	if err := os.WriteFile(clean, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		file string
		code int
	}{{clean, 0}, {"../../fixtures/fixtures.go", 1}} {
		cfg := filepath.Join(dir, "vet.cfg")
		if err := os.WriteFile(cfg, []byte(`{"GoFiles":["`+c.file+`"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if got := vet([]string{cfg}, &out, &errb); got != c.code {
			t.Errorf("%s: vet = %d, want %d; stderr %q", c.file, got, c.code, errb.String())
		}
	}
}

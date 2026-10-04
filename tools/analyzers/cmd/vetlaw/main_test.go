package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// vetlaw is the vettool's entry point in process.
var vetlaw = testkit.Main(func(args []string, _ io.Reader, stdout, stderr io.Writer) int { return vet(args, stdout, stderr) })

func parse(t *testing.T, name, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	require.NoError(t, err)
	return fset, f
}

// The fixture trips all three laws (the positive control).
func TestFixtureTripsEveryLaw(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../../fixtures/fixtures.go", nil, 0)
	require.NoError(t, err)
	for name, got := range map[string][]string{
		"zero-byte":    checkZeroByte(fset, f),
		"help-refused": checkHelpRefused(fset, f),
		"argv-text":    checkArgvText(fset, f),
	} {
		assert.Len(t, got, 1, "%s: want 1 diagnostic on the fixture", name)
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
	t.Parallel()
	fset, f := parse(t, "verb.go", verbShape)
	require.Empty(t, checkHelpRefused(fset, f))
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
	t.Parallel()
	fset, f := parse(t, "verb.go", helpRefusedCase)
	got := checkHelpRefused(fset, f)
	require.Len(t, got, 1, "want one diagnostic at verb.go:6")
	require.Contains(t, got[0], "verb.go:6:")
}

func TestTestFilesAreNeverFlagged(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, "verb_test.go", helpRefusedCase)
	require.Empty(t, checkHelpRefused(fset, f), "a _test.go file is never flagged")
}

// refused runs vet and requires the #2724 HOLD 6 shape: exit 2, nothing on
// stdout, exactly one stderr line carrying every want fragment.
func refused(t *testing.T, args []string, want ...string) {
	t.Helper()
	r := vetlaw.Do(t, args...).Exit(2)
	assert.Empty(t, r.Stdout, r)
	require.Len(t, strings.Split(strings.TrimRight(r.Stderr, "\n"), "\n"), 1, "want one stderr line: %s", r)
	r.Err(want...)
}

func TestConfigAbsentRefuses(t *testing.T) {
	t.Parallel()
	refused(t, nil, "vet config", "absent")
	missing := filepath.Join(t.TempDir(), "no-such.cfg")
	refused(t, []string{missing}, missing, "unreadable")
}

func TestConfigUnreadableRefuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir() // a directory: ReadFile fails for every user, root included
	refused(t, []string{dir}, dir, "unreadable")
}

func TestConfigInvalidJSONRefuses(t *testing.T) {
	t.Parallel()
	cfg := filepath.Join(t.TempDir(), "vet.cfg")
	testkit.WriteFile(t, cfg, "{not json")
	refused(t, []string{cfg}, cfg, "invalid JSON")
}

func TestGoFileParseErrorRefuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.go")
	testkit.Tree(t, dir, map[string]string{"bad.go": "package p\nfunc {", "vet.cfg": `{"GoFiles":[` + strconv.Quote(bad) + `]}`})
	refused(t, []string{filepath.Join(dir, "vet.cfg")}, bad, "does not parse")
}

func TestVetxOutputUnwritableRefuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	vetx := filepath.Join(dir, "missing-dir", "vet.out")
	cfg := filepath.Join(dir, "vet.cfg")
	testkit.WriteFile(t, cfg, `{"VetxOnly":true,"VetxOutput":`+strconv.Quote(vetx)+`}`)
	refused(t, []string{cfg}, vetx, "unwritable")
}

// The good path still answers 0 with a clean file and 1 on the fixture.
func TestValidConfigRuns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	clean := filepath.Join(dir, "clean.go")
	testkit.WriteFile(t, clean, "package p\n")
	for _, c := range []struct {
		file string
		code int
	}{{clean, 0}, {"../../fixtures/fixtures.go", 1}} {
		cfg := filepath.Join(dir, "vet.cfg")
		testkit.WriteFile(t, cfg, `{"GoFiles":["`+c.file+`"]}`)
		r := vetlaw.Do(t, cfg)
		assert.Equal(t, c.code, r.Code, "%s: %s", c.file, r)
	}
}

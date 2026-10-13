package secretcheck_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/secretcheck"
)

// leaky is a driven function that carries its string input into its error.
func leaky(_ context.Context, dsn string) error { return fmt.Errorf("open %s: refused", dsn) }

// clean refuses with a fixed sentence and never the input.
func clean(_ context.Context, _ string) error { return errors.New("refused: bad dsn") }

// TestWindowsSkipThePublicPrefix: a window is every 8-byte substring that
// starts after the public prefix; a text shorter than the window has none.
func TestWindowsSkipThePublicPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, s, public string
		want            []string
	}{
		{"plain", "abcdefghij", "", []string{"abcdefgh", "bcdefghi", "cdefghij"}},
		{"exactly one window", "abcdefgh", "", []string{"abcdefgh"}},
		{"too short", "abcdefg", "", nil},
		{"prefix skipped", "pre-abcdefghi", "pre-", []string{"abcdefgh", "bcdefghi"}},
		{"prefix longer than the text", "abc", "pre-", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, secretcheck.Windows(tc.s, tc.public))
		})
	}
}

// TestLeaksNamesEachTextOnce: a text holding any window is named once with
// the first window found, sorted by where; a text holding only the public
// prefix is not a leak.
func TestLeaksNamesEachTextOnce(t *testing.T) {
	t.Parallel()

	secret, public := "tok_SECRETVALUE99", "tok_"
	texts := map[string]string{
		"log":    "wrote SECRETVALUE99 twice: SECRETVALUE99",
		"error":  "open tok_SECRETVA",
		"result": "refused",
		"panic":  "tok_",
	}
	got := secretcheck.Leaks(secret, public, texts)
	assert.Equal(t, []string{`error holds "SECRETVA"`, `log holds "SECRETVA"`}, got)
	assert.True(t, sort.StringsAreSorted(got))
	assert.Empty(t, secretcheck.Leaks(secret, public, map[string]string{"error": "nothing"}))
}

// TestShapesAreDistinctAndWindowed: every shape's secret sits in its input,
// leaves at least one window after its public prefix, and no shape's window
// appears in another shape's secret, so a leak is attributed to one shape.
func TestShapesAreDistinctAndWindowed(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, secretcheck.Shapes)
	for i, s := range secretcheck.Shapes {
		t.Run(s.Name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, s.Input, s.Secret)
			assert.True(t, strings.HasPrefix(s.Secret, s.Public), "the public prefix opens the secret")
			assert.NotEmpty(t, secretcheck.Windows(s.Secret, s.Public))
			for j, o := range secretcheck.Shapes {
				if i == j {
					continue
				}
				assert.Empty(t, secretcheck.Leaks(s.Secret, s.Public, map[string]string{o.Name: o.Secret}),
					"shape %s shares a window with %s", s.Name, o.Name)
			}
		})
	}
}

// fakeRefusal is a returned refusal value: Drive reads any result whose type
// name holds "Refusal".
type fakeRefusal struct{ Why string }

// TestDriveReadsEveryOutput: Drive hands the input to every string
// parameter (a variadic one too), zero values and stand-ins elsewhere, and
// reads an error, a *Refusal* result, a panic and the std/slog logs; a nil
// error and a nil result are nothing.
func TestDriveReadsEveryOutput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fn   any
		want map[string]string
	}{
		{"error carries the input", leaky, map[string]string{"error": "open IN: refused", "result": "", "log": ""}},
		{"fixed sentence", clean, map[string]string{"error": "refused: bad dsn", "result": "", "log": ""}},
		{"refusal result", func(s string) *fakeRefusal { return &fakeRefusal{Why: s} }, map[string]string{"error": "", "result": "&{Why:IN}", "log": ""}},
		{"nil result and nil error", func(string) (*fakeRefusal, error) { return nil, nil }, map[string]string{"error": "", "result": "", "log": ""}},
		{"variadic string and other parameters", func(w io.Writer, f func() int, n int, rest ...string) error {
			return fmt.Errorf("%v %d %d %s", w == io.Discard, f(), n, strings.Join(rest, ","))
		}, map[string]string{"error": "true 0 0 IN", "result": "", "log": ""}},
		// A panic stops the call before error and result are read: only log and panic come back.
		{"panic is read", func(s string) { panic("boom " + s) }, map[string]string{"log": "", "panic": "boom IN"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, secretcheck.Drive(tc.fn, "IN"))
		})
	}
	t.Run("std log is captured", func(t *testing.T) {
		t.Parallel()
		texts := secretcheck.Drive(func(s string) { log.Print("saw ", s) }, "IN")
		assert.Contains(t, texts["log"], "saw IN")
		assert.Equal(t, "", texts["error"])
	})
}

// TestFunctionNameIsQualified: the runtime's name ends in the package path
// and the function, which is what TableFindings compares a row against.
func TestFunctionNameIsQualified(t *testing.T) {
	t.Parallel()

	assert.True(t, strings.HasSuffix(secretcheck.FunctionName(leaky), "/pkg/secretcheck_test.leaky"), secretcheck.FunctionName(leaky))
}

// TestParseAllowlist: blank and # lines are skipped, a row is `<key> <reason>`
// with the reason trimmed, and a row with no reason is named bad and kept out.
func TestParseAllowlist(t *testing.T) {
	t.Parallel()

	rows, bad := secretcheck.ParseAllowlist("# comment\n\n  a.Open   leaks by design  \nb.Parse\nc.New \t\n")
	assert.Equal(t, map[string]string{"a.Open": "leaks by design"}, rows)
	assert.Equal(t, []string{"b.Parse: a row needs a reason after the function", "c.New: a row needs a reason after the function"}, bad)
	rows, bad = secretcheck.ParseAllowlist("")
	assert.Empty(t, rows)
	assert.Empty(t, bad)
}

func parse(t *testing.T, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	require.NoError(t, err)
	return f
}

// TestOpenersIn: an opener is an exported top-level Open*/Parse*/Dial*/New*
// function taking a string or ...string, keyed by its directory; methods,
// unexported names, functions without a string, testdata files and files
// that did not parse are left out.
func TestOpenersIn(t *testing.T) {
	t.Parallel()

	src := `package a
type T struct{}
func OpenA(s string) error { return nil }
func ParseB(xs ...string) {}
func DialC(n int, s string) {}
func NewD(n int) {}
func NewE() {}
func openF(s string) {}
func (T) OpenG(s string) {}
func OtherH(s string) {}
`
	files := []secretcheck.File{
		{Rel: "cmd/a/a.go", AST: parse(t, src)},
		{Rel: "internal/td/testdata/a.go", AST: parse(t, src), Testdata: true},
		{Rel: "internal/broken/b.go", AST: nil},
		{Rel: "internal/b/b.go", AST: parse(t, "package b\nfunc OpenZ(s string) {}\n")},
	}
	got := secretcheck.OpenersIn(files)
	assert.Equal(t, map[string]bool{"cmd/a.OpenA": true, "cmd/a.ParseB": true, "cmd/a.DialC": true, "internal/b.OpenZ": true}, got)

	for name, want := range map[string]bool{"Open": true, "OpenX": true, "Parse": true, "Dial": true, "New": true, "Newt": true, "open": false, "Read": false, "": false} {
		assert.Equal(t, want, secretcheck.OpenerName(name), name)
	}
	assert.False(t, secretcheck.TakesString(nil))
	assert.False(t, secretcheck.TakesString(&ast.FieldList{}))
}

// TestTableFindingsBothDirections: a found opener neither driven nor exempt,
// a table row naming no function, a row holding the wrong function, an
// exempt row naming no function, and a key both driven and exempt are each
// one finding; a correct row is none; the findings are sorted.
func TestTableFindingsBothDirections(t *testing.T) {
	t.Parallel()

	const me = "pkg/secretcheck_test.leaky"
	found := map[string]bool{me: true, "pkg/secretcheck_test.OpenOther": true, "x/y.Gone": true, "x/y.Unlisted": true}
	table := map[string]any{me: leaky, "pkg/secretcheck_test.OpenOther": leaky, "x/y.Missing": leaky}
	exempt := map[string]string{"x/y.Gone": "cannot be driven", "x/y.Missing2": "gone", me: "dup"}
	got := secretcheck.TableFindings(found, table, exempt)
	assert.True(t, sort.StringsAreSorted(got))
	require.Len(t, got, 5, strings.Join(got, "\n"))
	assert.Contains(t, got[0], "pkg/secretcheck_test.OpenOther: the row holds ")
	assert.Contains(t, got[1], me+": is both driven and exempt")
	assert.Contains(t, got[2], "x/y.Missing2: a secretExempt row names no such function")
	assert.Contains(t, got[3], "x/y.Missing: a secretOpeners row names no such function")
	assert.Contains(t, got[4], "x/y.Unlisted: an exported Open*/Parse*/Dial*/New* function")

	assert.Empty(t, secretcheck.TableFindings(map[string]bool{me: true}, map[string]any{me: leaky}, nil))
}

// TestLeakFindingsAndVerdict: a function that carries the input leaks on
// every shape and is a finding unless allowlisted; a clean function leaks
// nowhere, and its allowlist row is a stale finding.
func TestLeakFindingsAndVerdict(t *testing.T) {
	t.Parallel()

	table := map[string]any{"k.leaky": leaky, "k.clean": clean}
	leaks := secretcheck.LeakFindings(table, secretcheck.Shapes)
	assert.Len(t, leaks["k.leaky"], len(secretcheck.Shapes))
	assert.True(t, sort.StringsAreSorted(leaks["k.leaky"]))
	assert.Contains(t, leaks["k.leaky"][0], ": error holds \"")
	assert.Empty(t, leaks["k.clean"])

	got := secretcheck.Verdict(leaks, nil)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "k.leaky carries a secret into its output (")
	assert.Contains(t, got[0], "the allowlist does not grow")

	got = secretcheck.Verdict(leaks, map[string]string{"k.leaky": "known", "k.clean": "stale"})
	assert.Equal(t, []string{"k.clean: listed in secretLeakAllowlist and no longer leaks; delete the row"}, got)
}

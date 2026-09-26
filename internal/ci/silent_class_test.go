package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// silentAllowlistPath is the shrink-only list of the silent shapes the live
// path still permits, one `file:function` per row. It is empty today and
// the class test refuses a row whose shape has left, so it only ever gets
// shorter: a new `_ = err` is a refusal, not a parking place.
const silentAllowlistPath = "testdata/silent_allowlist.txt"

// silentLivePackages are the packages of the copy model's live path (Glenn
// 2026-09-26 11:30 AM ET: "every verb in nova tools related to current work
// should not fail silently"): the nova-sprint and nova-card verbs, the
// reconciler and its duties, the table, the wrapper and the copy ledger,
// the launcher, the function library loader, capacity and the pipeline
// reader. A new live package is added here, never the other way round.
var silentLivePackages = []string{
	"cmd/nova-sprint", "cmd/nova-card",
	"internal/nsprint/reconcile", "internal/nsprint/taskcard", "internal/nsprint/table",
	"internal/nsprint/card", "internal/nsprint/launch", "internal/nsprint/fn",
	"internal/nsprint/capacity", "internal/nsprint/pipeerr",
}

// silentErrIdent is an identifier that holds an error by its name: err,
// xerr, cerr, closeErr and so on. `_ = <one of these>` throws the failure
// away in one token.
var silentErrIdent = regexp.MustCompile(`(^|[a-z0-9_])[eE]rr$`)

// TestNoSilentFailureOnTheLivePath is the class rule of Glenn's standing
// order (2026-09-25: "every verb must return an error that you see, for
// breadcrumbs as you work, failing silent is not allowed. Without this
// every thing we have done shows it is not possible to make a reliable
// system"). It reads every non-test .go file of the live packages and
// refuses two shapes:
//
//   - `_ = err` (any error-named identifier assigned to the blank
//     identifier): the failure is dropped where it happened. Return it,
//     print one typed line (REFUSED <verb>: <why>) or count it into the
//     pass's DUTY line.
//   - `|| true` inside a Go string literal: an embedded script step whose
//     exit is thrown away. Drop the `|| true` and read the step's exit.
//
// Every allowlist row is checked both ways: a shape not listed is a red run,
// and a listed shape that has left is a stale row and also a red run.
func TestNoSilentFailureOnTheLivePath(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	allow := readSilentAllowlist(t)
	seen := map[string]bool{}
	var violations []string
	note := func(key, rel string, pos token.Pos, remedy string) {
		seen[key] = true
		if allow.Has(key) {
			return
		}
		violations = append(violations, fmt.Sprintf("%s:%d: %s", rel, tree.FSet.Position(pos).Line, remedy))
	}

	for _, src := range tree.GoFilesUnder(false, silentLivePackages...) {
		if src.ParseErr != nil {
			t.Fatal(src.ParseErr)
		}
		rel := src.Rel
		for _, decl := range src.AST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				// A package-level literal (an embedded script in a const)
				// is keyed by the file alone.
				ast.Inspect(decl, func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && silentTrueLiteral(lit) {
						note(rel+":<package>", rel, lit.Pos(), silentTrueRemedy)
					}
					return true
				})
				continue
			}
			key := rel + ":" + removeAllFuncName(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.AssignStmt:
					if silentDiscard(x) {
						note(key, rel, x.Pos(), silentDiscardRemedy)
					}
				case *ast.BasicLit:
					if silentTrueLiteral(x) {
						note(key, rel, x.Pos(), silentTrueRemedy)
					}
				}
				return true
			})
		}
	}
	// The list only shrinks: a row whose shape has left is a red run, so
	// nobody can quietly widen the exception set and leave it there.
	for _, row := range allowlist.Check(t, allow, seen).Stale {
		violations = append(violations, fmt.Sprintf(
			"%s lists %s, but no `_ = err` or `|| true` literal is there any more; delete the stale entry (the list only shrinks; NOVA_CI_UPDATE=1 drops it)",
			silentAllowlistPath, row.Key))
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

const (
	silentDiscardRemedy = "`_ = err` drops the failure where it happened; return it, print one typed line (REFUSED <verb>: <why>) or count it into the pass's DUTY line"
	silentTrueRemedy    = "`|| true` inside a Go string literal hides an embedded script step's failure; drop it and read the step's exit"
)

// silentDiscard reports whether a is `_ = <error-named identifier>`.
func silentDiscard(a *ast.AssignStmt) bool {
	if a.Tok != token.ASSIGN || len(a.Lhs) != 1 || len(a.Rhs) != 1 {
		return false
	}
	lhs, ok := a.Lhs[0].(*ast.Ident)
	if !ok || lhs.Name != "_" {
		return false
	}
	rhs, ok := a.Rhs[0].(*ast.Ident)
	return ok && silentErrIdent.MatchString(rhs.Name)
}

// silentTrueLiteral reports whether lit is a string literal holding `|| true`.
func silentTrueLiteral(lit *ast.BasicLit) bool {
	return lit.Kind == token.STRING && strings.Contains(lit.Value, "|| true")
}

// readSilentAllowlist reads the shrink-only list through the one helper
// (the allowlist rule, #4339), one `file:function` per row, a reason after
// the key: a row with no reason is a parking place, not a judgement.
func readSilentAllowlist(t *testing.T) *allowlist.List {
	t.Helper()
	allow := loadAllowlist(t, silentAllowlistPath, shrinkOnly)
	for _, row := range allow.Rows() {
		if _, reason, _ := strings.Cut(row.Text, " "); strings.TrimSpace(reason) == "" {
			t.Errorf("%s: %q carries no reason; a row says why the shape is judged not silent", silentAllowlistPath, row.Text)
		}
	}
	return allow
}

// TestSilentRuleReadsTheTwoShapes proves the rule over source: the two
// shapes are refused, and the shapes that look alike (a discarded value
// that is not an error, `|| true` in a comment) are not.
func TestSilentRuleReadsTheTwoShapes(t *testing.T) {
	t.Parallel()
	src := `package p

// a comment may say || true
const script = "step 2>/dev/null || true"

func f() {
	var err error
	var count int
	_ = err
	_ = count
	_, _ = g()
	s := "x || true"
	_ = s
}

func g() (int, error) { return 0, nil }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var discards, literals int
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if silentDiscard(x) {
				discards++
			}
		case *ast.BasicLit:
			if silentTrueLiteral(x) {
				literals++
			}
		}
		return true
	})
	if discards != 1 {
		t.Errorf("discards = %d, want 1 (only `_ = err`)", discards)
	}
	if literals != 2 {
		t.Errorf("literals = %d, want 2 (the const and the local string, never the comment)", literals)
	}
}

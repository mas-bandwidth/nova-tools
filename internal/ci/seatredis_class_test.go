package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// THE CLASS RULE: NO nova-sprint VERB REFUSES AN EMPTY --redis WHILE A SEAT
// IS SELECTED (nova-tools#4330).
//
// PR #4357 gave nova-sprint `--seat coordinator`: the seat's row in seats.tsv
// names its Redis, and store.Open falls back to it. The cold read found the
// seam: table, census, digest and fn load declared --redis with no default and
// refused the empty address themselves, before store.Open could fall back, so
// a session still typed --redis on every line -- the wrapper script's job,
// done by hand. The fix is one default for every verb, and this rule holds it
// at the source: every --redis flag in cmd/nova-sprint and internal/nsprint
// defaults to a seat-aware expression, and every hand-parsed --redis goes
// through redisOr in the function that reads it. A verb that declares
// `fs.String("redis", "", ...)` again is red here, naming file:line.

// seatRedisDirs are the trees the rule reads: the nova-sprint verbs and the
// packages whose verbs nova-sprint dispatches to.
var seatRedisDirs = []string{"cmd/nova-sprint", "internal/nsprint"}

// seatRedisDefaults are the expressions a --redis flag may default to: the
// helpers in cmd/nova-sprint/seat.go (and redis_raw.go's seat-first default),
// and seatcred.Addr() for a package outside cmd/nova-sprint.
var seatRedisDefaults = map[string]bool{
	"redisDefault":     true,
	"redisDefaultFrom": true,
	"redisOr":          true,
	"rawAddrDefault":   true,
	"seatcred.Addr":    true,
}

// seatRedisFlagNames are the flag names that carry the sprint's Redis address:
// --redis everywhere, and --store for nova-sprint note/rote.
func seatRedisFlagName(rel, name string) bool {
	return name == "redis" || (name == "store" && strings.HasPrefix(rel, "cmd/nova-sprint/"))
}

// seatRedisCallName is a call's function as written: "f" or "pkg.f".
func seatRedisCallName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			return id.Name + "." + x.Sel.Name
		}
		return x.Sel.Name
	}
	return ""
}

func seatRedisStringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// seatRedisViolations reads one parsed file and returns "<line>: <why>" for
// every --redis flag with a default that is not seat-aware, and every
// hand-parsed --redis read in a function that never calls redisOr. flags is
// how many --redis flags it saw.
func seatRedisViolations(fset *token.FileSet, rel string, f *ast.File) (out []string, flags int) {
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var handReads []token.Pos
		callsRedisOr := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				name := seatRedisCallName(x.Fun)
				if name == "redisOr" {
					callsRedisOr = true
				}
				sel, isSel := x.Fun.(*ast.SelectorExpr)
				switch {
				case isSel && sel.Sel.Name == "String" && len(x.Args) == 3:
					if flag, ok := seatRedisStringLit(x.Args[0]); ok && seatRedisFlagName(rel, flag) {
						flags++
						if def, ok := x.Args[1].(*ast.CallExpr); !ok || !seatRedisDefaults[seatRedisCallName(def.Fun)] {
							out = append(out, fmt.Sprintf("%d: --%s defaults to something that is not the seat's address", fset.Position(x.Pos()).Line, flag))
						}
					}
				case isSel && sel.Sel.Name == "StringVar" && len(x.Args) == 4:
					if flag, ok := seatRedisStringLit(x.Args[1]); ok && seatRedisFlagName(rel, flag) {
						flags++
						if def, ok := x.Args[2].(*ast.CallExpr); !ok || !seatRedisDefaults[seatRedisCallName(def.Fun)] {
							out = append(out, fmt.Sprintf("%d: --%s defaults to something that is not the seat's address", fset.Position(x.Pos()).Line, flag))
						}
					}
				case name == "flagValues" && len(x.Args) == 2:
					if flag, ok := seatRedisStringLit(x.Args[1]); ok && flag == "redis" {
						handReads = append(handReads, x.Pos())
					}
				}
			case *ast.IndexExpr:
				if key, ok := seatRedisStringLit(x.Index); ok && key == "redis" {
					handReads = append(handReads, x.Pos())
				}
			}
			return true
		})
		if !callsRedisOr {
			for _, p := range handReads {
				out = append(out, fmt.Sprintf("%d: a hand-parsed --redis in %s, which never calls redisOr", fset.Position(p).Line, fn.Name.Name))
			}
		}
	}
	return out, flags
}

// TestNoVerbRefusesAnEmptyRedisUnderASeat is #4330's class test: every
// nova-sprint verb's --redis falls back to the selected seat's address, so no
// verb refuses an empty --redis while a seat is selected. The remedy for a red
// line is `fs.String("redis", redisDefault(), "")` (or redisDefault("ENV") to
// keep a verb's own environment default), redisOr(v) for a hand-parsed flag,
// and seatcred.Addr() in a package outside cmd/nova-sprint.
func TestNoVerbRefusesAnEmptyRedisUnderASeat(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	var violations []string
	total := 0
	for _, f := range tree.GoFilesUnder(false, seatRedisDirs...) {
		if f.ParseErr != nil {
			t.Fatalf("%s: %v", f.Rel, f.ParseErr)
		}
		v, n := seatRedisViolations(tree.FSet, f.Rel, f.AST)
		total += n
		for _, line := range v {
			violations = append(violations, f.Rel+":"+line)
		}
	}
	// nova-sprint declares over a hundred --redis flags; a count this low
	// means the matcher has stopped seeing them, and a rule that sees
	// nothing passes by checking nothing.
	if total < 100 {
		t.Fatalf("only %d --redis flags found under %s; the matcher has stopped matching", total, strings.Join(seatRedisDirs, ", "))
	}
	if len(violations) > 0 {
		t.Fatalf("%d --redis read(s) refuse an empty address under --seat (#4330); default the flag to redisDefault() (or redisDefault(\"ENV\")), wrap a hand-parsed one in redisOr, or use seatcred.Addr() outside cmd/nova-sprint:\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// TestSeatRedisRuleSeesEachShape is the rule's own control: the four shapes
// the cold read found are red, and the seat-aware spellings are green.
func TestSeatRedisRuleSeesEachShape(t *testing.T) {
	t.Parallel()

	const src = `package main

func bad1() { _ = fs.String("redis", "", "") }
func bad2() { _ = fs.String("redis", os.Getenv("NOVA_SPRINT_REDIS"), "") }
func bad3() { fs.StringVar(&o.redis, "redis", "", "") }
func bad4(flags map[string]string) { open(flags["redis"]) }
func bad5() { _ = fs.String("store", "", "") }
func good1() { _ = fs.String("redis", redisDefault(), "") }
func good2() { _ = fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), "") }
func good3() { fs.StringVar(&o.redis, "redis", redisDefault(), "") }
func good4(flags map[string]string) { a := redisOr(flags["redis"]); open(a, flags["redis"]) }
func good5() { _ = fs.String("redis", seatcred.Addr(), "") }
func good6() { _ = fs.String("repo", "", "") }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, n := seatRedisViolations(fset, "cmd/nova-sprint/x.go", f)
	want := []string{"3:", "4:", "5:", "6:", "7:"}
	if len(got) != len(want) || n != 8 {
		t.Fatalf("violations %q (flags %d); want one on each of lines 3-7 and 8 flags seen", got, n)
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w) {
			t.Fatalf("violation %d = %q; want line %s", i, got[i], w)
		}
	}
	// --store is the sprint's Redis only in nova-sprint.
	if got, _ := seatRedisViolations(fset, "internal/nsprint/x.go", f); len(got) != 4 {
		t.Fatalf("outside cmd/nova-sprint --store is another flag: %q", got)
	}
}

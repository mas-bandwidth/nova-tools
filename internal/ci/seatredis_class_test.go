package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLASS RULE: NO VERB REFUSES AN EMPTY --redis WHILE A SEAT IS SELECTED
// (nova-tools#4330).
//
// PR #4357 gave nova-sprint `--seat coordinator`: the seat's row in seats.tsv
// names its Redis, and store.Open falls back to it. The cold read found the
// seam: table, census, digest and fn load declared --redis with no default and
// refused the empty address themselves, before store.Open could fall back, so
// a session still typed --redis on every line -- the wrapper script's job,
// done by hand. The fix is one default for every verb, and this rule holds it
// at the source: every --redis flag of a tool that selects a seat defaults to
// a seat-aware expression, and every hand-parsed --redis goes through redisOr
// in the function that reads it. A verb that declares `fs.String("redis", "",
// ...)` again is red here, naming file:line.
//
// WHICH PACKAGES: the rule was written over cmd/nova-sprint and
// internal/nsprint. nova-sprint is deprecated (Glenn 2026-09-27: deprecated
// code is not tested and never blocks CI; pkg/pkgselect/DEPRECATED), and a class
// rule over it is a test of it, so the rule reads the live packages
// (liveTree, the reading CI's selection uses) that select a seat: a package
// whose Go calls seatcred's FromArgs, directly or on seatcred.Process().
// Today that is cmd/nova-swarm, cmd/nova-table and cmd/nova-wake, and of them
// cmd/nova-table declares --redis: it carries nova-sprint's seat resolution
// (selectSeat) so a session names one seat for both. A live tool that starts
// selecting a seat joins the set by that call. A live tool that selects no
// seat has no seat to fall back to, and its empty --redis is its own refusal.

// seatRedisSelects is the call that makes a package select a seat.
var seatRedisSelects = regexp.MustCompile(`seatcred\.FromArgs\(|seatcred\.Process\(\)[\s\S]*\.FromArgs\(`)

// seatRedisDefaults are the expressions a --redis flag may default to: a
// tool's own seat-first helper (nova-table's redisDefault), redisOr around a
// hand-parsed value, and seatcred.Addr().
var seatRedisDefaults = map[string]bool{
	"redisDefault":  true,
	"redisOr":       true,
	"seatcred.Addr": true,
}

// seatRedisFlagName is the flag name that carries the store's address.
func seatRedisFlagName(name string) bool {
	return name == "redis"
}

// seatRedisDirs are the live packages under cmd/ and internal/ that select a
// seat, from the tree the class tests share.
func seatRedisDirs(t *testing.T, tree *repoTreeIndex) []string {
	t.Helper()
	lt := loadLiveTree(t, repoRoot(t))
	seen := map[string]bool{}
	var dirs []string
	for _, f := range tree.GoFilesUnder(false, "cmd", "internal") {
		dir := path.Dir(f.Rel)
		if seen[dir] || !lt.Package(dir) || !seatRedisSelects.Match(f.Src) {
			continue
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
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
func seatRedisViolations(fset *token.FileSet, f *ast.File) (out []string, flags int) {
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var handReads []token.Pos
		callsRedisOr := false
		// seatAware is whether a flag's default is the seat's address: a
		// call to one of seatRedisDefaults, or a name this function declared
		// from one (`addr := redisDefault(os.Getenv)`, nova-table's redisFlag
		// since #4458, whose resident shell then passes its own resolved
		// --redis instead). Only the declaration is read: the name's later
		// assignments are the function's own, and a declaration from
		// anything else, or a name it did not declare, is red.
		declared := map[string]ast.Expr{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if a, ok := n.(*ast.AssignStmt); ok && a.Tok == token.DEFINE && len(a.Lhs) == len(a.Rhs) {
				for i, l := range a.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						declared[id.Name] = a.Rhs[i]
					}
				}
			}
			return true
		})
		seatAware := func(e ast.Expr) bool {
			if id, ok := e.(*ast.Ident); ok {
				e = declared[id.Name]
			}
			call, ok := e.(*ast.CallExpr)
			return ok && seatRedisDefaults[seatRedisCallName(call.Fun)]
		}
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
					if flag, ok := seatRedisStringLit(x.Args[0]); ok && seatRedisFlagName(flag) {
						flags++
						if !seatAware(x.Args[1]) {
							out = append(out, fmt.Sprintf("%d: --%s defaults to something that is not the seat's address", fset.Position(x.Pos()).Line, flag))
						}
					}
				case isSel && sel.Sel.Name == "StringVar" && len(x.Args) == 4:
					if flag, ok := seatRedisStringLit(x.Args[1]); ok && seatRedisFlagName(flag) {
						flags++
						if !seatAware(x.Args[2]) {
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
// --redis of a live tool that selects a seat falls back to the selected
// seat's address, so no verb refuses an empty --redis while a seat is
// selected. The remedy for a red line is the tool's seat-first default
// (nova-table: `fs.String("redis", redisDefault(os.Getenv), "")`), redisOr(v)
// for a hand-parsed flag, or seatcred.Addr().
func TestNoVerbRefusesAnEmptyRedisUnderASeat(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	dirs := seatRedisDirs(t, tree)
	// nova-table selects a seat (selectSeat, seatcred.Process().FromArgs);
	// a set without it means the selection matcher has stopped matching,
	// and a rule over no package passes by checking nothing.
	i := sort.SearchStrings(dirs, "cmd/nova-table")
	require.Truef(t, i < len(dirs) && dirs[i] == "cmd/nova-table", "the live packages that select a seat are %q, without cmd/nova-table; the selection matcher has stopped matching", dirs)
	var violations []string
	table := 0
	for _, f := range tree.GoFilesUnder(false, dirs...) {
		require.NoErrorf(t, f.ParseErr, "%s: %v", f.Rel, f.ParseErr)
		v, n := seatRedisViolations(tree.FSet, f.AST)
		if path.Dir(f.Rel) == "cmd/nova-table" {
			table += n
		}
		for _, line := range v {
			violations = append(violations, f.Rel+":"+line)
		}
	}
	// nova-table declares its --redis once (redisFlag) and every verb takes
	// it from there; none seen means the flag matcher has stopped seeing
	// them. (nova-wake and nova-swarm select a seat and take no --redis.)
	require.GreaterOrEqualf(t, table, 1, "no --redis flag found in cmd/nova-table (set %s); the matcher has stopped matching", strings.Join(dirs, ", "))
	require.Emptyf(t, violations, "%d --redis read(s) refuse an empty address under --seat (#4330); default the flag to the tool's seat-first default (nova-table: redisDefault(os.Getenv)), wrap a hand-parsed one in redisOr, or use seatcred.Addr():\n  %s",
		len(violations), strings.Join(violations, "\n  "))
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
func bad5() { _ = fs.String("redis", redisDefaultFrom(), "") }
func good1() { _ = fs.String("redis", redisDefault(), "") }
func good2() { _ = fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), "") }
func good3() { fs.StringVar(&o.redis, "redis", redisDefault(), "") }
func good4(flags map[string]string) { a := redisOr(flags["redis"]); open(a, flags["redis"]) }
func good5() { _ = fs.String("redis", seatcred.Addr(), "") }
func good6() { _ = fs.String("repo", "", "") }
func good7() { _ = fs.String("store", "", "") }
func good8() { a := redisDefault(os.Getenv); if shared { a = own }; _ = fs.String("redis", a, "") }
func bad6() { a := os.Getenv("X"); _ = fs.String("redis", a, "") }
func bad7(a string) { _ = fs.String("redis", a, "") }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	require.NoError(t, err)
	got, n := seatRedisViolations(fset, f)
	want := []string{"3:", "4:", "5:", "6:", "7:", "16:", "17:"}
	require.Truef(t, len(got) == len(want) && n == 11, "violations %q (flags %d); want one on each of lines 3-7, 16 and 17 and 11 flags seen", got, n)
	for i, w := range want {
		require.Truef(t, strings.HasPrefix(got[i], w), "violation %d = %q; want line %s", i, got[i], w)
	}
	// The selection matcher: both spellings a tool selects a seat by, and
	// neither a Process() read with no FromArgs nor another package's
	// FromArgs.
	for src, want := range map[string]bool{
		"args, err := seatcred.FromArgs(args, os.Getenv)":                    true,
		"rest, err := selectSeat(seatcred.Process(), args)\nsel.FromArgs(a)": true,
		"addr := seatcred.Process().Addr()":                                  false,
		"x := other.FromArgs(args)":                                          false,
	} {
		got := seatRedisSelects.MatchString(src)
		assert.Equalf(t, want, got, "seatRedisSelects on %q = %v, want %v", src, got, want)
	}
}

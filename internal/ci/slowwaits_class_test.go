package ci

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// slowwaits_class_test.go is Glenn's ceiling on 2026-09-25: a per-commit run
// finishes in two minutes at most, one minute is the target, and "as we have
// worked, we have made tests slower." On that day go-test-cmd took 129 s on the
// Linux bench: one verdict test sat 59 s in a provider retry wait nobody
// asserted, one route test polled the wrong key until its 30 s ceiling, and a
// deadline test waited out a stalled reader for 25 s. So a per-commit test may
// not pay a wait it wrote itself:
//
//  1. no time.Sleep of more than one second, and
//  2. no deadline over five seconds and under thirty handed to the code under
//     test -- a field, assignment or `--flag` value named deadline, timeout,
//     wall, grace or idle. That band is the shape of a test that proves a
//     deadline by reaching it (a ten-second wedge, a six-second first exec)
//     and pays the whole deadline, every run; a 200 ms deadline injected
//     through the same seam proves the same thing. Below five seconds is the
//     short injected deadline. Thirty seconds or more is the generous ceiling
//     the `waits` and ten-second laws ask for, a bound on an event that comes
//     long before it, which costs nothing and is not read here.
//
// The exception is a file behind `//go:build slow`, which the per-commit legs
// do not build and .github/workflows/nightly-slow.yml runs every night; the
// remedy for a test that must wait is to move it there. The ten-second law
// (TestNoTestAssertsAWallClockBoundUnderTenSeconds) reads a DIFFERENT shape and
// the two do not collide: a context.WithTimeout, time.After, NewTimer or
// time.Since bound is a ceiling on an event the test waits for, it costs
// nothing when the event comes, and that law wants it generous. Those lines are
// not read here.
//
// The hits that stood on the day the rule landed are on the shrink-only
// allowlist, one `path:TestOrFunc` per line with its reason; a listed entry
// with no hit left is a red run.
const slowWaitsAllowlistPath = "testdata/slowwaits_allowlist.txt"

// slowSleepCeiling is the longest sleep a per-commit test may write, and a
// deadline in [slowDeadlineCeiling, generousCeiling) is one it waits out.
const (
	slowSleepCeiling    = time.Second
	slowDeadlineCeiling = 5 * time.Second
	generousCeiling     = 30 * time.Second
)

// waitedOut reports whether a deadline handed to the code under test sits in
// the band a test pays in full.
func waitedOut(d time.Duration) bool { return d > slowDeadlineCeiling && d < generousCeiling }

// deadlineNameRe is a field, variable or flag that sets how long the code under
// test waits before it gives up.
var deadlineNameRe = regexp.MustCompile(`(?i)deadline|timeout|wall|grace|idle`)

// slowWait is one hit: the file, the line, the enclosing function and what was
// found there.
type slowWait struct {
	Rel, Func, What string
	Line            int
}

func TestNoTestSleepsOverASecondOrWaitsOutADeadlineOverFive(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	var hits []slowWait
	for _, f := range tree.GoFilesUnder(true, "cmd", "internal") {
		if f.HasDirNamed("testdata") || isSlowTagged(f.Src) {
			continue
		}
		if f.ParseErr != nil {
			t.Fatal(f.ParseErr)
		}
		hits = append(hits, slowWaitsIn(tree.FSet, f.Rel, f.AST)...)
	}
	allow := readSlowWaitsAllowlist(t)
	seen := map[string]bool{}
	var violations []string
	for _, h := range hits {
		key := h.Rel + ":" + h.Func
		seen[key] = true
		if _, ok := allow[key]; ok {
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"%s:%d: %s %s in a per-commit test; inject a short one through the seam (200 ms proves a deadline as well as 10 s does), wait on the event instead of the clock, or move the test behind //go:build slow (nightly-slow.yml runs it)",
			h.Rel, h.Line, h.Func, h.What))
	}
	for key := range allow {
		if !seen[key] {
			violations = append(violations, fmt.Sprintf(
				"%s lists %s, but it has no slow wait left; delete the stale entry (the list only shrinks)",
				slowWaitsAllowlistPath, key))
		}
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Error(v)
	}
}

// TestSlowWaitsRuleSeesEveryShape pins the reader against the shapes it exists
// to catch and the ones it must pass, so a reader that stopped matching cannot
// report a clean tree.
func TestSlowWaitsRuleSeesEveryShape(t *testing.T) {
	t.Parallel()

	src := `package p

func TestA(t *testing.T) { time.Sleep(2 * time.Second) }
func TestB(t *testing.T) { time.Sleep(1500 * time.Millisecond) }
func TestC(t *testing.T) { r.RunTimeout = 10 * time.Second }
func TestD(t *testing.T) { _ = Opts{Deadline: 20 * time.Second} }
func TestE(t *testing.T) { run("native", "--deadline", "10s") }
func TestF(t *testing.T) { idle := 6 * time.Second; _ = idle }
func TestOK1(t *testing.T) { time.Sleep(time.Second) }
func TestOK2(t *testing.T) { time.Sleep(50 * time.Millisecond) }
func TestOK3(t *testing.T) { ctx, _ := context.WithTimeout(ctx, 30*time.Second); _ = ctx }
func TestOK4(t *testing.T) { r.RunTimeout = 200 * time.Millisecond }
func TestOK5(t *testing.T) { run("native", "--deadline", "5s") }
func TestOK6(t *testing.T) { _ = Opts{Interval: 10 * time.Second} }
func TestOK7(t *testing.T) { run("native", "--deadline", "30s") }
func TestOK8(t *testing.T) { _ = Opts{Deadline: time.Minute} }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p_test.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, h := range slowWaitsIn(fset, "p_test.go", f) {
		got[h.Func] = true
	}
	for _, name := range []string{"TestA", "TestB", "TestC", "TestD", "TestE", "TestF"} {
		if !got[name] {
			t.Errorf("the slow-wait reader missed %s", name)
		}
	}
	for _, name := range []string{"TestOK1", "TestOK2", "TestOK3", "TestOK4", "TestOK5", "TestOK6", "TestOK7", "TestOK8"} {
		if got[name] {
			t.Errorf("the slow-wait reader flagged %s, which waits for nothing over the line", name)
		}
	}
}

// isSlowTagged reports whether the file's build constraint names the slow tag.
func isSlowTagged(src []byte) bool {
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if strings.HasPrefix(line, "//go:build") {
			for _, word := range strings.Fields(strings.NewReplacer("&&", " ", "||", " ", "(", " ", ")", " ").Replace(strings.TrimPrefix(line, "//go:build"))) {
				if word == "slow" {
					return true
				}
			}
		}
	}
	return false
}

// slowWaitsIn reads one parsed file. Durations are read by constDuration, the
// `waits` rule's own evaluator (ci_waits.go), so the two rules agree on what a
// literal duration is.
func slowWaitsIn(fset *token.FileSet, rel string, f *ast.File) []slowWait {
	var hits []slowWait
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			name = receiverName(fn.Recv.List[0].Type) + "." + name
		}
		add := func(n ast.Node, what string) {
			hits = append(hits, slowWait{Rel: rel, Func: name, What: what, Line: fset.Position(n.Pos()).Line})
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if isSelector(x.Fun, "time", "Sleep") && len(x.Args) == 1 {
					if d, ok := constDuration(x.Args[0]); ok && d > slowSleepCeiling {
						add(x, "sleeps "+d.String())
					}
				}
				for i := 0; i+1 < len(x.Args); i++ {
					flag, ok1 := stringLit(x.Args[i])
					val, ok2 := stringLit(x.Args[i+1])
					if !ok1 || !ok2 || !strings.HasPrefix(flag, "-") || !deadlineNameRe.MatchString(flag) {
						continue
					}
					if d, err := time.ParseDuration(val); err == nil && waitedOut(d) {
						add(x.Args[i], "hands the code under test "+flag+" "+val)
					}
				}
			case *ast.CompositeLit:
				for i := 0; i+1 < len(x.Elts); i++ {
					flag, ok1 := stringLit(x.Elts[i])
					val, ok2 := stringLit(x.Elts[i+1])
					if !ok1 || !ok2 || !strings.HasPrefix(flag, "-") || !deadlineNameRe.MatchString(flag) {
						continue
					}
					if d, err := time.ParseDuration(val); err == nil && waitedOut(d) {
						add(x.Elts[i], "hands the code under test "+flag+" "+val)
					}
				}
			case *ast.KeyValueExpr:
				if key, ok := x.Key.(*ast.Ident); ok && deadlineNameRe.MatchString(key.Name) {
					if d, ok := constDuration(x.Value); ok && waitedOut(d) {
						add(x, "sets "+key.Name+" to "+d.String())
					}
				}
			case *ast.AssignStmt:
				if len(x.Lhs) != len(x.Rhs) {
					return true
				}
				for i, l := range x.Lhs {
					lname := ""
					switch v := l.(type) {
					case *ast.Ident:
						lname = v.Name
					case *ast.SelectorExpr:
						lname = v.Sel.Name
					}
					if lname == "" || !deadlineNameRe.MatchString(lname) {
						continue
					}
					if d, ok := constDuration(x.Rhs[i]); ok && waitedOut(d) {
						add(x, "sets "+lname+" to "+d.String())
					}
				}
			}
			return true
		})
	}
	return hits
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// readSlowWaitsAllowlist reads `path:Func <reason>` lines.
func readSlowWaitsAllowlist(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(slowWaitsAllowlistPath))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, reason, _ := strings.Cut(text, " ")
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s: %q carries no reason", slowWaitsAllowlistPath, text)
		}
		out[key] = reason
	}
	return out
}

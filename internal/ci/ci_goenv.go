package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// ci_goenv.go is the machine behind the `goenv` class test. It reads every .go
// file under root/internal and root/cmd -- tests included, because a test
// helper that builds a binary is a tool spawning go exactly like a verb is --
// and refuses an exec.Command whose argv[0] is the literal "go" unless the
// function around it builds the child's environment with goenv.Clean.
//
// The class is one bug seen once: CI's `make test` exports GOFLAGS=-json, the
// inner `go test` of `nova-review mutate` inherited it, and the parser counting
// `--- PASS:` lines saw a JSON stream instead, called the green run red and
// reported `red=2 green=0` where the range is `red=1 green=1`. Three legs of
// integration-4 (#1332) failed on a tool that was working. Any tool that reads
// the output of a go command it started has the same hole; internal/goenv is
// the one place that closes it, and this checker is what keeps the next site
// from opening it again.
//
// It writes nothing. Its only input besides the tree is the allowlist of
// existing offenders, in the `file:line kind date reason` rows the waits and
// net lists already use, and like those it only ever shrinks.

// GoEnvVerbLine is the help line the class test is entered under.
const GoEnvVerbLine = "goenv   read every .go under cmd/ and internal/; refuse a child `go` that inherits the caller's environment"

const (
	// GoEnvRemedy is the one thing to do about a go command that inherits the
	// caller's environment.
	GoEnvRemedy = "set cmd.Env = goenv.Clean(os.Environ())"
	// GoEnvRemedyAllow is the one thing to do about an allowlist entry that
	// names no offender: the list only shrinks.
	GoEnvRemedyAllow = "delete the stale row; the allowlist only shrinks"
)

// goEnvPkgDir is the one implementation of the rule. The walk skips it: Clean
// is where the sanitized environment is built, so reading it against itself
// would be circular.
const goEnvPkgDir = "internal/goenv"

// checkGoEnvDirs are the two trees on the CI path, the same pair the waits and
// net checkers read.
var checkGoEnvDirs = []string{"internal", "cmd"}

// GoEnvFinding is one child `go` that inherits the caller's environment, with
// its file, line, the function it stands in and the one thing to do about it.
// Kind is "inherit" for a go command with no sanitized environment, or
// "allowlist" for a row that names no offender.
type GoEnvFinding struct {
	File   string
	Line   int
	Func   string
	Kind   string
	Remedy string
}

// Render is the one-line refusal for this finding.
func (f GoEnvFinding) Render() string {
	return fmt.Sprintf("CI-GOENV file=%s line=%d func=%s remedy=%q", f.File, f.Line, f.Func, f.Remedy)
}

// GoEnvResult is one run of the checker: the files read, the allowlist entries
// still honored, the offenders left, and the entries that name no offender.
type GoEnvResult struct {
	Files       int
	Allowlisted int
	Findings    []GoEnvFinding
	Stale       []GoEnvFinding
}

// Refused is the number of lines the run would print: offenders plus stale
// allowlist entries.
func (r GoEnvResult) Refused() int { return len(r.Findings) + len(r.Stale) }

// OKLine is the one line a clean run prints.
func (r GoEnvResult) OKLine() string {
	return fmt.Sprintf("CI-GOENV OK files=%d allowlisted=%d refused=0", r.Files, r.Allowlisted)
}

// FailLine closes a refusal with the counts.
func (r GoEnvResult) FailLine() string {
	return fmt.Sprintf("CI-GOENV FAIL files=%d allowlisted=%d refused=%d", r.Files, r.Allowlisted, r.Refused())
}

// ExitCode is the status the check would exit with: 2 when anything is
// refused, 0 when the tree is clean.
func (r GoEnvResult) ExitCode() int {
	if r.Refused() > 0 {
		return 2
	}
	return 0
}

// CheckGoEnv reads every .go file under root/internal and root/cmd and returns
// the go commands that inherit the caller's environment, the allowlist entries
// honored, and any entry that names no offender. The tree comes from the
// caller, never from a walk of the repository; testdata directories and
// internal/goenv itself are skipped.
func CheckGoEnv(root, allowlistPath string) (GoEnvResult, error) {
	var res GoEnvResult
	entries, err := readWaitAllowlist(allowlistPath)
	if err != nil {
		return res, err
	}
	matched := make([]bool, len(entries))

	err = walkCIGoFiles(root, func(rel string, raw []byte) error {
		res.Files++
		findings, ok := scanGoEnvFile(rel, raw)
		if ok {
			res.Findings = append(res.Findings, findings...)
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	var remaining []GoEnvFinding
	for _, f := range res.Findings {
		if i := matchGoEnvAllow(entries, matched, f); i >= 0 {
			matched[i] = true
			res.Allowlisted++
			continue
		}
		remaining = append(remaining, f)
	}
	res.Findings = remaining
	for i, e := range entries {
		if matched[i] {
			continue
		}
		res.Stale = append(res.Stale, GoEnvFinding{File: e.file, Line: e.line, Kind: "allowlist", Remedy: GoEnvRemedyAllow})
	}
	return res, nil
}

// matchGoEnvAllow returns the index of an unused entry that allows this
// finding, or -1. A row allows ONE offender of its kind in its file; the line
// is for a reader and an exact match is only preferred, never required, so a
// merge that shifts lines does not turn dev red (the lesson the waits list
// carries from #1073).
func matchGoEnvAllow(entries []waitAllow, used []bool, f GoEnvFinding) int {
	loose := -1
	for i, e := range entries {
		if used[i] || e.file != f.File || e.kind != f.Kind {
			continue
		}
		if e.line == f.Line {
			return i
		}
		if loose < 0 {
			loose = i
		}
	}
	return loose
}

// walkCIGoFiles hands every .go file under root/internal and root/cmd to fn,
// tests included. testdata, .git and vendor directories are skipped, and so is
// internal/goenv, which is the rule's own implementation.
func walkCIGoFiles(root string, fn func(rel string, src []byte) error) error {
	for _, dir := range checkGoEnvDirs {
		base := filepath.Join(root, dir)
		if _, statErr := os.Stat(base); statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return statErr
		}
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				switch d.Name() {
				case "testdata", ".git", "vendor":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if rel == goEnvPkgDir || strings.HasPrefix(rel, goEnvPkgDir+"/") {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			return fn(rel, raw)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// scanGoEnvFile returns the go commands in one file whose function does not
// build a sanitized environment. A file that does not parse is reported as
// read-but-clean rather than as an error: the checker refuses environments,
// never syntax, and the compiler has the better message for a broken file.
func scanGoEnvFile(rel string, raw []byte) ([]GoEnvFinding, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, raw, 0)
	if err != nil {
		return nil, false
	}
	var findings []GoEnvFinding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		// The whole function declaration is the scope, closures included: a
		// `cmd.Env = goenv.Clean(...)` beside the call is what a reader checks.
		sanitized := assignsCleanEnv(fn.Body)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			pos, ok := goCommandArgv0(call)
			if !ok || sanitized {
				return true
			}
			findings = append(findings, GoEnvFinding{
				File:   rel,
				Line:   fset.Position(pos).Line,
				Func:   goEnvFuncName(fn),
				Kind:   "inherit",
				Remedy: GoEnvRemedy,
			})
			return true
		})
	}
	return findings, true
}

// goCommandArgv0 reports whether call is exec.Command or exec.CommandContext
// with the literal "go" as argv[0], and where that literal stands.
func goCommandArgv0(call *ast.CallExpr) (token.Pos, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "exec" {
		return 0, false
	}
	argv0 := -1
	switch sel.Sel.Name {
	case "Command":
		argv0 = 0
	case "CommandContext":
		argv0 = 1
	default:
		return 0, false
	}
	if len(call.Args) <= argv0 {
		return 0, false
	}
	lit, ok := call.Args[argv0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return 0, false
	}
	if strings.Trim(lit.Value, "`\"") != "go" {
		return 0, false
	}
	return lit.Pos(), true
}

// assignsCleanEnv reports whether the body assigns some command's Env field
// from goenv.Clean, directly or through an append onto it. This is the
// conservative heuristic the class rule is written in: it asks to SEE the
// sanitized environment beside the call, so a helper that hides it one frame
// away is refused rather than trusted.
func assignsCleanEnv(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		var rhs []ast.Expr
		switch s := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range s.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Env" {
					rhs = s.Rhs
				}
			}
		case *ast.KeyValueExpr:
			// exec.Cmd{Env: goenv.Clean(os.Environ())}
			if key, ok := s.Key.(*ast.Ident); ok && key.Name == "Env" {
				rhs = []ast.Expr{s.Value}
			}
		}
		for _, expr := range rhs {
			ast.Inspect(expr, func(inner ast.Node) bool {
				call, ok := inner.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if ok && pkg.Name == "goenv" && sel.Sel.Name == "Clean" {
					found = true
					return false
				}
				return true
			})
		}
		return true
	})
	return found
}

// goEnvFuncName is the function a finding names: the method qualified by its
// receiver when there is one, so a name that reads the same on two types is
// still two entries.
func goEnvFuncName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// receiverName is the type a method hangs off, through a pointer and through a
// type parameter. The removeall class test names its functions the same way.
func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverName(t.X)
	}
	return "?"
}

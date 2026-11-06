//go:build functional && !windows

package contract

// The lint half of the fault contract: three rules read off the source, so a
// fault no row provokes yet still cannot be written in silence.
//
//  1. A discarded error ("_ = f()") carries an "ignored:" comment saying why.
//  2. A function that returns a non-zero exit code writes to stderr first.
//  3. A fleet task with "no_log: true" has a receipt: a later debug, fail or
//     assert task in the same file that reads the task's registered result, so
//     the cause is not lost with the hidden output.
//
// Where a tool breaks a rule today, the count is in the known tables below,
// with the audit's gap number. A count may not grow, and it must be lowered the
// day it shrinks, so a fix PR edits one number.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var lintTools = []string{"bus", "cairn", "config", "fleet", "reserve", "sandbox", "secrets"}

// Files that break rule 1 or 2 today and how many places each has (the audit's section 8).
var knownDiscards = map[string]int{
	"cairn/memory.go": 1, "cairn/store.go": 6, "config/inventory.go": 2, "config/rows.go": 1, "config/store.go": 1,
	"fleet/check.go": 5, "fleet/main.go": 11, "fleet/up.go": 6, "reserve/main.go": 5, "reserve/os_windows.go": 2,
	"sandbox/closefds.go": 2, "sandbox/probe.go": 1, "sandbox/probeclient_linux.go": 1, "sandbox/seccomp_linux.go": 1,
	"secrets/main.go": 15,
}

// sandbox/probe.go is the probe's helper child: its exit status is the finding, which the parent prints.
var knownSilentExits = map[string]int{"sandbox/probe.go": 3}

// no_log tasks in fleet/ with no receipt today, as "<file>: <task name>"; gap 9 of the audit.
var knownNoLog = map[string]bool{
	"fleet/roles/seal/tasks/main.yml: the password, read on this machine":                                                                             true,
	"fleet/roles/github-runner/tasks/unix-retire.yml: remove the runner from GitHub (the token reaches the runner on its stdin)":                      true,
	"fleet/roles/postgres/tasks/main.yml: a password per role, made once on this machine (it leaves only through nova-secrets)":                       true,
	"fleet/roles/tailscale/tasks/windows.yml: the key file":                                                                                           true,
	"fleet/roles/tailscale/tasks/windows.yml: tailscale up":                                                                                           true,
	"fleet/roles/github-runner/tasks/windows-retire.yml: remove it from GitHub and uninstall the service (the token reaches the runner on its stdin)": true,
	"fleet/roles/github-runner/tasks/windows-runner.yml: register with GitHub and install the service (the token reaches the runner on its stdin)":    true,
	"fleet/roles/postgres/tasks/main.yml: each role answers with its file's password over the listen address (scram-sha-256)":                         true,
	"fleet/roles/postgres/tasks/main.yml: set the password of each role whose proof failed (file to stdin, never argv, never a log)":                  true,
	"fleet/roles/postgres/tasks/main.yml: every role answers now":                                                                                     true,
	"fleet/roles/redis/tasks/acl.yml: a password per user, made once on this machine (it leaves only through nova-secrets)":                           true,
}

func goSources(t *testing.T) map[string]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	for _, tool := range lintTools {
		files, _ := filepath.Glob(filepath.Join("..", "..", tool, "*.go"))
		for _, p := range files {
			if strings.HasSuffix(p, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, p, nil, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			out[tool+"/"+filepath.Base(p)] = f
			fileSet[f] = fset
		}
	}
	return out
}

var fileSet = map[*ast.File]*token.FileSet{}

func ratchet(t *testing.T, rule string, found map[string][]string, known map[string]int) {
	t.Helper()
	files := map[string]bool{}
	for f := range found {
		files[f] = true
	}
	for f := range known {
		files[f] = true
	}
	names := make([]string, 0, len(files))
	for f := range files {
		names = append(names, f)
	}
	sort.Strings(names)
	for _, f := range names {
		switch got, want := len(found[f]), known[f]; {
		case got > want:
			t.Errorf("%s: %s has %d places, %d known:\n  %s", rule, f, got, want, strings.Join(found[f], "\n  "))
		case got < want:
			t.Errorf("%s: %s now has %d places: lower its known count from %d", rule, f, got, want)
		case got > 0:
			t.Logf("known gap: %s: %s has %d places", rule, f, got)
		}
	}
}

// the last name on the left is blank: "_ = f()", "_, _ = f()", "v, _ := f()"
var blankAssign = regexp.MustCompile(`^\s*([\w.\[\]*]+\s*,\s*)*_\s*:?=\s`)

// a blank second value that is not an error: Cut's found, a decoded size, a type assertion's ok
var notAnError = regexp.MustCompile(`strings\.Cut|utf8\.Decode|path\.Split|\.\(\w+\)`)

func TestLintDiscardedErrors(t *testing.T) {
	found := map[string][]string{}
	for name := range goSources(t) {
		src, _ := os.ReadFile(filepath.Join("..", "..", name))
		lines := strings.Split(string(src), "\n")
		for i, l := range lines {
			if !blankAssign.MatchString(l) || notAnError.MatchString(l) {
				continue
			}
			if strings.Contains(l, "ignored:") || (i > 0 && strings.Contains(lines[i-1], "ignored:")) {
				continue
			}
			found[name] = append(found[name], fmt.Sprintf("%s:%d: %s", name, i+1, strings.TrimSpace(l)))
		}
	}
	ratchet(t, "discarded error without an \"ignored:\" comment", found, knownDiscards)
}

var talks = regexp.MustCompile(`(?i)stderr|\berrw\b|\berrs\b|Fprint|refus|usage|fail|\bsay\(|die\(`)

// An exit is a "return N" or "os.Exit(N)" with N one of the exit codes. It must be
// preceded, in its own block or an enclosing one, by a statement that talks: a
// write to stderr in a sibling branch does not count.
func TestLintSilentExits(t *testing.T) {
	found := map[string][]string{}
	for name, f := range goSources(t) {
		fset := fileSet[f]
		src, _ := os.ReadFile(filepath.Join("..", "..", name))
		text := func(n ast.Node) string {
			return string(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset])
		}
		exitCode := func(s ast.Stmt) bool {
			var code ast.Expr
			switch s := s.(type) {
			case *ast.ReturnStmt:
				if len(s.Results) == 1 {
					code = s.Results[0]
				}
			case *ast.ExprStmt:
				if c, ok := s.X.(*ast.CallExpr); ok && len(c.Args) == 1 && strings.HasSuffix(text(c.Fun), "os.Exit") {
					code = c.Args[0]
				}
			}
			lit, ok := code.(*ast.BasicLit)
			return ok && lit.Kind == token.INT && strings.Contains(" 1 2 125 126 127 ", " "+lit.Value+" ")
		}
		var walk func(list []ast.Stmt, talked bool)
		walk = func(list []ast.Stmt, talked bool) {
			for _, s := range list {
				switch s := s.(type) {
				case *ast.BlockStmt:
					walk(s.List, talked)
				case *ast.IfStmt:
					if talks.MatchString(text(s.Cond)) || (s.Init != nil && talks.MatchString(text(s.Init))) {
						talked = true // the condition runs a call that writes
					}
					walk(s.Body.List, talked)
					if s.Else != nil {
						walk([]ast.Stmt{s.Else}, talked)
					}
				case *ast.ForStmt:
					walk(s.Body.List, talked)
				case *ast.RangeStmt:
					walk(s.Body.List, talked)
				case *ast.LabeledStmt:
					walk([]ast.Stmt{s.Stmt}, talked)
				case *ast.SwitchStmt:
					walk([]ast.Stmt{s.Body}, talked)
				case *ast.TypeSwitchStmt:
					walk([]ast.Stmt{s.Body}, talked)
				case *ast.SelectStmt:
					walk([]ast.Stmt{s.Body}, talked)
				case *ast.CaseClause:
					walk(s.Body, talked)
				case *ast.CommClause:
					walk(s.Body, talked)
				default:
					if exitCode(s) && !talked {
						pos := fset.Position(s.Pos())
						found[name] = append(found[name], fmt.Sprintf("%s:%d: %s", name, pos.Line, text(s)))
					}
					if talks.MatchString(text(s)) {
						talked = true
					}
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch fn := n.(type) {
			case *ast.FuncDecl:
				if fn.Body != nil {
					walk(fn.Body.List, false)
				}
			case *ast.FuncLit:
				walk(fn.Body.List, false)
			}
			return true
		})
	}
	ratchet(t, "non-zero exit with nothing written to stderr before it", found, knownSilentExits)
}

var (
	noLogRe    = regexp.MustCompile(`^\s*no_log:\s*true\b`)
	taskStart  = regexp.MustCompile(`^(\s*)- `)
	registerRe = regexp.MustCompile(`register:\s*([A-Za-z_][A-Za-z0-9_]*)`)
	receiptMod = regexp.MustCompile(`ansible\.builtin\.(debug|fail|assert)\b`)
)

// taskAt returns the line range [from, to) of the YAML task that holds line i.
func taskAt(lines []string, i int) (int, int) {
	ind := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
	from := i
	for ; from >= 0; from-- {
		if m := taskStart.FindStringSubmatch(lines[from]); m != nil && len(m[1]) < ind {
			break
		}
	}
	if from < 0 {
		from = 0
	}
	dash := len(taskStart.FindStringSubmatch(lines[from])[1])
	to := from + 1
	for ; to < len(lines); to++ {
		l := lines[to]
		if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		if len(l)-len(strings.TrimLeft(l, " ")) <= dash {
			break
		}
	}
	return from, to
}

func TestLintNoLogReceipts(t *testing.T) {
	var plays []string
	filepath.WalkDir(filepath.Join("..", "..", "fleet"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".yml") {
			plays = append(plays, p)
		}
		return nil
	})
	sort.Strings(plays)
	found := map[string]bool{}
	for _, p := range plays {
		b, _ := os.ReadFile(p)
		lines := strings.Split(string(b), "\n")
		for i, l := range lines {
			if !noLogRe.MatchString(l) {
				continue
			}
			from, to := taskAt(lines, i)
			task := strings.Join(lines[from:to], "\n")
			label := strings.TrimPrefix(filepath.ToSlash(p), "../../") + ": " + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[from]), "- name:"))
			if strings.Contains(task, "ansible.builtin.set_fact") {
				continue // sets a fact and runs nothing that could fail
			}
			reg := registerRe.FindStringSubmatch(task)
			if reg == nil {
				found[label] = true // nothing registered, so no cause can be shown
				continue
			}
			ref := regexp.MustCompile(`\b` + reg[1] + `\.`)
			ok := false
			for j := to; j < len(lines) && !ok; j++ {
				if ref.MatchString(lines[j]) {
					a, b := taskAt(lines, j)
					ok = receiptMod.MatchString(strings.Join(lines[a:b], "\n"))
				}
			}
			if !ok {
				found[label] = true
			}
		}
	}
	for label := range found {
		if knownNoLog[label] {
			t.Logf("known gap #9: no_log without a receipt: %s", label)
		} else {
			t.Errorf("no_log task without a receipt (a later debug, fail or assert reading its registered result): %s", label)
		}
	}
	for label := range knownNoLog {
		if !found[label] {
			t.Errorf("no_log task %s now has a receipt: delete it from knownNoLog", label)
		}
	}
}

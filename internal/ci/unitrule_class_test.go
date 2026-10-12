package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// unitrule_class_test.go is Glenn's rule for unit tests, whole (2026-10-12):
// "If you want to fix your flaky tests, don't use real time, or sockets or
// external service calls in unit tests." "That's a rule. That's how we write
// unit tests here."
//
// A unit test is any _test.go the unit tier compiles (unitTierFile: no
// `//go:build functional`, slow, perf, nightly or race). It never waits on the
// real clock, never opens a real socket and never calls an external service.
// A test that needs the real thing is a functional test (`//go:build
// functional`); a test about logic uses a fake (a fake clock, net.Pipe, an
// in-process RoundTripper, httptest.NewRecorder, a channel the fake signals).
//
// Two older rules already hold two parts of it under cmd/, internal/, pkg/ and
// tools/: unitwaits (time.Sleep/After/Tick/NewTimer/NewTicker/AfterFunc and a
// context deadline waited on) and unit-sockets (net.Listen/Dial and the
// httptest servers). THIS rule holds the rest, so between them every unit-tier
// test file of the repository is read for every shape:
//
//   - eventually: testify's Eventually, Eventuallyf, EventuallyWithT,
//     EventuallyWithTf, Never and Neverf, which poll on the real clock until
//     a real deadline: the outcome turns on the machine's load;
//   - clock: a testify assertion whose verdict is an elapsed real time:
//     time.Since or time.Until anywhere in its arguments, or time.Now() handed
//     to the assertion itself (time.Now() passed on into the subject is a
//     timestamp the subject is given, and is not one);
//   - deadline: a conn's SetDeadline/SetReadDeadline/SetWriteDeadline at
//     time.Now() plus a bound: a real-time deadline drives the outcome.
//     Inside the function literal handed to synctest.Test or synctest.Run,
//     package time is the fake clock the rule asks for, so eventually, clock
//     and deadline are not read there; the call's other arguments are
//     evaluated before the bubble and are read (a socket or a service in a
//     bubble is still real);
//   - socket: the socket openers unit-sockets does not list (net.ListenUDP,
//     net.DialTCP and their kin, a net.Dialer or net.ListenConfig, http's
//     ListenAndServe and Serve, tls.Dial and tls.Listen, and miniredis.Run,
//     RunT and RunTLS called from a test file, whose client then dials the
//     fake over TCP), and, OUTSIDE cmd/ internal/ pkg/ tools/ (fleet/, hook/,
//     the nested modules), unit-sockets' own list too. The tree's fake store,
//     hook/hooktest.New and pkg/testkit.PipeStore (a miniredis served over
//     net.Pipe, whose cleanup fails the test that made a TCP connection), is
//     the remedy, not an offence: no byte of the test crosses a socket. That
//     shape is read, not named (miniredisTraps): a Cleanup asserts that the same
//     server's TotalConnectionCount is zero or fails on a nonzero count,
//     and the server is closed. A pipe call alone, a count read and
//     discarded, another server's count, or an assertion outside a Cleanup
//     proves nothing and the start is a socket;
//   - wait: OUTSIDE cmd/ internal/ pkg/ tools/, unitwaits' own list;
//   - http: net/http's Get, Head, Post, PostForm, DefaultClient and
//     DefaultTransport: a request on the real network;
//   - service: exec.Command, exec.CommandContext or exec.LookPath of an
//     external service or a network tool (redis-server, postgres, ansible,
//     docker, curl, gh ...);
//   - git-remote: a git command run with a network remote (https://, ssh://,
//     git://, git@).
//
// THE BURN-DOWN. internal/ci/unit-rule_burndown.txt lists the files that
// still break the rule, one counted row per file and kind
// (`<file>:<kind> <sites> <reason>`). It ONLY SHRINKS: a site with no row is
// red, a row whose file has more sites than it lists is red (a listed file is
// not grandfathered: its count is a ceiling, so one more site in it is red), a
// row whose file has fewer is red until its count is lowered and a row whose
// file has none is red until it is deleted (so a fixed file must leave the
// list), and a row or a count the change's base does not have is red
// (TestUnitRuleBurndownOnlyShrinksAgainstTheMergeParent, against ChangeBase:
// the pull request's own base in CI, else the merge base with
// origin/<GITHUB_BASE_REF>, dev when unset), so a change that adds an offender
// and its row together is red. It goes to zero across follow-up changes; it is
// never a place to park a new offender.
//
// THE GAPS, narrow and named. The scan reads the test file's own syntax,
// resolved through its imports, so it does not see: a wait, a socket or a
// deadline inside the subject or a helper in another package (a Timeout field
// on a store, a `Far(...)` that listens); a request sent through a custom
// http.Client's Do (only net/http's package-level Get/Post and the default
// client and transport are read); a fake-clock package imported under the
// name `time`, or a local variable or field named `time`, which shadow the
// package and are read as it (false positives, never a miss); a method value
// or a variable holding time.Sleep, net.Listen or a dialer (unitwaits reads
// the value for its own list); testify's Eventually through a dot import. A
// dot import of time IS read (`Since(start)` under `import . "time"`).
const unitRuleLedgerPath = "unit-rule_burndown.txt"

// unitRuleRemedy is the one thing to do about a site.
const unitRuleRemedy = "a unit test uses no real time, no real socket and no external service: fake it (a fake clock, a channel the fake signals, net.Pipe, an in-process RoundTripper, httptest.NewRecorder, the store through hooktest.New or testkit.PipeStore) when the test is about logic, or move the test under //go:build functional when its subject is the real thing; the burn-down only shrinks"

// unitRuleSite is one site the rule finds in a file.
type unitRuleSite struct {
	Line int
	Kind string
	What string
}

// unitRuleEventually are the testify assertions that poll on the real clock.
var unitRuleEventually = map[string]bool{"Eventually": true, "Eventuallyf": true, "EventuallyWithT": true, "EventuallyWithTf": true, "Never": true, "Neverf": true}

// unitRuleTestify are testify's assertion packages.
var unitRuleTestify = []string{"github.com/stretchr/testify/assert", "github.com/stretchr/testify/require"}

// unitRuleSockets are the socket openers this rule adds to unit-sockets' list,
// by import path. A miniredis a test file starts itself is one: its client
// dials the fake's loopback listener (the fakes over net.Pipe, hooktest.New
// and testkit.PipeStore, are the remedy and are not listed).
var unitRuleSockets = map[string][]string{
	"net":                              {"ListenUDP", "ListenIP", "ListenMulticastUDP", "DialTCP", "DialUDP", "DialUnix", "DialIP"},
	"net/http":                         {"ListenAndServe", "ListenAndServeTLS", "Serve", "ServeTLS"},
	"crypto/tls":                       {"Dial", "DialWithDialer", "Listen"},
	"github.com/alicebob/miniredis/v2": {"Run", "RunT", "RunTLS"},
}

// unitRuleSocketTypes are the types whose value is a socket opener.
var unitRuleSocketTypes = map[string][]string{"net": {"Dialer", "ListenConfig"}}

// unitRuleHTTP are net/http's names that send a request on the real network.
var unitRuleHTTP = map[string]string{"Get": "call", "Head": "call", "Post": "call", "PostForm": "call", "DefaultClient": "value", "DefaultTransport": "value"}

// unitRulePrograms are the external services and network tools a unit test
// never runs.
var unitRulePrograms = map[string]bool{
	"redis-server": true, "redis-cli": true, "postgres": true, "initdb": true, "pg_ctl": true, "psql": true,
	"ansible": true, "ansible-playbook": true, "ansible-inventory": true, "docker": true, "podman": true,
	"curl": true, "wget": true, "gh": true, "nc": true,
}

// unitRuleRemotes are the prefixes of a git remote on the network.
var unitRuleRemotes = []string{"https://", "http://", "ssh://", "git://", "git@"}

// unitRuleDeadlines are the conn methods whose time.Now() argument is a
// real-time deadline.
var unitRuleDeadlines = map[string]bool{"SetDeadline": true, "SetReadDeadline": true, "SetWriteDeadline": true}

// unitRuleOlderRuleDirs are the directories unitwaits and unit-sockets read;
// outside them this rule reads their shapes too.
func unitRuleOlderRuleDirs(rel string) bool {
	for _, d := range unitWaitDirs {
		if strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

// readsWallClock reports whether e calls time.Now, time.Since or time.Until
// through the file's name for package time, at any depth.
func readsWallClock(e ast.Node, timeName string) bool {
	if timeName == "" {
		return false
	}
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && timeCall(call, timeName, "Now", "Since", "Until") {
			found = true
		}
		return !found
	})
	return found
}

// wallClockVerdict reports whether an assertion's arguments make an elapsed
// real time its verdict: time.Since or time.Until anywhere in them (a measured
// real duration), or time.Now() handed to the assertion itself. time.Now()
// passed on into the subject (`run(args, ..., time.Now())`) is a timestamp the
// subject is given, not a verdict, and is not one.
func wallClockVerdict(args []ast.Expr, timeName string) bool {
	if timeName == "" {
		return false
	}
	for _, a := range args {
		if call, ok := a.(*ast.CallExpr); ok && timeCall(call, timeName, "Now") {
			return true
		}
		found := false
		ast.Inspect(a, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && timeCall(call, timeName, "Since", "Until") {
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// synctestBubbles are the ranges of the function literals handed to the
// file's synctest.Test and synctest.Run calls: inside one, package time is the
// bubble's fake clock (the remedy the rule asks for), so a poll, a verdict or a
// deadline on it is not real time. Only the literal's body is the bubble: Go
// evaluates the call's other arguments before it starts, on the real clock, so
// `synctest.Test(t, mk(time.Now()))` reads the real clock, and a named function
// passed instead of a literal is read where it is written.
func synctestBubbles(file *ast.File) [][2]token.Pos {
	name := importName(file, "testing/synctest")
	if name == "" {
		return nil
	}
	var out [][2]token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if pkg, sel := selName(call.Fun); pkg == name && (sel == "Test" || sel == "Run") {
				for _, a := range call.Args {
					if fl, ok := a.(*ast.FuncLit); ok {
						out = append(out, [2]token.Pos{fl.Pos(), fl.End()})
					}
				}
			}
		}
		return true
	})
	return out
}

// timeCall reports whether call is one of names (Now, Since, Until) of package
// time, through the file's name for it; under a dot import the bare call is one.
func timeCall(call *ast.CallExpr, timeName string, names ...string) bool {
	if timeName == "" {
		return false
	}
	var fn string
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		pkg, sel := selName(call.Fun)
		if pkg != timeName {
			return false
		}
		fn = sel
	case *ast.Ident:
		if timeName != "." {
			return false
		}
		fn = f.Name
	default:
		return false
	}
	return slices.Contains(names, fn)
}

// inRanges reports whether n lies inside one of the ranges.
func inRanges(n ast.Node, ranges [][2]token.Pos) bool {
	for _, r := range ranges {
		if n.Pos() >= r[0] && n.End() <= r[1] {
			return true
		}
	}
	return false
}

// miniredisCount is the locally bound server whose count e reads directly.
// Partial type-info bindings distinguish a shadowed variable of the same name.
func miniredisCount(e ast.Expr, info *types.Info) types.Object {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return nil
	}
	recv, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || recv.Sel.Name != "TotalConnectionCount" {
		return nil
	}
	if id, ok := recv.X.(*ast.Ident); ok {
		return info.ObjectOf(id)
	}
	return nil
}

// literalZero is the literal zero used by the narrow no-TCP guard shapes.
func literalZero(e ast.Expr) bool {
	v, ok := e.(*ast.BasicLit)
	return ok && v.Kind == token.INT && v.Value == "0"
}

// nonzeroCountGuard is a direct count != 0 condition, optionally using the
// count bound in this if's initializer. Other comparisons prove no zero bound.
func nonzeroCountGuard(s *ast.IfStmt, info *types.Info) types.Object {
	cond, ok := s.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.NEQ {
		return nil
	}
	count, zero := cond.X, cond.Y
	if literalZero(count) {
		count, zero = zero, count
	}
	if !literalZero(zero) {
		return nil
	}
	if id, ok := count.(*ast.Ident); ok {
		init, ok := s.Init.(*ast.AssignStmt)
		if !ok || len(init.Lhs) != 1 || len(init.Rhs) != 1 {
			return nil
		}
		bound, ok := init.Lhs[0].(*ast.Ident)
		if !ok || info.ObjectOf(id) == nil || info.ObjectOf(id) != info.ObjectOf(bound) {
			return nil
		}
		count = init.Rhs[0]
	}
	return miniredisCount(count, info)
}

// testingFailure is a direct Error/Fatal call on a locally bound *testing.T.
func testingFailure(call *ast.CallExpr, testingParams map[types.Object]bool, info *types.Info) bool {
	recv, method := selName(call.Fun)
	if !slices.Contains([]string{"Error", "Errorf", "Fatal", "Fatalf"}, method) || recv == "" {
		return false
	}
	id := call.Fun.(*ast.SelectorExpr).X.(*ast.Ident)
	return testingParams[info.ObjectOf(id)]
}

// miniredisTraps recognizes a same-server zero-count Cleanup assertion or
// nonzero-count failure guard, plus closure (RunT closes itself). A pipe call
// alone proves no transport ownership. Libraries considered: go/ast inspection,
// go/types partial bindings, and the existing selName/importName helpers; no
// whole-program dataflow or helper-name exemption is inferred.
func miniredisTraps(fset *token.FileSet, file *ast.File, testify map[string]bool) map[*ast.CallExpr]bool {
	name := importName(file, "github.com/alicebob/miniredis/v2")
	if name == "" {
		return nil
	}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}
	config := types.Config{Error: func(error) {}}
	// A single test file and parse-only fixtures need not type-check without their
	// imports and other package files. Keep partial local bindings; an unresolved
	// binding never grants an exemption. No package/import loader is needed.
	_, _ = config.Check(file.Name.Name, fset, []*ast.File{file}, info)
	testingParams := map[types.Object]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if field, ok := n.(*ast.Field); ok {
			if ptr, ok := field.Type.(*ast.StarExpr); ok {
				if pkg, typ := selName(ptr.X); pkg == importName(file, "testing") && pkg != "" && typ == "T" {
					for _, id := range field.Names {
						if obj := info.ObjectOf(id); obj != nil {
							testingParams[obj] = true
						}
					}
				}
			}
		}
		return true
	})
	out := map[*ast.CallExpr]bool{}
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		starts := map[types.Object]*ast.CallExpr{}
		selfClosing := map[types.Object]bool{}
		rebound := map[types.Object]bool{}
		var cleanups []*ast.FuncLit
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range x.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && info.ObjectOf(id) != nil && info.Defs[id] == nil {
						rebound[info.ObjectOf(id)] = true
					}
				}
				if len(x.Lhs) == 0 || len(x.Rhs) != 1 {
					return true
				}
				call, ok := x.Rhs[0].(*ast.CallExpr)
				id, bound := x.Lhs[0].(*ast.Ident)
				if !ok || !bound || info.ObjectOf(id) == nil {
					return true
				}
				if pkg, sel := selName(call.Fun); pkg == name && (sel == "Run" || sel == "RunT" || sel == "RunTLS") {
					starts[info.ObjectOf(id)] = call
					selfClosing[info.ObjectOf(id)] = sel == "RunT"
				}
			case *ast.CallExpr:
				if _, sel := selName(x.Fun); sel == "Cleanup" {
					for _, a := range x.Args {
						if lit, ok := a.(*ast.FuncLit); ok {
							cleanups = append(cleanups, lit)
						}
					}
				}
			}
			return true
		})
		asserted, closed := map[types.Object]bool{}, map[types.Object]bool{}
		for _, cleanup := range cleanups {
			ast.Inspect(cleanup.Body, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false // an uncalled function inside Cleanup proves nothing
				}
				if guard, ok := n.(*ast.IfStmt); ok {
					if server := nonzeroCountGuard(guard, info); server != nil {
						for _, stmt := range guard.Body.List {
							if expr, ok := stmt.(*ast.ExprStmt); ok {
								if call, ok := expr.X.(*ast.CallExpr); ok && testingFailure(call, testingParams, info) {
									asserted[server] = true
								}
							}
						}
					}
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				pkg, sel := selName(call.Fun)
				if recv, ok := call.Fun.(*ast.SelectorExpr); ok && sel == "Close" {
					if id, ok := recv.X.(*ast.Ident); ok && info.ObjectOf(id) != nil {
						closed[info.ObjectOf(id)] = true
					}
				}
				if testify[pkg] && len(call.Args) >= 2 && (sel == "Zero" || sel == "Zerof") {
					asserted[miniredisCount(call.Args[1], info)] = true
				}
				if testify[pkg] && len(call.Args) >= 3 && (sel == "Equal" || sel == "Equalf") {
					if literalZero(call.Args[1]) {
						asserted[miniredisCount(call.Args[2], info)] = true
					} else if literalZero(call.Args[2]) {
						asserted[miniredisCount(call.Args[1], info)] = true
					}
				}
				return true
			})
		}
		for v, call := range starts {
			if !rebound[v] && asserted[v] && (selfClosing[v] || closed[v]) {
				out[call] = true
			}
		}
	}
	return out
}

// unitRuleSitesIn scans one parsed unit-tier test file at rel and returns the
// sites this rule refuses in it.
func unitRuleSitesIn(fset *token.FileSet, rel string, file *ast.File) []unitRuleSite {
	var out []unitRuleSite
	add := func(n ast.Node, kind, what string) {
		out = append(out, unitRuleSite{Line: fset.Position(n.Pos()).Line, Kind: kind, What: what})
	}
	bubbles := synctestBubbles(file)
	inBubble := func(n ast.Node) bool { return inRanges(n, bubbles) }
	testify := map[string]bool{}
	for _, p := range unitRuleTestify {
		if name := importName(file, p); name != "" {
			testify[name] = true
		}
	}
	sockets := map[string][]string{}
	for p, fns := range unitRuleSockets {
		if name := importName(file, p); name != "" {
			sockets[name] = append(sockets[name], fns...)
		}
	}
	socketTypes := map[string][]string{}
	for p, types := range unitRuleSocketTypes {
		if name := importName(file, p); name != "" {
			socketTypes[name] = types
		}
	}
	timeName, httpName, execName := importName(file, "time"), importName(file, "net/http"), importName(file, "os/exec")
	miniredisName := importName(file, "github.com/alicebob/miniredis/v2")
	traps := miniredisTraps(fset, file, testify)
	calls := map[*ast.SelectorExpr]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			calls[sel] = true
			pkg, name := selName(x.Fun)
			switch {
			case pkg != "" && pkg == miniredisName && slices.Contains(sockets[pkg], name) && traps[x]:
				// a trap: its own cleanup guards zero TCP connections (miniredisTraps)
			case unitRuleEventually[sel.Sel.Name] && (testify[pkg] || pkg == "" || !isImportName(file, pkg)):
				// a testify package's function, or a method of an assertion
				// object or a suite: the same poll on the real clock
				if !inBubble(x) {
					add(x, "eventually", exprText(x.Fun))
				}
			case testify[pkg]:
				if wallClockVerdict(x.Args, timeName) && !inBubble(x) {
					add(x, "clock", exprText(x.Fun)+" over the wall clock")
				}
			case unitRuleDeadlines[sel.Sel.Name] && len(x.Args) == 1 && readsWallClock(x.Args[0], timeName):
				if !inBubble(x) {
					add(x, "deadline", exprText(x.Fun)+"(time.Now()...)")
				}
			case pkg != "" && sockets[pkg] != nil && slices.Contains(sockets[pkg], name):
				add(x, "socket", pkg+"."+name)
			case pkg != "" && pkg == httpName && unitRuleHTTP[name] == "call":
				add(x, "http", pkg+"."+name)
			case pkg != "" && pkg == execName && (name == "Command" || name == "CommandContext" || name == "LookPath"):
				args := x.Args
				if name == "CommandContext" && len(args) > 0 {
					args = args[1:]
				}
				if len(args) == 0 {
					return true
				}
				prog, ok := stringLit(args[0])
				if !ok {
					return true
				}
				if unitRulePrograms[filepath.Base(prog)] {
					add(x, "service", pkg+"."+name+"("+strconv.Quote(prog)+")")
				}
				if filepath.Base(prog) == "git" {
					for _, a := range args[1:] {
						if v, ok := stringLit(a); ok && hasAnyPrefix(v, unitRuleRemotes) {
							add(x, "git-remote", "git "+v)
							break
						}
					}
				}
			}
		case *ast.CompositeLit:
			if pkg, name := selName(x.Type); pkg != "" && slices.Contains(socketTypes[pkg], name) {
				add(x, "socket", pkg+"."+name+"{}")
			}
		}
		return true
	})
	// DefaultClient and DefaultTransport are values, read anywhere.
	if httpName != "" {
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || calls[sel] {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == httpName && unitRuleHTTP[sel.Sel.Name] == "value" {
				add(sel, "http", httpName+"."+sel.Sel.Name)
			}
			return true
		})
	}
	if !unitRuleOlderRuleDirs(rel) {
		for _, w := range scanWallClockWaits(fset, rel, file) {
			out = append(out, unitRuleSite{Line: w.Line, Kind: "wait", What: w.What + " in " + w.Func})
		}
		for _, s := range unitSocketsIn(fset, file) {
			out = append(out, unitRuleSite{Line: s.Line, Kind: "socket", What: s.What})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// isImportName reports whether name is the file's name for one of its imports.
func isImportName(file *ast.File, name string) bool {
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if importName(file, p) == name {
			return true
		}
	}
	return false
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// unitRuleTree is every site in every unit-tier test file of the repository,
// keyed `<file>:<kind>`, and how many files were read.
func unitRuleTree(t *testing.T) (map[string][]string, int) {
	t.Helper()
	tree := repoTree(t)
	sites := map[string][]string{}
	files := 0
	for _, f := range tree.Files {
		if !f.Go || !f.Test || f.HasDirNamed("testdata") || f.HasDirNamed("vendor") {
			continue
		}
		require.NoError(t, f.ParseErr, f.Rel)
		if !unitTierFile(f.Src) {
			continue
		}
		files++
		for _, s := range unitRuleSitesIn(tree.FSet, f.Rel, f.AST) {
			key := f.Rel + ":" + s.Kind
			sites[key] = append(sites[key], fmt.Sprintf("%s:%d: %s", f.Rel, s.Line, s.What))
		}
	}
	return sites, files
}

// unitRuleLedgerOptions is the burn-down's shape: counted, ceiling-only.
var unitRuleLedgerOptions = allowlist.Options{Ceiling: true, Counted: true}

// TestNoUnitTestUsesRealTimeSocketsOrServices holds every unit-tier test file
// of the repository to Glenn's rule through the burn-down: a site with no row,
// a row with fewer sites listed than are there, a row with more, and a row
// with none left are each red.
func TestNoUnitTestUsesRealTimeSocketsOrServices(t *testing.T) {
	t.Parallel()

	sites, files := unitRuleTree(t)
	require.NotZerof(t, files, "read no unit-tier test file; the rule would pass by checking nothing")
	list, err := allowlist.Load(unitRuleLedgerPath, unitRuleLedgerOptions)
	require.NoError(t, err)
	measured := map[string]int{}
	total := 0
	for k, w := range sites {
		measured[k] = len(w)
		total += len(w)
	}
	byFile := map[string]bool{}
	for _, row := range list.Rows() {
		byFile[strings.SplitN(row.Key, ":", 2)[0]] = true
	}
	t.Logf("UNIT-RULE unit-tier test files=%d sites=%d keys=%d burn-down rows=%d files=%d (the burn-down only shrinks, to zero)", files, total, len(sites), list.Len(), len(byFile))
	res := allowlist.CheckCountedMode(t, list, measured, allowlist.Updating())
	var problems []string
	for _, k := range res.Unlisted {
		for _, w := range sites[k] {
			problems = append(problems, fmt.Sprintf("UNIT-RULE %s [%s] has no row in internal/ci/%s: %s", w, k, unitRuleLedgerPath, unitRuleRemedy))
		}
	}
	for _, c := range res.Over {
		problems = append(problems, fmt.Sprintf("UNIT-RULE internal/ci/%s lists %s at %d sites, but %d are there now (%s); a new site is not covered by a row written for fewer: %s", unitRuleLedgerPath, c.Key, c.Listed, c.Measured, strings.Join(sites[c.Key], "; "), unitRuleRemedy))
	}
	for _, c := range res.Lowered {
		problems = append(problems, fmt.Sprintf("UNIT-RULE internal/ci/%s lists %s at %d sites, but only %d are there now; lower the row's count to %d (NOVA_CI_UPDATE=1 lowers it)", unitRuleLedgerPath, c.Key, c.Listed, c.Measured, c.Measured))
	}
	for _, row := range res.Stale {
		problems = append(problems, fmt.Sprintf("UNIT-RULE internal/ci/%s lists %s, but that file breaks the rule no more; delete the row (NOVA_CI_UPDATE=1 drops it)", unitRuleLedgerPath, row.Key))
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// unitRuleLedgerRows reads a burn-down's text into its counts by key.
func unitRuleLedgerRows(path, text string) (map[string]int, error) {
	l, err := allowlist.Parse(path, text, unitRuleLedgerOptions)
	if err != nil {
		return nil, err
	}
	rows := map[string]int{}
	for _, row := range l.Rows() {
		rows[row.Key] = l.Count(row.Key)
	}
	return rows, nil
}

// unitRuleGrowth is every key the head burn-down adds over the base's, and
// every count it raises.
func unitRuleGrowth(base, head map[string]int) []string {
	var grew []string
	for k, n := range head {
		was, ok := base[k]
		switch {
		case !ok:
			grew = append(grew, fmt.Sprintf("adds the row %s", k))
		case n > was:
			grew = append(grew, fmt.Sprintf("raises %s from %d sites to %d", k, was, n))
		}
	}
	sort.Strings(grew)
	return grew
}

// TestUnitRuleBurndownOnlyShrinksAgainstTheMergeParent: the burn-down at HEAD
// adds no row and raises no count over the burn-down at the change's base
// (ChangeBase: the pull request's own base in a GitHub Actions run, else the
// merge base of HEAD and origin/<GITHUB_BASE_REF>, dev when unset; the
// comparison the unexecuted-examples list makes), so a change that adds an
// offender and its row together is red, and a pull request against a stream
// branch is held to that stream's list, not dev's. No base at all is a refusal,
// never a pass. A base with no burn-down is the seed: the change that
// introduced it.
func TestUnitRuleBurndownOnlyShrinksAgainstTheMergeParent(t *testing.T) {
	t.Parallel()

	root := repoTree(t).Root
	rel := "internal/ci/" + unitRuleLedgerPath
	base, err := ChangeBase(root, os.Getenv)
	require.NoError(t, err, "cannot verify the burn-down's growth without a base; fetch origin/dev (or the pull request's base)")
	baseText, has, err := ListAtCommit(root, base, rel)
	require.NoError(t, err)
	if !has {
		t.Logf("%s is not in the base %s: this change is the burn-down's seed", rel, base[:9])
		return
	}
	baseRows, err := unitRuleLedgerRows(rel+"@base", baseText)
	require.NoError(t, err)
	headRows, err := unitRuleLedgerRows(rel, readFile(t, filepath.Join(root, filepath.FromSlash(rel))))
	require.NoError(t, err)
	for _, g := range unitRuleGrowth(baseRows, headRows) {
		t.Errorf("%s %s over the change's base %s; the burn-down only shrinks: fix the test (a fake, or //go:build functional) and leave the list alone", rel, g, base[:9])
	}
}

// TestUnitRuleFindsEachShapeAndNotItsFake proves the scan on a known yes and a
// known no: every shape in the offending fixture is found at its line, the same
// tests written with fakes find nothing, a functional-tagged copy is not a unit
// file, the older rules' shapes are read only outside their directories, and
// the burn-down's growth check refuses an added row and a raised count.
func TestUnitRuleFindsEachShapeAndNotItsFake(t *testing.T) {
	t.Parallel()

	const offending = `package p

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	req "github.com/stretchr/testify/require"
)

func TestOffends(t *testing.T) {
	req.Eventually(t, func() bool { return true }, time.Second, time.Millisecond)
	a := assert.New(t)
	a.Never(func() bool { return false }, time.Second, time.Millisecond)
	start := time.Now()
	assert.Less(t, time.Since(start), time.Minute)
	var c net.Conn
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	_, _ = net.DialTCP("tcp", nil, nil)
	d := net.Dialer{Timeout: time.Second}
	_ = http.ListenAndServe("127.0.0.1:0", nil)
	_, _ = tls.Dial("tcp", "127.0.0.1:1", nil)
	mr := miniredis.RunT(t)
	_, _ = http.Get("https://example.invalid")
	cl := http.DefaultClient
	_ = exec.Command("postgres", "--port", "0")
	_ = exec.CommandContext(context.Background(), "/usr/bin/ansible-playbook", "x.yml")
	_, _ = exec.LookPath("docker")
	_ = exec.Command("git", "clone", "https://remote.invalid/x/y.git")
	_, _, _ = d, mr, cl
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	_ = ln
	time.Sleep(time.Millisecond)
	assert.Equal(t, start, time.Now())
	req.Greater(t, time.Until(start.Add(time.Hour)), time.Minute)
}
`
	const fake = `package p

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func run(now time.Time) int { return 0 }

// pipeStore is a trap: the fake is served over net.Pipe, and the cleanup
// proves no TCP connection reached it.
func pipeStore(t *testing.T) *miniredis.Miniredis {
	mr := miniredis.RunT(t)
	t.Cleanup(func() { assert.Zero(t, mr.TotalConnectionCount()) })
	return mr
}

func TestUsesFakes(t *testing.T) {
	c := &fakeClock{now: time.Unix(0, 0)}
	done := make(chan struct{})
	go func() { close(done) }()
	<-done
	assert.Equal(t, time.Unix(0, 0), c.Now())
	client, server := net.Pipe()
	defer server.Close()
	require.NoError(t, client.SetDeadline(c.Now().Add(time.Second)))
	rec := httptest.NewRecorder()
	http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}).ServeHTTP(rec, nil)
	_ = exec.Command("git", "init", t.TempDir())
	_ = exec.Command("git", "-C", t.TempDir(), "log", "--format=https://%H")
	require.Equal(t, 0, run(time.Now()), "a timestamp handed to the subject")
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		require.Eventually(t, func() bool { return true }, time.Second, time.Millisecond)
		assert.Less(t, time.Since(start), time.Minute)
		require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	})
}
`
	scan := func(rel, src string) []string {
		t.Helper()
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, src, 0)
		require.NoError(t, err)
		if !unitTierFile([]byte(src)) {
			return nil
		}
		var got []string
		for _, s := range unitRuleSitesIn(fset, rel, f) {
			got = append(got, fmt.Sprintf("%d %s %s", s.Line, s.Kind, s.What))
		}
		return got
	}
	want := []string{
		"18 eventually req.Eventually",
		"20 eventually a.Never",
		"22 clock assert.Less over the wall clock",
		"24 deadline c.SetReadDeadline(time.Now()...)",
		"25 socket net.DialTCP",
		"26 socket net.Dialer{}",
		"27 socket http.ListenAndServe",
		"28 socket tls.Dial",
		"29 socket miniredis.RunT",
		"30 http http.Get",
		"31 http http.DefaultClient",
		"32 service exec.Command(\"postgres\")",
		"33 service exec.CommandContext(\"/usr/bin/ansible-playbook\")",
		"34 service exec.LookPath(\"docker\")",
		"35 git-remote git https://remote.invalid/x/y.git",
	}
	clocks := []string{"40 clock assert.Equal over the wall clock", "41 clock req.Greater over the wall clock"}
	got := scan("pkg/p/p_test.go", offending)
	assert.Equal(t, append(append([]string{}, want...), clocks...), got, "the offending fixture under pkg/ (unitwaits and unit-sockets hold its sleep and listen):\n%s", strings.Join(got, "\n"))

	outside := scan("fleet/p_test.go", offending)
	assert.Equal(t, append(append(append([]string{}, want...), "37 socket net.Listen", "39 wait time.Sleep in TestOffends"), clocks...), outside, "outside the older rules' directories their shapes are this rule's:\n%s", strings.Join(outside, "\n"))

	assert.Empty(t, scan("pkg/p/fake_test.go", fake), "the same test through fakes breaks no rule: a fake clock, a channel, net.Pipe, a recorder, a timestamp handed to the subject, and a synctest bubble")
	assert.Empty(t, scan("fleet/fake_test.go", fake), "the same test through fakes breaks no rule outside the older rules' directories")
	assert.Empty(t, scan("pkg/p/p_functional_test.go", "//go:build functional\n\n"+offending), "a functional-tagged file is not a unit file")
	for _, program := range []string{"redis-server", "postgres"} {
		src := fmt.Sprintf(`package p
import ("os/exec"; "testing")
func TestService(t *testing.T) {
	_ = exec.Command(%q, "--port", "0")
}`, program)
		assert.Equal(t, []string{fmt.Sprintf("4 service exec.Command(%q)", program)}, scan("pkg/p/service_test.go", src), "the service fixture is parsed, never launched")
	}

	// Only the function literal is the bubble: the call's other arguments are
	// evaluated before it, on the real clock.
	const bubbleArgs = `package p

import (
	"net"
	"testing"
	"testing/synctest"
	"time"

	req "github.com/stretchr/testify/require"
)

func TestArgs(t *testing.T) {
	var c net.Conn
	synctest.Test(t, func() func(*testing.T) { req.Eventually(t, func() bool { return true }, time.Second, time.Millisecond); return func(*testing.T) {} }())
	synctest.Test(t, func() func(*testing.T) { _ = c.SetDeadline(time.Now().Add(time.Second)); return func(*testing.T) {} }())
	synctest.Test(t, func(t *testing.T) { req.Eventually(t, func() bool { return true }, time.Second, time.Millisecond) })
	synctest.Test(t, inBubble)
}

func inBubble(t *testing.T) {}
`
	assert.Equal(t, []string{"14 eventually req.Eventually", "15 deadline c.SetDeadline(time.Now()...)"}, scan("pkg/p/args_test.go", bubbleArgs), "a synctest call's arguments are read; its function literal is not; a named function passed to it is read where it is written")

	// A dot import of time is read.
	const dotTime = `package p

import (
	"testing"
	. "time"

	"github.com/stretchr/testify/assert"
)

func TestDot(t *testing.T) {
	start := Now()
	assert.Less(t, Since(start), Minute)
}
`
	assert.Equal(t, []string{"12 clock assert.Less over the wall clock"}, scan("pkg/p/dot_test.go", dotTime), "time's dot import is read")

	// A miniredis is a trap only on proof: a count read and discarded, another
	// server's count, an assertion outside a Cleanup, and a Run that nothing
	// closes are each a socket. Only a same-server zero guard proves a trap;
	// even a pipe served by the fake needs that proof.
	const notTraps = `package p

import (
	"net"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
)

func discardedCount(t *testing.T) *miniredis.Miniredis {
	mr := miniredis.RunT(t)
	_ = mr.TotalConnectionCount()
	return mr
}

func otherServersCount(t *testing.T) *miniredis.Miniredis {
	mr := miniredis.RunT(t)
	other := miniredis.RunT(t)
	t.Cleanup(func() { assert.Zero(t, other.TotalConnectionCount()) })
	return mr
}

func assertedOutsideCleanup(t *testing.T) *miniredis.Miniredis {
	mr := miniredis.RunT(t)
	assert.Zero(t, mr.TotalConnectionCount())
	return mr
}

func runNeverClosed(t *testing.T) *miniredis.Miniredis {
	mr, _ := miniredis.Run()
	t.Cleanup(func() { assert.Zero(t, mr.TotalConnectionCount()) })
	return mr
}

func pipedClient(t *testing.T) net.Conn {
	mr := miniredis.RunT(t)
	near, far := net.Pipe()
	mr.Server().ServeConn(far)
	return near
}

func runClosedAndAsserted(t *testing.T) *miniredis.Miniredis {
	mr, _ := miniredis.Run()
	t.Cleanup(func() {
		mr.Close()
		assert.Zero(t, mr.TotalConnectionCount(), "a TCP connection reached the fake")
	})
	return mr
}

func positiveCount(t *testing.T) *miniredis.Miniredis {
	mr := miniredis.RunT(t)
	t.Cleanup(func() { assert.NotZero(t, mr.TotalConnectionCount()) })
	return mr
}

func positiveEqual(t *testing.T) *miniredis.Miniredis {
	mr := miniredis.RunT(t)
	t.Cleanup(func() { assert.Equal(t, 1, mr.TotalConnectionCount()) })
	return mr
}

func unusedPipe(t *testing.T) *miniredis.Miniredis {
	mr := miniredis.RunT(t)
	near, far := net.Pipe()
	near.Close()
	far.Close()
	return mr
}

func wrongServerWithPipe(t *testing.T) *miniredis.Miniredis {
	mr := miniredis.RunT(t)
	other := miniredis.RunT(t)
	near, far := net.Pipe()
	other.Server().ServeConn(far)
	t.Cleanup(func() { near.Close(); assert.Zero(t, other.TotalConnectionCount()) })
	return mr
}

func conditionalZeroGuard(t *testing.T) *miniredis.Miniredis {
	mr := miniredis.RunT(t)
	t.Cleanup(func() {
		if got := mr.TotalConnectionCount(); got != 0 {
			t.Errorf("a TCP connection reached the fake: %d", got)
		}
	})
	return mr
}

func zeroEqualGuard(t *testing.T) *miniredis.Miniredis {
	mr := miniredis.RunT(t)
	t.Cleanup(func() { assert.Equal(t, 0, mr.TotalConnectionCount()) })
	return mr
}

func reboundServer(t *testing.T) *miniredis.Miniredis {
	mr := miniredis.RunT(t)
	other := miniredis.RunT(t)
	mr = other
	t.Cleanup(func() { assert.Zero(t, mr.TotalConnectionCount()); assert.Zero(t, other.TotalConnectionCount()) })
	return mr
}
`
	assert.Equal(t, []string{"12 socket miniredis.RunT", "18 socket miniredis.RunT", "25 socket miniredis.RunT", "31 socket miniredis.Run", "37 socket miniredis.RunT", "53 socket miniredis.RunT", "59 socket miniredis.RunT", "65 socket miniredis.RunT", "73 socket miniredis.RunT", "98 socket miniredis.RunT"}, scan("pkg/p/traps_test.go", notTraps), "a miniredis needs a same-server zero-count Cleanup guard and closure; positive assertions and unrelated pipes prove nothing")

	base := map[string]int{"pkg/p/p_test.go:eventually": 2, "pkg/p/q_test.go:socket": 1}
	assert.Empty(t, unitRuleGrowth(base, map[string]int{"pkg/p/p_test.go:eventually": 1}), "a lowered count and a dropped row are the burn-down shrinking")
	assert.Equal(t, []string{"adds the row pkg/p/r_test.go:http", "raises pkg/p/p_test.go:eventually from 2 sites to 3"},
		unitRuleGrowth(base, map[string]int{"pkg/p/p_test.go:eventually": 3, "pkg/p/r_test.go:http": 1}), "an added row and a raised count are growth")
}

// TestImportNameReadsAMajorVersionPath: an unaliased import of a module path
// that ends in a major version is read as the element before it (miniredis,
// not v2), so the scan resolves miniredis.RunT; a plain path is its last
// element, and an alias wins over both.
func TestImportNameReadsAMajorVersionPath(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "miniredis", importPathName("github.com/alicebob/miniredis/v2"))
	// the path's element, not the package clause: go-redis's package is redis,
	// which only an alias or the package clause tells; none of the rules' lists
	// names a package whose name differs from its path element
	assert.Equal(t, "go-redis", importPathName("github.com/redis/go-redis/v9"))
	assert.Equal(t, "yaml.v3", importPathName("gopkg.in/yaml.v3"), "gopkg.in spells the version in the element; the package clause says yaml")
	assert.Equal(t, "assert", importPathName("github.com/stretchr/testify/assert"))
	assert.Equal(t, "v2", importPathName("v2"), "a bare element has nothing before it")

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p_test.go", "package p\n\nimport (\n\t\"github.com/alicebob/miniredis/v2\"\n\tmr \"github.com/alicebob/miniredis/v2/server\"\n)\n", 0)
	require.NoError(t, err)
	assert.Equal(t, "miniredis", importName(f, "github.com/alicebob/miniredis/v2"))
	assert.Equal(t, "mr", importName(f, "github.com/alicebob/miniredis/v2/server"), "the alias wins")
	assert.Equal(t, "", importName(f, "net"), "not imported")
}

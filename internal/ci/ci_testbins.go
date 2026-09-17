package ci

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ci_testbins.go is the machine behind the `testbins` class test in
// docs/SPEC-CI.md. It reads every _test.go under internal/ and cmd/ as text and
// refuses one shape with the file and the line: an os.WriteFile (or an io.Copy
// into an os.Create/os.OpenFile) whose mode literal carries an execute bit and
// whose data came from os.ReadFile. That shape copies a compiled executable
// into a fixture. On macOS every fresh copy of a binary is a never-seen file
// the system policy scanner assesses on first exec, so a package run that
// copies one built helper into dozens of fixtures queues them behind the
// scanner for longer than a test waits (internal/swarm's
// TestBatchAbstainNamesReason/result-after-deadline, 2026-09-17, went from FAIL
// at 74 s to ok at 30 s when the fixture hard-linked instead of copying). The
// allowed shape is internal/testbin.Place, which links first and copies only
// where a link is impossible. A shell script written 0o755 is NOT the shape:
// the interpreter is the executable and its bytes are never scanned. It writes
// nothing. Its only input besides the tree is an allowlist of existing
// offenders, each with the file, the line and the kind; an entry that names no
// offender is refused, so the file only ever shrinks, and matching is by file
// and kind and never by line -- see matchWaitAllow for why a line-keyed list
// turned dev red the moment a merge shifted the lines.

// TestbinVerbLine is the help line the class test is entered under, word for
// word as docs/SPEC-CI.md prints it.
const TestbinVerbLine = "testbins   read every _test.go on the CI path; refuse copying a built executable into a fixture"

const (
	// TestbinRemedyCopy is the one thing to do about a copied built binary.
	TestbinRemedyCopy = "place built binaries with testbin.Place: link, never copy"
	// TestbinRemedyAllow is the one thing to do about an allowlist entry that
	// names no offender: the file only shrinks.
	TestbinRemedyAllow = "fix the copy; the allowlist only shrinks"
)

// TestbinFinding is one copied built binary, with its file, line, kind and the
// one thing to do about it. Kind is always "copy" for an offender; "allowlist"
// marks a stale allowlist entry.
type TestbinFinding struct {
	File   string
	Line   int
	Kind   string
	Remedy string
}

// Render is the one-line refusal for this finding.
func (f TestbinFinding) Render() string {
	return fmt.Sprintf("CI-TESTBIN file=%s line=%d kind=%s remedy=%q", f.File, f.Line, f.Kind, f.Remedy)
}

// TestbinsResult is one run of the checker: the tests read, the allowlist
// entries still honored, the copies left, and the allowlist entries that name
// no offender.
type TestbinsResult struct {
	Tests       int
	Allowlisted int
	Findings    []TestbinFinding
	Stale       []TestbinFinding
}

// Refused is the number of lines the run would print: offenders plus stale
// allowlist entries. The count is the truth about the CI path whether or not
// the lines printed.
func (r TestbinsResult) Refused() int { return len(r.Findings) + len(r.Stale) }

// OKLine is the one line a clean run prints.
func (r TestbinsResult) OKLine() string {
	return fmt.Sprintf("CI-TESTBIN OK tests=%d allowlisted=%d refused=0", r.Tests, r.Allowlisted)
}

// FailLine closes a refusal with the counts.
func (r TestbinsResult) FailLine() string {
	return fmt.Sprintf("CI-TESTBIN FAIL tests=%d allowlisted=%d refused=%d", r.Tests, r.Allowlisted, r.Refused())
}

// ExitCode is the status the check would exit with: 2 when anything is
// refused, 0 when the path is clean.
func (r TestbinsResult) ExitCode() int {
	if r.Refused() > 0 {
		return 2
	}
	return 0
}

// checkTestbinDirs are the trees the CI path runs: every _test.go under
// internal/ and cmd/.
var checkTestbinDirs = []string{"internal", "cmd"}

// CheckTestbins reads every _test.go under root/internal and root/cmd and
// returns the copied built binaries, the allowlist entries honored, and any
// allowlist entry that names no offender. The tree comes from the caller,
// never from a walk of the repository; testdata directories are skipped so the
// fixtures are never read as offenders.
func CheckTestbins(root, allowlistPath string) (TestbinsResult, error) {
	var res TestbinsResult
	entries, err := readTestbinAllowlist(allowlistPath)
	if err != nil {
		return res, err
	}
	matched := make([]bool, len(entries))

	for _, dir := range checkTestbinDirs {
		base := filepath.Join(root, dir)
		if _, statErr := os.Stat(base); statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return res, statErr
		}
		err = filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
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
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			res.Tests++
			findings, ok := scanTestbinFile(rel, raw)
			if !ok {
				return nil
			}
			res.Findings = append(res.Findings, findings...)
			return nil
		})
		if err != nil {
			return res, err
		}
	}

	var remaining []TestbinFinding
	for _, f := range res.Findings {
		if i := matchTestbinAllow(entries, matched, f); i >= 0 {
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
		res.Stale = append(res.Stale, TestbinFinding{File: e.file, Line: e.line, Kind: "allowlist", Remedy: TestbinRemedyAllow})
	}
	return res, nil
}

// testbinAllow is one allowlist row: the offender it names. The date and the
// reason that follow are for a reader and are not matched on.
type testbinAllow struct {
	file string
	line int
	kind string
}

// readTestbinAllowlist parses `file:line kind date reason` rows, ignoring blank
// lines and # comments. A missing file is an empty allowlist, never an error:
// a tree with nothing parked in it is the goal.
func readTestbinAllowlist(path string) ([]testbinAllow, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []testbinAllow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		colon := strings.LastIndex(fields[0], ":")
		if colon < 0 {
			continue
		}
		n, convErr := strconv.Atoi(fields[0][colon+1:])
		if convErr != nil {
			continue
		}
		out = append(out, testbinAllow{file: fields[0][:colon], line: n, kind: fields[1]})
	}
	return out, sc.Err()
}

// matchTestbinAllow returns the index of an unused entry that allows this
// finding, or -1.
//
// A row allows ONE offender of its kind in its file, the way matchWaitAllow
// does. The line in the row is where the offender stood when the row was
// written, for a reader; it is not matched on, because a merge that shifts the
// lines of a listed file must not turn dev red (2026-09-17, #1073). An exact
// line match is preferred so the stale row reported is the one a reader
// expects.
func matchTestbinAllow(entries []testbinAllow, used []bool, f TestbinFinding) int {
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

// scanTestbinFile parses one _test.go and returns its copied-built-binary
// findings. The second result is false when the file does not parse: a file
// that is not Go cannot carry the shapes this check reads, and a fixture
// deliberately holding a broken literal is not the offender itself.
func scanTestbinFile(rel string, src []byte) ([]TestbinFinding, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, false
	}
	tainted := readByteVars(file)
	execFiles := execFileVars(file)

	var out []TestbinFinding
	seen := map[string]bool{}
	add := func(n ast.Node, kind, remedy string) {
		pos := fset.Position(n.Pos())
		key := strconv.Itoa(pos.Line) + ":" + kind
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, TestbinFinding{File: rel, Line: pos.Line, Kind: kind, Remedy: remedy})
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		pkg, sel := selName(call.Fun)
		switch {
		case pkg == "os" && sel == "WriteFile" && len(call.Args) == 3:
			if modeHasExec(call.Args[2]) && taintedExpr(call.Args[1], tainted) {
				add(call, "copy", TestbinRemedyCopy)
			}
		case pkg == "io" && sel == "Copy" && len(call.Args) == 2:
			if isExecFile(call.Args[0], execFiles) && taintedExpr(call.Args[1], tainted) {
				add(call, "copy", TestbinRemedyCopy)
			}
		}
		return true
	})
	return out, true
}

// readByteVars collects the identifiers a file fills from os.ReadFile, so a
// WriteFile of one is a copy of bytes read off disk. A package-level map such
// as nova-wake's fakeBins is filled one element at a time (`bins[name] = raw`)
// and is tainted too, because the copy happens at the write, in another
// function. Propagation is iterated to a fixed point so a chain of assignments
// carries the taint to its end.
func readByteVars(file *ast.File) map[string]bool {
	tainted := map[string]bool{}
	for changed := true; changed; {
		changed = false
		ast.Inspect(file, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range s.Lhs {
					if i >= len(s.Rhs) || !taintedExpr(s.Rhs[i], tainted) {
						continue
					}
					if taintLHS(lhs, tainted) {
						changed = true
					}
				}
			case *ast.ValueSpec:
				for i, name := range s.Names {
					if i >= len(s.Values) || !taintedExpr(s.Values[i], tainted) {
						continue
					}
					if !tainted[name.Name] {
						tainted[name.Name] = true
						changed = true
					}
				}
			}
			return true
		})
	}
	return tainted
}

// execFileVars collects the identifiers a file opens with an execute bit
// through os.Create or os.OpenFile, so an io.Copy into one is a copy to an
// executable destination.
func execFileVars(file *ast.File) map[string]bool {
	vars := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		s, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range s.Lhs {
			if i >= len(s.Rhs) {
				continue
			}
			id, ok := lhs.(*ast.Ident)
			if !ok {
				continue
			}
			call, ok := s.Rhs[i].(*ast.CallExpr)
			if !ok {
				continue
			}
			pkg, sel := selName(call.Fun)
			switch {
			case pkg == "os" && sel == "Create":
				vars[id.Name] = true
			case pkg == "os" && sel == "OpenFile" && len(call.Args) == 3:
				if modeHasExec(call.Args[2]) {
					vars[id.Name] = true
				}
			}
		}
		return true
	})
	return vars
}

// taintedExpr reports whether an expression is bytes read from an os.ReadFile
// or an identifier a file filled from one. An index into a tainted map or
// slice is tainted too; the bytes are the same whatever key names them.
func taintedExpr(e ast.Expr, tainted map[string]bool) bool {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return taintedExpr(x.X, tainted)
	case *ast.CallExpr:
		pkg, sel := selName(x.Fun)
		return pkg == "os" && sel == "ReadFile"
	case *ast.Ident:
		return tainted[x.Name]
	case *ast.IndexExpr:
		return taintedExpr(x.X, tainted)
	}
	return false
}

// taintLHS marks the identifier on the left of an assignment tainted, or the
// base identifier of a map or slice element assignment. It returns true when
// it added a name, so the fixed-point loop knows to run again.
func taintLHS(lhs ast.Expr, tainted map[string]bool) bool {
	switch x := lhs.(type) {
	case *ast.Ident:
		if tainted[x.Name] {
			return false
		}
		tainted[x.Name] = true
		return true
	case *ast.IndexExpr:
		return taintLHS(x.X, tainted)
	}
	return false
}

// isExecFile reports whether an expression is a file handle opened with an
// execute bit.
func isExecFile(e ast.Expr, execFiles map[string]bool) bool {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return isExecFile(x.X, execFiles)
	case *ast.Ident:
		return execFiles[x.Name]
	}
	return false
}

// modeHasExec reports whether a mode expression is an integer literal with any
// execute bit set -- 0o755, 0o775, 0o700 and the like. A non-literal mode is
// unknowable and is not guessed at.
func modeHasExec(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return modeHasExec(x.X)
	case *ast.BasicLit:
		if x.Kind != token.INT {
			return false
		}
		n, err := strconv.ParseInt(x.Value, 0, 64)
		if err != nil {
			return false
		}
		return n&0o111 != 0
	}
	return false
}

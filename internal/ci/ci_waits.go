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
	"time"
)

// ci_waits.go is the machine behind the `waits` class test in
// docs/SPEC-CI.md. It reads every _test.go under internal/ and cmd/ as text
// and refuses a fixed wall-clock wait: a time.Sleep whose constant is over
// 100 ms, a context or timer bound under ten seconds used as a pass/fail
// condition, and an assertion on elapsed time. A fixed wait asserts the
// machine's load rather than the code, which is how a card that cut waits to
// fit the two-minute gate made TestTheLaunchIsATransaction flaky under load.
// The allowed shape is a poll for the event up to a generous bound read from
// NOVA_TEST_WAIT (default 30s), and a fake clock where the code under test
// needs one. It writes nothing. Its only input besides the tree is an
// allowlist of existing offenders, each with the file, the line, the kind, a
// date and a reason; an entry that names no offender is a place to park a
// wait and is refused, so the file only ever shrinks.

// WaitsVerbLine is the help line the class test is entered under, word for
// word as docs/SPEC-CI.md prints it.
const WaitsVerbLine = "waits   read every _test.go on the CI path; refuse a fixed wall-clock wait or bound"

const (
	// WaitRemedySleep is the one thing to do about a fixed sleep.
	WaitRemedySleep = "poll for the event up to NOVA_TEST_WAIT, not a fixed sleep"
	// WaitRemedyBound is the one thing to do about a short context or timer bound.
	WaitRemedyBound = "raise the bound to thirty seconds or more, or inject a fake clock"
	// WaitRemedyElapsed is the one thing to do about an elapsed-time assertion.
	WaitRemedyElapsed = "assert the event, not the clock; use a fake clock if the code needs one"
	// WaitRemedyAllow is the one thing to do about an allowlist entry that
	// names no offender: the file only shrinks.
	WaitRemedyAllow = "fix the wait; the allowlist only shrinks"
)

// WaitFinding is one fixed wait, with its file, line, kind and the one thing
// to do about it. Kind is "sleep", "bound", "elapsed", or "allowlist" for a
// stale entry.
type WaitFinding struct {
	File   string
	Line   int
	Kind   string
	Remedy string
}

// Render is the one-line refusal for this finding.
func (f WaitFinding) Render() string {
	return fmt.Sprintf("CI-WAITS file=%s line=%d kind=%s remedy=%q", f.File, f.Line, f.Kind, f.Remedy)
}

// WaitsResult is one run of the checker: the tests read, the allowlist entries
// still honored, the fixed waits left, and the allowlist entries that name no
// offender.
type WaitsResult struct {
	Tests       int
	Allowlisted int
	Findings    []WaitFinding
	Stale       []WaitFinding
}

// Refused is the number of lines the run would print: offenders plus stale
// allowlist entries. The count is the truth about the CI path whether or not
// the lines printed.
func (r WaitsResult) Refused() int { return len(r.Findings) + len(r.Stale) }

// OKLine is the one line a clean run prints.
func (r WaitsResult) OKLine() string {
	return fmt.Sprintf("CI-WAITS OK tests=%d allowlisted=%d refused=0", r.Tests, r.Allowlisted)
}

// FailLine closes a refusal with the counts.
func (r WaitsResult) FailLine() string {
	return fmt.Sprintf("CI-WAITS FAIL tests=%d allowlisted=%d refused=%d", r.Tests, r.Allowlisted, r.Refused())
}

// ExitCode is the status the check would exit with: 2 when anything is
// refused, 0 when the path is clean.
func (r WaitsResult) ExitCode() int {
	if r.Refused() > 0 {
		return 2
	}
	return 0
}

// checkWaitsDirs are the trees the CI path runs: every _test.go under internal/
// and cmd/.
var checkWaitsDirs = []string{"internal", "cmd"}

// CheckWaits reads every _test.go under root/internal and root/cmd and returns
// the fixed waits, the allowlist entries honored, and any allowlist entry that
// names no offender. The tree comes from the caller, never from a walk of the
// repository; testdata directories are skipped so the fixtures are never read
// as offenders.
func CheckWaits(root, allowlistPath string) (WaitsResult, error) {
	var res WaitsResult
	entries, err := readWaitAllowlist(allowlistPath)
	if err != nil {
		return res, err
	}
	matched := make([]bool, len(entries))

	for _, dir := range checkWaitsDirs {
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
			findings, ok := scanWaitFile(rel, raw)
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

	var remaining []WaitFinding
	for _, f := range res.Findings {
		if i := matchWaitAllow(entries, f); i >= 0 {
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
		res.Stale = append(res.Stale, WaitFinding{File: e.file, Line: e.line, Kind: "allowlist", Remedy: WaitRemedyAllow})
	}
	return res, nil
}

// waitAllow is one allowlist row: the offender it names. The date and the
// reason that follow are for a reader and are not matched on.
type waitAllow struct {
	file string
	line int
	kind string
}

// readWaitAllowlist parses `file:line kind date reason` rows, ignoring blank
// lines and # comments. A missing file is an empty allowlist, never an error:
// a tree with nothing parked in it is the goal.
func readWaitAllowlist(path string) ([]waitAllow, error) {
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
	var out []waitAllow
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
		out = append(out, waitAllow{file: fields[0][:colon], line: n, kind: fields[1]})
	}
	return out, sc.Err()
}

// matchWaitAllow returns the index of the entry naming this finding, or -1.
func matchWaitAllow(entries []waitAllow, f WaitFinding) int {
	for i, e := range entries {
		if e.file == f.File && e.line == f.Line && e.kind == f.Kind {
			return i
		}
	}
	return -1
}

// scanWaitFile parses one _test.go and returns its fixed-wait findings. The
// second result is false when the file does not parse: a file that is not Go
// cannot carry the shapes this check reads, and a fixture deliberately holding
// a broken literal is not the offender itself.
func scanWaitFile(rel string, src []byte) ([]WaitFinding, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, false
	}
	since := sinceVars(file)
	var out []WaitFinding
	seen := map[string]bool{}
	add := func(n ast.Node, kind, remedy string) {
		pos := fset.Position(n.Pos())
		key := strconv.Itoa(pos.Line) + ":" + kind
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, WaitFinding{File: rel, Line: pos.Line, Kind: kind, Remedy: remedy})
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.CallExpr:
			pkg, sel := selName(e.Fun)
			switch {
			case pkg == "time" && sel == "Sleep" && len(e.Args) == 1:
				if d, ok := constDuration(e.Args[0]); ok && d > 100*time.Millisecond {
					add(e, "sleep", WaitRemedySleep)
				}
			case pkg == "context" && sel == "WithTimeout" && len(e.Args) == 2:
				if d, ok := constDuration(e.Args[1]); ok && d < 10*time.Second {
					add(e, "bound", WaitRemedyBound)
				}
			case pkg == "context" && sel == "WithDeadline" && len(e.Args) == 2:
				if d, ok := constDuration(absoluteDuration(e.Args[1])); ok && d < 10*time.Second {
					add(e, "bound", WaitRemedyBound)
				}
			case pkg == "time" && (sel == "After" || sel == "NewTimer") && len(e.Args) == 1:
				if d, ok := constDuration(e.Args[0]); ok && d < 10*time.Second {
					add(e, "bound", WaitRemedyBound)
				}
			}
		case *ast.BinaryExpr:
			if isComparison(e.Op) && (elapsedOperand(e.X, since) || elapsedOperand(e.Y, since)) {
				add(e, "elapsed", WaitRemedyElapsed)
			}
		case *ast.CompositeLit:
			for _, elt := range e.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || (key.Name != "Deadline" && key.Name != "Idle" && key.Name != "deadline" && key.Name != "idle") {
					continue
				}
				if d, ok := constDuration(kv.Value); ok && d < 10*time.Second {
					add(kv, "bound", WaitRemedyBound)
				}
			}
		}
		return true
	})
	return out, true
}

// selName returns the package identifier and the selector of a call such as
// time.Sleep or context.WithTimeout, or two empty strings for any other call.
func selName(fun ast.Expr) (string, string) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", ""
	}
	return id.Name, sel.Sel.Name
}

// isComparison reports whether op compares two values.
func isComparison(op token.Token) bool {
	switch op {
	case token.LSS, token.GTR, token.LEQ, token.GEQ, token.EQL, token.NEQ:
		return true
	}
	return false
}

// elapsedOperand reports whether an expression is a wall-clock measurement
// used as an assertion: a time.Since call, or an identifier a file assigns one
// to.
func elapsedOperand(e ast.Expr, since map[string]bool) bool {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return elapsedOperand(x.X, since)
	case *ast.CallExpr:
		pkg, sel := selName(x.Fun)
		return pkg == "time" && sel == "Since"
	case *ast.Ident:
		return since[x.Name]
	}
	return false
}

// sinceVars collects the identifiers a file assigns a time.Since result to, so
// `took := time.Since(start)` makes a later `took` a wall-clock measurement.
func sinceVars(file *ast.File) map[string]bool {
	vars := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || i >= len(s.Rhs) {
					continue
				}
				if elapsedOperand(s.Rhs[i], vars) {
					vars[id.Name] = true
				}
			}
		case *ast.ValueSpec:
			for i, name := range s.Names {
				if i >= len(s.Values) {
					continue
				}
				if elapsedOperand(s.Values[i], vars) {
					vars[name.Name] = true
				}
			}
		}
		return true
	})
	return vars
}

// absoluteDuration returns the duration inside a time.Time expression, for a
// context.WithDeadline bound written as time.Now().Add(d).
func absoluteDuration(e ast.Expr) ast.Expr {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return e
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Add" || len(call.Args) != 1 {
		return e
	}
	return call.Args[0]
}

// timeUnit is one time.Duration constant, in nanoseconds.
var timeUnits = map[string]time.Duration{
	"Nanosecond":  time.Nanosecond,
	"Microsecond": time.Microsecond,
	"Millisecond": time.Millisecond,
	"Second":      time.Second,
	"Minute":      time.Minute,
	"Hour":        time.Hour,
}

// constDuration evaluates a duration expression made only of integer literals
// and time unit constants -- `150 * time.Millisecond`, `time.Second`, `30 *
// time.Minute`. It returns false for anything it cannot see whole, such as a
// variable read from the environment: an unknowable bound is the allowed poll,
// never a guessed refusal.
func constDuration(e ast.Expr) (time.Duration, bool) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return constDuration(x.X)
	case *ast.BasicLit:
		if x.Kind != token.INT {
			return 0, false
		}
		n, err := strconv.ParseInt(x.Value, 0, 64)
		if err != nil {
			return 0, false
		}
		return time.Duration(n), true
	case *ast.SelectorExpr:
		return unitDuration(x)
	case *ast.BinaryExpr:
		if x.Op != token.MUL {
			return 0, false
		}
		if n, ok := constInt(x.X); ok {
			if u, ok := constDuration(x.Y); ok {
				return time.Duration(n) * u, true
			}
		}
		if n, ok := constInt(x.Y); ok {
			if u, ok := constDuration(x.X); ok {
				return time.Duration(n) * u, true
			}
		}
	}
	return 0, false
}

// constInt evaluates an integer literal.
func constInt(e ast.Expr) (int64, bool) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return constInt(x.X)
	case *ast.BasicLit:
		if x.Kind != token.INT {
			return 0, false
		}
		n, err := strconv.ParseInt(x.Value, 0, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// unitDuration evaluates a time unit selector such as time.Second.
func unitDuration(e *ast.SelectorExpr) (time.Duration, bool) {
	id, ok := e.X.(*ast.Ident)
	if !ok || id.Name != "time" {
		return 0, false
	}
	u, ok := timeUnits[e.Sel.Name]
	return u, ok
}

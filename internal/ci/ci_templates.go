package ci

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ci_templates.go is the machine behind the `templates` class test in
// docs/SPEC-CI.md. It reads every _test.go under a tree as text and refuses a
// filesystem path concatenated unquoted into a JSON or text/template literal:
// a backslash in a Windows path begins an escape the literal's grammar does
// not have, so the file parses on Linux and darwin and fails on Windows
// (#904, #920). The allowed shape is strconv.Quote(path) -- or oneline.Quote,
// its wrapper -- whose escaped output is a string on every platform. It
// writes nothing. Its only input besides the tree is an allowlist of existing
// offenders, each with the file, the line, a reason and the date; an entry
// that names no offender is a place to park a path and is refused, so the
// file only ever shrinks.

// TemplatesVerbLine is the help line the class test is entered under, word for
// word as docs/SPEC-CI.md prints it.
const TemplatesVerbLine = "templates   read every _test.go; refuse a filesystem path unquoted in a JSON or template literal"

const (
	// TemplateRemedyJoin is the one thing to do about a raw filepath.Join.
	TemplateRemedyJoin = "wrap the path in strconv.Quote"
	// TemplateRemedyLiteral is the one thing to do about an OS path literal.
	TemplateRemedyLiteral = "wrap the path in strconv.Quote; a backslash there is an escape the literal does not have"
	// TemplateRemedyAllow is the one thing to do about an allowlist entry that
	// names no offender: the file only shrinks.
	TemplateRemedyAllow = "quote the path; the allowlist only shrinks"
)

// TemplateFinding is one unquoted path, with its file, line, kind and the one
// thing to do about it.
type TemplateFinding struct {
	File   string
	Line   int
	Kind   string // "join", "literal", or "allowlist" for a stale entry
	Remedy string
}

// Line renders the one-line refusal for this finding.
func (f TemplateFinding) Render() string {
	return fmt.Sprintf("CI-TEMPLATES file=%s line=%d kind=%s remedy=%q", f.File, f.Line, f.Kind, f.Remedy)
}

// TemplatesResult is one run of the checker: the tests read, the allowlist
// entries still honored, the offenders left, and the allowlist entries that
// name no offender.
type TemplatesResult struct {
	Tests       int
	Allowlisted int
	Findings    []TemplateFinding
	Stale       []TemplateFinding
}

// Refused is the number of lines the run would print: offenders plus stale
// allowlist entries. The count is the truth about the tree whether or not the
// lines printed.
func (r TemplatesResult) Refused() int { return len(r.Findings) + len(r.Stale) }

// OKLine is the one line a clean run prints.
func (r TemplatesResult) OKLine() string {
	return fmt.Sprintf("CI-TEMPLATES OK tests=%d allowlisted=%d refused=0", r.Tests, r.Allowlisted)
}

// FailLine closes a refusal with the counts.
func (r TemplatesResult) FailLine() string {
	return fmt.Sprintf("CI-TEMPLATES FAIL tests=%d allowlisted=%d refused=%d", r.Tests, r.Allowlisted, r.Refused())
}

// ExitCode is the status the check would exit with: 2 when anything is
// refused, 0 when the tree is clean.
func (r TemplatesResult) ExitCode() int {
	if r.Refused() > 0 {
		return 2
	}
	return 0
}

// CheckTemplates reads every _test.go under root and returns the offenders,
// the allowlist entries honored, and any allowlist entry that names no
// offender. The tree comes from the caller, never from a walk of the
// repository; testdata directories are skipped so the fixtures are never read
// as offenders.
func CheckTemplates(root, allowlistPath string) (TemplatesResult, error) {
	var res TemplatesResult
	entries, err := readTemplateAllowlist(allowlistPath)
	if err != nil {
		return res, err
	}
	matched := make([]bool, len(entries))

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
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
		findings, ok := scanTemplateFile(rel, raw)
		if !ok {
			return nil
		}
		res.Findings = append(res.Findings, findings...)
		return nil
	})
	if err != nil {
		return res, err
	}

	var remaining []TemplateFinding
	for _, f := range res.Findings {
		if i := matchTemplateAllow(entries, f); i >= 0 {
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
		res.Stale = append(res.Stale, TemplateFinding{File: e.file, Line: e.line, Kind: "allowlist", Remedy: TemplateRemedyAllow})
	}
	return res, nil
}

// templateAllow is one allowlist row: the offender it names. The reason and the
// date that follow are for a reader and are not matched on.
type templateAllow struct {
	file string
	line int
	kind string
}

// readTemplateAllowlist parses `file:line kind date reason` rows, ignoring
// blank lines and # comments. A missing file is an empty allowlist, never an
// error: a tree with nothing parked in it is the goal.
func readTemplateAllowlist(path string) ([]templateAllow, error) {
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
	var out []templateAllow
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
		out = append(out, templateAllow{file: fields[0][:colon], line: n, kind: fields[1]})
	}
	return out, sc.Err()
}

// matchTemplateAllow returns the index of the entry naming this finding, or -1.
func matchTemplateAllow(entries []templateAllow, f TemplateFinding) int {
	for i, e := range entries {
		if e.file == f.File && e.line == f.Line && e.kind == f.Kind {
			return i
		}
	}
	return -1
}

// scanTemplateFile parses one _test.go and returns its unquoted-path findings.
// The second result is false when the file does not parse: a file that is not
// Go cannot carry the shape this check reads, and a fixture deliberately
// holding a broken literal is not the offender itself.
func scanTemplateFile(rel string, src []byte) ([]TemplateFinding, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		return nil, false
	}
	vars := templatePathVars(file)
	var out []TemplateFinding
	seen := map[string]bool{}
	add := func(n ast.Node, kind, remedy string) {
		pos := fset.Position(n.Pos())
		key := strconv.Itoa(pos.Line) + ":" + kind
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, TemplateFinding{File: rel, Line: pos.Line, Kind: kind, Remedy: remedy})
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.BinaryExpr:
			if e.Op != token.ADD {
				return true
			}
			operands := flattenAdds(e)
			// A JSON or template literal anywhere in the concatenation makes
			// the whole expression a literal context.
			context := false
			for _, op := range operands {
				if lit, ok := op.(*ast.BasicLit); ok && lit.Kind == token.STRING && templateLiteral(lit.Value) {
					context = true
				}
			}
			if !context {
				return true
			}
			for _, op := range operands {
				if quoteWrapped(op) {
					continue
				}
				kind, ok := templatePathKind(op, vars)
				if !ok {
					continue
				}
				remedy := TemplateRemedyLiteral
				if kind == "join" {
					remedy = TemplateRemedyJoin
				}
				add(op, kind, remedy)
			}
		case *ast.BasicLit:
			if e.Kind == token.STRING && templateLiteral(e.Value) && containsOSPath(litValue(e.Value)) {
				add(e, "literal", TemplateRemedyLiteral)
			}
		}
		return true
	})
	return out, true
}

// flattenAdds splits an addition chain into its operands.
func flattenAdds(e ast.Expr) []ast.Expr {
	if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
		return append(flattenAdds(b.X), flattenAdds(b.Y)...)
	}
	return []ast.Expr{e}
}

// templatePathVars collects the identifiers a file assigns a filesystem path
// to, so `p := filepath.Join(dir, "key")` makes a later `p` a path.
func templatePathVars(file *ast.File) map[string]string {
	vars := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok || i >= len(s.Rhs) {
					continue
				}
				if k, ok := templatePathKind(s.Rhs[i], vars); ok {
					if _, exists := vars[id.Name]; !exists {
						vars[id.Name] = k
					}
				}
			}
		case *ast.ValueSpec:
			for i, name := range s.Names {
				if i >= len(s.Values) {
					continue
				}
				if k, ok := templatePathKind(s.Values[i], vars); ok {
					if _, exists := vars[name.Name]; !exists {
						vars[name.Name] = k
					}
				}
			}
		}
		return true
	})
	return vars
}

// templatePathKind classifies an expression that holds a filesystem path and
// returns its kind: "join" for a filepath.Join (bare or through ToSlash), and
// "literal" for a Windows path literal, os.Getenv, or a variable holding
// either.
func templatePathKind(e ast.Expr, vars map[string]string) (string, bool) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return templatePathKind(x.X, vars)
	case *ast.Ident:
		if k, ok := vars[x.Name]; ok {
			return k, true
		}
		return "", false
	case *ast.BasicLit:
		if x.Kind == token.STRING && containsOSPath(litValue(x.Value)) {
			return "literal", true
		}
		return "", false
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		switch {
		case (id.Name == "filepath" || id.Name == "path") && sel.Sel.Name == "Join":
			return "join", true
		case (id.Name == "filepath" || id.Name == "path") && sel.Sel.Name == "ToSlash" && len(x.Args) == 1:
			return templatePathKind(x.Args[0], vars)
		case id.Name == "os" && sel.Sel.Name == "Getenv":
			return "literal", true
		}
	}
	return "", false
}

// quoteWrapped reports whether the expression is strconv.Quote or
// oneline.Quote, the allowed shape.
func quoteWrapped(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return (id.Name == "strconv" || id.Name == "oneline") && (sel.Sel.Name == "Quote" || sel.Sel.Name == "QuoteToASCII")
}

// jsonKeyRe matches a JSON object key and its colon: `"name":`. It is what
// tells a JSON literal from a shell script full of `${...}` and `"`, which the
// looser `{` `:` `"` test could not.
var jsonKeyRe = regexp.MustCompile(`"[A-Za-z_][A-Za-z0-9_.-]*"\s*:`)

// templateLiteral reports whether a string literal's value is a JSON or
// text/template literal: it holds `{{`, or a JSON key and colon.
func templateLiteral(raw string) bool {
	s := litValue(raw)
	if strings.Contains(s, "{{") {
		return true
	}
	return jsonKeyRe.MatchString(s)
}

// litValue unquotes a Go string literal, raw or interpreted.
func litValue(raw string) string {
	if s, err := strconv.Unquote(raw); err == nil {
		return s
	}
	if len(raw) >= 2 {
		return raw[1 : len(raw)-1]
	}
	return raw
}

// containsOSPath reports whether s holds a Windows drive path (C:\ or C:/) or
// an unquoted UNC path (\\host\share). The drive letter must start a token, so
// a URL's `http://` does not read as a drive named h; the UNC backslashes must
// sit at a JSON value boundary, so a doubled backslash inside a JSON string --
// `\\x20`, `\\"`, `\\n`, all already escaped -- does not read as one.
func containsOSPath(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] != '\\' || s[i+1] != '\\' {
			continue
		}
		if i == 0 || strings.ContainsRune(" \t\n\r:[{,=", rune(s[i-1])) {
			return true
		}
	}
	for i := 0; i+2 < len(s); i++ {
		if !isASCIILetter(s[i]) || s[i+1] != ':' || (s[i+2] != '\\' && s[i+2] != '/') {
			continue
		}
		if i > 0 && isPathWordByte(s[i-1]) {
			continue
		}
		return true
	}
	return false
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isPathWordByte(b byte) bool {
	return isASCIILetter(b) || (b >= '0' && b <= '9') || b == '_'
}

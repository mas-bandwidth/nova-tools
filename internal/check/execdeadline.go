package check

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ExecDeadlineFinding represents one exec.CommandContext call without a deadline or timeout.
type ExecDeadlineFinding struct {
	File   string // file path (slash-separated, relative or absolute)
	Line   int    // 1-based line number
	Column int    // 1-based column number
	Func   string // enclosing function or method name (empty if package-level)
	Detail string // explanation of why the call violates the deadline rule
}

// ExecDeadlineOptions contains options for scanning.
type ExecDeadlineOptions struct {
	Exclude      []string // path prefixes to exclude
	IncludeTests bool     // whether to include _test.go files when walking a directory
	Strict       bool     // whether to flag context parameters without local WithTimeout/WithDeadline or Deadline check
}

// ExecDeadlineResult is the aggregate outcome of scanning files or directories.
type ExecDeadlineResult struct {
	FilesScanned int
	ExecCalls    int
	Findings     []ExecDeadlineFinding
	Excluded     int
}

type deadlineState int

const (
	deadlineUnknown deadlineState = iota
	deadlineHasDeadline
	deadlineNoDeadline
)

// CheckExecDeadlineDir scans all .go files under dir (recursively), returning any findings.
func CheckExecDeadlineDir(dir string, opts ExecDeadlineOptions) (ExecDeadlineResult, error) {
	var res ExecDeadlineResult
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return res, fmt.Errorf("dir %q: %w", dir, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return res, fmt.Errorf("dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return res, fmt.Errorf("dir %q is not a directory", dir)
	}

	normExcludes := make([]string, len(opts.Exclude))
	for i, ex := range opts.Exclude {
		normExcludes[i] = filepath.ToSlash(filepath.Clean(ex))
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)

		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			for _, ex := range normExcludes {
				if ex != "" && (relSlash == ex || strings.HasPrefix(relSlash, ex+"/")) {
					return filepath.SkipDir
				}
			}
			return nil
		}

		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		if !opts.IncludeTests && strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}

		for _, ex := range normExcludes {
			if ex != "" && (relSlash == ex || strings.HasPrefix(relSlash, ex+"/")) {
				res.Excluded++
				return nil
			}
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %q: %w", path, err)
		}

		findings, calls, err := AuditExecDeadlineSource(relSlash, src, opts)
		if err != nil {
			return fmt.Errorf("parse %q: %w", path, err)
		}
		res.FilesScanned++
		res.ExecCalls += calls
		res.Findings = append(res.Findings, findings...)
		return nil
	})
	if err != nil {
		return res, err
	}

	sort.Slice(res.Findings, func(i, j int) bool {
		if res.Findings[i].File != res.Findings[j].File {
			return res.Findings[i].File < res.Findings[j].File
		}
		if res.Findings[i].Line != res.Findings[j].Line {
			return res.Findings[i].Line < res.Findings[j].Line
		}
		return res.Findings[i].Column < res.Findings[j].Column
	})
	return res, nil
}

// CheckExecDeadlineFiles scans a specific list of files.
func CheckExecDeadlineFiles(root string, files []string, opts ExecDeadlineOptions) (ExecDeadlineResult, error) {
	var res ExecDeadlineResult
	normExcludes := make([]string, len(opts.Exclude))
	for i, ex := range opts.Exclude {
		normExcludes[i] = filepath.ToSlash(filepath.Clean(ex))
	}

	for _, f := range files {
		targetPath := f
		if root != "" && !filepath.IsAbs(f) {
			targetPath = filepath.Join(root, f)
		}
		relPath := f
		if root != "" {
			if r, err := filepath.Rel(root, targetPath); err == nil {
				relPath = filepath.ToSlash(r)
			}
		}

		excluded := false
		for _, ex := range normExcludes {
			if ex != "" && (relPath == ex || strings.HasPrefix(relPath, ex+"/")) {
				res.Excluded++
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		src, err := os.ReadFile(targetPath)
		if err != nil {
			return res, fmt.Errorf("read %q: %w", targetPath, err)
		}

		findings, calls, err := AuditExecDeadlineSource(relPath, src, opts)
		if err != nil {
			return res, fmt.Errorf("parse %q: %w", targetPath, err)
		}
		res.FilesScanned++
		res.ExecCalls += calls
		res.Findings = append(res.Findings, findings...)
	}

	sort.Slice(res.Findings, func(i, j int) bool {
		if res.Findings[i].File != res.Findings[j].File {
			return res.Findings[i].File < res.Findings[j].File
		}
		if res.Findings[i].Line != res.Findings[j].Line {
			return res.Findings[i].Line < res.Findings[j].Line
		}
		return res.Findings[i].Column < res.Findings[j].Column
	})
	return res, nil
}

// AuditExecDeadlineSource parses Go source and returns any exec.CommandContext deadline violations.
func AuditExecDeadlineSource(relPath string, src []byte, opts ExecDeadlineOptions) ([]ExecDeadlineFinding, int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, relPath, src, parser.ParseComments)
	if err != nil {
		return nil, 0, err
	}

	var (
		hasExecImport  bool
		execPkgName    = "exec"
		contextPkgName = "context"
		dotExec        bool
	)
	for _, imp := range f.Imports {
		pathVal := strings.Trim(imp.Path.Value, `"`)
		if pathVal == "os/exec" {
			hasExecImport = true
			if imp.Name != nil {
				if imp.Name.Name == "." {
					dotExec = true
				} else {
					execPkgName = imp.Name.Name
				}
			}
		}
		if pathVal == "context" {
			if imp.Name != nil && imp.Name.Name != "_" && imp.Name.Name != "." {
				contextPkgName = imp.Name.Name
			}
		}
	}

	if !hasExecImport && !dotExec {
		return nil, 0, nil
	}

	var (
		findings  []ExecDeadlineFinding
		execCalls int
		funcStack []ast.Node
	)

	var inspect func(node ast.Node) bool
	inspect = func(node ast.Node) bool {
		if node == nil {
			return true
		}

		switch n := node.(type) {
		case *ast.FuncDecl:
			funcStack = append(funcStack, n)
			if n.Body != nil {
				for _, stmt := range n.Body.List {
					ast.Inspect(stmt, inspect)
				}
			}
			funcStack = funcStack[:len(funcStack)-1]
			return false
		case *ast.FuncLit:
			funcStack = append(funcStack, n)
			if n.Body != nil {
				for _, stmt := range n.Body.List {
					ast.Inspect(stmt, inspect)
				}
			}
			funcStack = funcStack[:len(funcStack)-1]
			return false
		case *ast.CallExpr:
			isCmdCtx := false
			if dotExec {
				if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "CommandContext" {
					isCmdCtx = true
				}
			}
			if !isCmdCtx {
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "CommandContext" {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == execPkgName {
						isCmdCtx = true
					}
				}
			}
			if isCmdCtx {
				execCalls++
				var enclosing ast.Node
				if len(funcStack) > 0 {
					enclosing = funcStack[len(funcStack)-1]
				}
				funcName := funcNodeName(enclosing)
				pos := fset.Position(n.Pos())

				if len(n.Args) == 0 {
					findings = append(findings, ExecDeadlineFinding{
						File:   relPath,
						Line:   pos.Line,
						Column: pos.Column,
						Func:   funcName,
						Detail: "exec.CommandContext called without context argument",
					})
					return true
				}

				ctxArg := n.Args[0]
				hasDeadline, detail := evaluateContextArg(ctxArg, n.Pos(), funcStack, contextPkgName, opts)
				if !hasDeadline {
					findings = append(findings, ExecDeadlineFinding{
						File:   relPath,
						Line:   pos.Line,
						Column: pos.Column,
						Func:   funcName,
						Detail: detail,
					})
				}
			}
		}
		return true
	}

	for _, decl := range f.Decls {
		ast.Inspect(decl, inspect)
	}

	return findings, execCalls, nil
}

func evaluateContextArg(ctxArg ast.Expr, callPos token.Pos, funcStack []ast.Node, contextPkg string, opts ExecDeadlineOptions) (bool, string) {
	if id, ok := ctxArg.(*ast.Ident); ok && id.Name == "nil" {
		return false, "nil context passed to exec.CommandContext has no deadline or timeout"
	}

	if call, ok := ctxArg.(*ast.CallExpr); ok {
		pkg, sel := selName(call.Fun)
		if pkg == contextPkg {
			switch sel {
			case "Background":
				return false, "context.Background() has no deadline or timeout; wrap with context.WithTimeout or context.WithDeadline"
			case "TODO":
				return false, "context.TODO() has no deadline or timeout; wrap with context.WithTimeout or context.WithDeadline"
			case "WithCancel", "WithCancelCause":
				if len(call.Args) > 0 {
					return evaluateContextArg(call.Args[0], callPos, funcStack, contextPkg, opts)
				}
				return false, fmt.Sprintf("context.%s has no deadline or timeout; use context.WithTimeout or context.WithDeadline", sel)
			case "WithTimeout", "WithDeadline", "WithTimeoutCause", "WithDeadlineCause":
				return true, ""
			case "WithValue":
				if len(call.Args) > 0 {
					return evaluateContextArg(call.Args[0], callPos, funcStack, contextPkg, opts)
				}
				return false, "context.WithValue has no deadline or timeout; wrap with context.WithTimeout or context.WithDeadline"
			}
		}
		if sel == "Context" {
			// e.g. t.Context() in Go tests
			return true, ""
		}
	}

	if id, ok := ctxArg.(*ast.Ident); ok {
		return traceIdentDeadline(id.Name, callPos, funcStack, contextPkg, opts)
	}

	if sel, ok := ctxArg.(*ast.SelectorExpr); ok {
		if opts.Strict {
			return false, fmt.Sprintf("context field %s has no verified deadline or timeout in scope", exprString(sel))
		}
		return true, ""
	}

	return true, ""
}

func traceIdentDeadline(varName string, callPos token.Pos, funcStack []ast.Node, contextPkg string, opts ExecDeadlineOptions) (bool, string) {
	if len(funcStack) == 0 {
		if opts.Strict {
			return false, fmt.Sprintf("context variable %q has no verified deadline or timeout", varName)
		}
		return true, ""
	}

	for i := len(funcStack) - 1; i >= 0; i-- {
		fnNode := funcStack[i]
		state, origin, detail := inspectFunctionStatements(varName, callPos, fnNode, funcStack[:i+1], contextPkg, opts)
		if state == deadlineHasDeadline {
			return true, ""
		}
		if state == deadlineNoDeadline {
			if detail != "" {
				return false, fmt.Sprintf("variable %q derived from %s; wrap with context.WithTimeout or context.WithDeadline", varName, detail)
			}
			return false, fmt.Sprintf("variable %q derived from %s has no deadline or timeout; wrap with context.WithTimeout or context.WithDeadline", varName, origin)
		}

		// If state is unknown, check if varName is a parameter of fnNode
		isParam := isFuncParam(varName, fnNode)
		if isParam {
			// Check if varName.Deadline() was checked anywhere in fnNode
			if hasDeadlineCheck(varName, fnNode) {
				return true, ""
			}
			if opts.Strict {
				return false, fmt.Sprintf("context parameter %q has no verified deadline or timeout in this function; wrap with context.WithTimeout or context.WithDeadline", varName)
			}
			return true, ""
		}
	}

	if opts.Strict {
		return false, fmt.Sprintf("context variable %q has no verified deadline or timeout in scope", varName)
	}
	return true, ""
}

func inspectFunctionStatements(varName string, callPos token.Pos, fnNode ast.Node, stack []ast.Node, contextPkg string, opts ExecDeadlineOptions) (deadlineState, string, string) {
	var bodyList []ast.Stmt
	switch fn := fnNode.(type) {
	case *ast.FuncDecl:
		if fn.Body != nil {
			bodyList = fn.Body.List
		}
	case *ast.FuncLit:
		if fn.Body != nil {
			bodyList = fn.Body.List
		}
	}

	currentState := deadlineUnknown
	origin := ""
	detail := ""

	var walkStmts func(stmts []ast.Stmt)
	walkStmts = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			if stmt.Pos() >= callPos {
				continue
			}

			switch s := stmt.(type) {
			case *ast.AssignStmt:
				for idx, lhs := range s.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Name == varName {
						var rhs ast.Expr
						if len(s.Rhs) == len(s.Lhs) {
							rhs = s.Rhs[idx]
						} else if len(s.Rhs) == 1 {
							rhs = s.Rhs[0]
						}

						if rhs != nil {
							st, orig, det := evaluateRHSExpr(rhs, s.Pos(), stack, contextPkg, opts)
							if st != deadlineUnknown {
								currentState = st
								origin = orig
								detail = det
							}
						}
					}
				}
			case *ast.DeclStmt:
				if gen, ok := s.Decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
					for _, spec := range gen.Specs {
						if valSpec, ok := spec.(*ast.ValueSpec); ok {
							for idx, name := range valSpec.Names {
								if name.Name == varName {
									if len(valSpec.Values) == 0 {
										currentState = deadlineNoDeadline
										origin = "uninitialized variable"
										detail = ""
									} else {
										var rhs ast.Expr
										if len(valSpec.Values) == len(valSpec.Names) {
											rhs = valSpec.Values[idx]
										} else if len(valSpec.Values) == 1 {
											rhs = valSpec.Values[0]
										}
										if rhs != nil {
											st, orig, det := evaluateRHSExpr(rhs, s.Pos(), stack, contextPkg, opts)
											if st != deadlineUnknown {
												currentState = st
												origin = orig
												detail = det
											}
										}
									}
								}
							}
						}
					}
				}
			case *ast.BlockStmt:
				walkStmts(s.List)
			case *ast.IfStmt:
				if s.Init != nil {
					walkStmts([]ast.Stmt{s.Init})
				}
				if s.Body != nil {
					walkStmts(s.Body.List)
				}
				if s.Else != nil {
					if elseBlock, ok := s.Else.(*ast.BlockStmt); ok {
						walkStmts(elseBlock.List)
					}
				}
			case *ast.ForStmt:
				if s.Init != nil {
					walkStmts([]ast.Stmt{s.Init})
				}
				if s.Body != nil {
					walkStmts(s.Body.List)
				}
			case *ast.RangeStmt:
				if s.Body != nil {
					walkStmts(s.Body.List)
				}
			}
		}
	}

	walkStmts(bodyList)
	return currentState, origin, detail
}

func evaluateRHSExpr(rhs ast.Expr, pos token.Pos, stack []ast.Node, contextPkg string, opts ExecDeadlineOptions) (deadlineState, string, string) {
	if id, ok := rhs.(*ast.Ident); ok {
		if id.Name == "nil" {
			return deadlineNoDeadline, "nil", ""
		}
		// Alias of another identifier
		hasDl, det := traceIdentDeadline(id.Name, pos, stack, contextPkg, opts)
		if hasDl {
			return deadlineHasDeadline, "", ""
		}
		return deadlineNoDeadline, id.Name, det
	}

	if call, ok := rhs.(*ast.CallExpr); ok {
		pkg, sel := selName(call.Fun)
		if pkg == contextPkg {
			switch sel {
			case "WithTimeout", "WithDeadline", "WithTimeoutCause", "WithDeadlineCause":
				return deadlineHasDeadline, "", ""
			case "Background":
				return deadlineNoDeadline, "context.Background()", ""
			case "TODO":
				return deadlineNoDeadline, "context.TODO()", ""
			case "WithCancel", "WithCancelCause":
				if len(call.Args) > 0 {
					st, orig, det := evaluateRHSExpr(call.Args[0], pos, stack, contextPkg, opts)
					if st == deadlineHasDeadline {
						return deadlineHasDeadline, "", ""
					}
					if st == deadlineNoDeadline {
						if orig != "" {
							return deadlineNoDeadline, fmt.Sprintf("context.%s(%s)", sel, orig), det
						}
						return deadlineNoDeadline, fmt.Sprintf("context.%s()", sel), det
					}
					return deadlineUnknown, "", ""
				}
				return deadlineNoDeadline, fmt.Sprintf("context.%s()", sel), ""
			case "WithValue":
				if len(call.Args) > 0 {
					return evaluateRHSExpr(call.Args[0], pos, stack, contextPkg, opts)
				}
				return deadlineNoDeadline, "context.WithValue()", ""
			}
		}
		if sel == "Context" {
			// t.Context()
			return deadlineHasDeadline, "", ""
		}
	}

	return deadlineUnknown, "", ""
}

func isFuncParam(varName string, fnNode ast.Node) bool {
	var params *ast.FieldList
	switch fn := fnNode.(type) {
	case *ast.FuncDecl:
		if fn.Type != nil {
			params = fn.Type.Params
		}
	case *ast.FuncLit:
		if fn.Type != nil {
			params = fn.Type.Params
		}
	}
	if params == nil {
		return false
	}
	for _, field := range params.List {
		for _, id := range field.Names {
			if id.Name == varName {
				return true
			}
		}
	}
	return false
}

func hasDeadlineCheck(varName string, fnNode ast.Node) bool {
	checked := false
	ast.Inspect(fnNode, func(n ast.Node) bool {
		if checked {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			pkg, sel := selName(call.Fun)
			if pkg == varName && sel == "Deadline" {
				checked = true
				return false
			}
		}
		return true
	})
	return checked
}

func selName(fun ast.Expr) (string, string) {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", sel.Sel.Name
	}
	return id.Name, sel.Sel.Name
}

func funcNodeName(n ast.Node) string {
	if n == nil {
		return ""
	}
	switch fn := n.(type) {
	case *ast.FuncDecl:
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			t := fn.Recv.List[0].Type
			if s, ok := t.(*ast.StarExpr); ok {
				t = s.X
			}
			if ix, ok := t.(*ast.IndexExpr); ok {
				t = ix.X
			}
			if id, ok := t.(*ast.Ident); ok {
				return id.Name + "." + fn.Name.Name
			}
		}
		return fn.Name.Name
	case *ast.FuncLit:
		return "closure"
	}
	return ""
}

func exprString(expr ast.Expr) string {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		return exprString(sel.X) + "." + sel.Sel.Name
	}
	return "expr"
}

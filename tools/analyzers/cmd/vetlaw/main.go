package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
)

type vcfg struct {
	GoFiles    []string `json:"GoFiles"`
	VetxOnly   bool     `json:"VetxOnly"`
	VetxOutput string   `json:"VetxOutput"`
}

func main() {
	args := os.Args[1:]

	for _, a := range args {
		if a == "-flags" {
			os.Stdout.WriteString("[]\n")
			return
		}
		if strings.HasPrefix(a, "-V=") {
			os.Stdout.WriteString("vet version go1.26.6\n")
			return
		}
	}

	var cfgFile string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		cfgFile = a
		break
	}
	if cfgFile == "" {
		return
	}

	data, err := os.ReadFile(cfgFile)
	if err != nil {
		return
	}

	var cfg vcfg
	if err := json.Unmarshal(data, &cfg); err != nil {
		return
	}

	if cfg.VetxOnly {
		if cfg.VetxOutput != "" {
			os.WriteFile(cfg.VetxOutput, nil, 0644)
		}
		return
	}

	if len(cfg.GoFiles) == 0 {
		return
	}

	fset := token.NewFileSet()
	code := 0
	for _, f := range cfg.GoFiles {
		node, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if err != nil {
			continue
		}
		for _, diag := range checkZeroByte(fset, node) {
			code = 1
			os.Stderr.WriteString(diag + "\n")
		}
		for _, diag := range checkHelpRefused(fset, node) {
			code = 1
			os.Stderr.WriteString(diag + "\n")
		}
		for _, diag := range checkArgvText(fset, node) {
			code = 1
			os.Stderr.WriteString(diag + "\n")
		}
	}
	if code != 0 {
		os.Exit(code)
	}
}

func checkZeroByte(fset *token.FileSet, file *ast.File) []string {
	var diags []string
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "run" {
			continue
		}
		if fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
			continue
		}
		rt, ok := fd.Type.Results.List[0].Type.(*ast.Ident)
		if !ok || rt.Name != "int" {
			continue
		}
		if hasReturnZero(fd) && !hasFmtFprintCall(fd) {
			pos := fset.Position(fd.Pos())
			diags = append(diags, pos.Filename+":"+
				itoa(pos.Line)+":"+
				itoa(pos.Column)+": verb-law: exit-zero-with-zero-bytes: function run returns int with return 0 but no fmt.Fprint* call (law #2573)")
		}
	}
	return diags
}

func hasReturnZero(fd *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		rs, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, e := range rs.Results {
			bl, ok := e.(*ast.BasicLit)
			if ok && bl.Kind == token.INT && bl.Value == "0" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func hasFmtFprintCall(fd *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		se, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if se.X == nil {
			return true
		}
		if xid, ok := se.X.(*ast.Ident); ok && xid.Name == "fmt" {
			if strings.HasPrefix(se.Sel.Name, "Fprint") || strings.HasPrefix(se.Sel.Name, "Fprintf") || se.Sel.Name == "Print" || se.Sel.Name == "Printf" || se.Sel.Name == "Println" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// checkHelpRefused flags a help branch that answers REFUSED (law #2575).
// A help branch is an if whose condition compares against a help literal
// ("help", "--help", "-h", "-help") with ==, or a switch case that lists one;
// only a REFUSED string literal inside THAT branch's body is a diagnostic.
// A function that merely handles help somewhere and refuses somewhere else is
// the normal shape of a verb and is not flagged, and _test.go files are never
// flagged: tests name both help and REFUSED to assert the verb's own lines.
func checkHelpRefused(fset *token.FileSet, file *ast.File) []string {
	if strings.HasSuffix(fset.Position(file.Pos()).Filename, "_test.go") {
		return nil
	}
	var diags []string
	ast.Inspect(file, func(n ast.Node) bool {
		var body []ast.Stmt
		switch s := n.(type) {
		case *ast.IfStmt:
			if !isHelpTest(s.Cond) {
				return true
			}
			body = s.Body.List
		case *ast.CaseClause:
			match := false
			for _, e := range s.List {
				if isHelpLit(e) || isHelpTest(e) {
					match = true
					break
				}
			}
			if !match {
				return true
			}
			body = s.Body
		default:
			return true
		}
		if lit := refusedLit(body); lit != nil {
			pos := fset.Position(lit.Pos())
			diags = append(diags, pos.Filename+":"+
				itoa(pos.Line)+":"+
				itoa(pos.Column)+": verb-law: help-answers-refused: the --help branch answers REFUSED (law #2575)")
		}
		return true
	})
	return diags
}

// isHelpLit reports whether e is a string literal naming a help door.
func isHelpLit(e ast.Expr) bool {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return false
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return false
	}
	return s == "help" || s == "--help" || s == "-h" || s == "-help"
}

// isHelpTest reports whether cond contains an == comparison with a help literal.
func isHelpTest(cond ast.Expr) bool {
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if ok && be.Op == token.EQL && (isHelpLit(be.X) || isHelpLit(be.Y)) {
			found = true
			return false
		}
		return !found
	})
	return found
}

// refusedLit returns the first string literal containing REFUSED in body.
func refusedLit(body []ast.Stmt) *ast.BasicLit {
	var hit *ast.BasicLit
	for _, st := range body {
		ast.Inspect(st, func(n ast.Node) bool {
			if hit != nil {
				return false
			}
			bl, ok := n.(*ast.BasicLit)
			if ok && bl.Kind == token.STRING && strings.Contains(bl.Value, "REFUSED") {
				hit = bl
				return false
			}
			return true
		})
		if hit != nil {
			break
		}
	}
	return hit
}

func checkArgvText(fset *token.FileSet, file *ast.File) []string {
	var diags []string
	ast.Inspect(file, func(n ast.Node) bool {
		ie, ok := n.(*ast.IndexExpr)
		if !ok {
			return true
		}
		se, ok := ie.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		xid, ok := se.X.(*ast.Ident)
		if !ok || xid.Name != "os" || se.Sel.Name != "Args" {
			return true
		}
		idx, ok := ie.Index.(*ast.BasicLit)
		if !ok || idx.Kind != token.INT {
			return true
		}
		if idx.Value != "0" && idx.Value != "1" {
			pos := fset.Position(ie.Pos())
			diags = append(diags, pos.Filename+":"+
				itoa(pos.Line)+":"+
				itoa(pos.Column)+": verb-law: task-text-in-argv: os.Args["+idx.Value+"] reads positional arguments as task text (law #2583)")
		}
		return true
	})
	return diags
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

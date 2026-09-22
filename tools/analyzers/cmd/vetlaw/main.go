package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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

func checkHelpRefused(fset *token.FileSet, file *ast.File) []string {
	var diags []string
	var helpRef *ast.FuncDecl
	ast.Inspect(file, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if hasHelpHandler(fd) && hasRefusedString(fd) {
			helpRef = fd
			return false
		}
		return true
	})
	if helpRef != nil {
		pos := fset.Position(helpRef.Pos())
		diags = append(diags, pos.Filename+":"+
			itoa(pos.Line)+":"+
			itoa(pos.Column)+": verb-law: help-answers-refused: function handles --help but contains REFUSED string (law #2575)")
	}
	return diags
}

func hasHelpHandler(fd *ast.FuncDecl) bool {
	if fd.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		bl, ok := n.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return true
		}
		s := strings.Trim(bl.Value, `"`)
		if s == "help" || s == "--help" || s == "-h" || s == "-help" {
			found = true
			return false
		}
		return true
	})
	return found
}

func hasRefusedString(fd *ast.FuncDecl) bool {
	if fd.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		bl, ok := n.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return true
		}
		if strings.Contains(bl.Value, "REFUSED") {
			found = true
			return false
		}
		return true
	})
	return found
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

package ci

import (
	"go/ast"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ci_benchrunner.go is the machine behind TestCIOneBenchRunner (#2932, the
// control for #3291): the fleet plays are the one way this tree runs a script
// on a bench, and every ssh exec site left in Go is a row of
// testdata/bench-runners.allow with its
// shape and retiring issue. The list may only shrink.
//
// A SITE is a function (go/ast, every non-test .go file under cmd/ and
// internal/ outside internal/testguard) that calls
// exec.Command, exec.CommandContext or testguard.RefuseHosts with a program
// argument that is ssh:
//
//   - the literal "ssh";
//   - argv[0] of a slice whose literal first element is "ssh";
//   - an identifier or field the same function sets to "ssh" (`bin = "ssh"`,
//     `prog := fs.String("ssh", "ssh", ...)`), or a field a composite literal
//     of the receiver's type defaults to "ssh" (`powerSSHRunner{Program: "ssh"}`);
//   - a parameter or field named ssh, sshPath or SSH;
//   - the Path of a type named ExecSSH.
//
// gh, git, rsync and scp are not bench runners: they run no remote command.
//
// GIT-TRANSPORT EXCLUSION. A function that names ssh only to testguard (it
// execs no ssh itself) and whose ssh command reaches a child only as git's
// transport -- a GIT_SSH_COMMAND= env entry or core.sshCommand= value built in
// the same function, or a string it returns that a caller in the same file
// puts there -- is not a site: git runs only its own git-upload-pack over it.

// BenchRunnerAllowPath is the allow file, relative to internal/ci.
const BenchRunnerAllowPath = "testdata/bench-runners.allow"

// benchRunnerSkipDirs are read against themselves: the runner and the guard.
var benchRunnerSkipDirs = []string{"internal/testguard/"}

// BenchRunnerSite is one ssh exec site: its file (slash, from the repo root)
// and its function key (Recv.Name or Name).
type BenchRunnerSite struct {
	File string
	Func string
	Line int
}

// Key is the allow row key: `<file> <func>` (lines move; the key does not).
func (s BenchRunnerSite) Key() string { return s.File + " " + s.Func }

// FindBenchRunners walks cmd/ and internal/ under root.
func FindBenchRunners(root string) ([]BenchRunnerSite, error) {
	var sites []BenchRunnerSite
	for _, dir := range []string{"cmd", "internal"} {
		err := walkSourceDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			for _, skip := range benchRunnerSkipDirs {
				if strings.HasPrefix(rel, skip) {
					return nil
				}
			}
			if strings.Contains(rel, "/testdata/") {
				return nil
			}
			raw, err := readSourceFile(path)
			if err != nil {
				return err
			}
			found, err := BenchRunnersInSource(rel, raw)
			if err != nil {
				return err
			}
			sites = append(sites, found...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].Key() < sites[j].Key() })
	return sites, nil
}

// BenchRunnersInSource finds the sites in one file's source.
func BenchRunnersInSource(rel string, src []byte) ([]BenchRunnerSite, error) {
	fset, file, err := parseSource(rel, src, 0)
	if err != nil {
		return nil, err
	}
	defaults := runnerStructDefaults(file)
	transport := gitTransportFuncs(file)
	var sites []BenchRunnerSite
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		execs, guards := runnerCalls(fn, defaults)
		if execs == 0 && guards == 0 {
			continue
		}
		if execs == 0 && transport[fn.Name.Name] {
			continue // git transport only
		}
		sites = append(sites, BenchRunnerSite{File: rel, Func: benchRunnerFuncKey(fn), Line: fset.Position(fn.Pos()).Line})
	}
	return sites, nil
}

func benchRunnerFuncKey(fn *ast.FuncDecl) string {
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
}

func recvType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	t := fn.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func isRunnerLit(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	v, err := strconv.Unquote(lit.Value)
	return err == nil && v == "ssh"
}

// runnerStructDefaults is every Type.Field a composite literal in the file sets
// to "ssh" (a struct default), as "Type.Field".
func runnerStructDefaults(file *ast.File) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		id, ok := cl.Type.(*ast.Ident)
		if !ok {
			return true
		}
		for _, el := range cl.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if k, ok := kv.Key.(*ast.Ident); ok && isRunnerLit(kv.Value) {
				out[id.Name+"."+k.Name] = true
			}
		}
		return true
	})
	return out
}

var sshNames = map[string]bool{"ssh": true, "sshPath": true, "SSH": true}

// runnerCalls counts fn's exec calls and RefuseHosts calls whose program is ssh.
func runnerCalls(fn *ast.FuncDecl, defaults map[string]bool) (execs, guards int) {
	sshIdents := map[string]bool{} // identifiers set to "ssh" in this function
	argvSlices := map[string]bool{}
	recv := recvType(fn)
	execTypes := map[string]bool{} // identifiers whose type is ExecSSH
	if recv == "ExecSSH" && len(fn.Recv.List[0].Names) > 0 {
		execTypes[fn.Recv.List[0].Names[0].Name] = true
	}
	for _, f := range fn.Type.Params.List {
		t := f.Type
		if s, ok := t.(*ast.StarExpr); ok {
			t = s.X
		}
		if id, ok := t.(*ast.Ident); ok && id.Name == "ExecSSH" {
			for _, n := range f.Names {
				execTypes[n.Name] = true
			}
		}
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, rhs := range as.Rhs {
			name := exprName(as.Lhs[i])
			if name == "" {
				continue
			}
			switch v := rhs.(type) {
			case *ast.BasicLit:
				if isRunnerLit(v) {
					sshIdents[name] = true
				}
			case *ast.CallExpr: // fs.String("ssh", "ssh", ...)
				if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "String" && len(v.Args) >= 2 && isRunnerLit(v.Args[1]) {
					sshIdents[name] = true
				}
				if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "append" && len(v.Args) > 0 && sliceStartsRunner(v.Args[0]) {
					argvSlices[name] = true
				}
			case *ast.CompositeLit:
				if sliceStartsRunner(v) {
					argvSlices[name] = true
				}
			}
		}
		return true
	})
	isSSH := func(e ast.Expr) bool {
		if isRunnerLit(e) {
			return true
		}
		if st, ok := e.(*ast.StarExpr); ok {
			e = st.X
		}
		switch v := e.(type) {
		case *ast.Ident:
			return sshIdents[v.Name] || sshNames[v.Name]
		case *ast.SelectorExpr:
			if sshNames[v.Sel.Name] || sshIdents[exprName(v)] {
				return true
			}
			if x, ok := v.X.(*ast.Ident); ok {
				if v.Sel.Name == "Path" && execTypes[x.Name] {
					return true
				}
				if recv != "" && len(fn.Recv.List[0].Names) > 0 && x.Name == fn.Recv.List[0].Names[0].Name && defaults[recv+"."+v.Sel.Name] {
					return true
				}
			}
		case *ast.IndexExpr:
			if id, ok := v.X.(*ast.Ident); ok && argvSlices[id.Name] {
				if lit, ok := v.Index.(*ast.BasicLit); ok && lit.Value == "0" {
					return true
				}
			}
		}
		return false
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case pkg.Name == "exec" && sel.Sel.Name == "Command" && len(call.Args) > 0:
			if isSSH(call.Args[0]) {
				execs++
			}
		case pkg.Name == "exec" && sel.Sel.Name == "CommandContext" && len(call.Args) > 1:
			if isSSH(call.Args[1]) {
				execs++
			}
		case pkg.Name == "testguard" && sel.Sel.Name == "RefuseHosts" && len(call.Args) > 0:
			if isSSH(call.Args[0]) {
				guards++
			}
		}
		return true
	})
	return execs, guards
}

func sliceStartsRunner(e ast.Expr) bool {
	cl, ok := e.(*ast.CompositeLit)
	if !ok || len(cl.Elts) == 0 {
		return false
	}
	return isRunnerLit(cl.Elts[0])
}

func exprName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		if x := exprName(v.X); x != "" {
			return x + "." + v.Sel.Name
		}
	}
	return ""
}

// gitTransportFuncs is every function in the file whose ssh reaches a child
// only as git's transport: its body builds GIT_SSH_COMMAND= or
// core.sshCommand=, or a function in the same file that does calls it.
func gitTransportFuncs(file *ast.File) map[string]bool {
	builds := map[string]bool{}
	calls := map[string][]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.BasicLit:
				if v.Kind == token.STRING && (strings.Contains(v.Value, "GIT_SSH_COMMAND=") || strings.Contains(v.Value, "core.sshCommand=")) {
					builds[fn.Name.Name] = true
				}
			case *ast.CallExpr:
				if id, ok := v.Fun.(*ast.Ident); ok {
					calls[fn.Name.Name] = append(calls[fn.Name.Name], id.Name)
				}
			}
			return true
		})
	}
	out := map[string]bool{}
	for f := range builds {
		out[f] = true
		for _, callee := range calls[f] {
			out[callee] = true
		}
	}
	return out
}

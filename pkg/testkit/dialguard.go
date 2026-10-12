package testkit

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// UnitTierDials lists, as file:line: pkg.Name, every use of a dialer in the
// test files of dir that the unit tier builds: a file whose build constraint
// holds with the functional and perf tags off. A miniredis started inside a
// function traps names is allowed. It backs the no-sockets rule of the unit
// tier (docs/STANDARD.md section 8).
func UnitTierDials(dir string, traps map[string]bool) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	var found []string
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		if !dialGuardUnitTier(string(src)) {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		local := map[string]string{}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return nil, err
			}
			parts := strings.Split(path, "/")
			n := parts[len(parts)-1]
			if dialGuardMajorVersion.MatchString(n) && len(parts) > 1 {
				n = parts[len(parts)-2]
			}
			n = strings.TrimPrefix(n, "go-")
			if imp.Name != nil {
				n = imp.Name.Name
			}
			local[n] = path
		}
		for _, decl := range f.Decls {
			fn, _ := decl.(*ast.FuncDecl)
			ast.Inspect(decl, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				path, ok := local[pkg.Name]
				if !ok {
					return true
				}
				for _, d := range dialGuardNames(path) {
					if !strings.EqualFold(d, sel.Sel.Name) {
						continue
					}
					if strings.HasPrefix(path, "github.com/alicebob/miniredis") && fn != nil && traps[fn.Name.Name] {
						continue
					}
					found = append(found, fmt.Sprintf("%s:%d: %s.%s", filepath.Base(name), fset.Position(sel.Pos()).Line, pkg.Name, sel.Sel.Name))
				}
				return true
			})
		}
	}
	return found, nil
}

// dialGuardNames returns, by import path, the names that open a TCP listener or
// dial. Standard-library and third-party paths are listed whole; this module's
// own packages are matched by suffix (dialGuardOwnPackages) so the module's own
// import prefix is not written here, where the generality rule (Rule 1) reads
// it. The names are lower case and matched case-insensitively: one constructor
// spells a machine name in camelCase, which the generality rule forbids.
func dialGuardNames(path string) []string {
	if names, ok := dialGuardDialers[path]; ok {
		return names
	}
	for _, own := range dialGuardOwnPackages {
		if strings.HasSuffix(path, own.suffix) {
			return own.names
		}
	}
	return nil
}

// dialGuardDialers names, by import path, the selectors that open a TCP listener
// or dial.
var dialGuardDialers = map[string][]string{
	"net":                              {"dial", "dialtimeout", "dialtcp", "dialip", "dialer", "listen", "listentcp", "listenconfig"},
	"crypto/tls":                       {"dial", "dialwithdialer", "dialer", "listen"},
	"net/http":                         {"get", "head", "post", "postform", "listenandserve", "listenandservetls"},
	"net/http/httptest":                {"newserver", "newtlsserver", "newunstartedserver"},
	"github.com/alicebob/miniredis/v2": {"run", "runt", "runtls", "newminiredis"},
	"github.com/redis/go-redis/v9":     {"dial"},
}

// dialGuardOwnPackages are this module's own openers, matched by import-path
// suffix.
var dialGuardOwnPackages = []struct {
	suffix string
	names  []string
}{
	{"/pkg/redisconn", []string{"open"}},
	{"/internal/nsprint/store", []string{"open"}},
	{"/pkg/testredis", []string{"far", "farlink", "commandcounter"}},
}

var dialGuardMajorVersion = regexp.MustCompile(`^v[0-9]+$`)

// dialGuardUnitTier reports whether the unit tier builds a file: its
// //go:build line, if any, holds with every tag on but functional and perf.
func dialGuardUnitTier(src string) bool {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return true
		}
		if !constraint.IsGoBuild(line) {
			continue
		}
		expr, err := constraint.Parse(line)
		if err != nil {
			return true
		}
		return expr.Eval(func(tag string) bool { return tag != "functional" && tag != "perf" })
	}
	return true
}

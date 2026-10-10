package update

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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnitTierDialsNoSocket: no unit test of this package opens a TCP
// connection (docs/STANDARD.md section 8: unit tests own no sockets; the post
// step's wall denies TCP, loopback included). Real sockets are the functional
// tier (`//go:build functional`); a unit test reaches a store or a server over
// an in-memory transport (pipeStore, a transport that calls its handler) or a
// seam. This reads every test file the unit tier builds and names each use of
// what dials or listens: net's Dial and Listen family and net.Dialer,
// tls.Dial, an httptest server, http.Get and its kin, the store openers
// redisconn.Open and store.Open, testredis's listeners, and a miniredis
// started anywhere but the helpers whose cleanup fails a test that dialled
// the fake over TCP (dialTraps).
func TestUnitTierDialsNoSocket(t *testing.T) {
	t.Parallel()
	found, err := unitTierDials(".", dialTraps)
	require.NoError(t, err)
	assert.Empty(t, found, "the unit tier dials or listens; move the test behind //go:build functional or give it an in-memory transport")
}

// dialTraps are this package's helpers allowed to start a miniredis: each
// fails its test at cleanup when the fake took a TCP connection.
var dialTraps = map[string]bool{"pipeStore": true}

// dialers are, by import path, the names that open a TCP listener or dial.
var dialers = map[string][]string{
	"net":                              {"Dial", "DialTimeout", "DialTCP", "DialIP", "Dialer", "Listen", "ListenTCP", "ListenConfig"},
	"crypto/tls":                       {"Dial", "DialWithDialer", "Dialer", "Listen"},
	"net/http":                         {"Get", "Head", "Post", "PostForm", "ListenAndServe", "ListenAndServeTLS"},
	"net/http/httptest":                {"NewServer", "NewTLSServer", "NewUnstartedServer"},
	"github.com/alicebob/miniredis/v2": {"Run", "RunT", "RunTLS", "NewMiniRedis"},
	"github.com/redis/go-redis/v9":     {"Dial"},
	"github.com/mas-bandwidth/nova-tools/pkg/redisconn":          {"Open"},
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store": {"Open"},
	"github.com/mas-bandwidth/nova-tools/pkg/testredis":          {"Far", "FarLink", "CommandCounter"},
}

var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// unitTierDials lists, as file:line: pkg.Name, every use of a dialer in the
// test files of dir that the unit tier builds: a file whose build constraint
// holds with the functional and perf tags off. A miniredis started inside a
// function traps names is allowed.
func unitTierDials(dir string, traps map[string]bool) ([]string, error) {
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
		if !unitTier(string(src)) {
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
			if majorVersion.MatchString(n) && len(parts) > 1 {
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
				for _, d := range dialers[path] {
					if d != sel.Sel.Name {
						continue
					}
					if strings.HasPrefix(path, "github.com/alicebob/miniredis") && fn != nil && traps[fn.Name.Name] {
						continue
					}
					found = append(found, fmt.Sprintf("%s:%d: %s.%s", filepath.Base(name), fset.Position(sel.Pos()).Line, pkg.Name, d))
				}
				return true
			})
		}
	}
	return found, nil
}

// unitTier reports whether the unit tier builds a file: its //go:build line,
// if any, holds with every tag on but functional and perf.
func unitTier(src string) bool {
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

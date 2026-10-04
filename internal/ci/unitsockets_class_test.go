package ci

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unitsockets_class_test.go holds the unit tier's socket rule (docs/STANDARD.md
// section 8: "Unit tests own no sockets and start no redis-server"; the owner's
// rule behind it: "Never test with real sockets, otherwise the only tests you
// can do are by definition, functional tests, and those are slower"). A unit
// test that opens a socket -- even a loopback one -- is by definition a
// functional test: it is slower, it needs a free port, and its verdict can turn
// on the machine's load.
//
// THE RULE. Every _test.go the unit tier compiles -- under cmd/, internal/ and
// tools/, outside testdata, behind no `//go:build functional`, slow, perf or
// nightly constraint -- is scanned, and a call of net.Listen,
// net.ListenPacket, net.ListenTCP, net.ListenUnix, net.Dial or net.DialTimeout,
// or of httptest.NewServer, httptest.NewTLSServer or
// httptest.NewUnstartedServer, is refused, naming file:line. The call is
// resolved through the file's imports, so an aliased import counts. The
// remedies are never refused: a conn comes from net.Pipe, a handler is proved
// with httptest.NewRecorder, a client with an in-process RoundTripper; a test
// whose subject is the real socket moves under //go:build functional.
//
// THE LEDGER. internal/ci/testdata/unit-sockets holds one counted shard per
// source package, one `<file>:socket <sites> <reason>` row per test file still
// short. It only shrinks: a file that opens more sockets than its row, a file
// with a site and no row, and a row above what the file opens are each red, and
// NOVA_CI_UPDATE=1 lowers the counts and drops the rows at zero, never raises a
// count and never adds a row.
const unitSocketsLedgerPath = "testdata/unit-sockets"

// unitSocketsRefused are the socket-opening functions the rule refuses, by
// import path and the member name a call spells after the file's own name for
// that import.
var unitSocketsRefused = map[string][]string{
	"net":               {"Listen", "ListenPacket", "ListenTCP", "ListenUnix", "Dial", "DialTimeout"},
	"net/http/httptest": {"NewServer", "NewTLSServer", "NewUnstartedServer"},
}

// unitSocketsRemedy is the one thing to do about a unit test that opens a
// socket.
const unitSocketsRemedy = "test the logic through a seam: the handler with httptest.NewRecorder, the client with an in-process RoundTripper, a conn with net.Pipe; a test whose subject is the real socket moves under //go:build functional"

// unitSocketsExemptTags are the build tags that take a test file out of the
// unit tier: those suites run where the network and the real stores are.
var unitSocketsExemptTags = []string{"functional", "slow", "perf", "nightly"}

// unitSocketsExempt reports whether the file's header build constraint carries
// one of unitSocketsExemptTags. The constraint is read as ci_net.go reads its
// nightly/soak exemption (netBuildTagExempt): go/build/constraint over the
// header, in both the `//go:build` and the legacy `// +build` form, a negated
// tag not counting (`!functional` is the file that builds in the ordinary
// suite, which this check reads). The file's raw bytes are read because the
// shared tree is parsed with mode 0 and keeps no comments (tree_test.go).
func unitSocketsExempt(src []byte) bool {
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			break
		}
		if !constraint.IsGoBuild(line) && !constraint.IsPlusBuild(line) {
			continue
		}
		expr, err := constraint.Parse(line)
		if err != nil {
			continue
		}
		for _, tag := range unitSocketsExemptTags {
			if constraintTag(expr, tag) {
				return true
			}
		}
	}
	return false
}

// unitSocketSite is one refused call: the line it stands on and the call as
// the file spells it, through its own name for the import.
type unitSocketSite struct {
	Line int
	What string
}

// unitSocketsIn scans one parsed test file and returns the refused socket
// calls it makes, resolved through the file's imports (the unitwaits rule's
// import-name map) so an aliased import counts. It reads calls only: a name
// used as a value, a comment or a string literal is not a socket. net.Pipe and
// httptest.NewRecorder are the remedies and are never refused.
func unitSocketsIn(fset *token.FileSet, file *ast.File) []unitSocketSite {
	local := map[string]string{} // the file's name for an import -> its path
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		local[name] = p
	}
	var out []unitSocketSite
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		p, ok := local[id.Name]
		if !ok {
			return true
		}
		for _, fn := range unitSocketsRefused[p] {
			if sel.Sel.Name == fn {
				out = append(out, unitSocketSite{Line: fset.Position(call.Pos()).Line, What: id.Name + "." + fn})
			}
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// TestNoUnitTestOpensASocket holds every _test.go the unit tier compiles
// (docs/STANDARD.md sections 8 and 9, rule 2) to the shrink-only unit-sockets
// ledger: a socket call is refused naming file:line, and the ledger only
// shrinks. The files are read from the shared tree; the tier is decided the
// way ci_net.go decides its nightly/soak exemption.
func TestNoUnitTestOpensASocket(t *testing.T) {
	t.Parallel()

	tree := repoTree(t)
	ledger := newSiteLedger(t, unitSocketsLedgerPath)
	files, sites := 0, 0
	for _, f := range tree.GoFilesUnder(true, unitWaitDirs...) {
		if f.HasDirNamed("testdata") {
			continue
		}
		require.NoError(t, f.ParseErr, f.Rel)
		if unitSocketsExempt(f.Src) {
			continue
		}
		files++
		for _, s := range unitSocketsIn(tree.FSet, f.AST) {
			sites++
			ledger.add(f.Rel+":socket", fmt.Sprintf("%s:%d: %s(...)", f.Rel, s.Line, s.What))
		}
	}
	require.NotZerof(t, files, "read no unit-tier test file; the rule would pass by checking nothing")
	t.Logf("unit-tier test files=%d socket calls=%d (SPEC-CI's ratchet row: the unit-sockets ledger's site count only falls)", files, sites)
	for _, v := range ledger.violations(t, unitSocketsRemedy) {
		t.Error(v)
	}
}

// TestUnitSocketsDetectorRefusesTheCallsAndNotTheRemedies pins the scan over
// one fixture: every refused call is found naming its line, an aliased import
// counts, the remedies (net.Pipe, httptest.NewRecorder) are never refused, a
// functional-tagged file is skipped whole, and the fixed fixture is clean.
func TestUnitSocketsDetectorRefusesTheCallsAndNotTheRemedies(t *testing.T) {
	t.Parallel()

	const offending = `package p

import (
	"net"
	"net/http"
	nh "net/http/httptest"
	"testing"
	"time"
)

func TestOpens(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = net.ListenPacket("tcp", "127.0.0.1:0")
	_ = net.ListenTCP("tcp", nil)
	_ = net.ListenUnix("unix", nil)
	_, _ = net.Dial("tcp", "127.0.0.1:0")
	_, _ = net.DialTimeout("tcp", "127.0.0.1:0", time.Second)
	srv := nh.NewServer(http.HandlerFunc(nil))
	tlsSrv := nh.NewTLSServer(http.HandlerFunc(nil))
	unstarted := nh.NewUnstartedServer(http.HandlerFunc(nil))
	_, _, _ = srv, tlsSrv, unstarted
	_ = ln
}
`
	const rel = "cmd/p/p_test.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, offending, 0)
	require.NoError(t, err)
	var got []string
	for _, s := range unitSocketsIn(fset, f) {
		got = append(got, fmt.Sprintf("%s:%d %s", rel, s.Line, s.What))
	}
	want := []string{
		rel + ":12 net.Listen",
		rel + ":16 net.ListenPacket",
		rel + ":17 net.ListenTCP",
		rel + ":18 net.ListenUnix",
		rel + ":19 net.Dial",
		rel + ":20 net.DialTimeout",
		rel + ":21 nh.NewServer",
		rel + ":22 nh.NewTLSServer",
		rel + ":23 nh.NewUnstartedServer",
	}
	assert.Equal(t, want, got, "the offending fixture's refused calls:\n%s", strings.Join(got, "\n"))

	const fixed = `package p

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestThroughTheSeam(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	rec := httptest.NewRecorder()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	handler(rec, nil)
	_ = client
}
`
	fset = token.NewFileSet()
	f, err = parser.ParseFile(fset, rel, fixed, 0)
	require.NoError(t, err)
	assert.Empty(t, unitSocketsIn(fset, f), "the fixed fixture opens no socket; the remedies are never refused")

	for src, exempt := range map[string]bool{
		"package p\n":                                   false,
		"//go:build functional\n\npackage p\n":          true,
		"//go:build slow\n\npackage p\n":                true,
		"//go:build perf\n\npackage p\n":                true,
		"//go:build nightly\n\npackage p\n":             true,
		"//go:build !functional\n\npackage p\n":         false,
		"//go:build darwin\n\npackage p\n":              false,
		"//go:build linux\n\npackage p\n":               false,
		"//go:build functional && linux\n\npackage p\n": true,
		"// +build functional\n\npackage p\n":           true,
	} {
		assert.Equalf(t, exempt, unitSocketsExempt([]byte(src)), "unitSocketsExempt(%q)", src)
	}
	_, err = parser.ParseFile(token.NewFileSet(), rel, "//go:build functional\n\n"+offending, 0)
	require.NoError(t, err)
	assert.True(t, unitSocketsExempt([]byte("//go:build functional\n\n"+offending)), "a functional-tagged fixture is skipped whole, not scanned")
}

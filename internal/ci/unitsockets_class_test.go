package ci

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
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
// net.ListenPacket, net.ListenTCP, net.ListenUnix, net.ListenUDP,
// net.ListenUnixgram, net.ListenIP, net.ListenMulticastUDP, net.Dial,
// net.DialTimeout, net.DialTCP, net.DialUDP, net.DialUnix or net.DialIP, or of
// httptest.NewServer, httptest.NewTLSServer or httptest.NewUnstartedServer, is
// refused, naming file:line. So is an os/exec Command or CommandContext whose
// literal program is a networked tool (curl, wget, nc, ncat, netcat, socat,
// telnet, ssh, scp, redis-cli), or any of whose literal arguments -- a literal
// operand of a + concatenation included -- contains "/dev/tcp/" or
// "/dev/udp/" (a networked program run by exec is a socket just the same). The
// call is resolved through the file's imports, so an aliased import counts. The
// scan is syntactic: a program or argument that is not a literal is not judged. The remedies are never refused: a conn comes from
// net.Pipe, a handler is proved with httptest.NewRecorder, a client with an
// in-process RoundTripper; a test whose subject is the real socket moves under
// //go:build functional.
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
	"net":               {"Listen", "ListenPacket", "ListenTCP", "ListenUnix", "ListenUDP", "ListenUnixgram", "ListenIP", "ListenMulticastUDP", "Dial", "DialTimeout", "DialTCP", "DialUDP", "DialUnix", "DialIP"},
	"net/http/httptest": {"NewServer", "NewTLSServer", "NewUnstartedServer"},
}

// unitSocketsSharedNet are the added net shapes unit-rule already carries. That
// rule reads this scan only outside cmd/, internal/, pkg/ and tools/ (the unit
// tier), where it carries these shapes itself; reading them here too would
// count one call twice. Inside the unit tier unit-sockets is the only reader.
var unitSocketsSharedNet = map[string]bool{
	"DialTCP": true, "DialUDP": true, "DialUnix": true, "DialIP": true,
	"ListenUDP": true, "ListenIP": true, "ListenMulticastUDP": true,
}

// unitSocketsNetworkPrograms are the programs whose run by os/exec is a socket:
// each speaks to the network itself.
var unitSocketsNetworkPrograms = map[string]bool{
	"curl": true, "wget": true, "nc": true, "ncat": true, "netcat": true,
	"socat": true, "telnet": true, "ssh": true, "scp": true, "redis-cli": true,
}

// unitSocketsRemedy is the one thing to do about a unit test that opens a
// socket.
const unitSocketsRemedy = "test the logic through a seam: the handler with httptest.NewRecorder, the client with an in-process RoundTripper, a conn with net.Pipe; a test whose subject is the real socket moves under //go:build functional"

// unitSocketSite is one refused call: the line it stands on and the call as
// the file spells it, through its own name for the import.
type unitSocketSite struct {
	Line int
	What string
}

// unitSocketsIn scans one parsed test file and returns the refused socket
// calls it makes (docs/SPEC-CI.md, `unit-sockets`), resolved through the file's
// imports so an aliased import counts. It reads calls only: a name used as a
// value, a comment or a string literal is not a socket. net.Pipe and
// httptest.NewRecorder are the remedies and are never refused. An os/exec
// Command or CommandContext is refused when its program is a literal networked
// tool, or when a literal argument -- a literal operand of a + concatenation
// included -- contains "/dev/tcp/" or "/dev/udp/". The scan is syntactic: a
// program or argument that is not a literal is not judged.
//
// The seven shared net shapes (unitSocketsSharedNet) are read only for a file in
// the unit tier, judged by the name the file was parsed under; a caller that
// parses under an absolute path and knows the file is in the tier calls
// unitSocketsInTier with true.
func unitSocketsIn(fset *token.FileSet, file *ast.File) []unitSocketSite {
	return unitSocketsInTier(fset, file, unitSocketsInUnitDir(fset.Position(file.Pos()).Filename))
}

// unitSocketsInTier is unitSocketsIn with the unit-tier question answered by the
// caller: false drops the shared net shapes unit-rule carries itself.
func unitSocketsInTier(fset *token.FileSet, file *ast.File, inTier bool) []unitSocketSite {
	local := make(map[string]string, len(unitSocketsRefused))
	for p := range unitSocketsRefused {
		if name := importName(file, p); name != "" {
			local[name] = p
		}
	}
	execName := importName(file, "os/exec")
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
		if p, ok := local[id.Name]; ok {
			for _, fn := range unitSocketsRefused[p] {
				if sel.Sel.Name != fn {
					continue
				}
				if unitSocketsSharedNet[fn] && !inTier {
					continue
				}
				out = append(out, unitSocketSite{Line: fset.Position(call.Pos()).Line, What: id.Name + "." + fn})
			}
			return true
		}
		if execName == "" || id.Name != execName || (sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext") {
			return true
		}
		first := 0
		if sel.Sel.Name == "CommandContext" {
			first = 1
		}
		if len(call.Args) <= first {
			return true
		}
		prog, ok := stringLit(call.Args[first])
		if !ok {
			return true
		}
		what := id.Name + "." + sel.Sel.Name + " " + prog
		if unitSocketsNetworkPrograms[filepath.Base(prog)] {
			out = append(out, unitSocketSite{Line: fset.Position(call.Pos()).Line, What: what})
			return true
		}
		for _, arg := range call.Args[first+1:] {
			hit := false
			for _, v := range stringLitParts(arg) {
				switch {
				case strings.Contains(v, "/dev/tcp/"):
					out = append(out, unitSocketSite{Line: fset.Position(call.Pos()).Line, What: what + " /dev/tcp"})
					hit = true
				case strings.Contains(v, "/dev/udp/"):
					out = append(out, unitSocketSite{Line: fset.Position(call.Pos()).Line, What: what + " /dev/udp"})
					hit = true
				}
				if hit {
					break
				}
			}
			if hit {
				break
			}
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// stringLitParts returns the string-literal parts of an expression the way the
// exec branch judges an argument: a string literal itself, and the literal
// operands of a + concatenation. A non-literal operand is not judged; the scan
// is syntactic.
func stringLitParts(e ast.Expr) []string {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			if s, err := strconv.Unquote(v.Value); err == nil {
				return []string{s}
			}
		}
		return nil
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return nil
		}
		return append(stringLitParts(v.X), stringLitParts(v.Y)...)
	}
	return nil
}

// unitSocketsInUnitDir reports whether rel is a file under one of the trees the
// unit-sockets rule scans (unitWaitDirs).
func unitSocketsInUnitDir(rel string) bool {
	for _, d := range unitWaitDirs {
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

// TestNoUnitTestOpensASocket holds every _test.go the unit tier compiles
// (docs/STANDARD.md sections 8 and 9, rule 2) to the shrink-only unit-sockets
// ledger: a socket call is refused naming file:line, and the ledger only
// shrinks. The files are read from the shared tree, and unitTierFile decides
// whether the unit suite compiles each file (docs/SPEC-CI.md, `unit-sockets`).
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
		if !unitTierFile(f.Src) {
			continue
		}
		files++
		for _, s := range unitSocketsInTier(tree.FSet, f.AST, true) {
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
// functional-tagged file is skipped whole, and the fixed fixture is clean
// (docs/SPEC-CI.md, `unit-sockets`).
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

	for src, unit := range map[string]bool{
		"package p\n":                           true,
		"//go:build functional\n\npackage p\n":  false,
		"//go:build slow\n\npackage p\n":        false,
		"//go:build perf\n\npackage p\n":        false,
		"//go:build nightly\n\npackage p\n":     false,
		"//go:build !functional\n\npackage p\n": true,
		"//go:build darwin\n\npackage p\n":      true,
		"//go:build linux\n\npackage p\n":       true,
		"//go:build windows\n\npackage p\n":     false,
	} {
		assert.Equalf(t, unit, unitTierFile([]byte(src)), "unitTierFile(%q)", src)
	}
	_, err = parser.ParseFile(token.NewFileSet(), rel, "//go:build functional\n\n"+offending, 0)
	require.NoError(t, err)
	assert.False(t, unitTierFile([]byte("//go:build functional\n\n"+offending)), "a functional-tagged fixture is skipped whole, not scanned")
}

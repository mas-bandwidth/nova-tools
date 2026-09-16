package main

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sessionOKLine is the spec's own SESSION OK grammar line (docs/SPEC-WORK.md,
// Output grammar: "SESSION OK is one shape, printed by session start, session
// status and session stop alike") with every field instantiated once, so the
// byte-for-byte pass-through tests compare against the shape the spec prints
// and not a shape this package invented.
const sessionOKLine = "SESSION OK session=/sessions/alpha.sock owner=rowan generation=3 state=live until=2026-09-16T18:00:00Z file=snapshot.sexp base=8f14e45fceea167a5a36dedd4bea2543f7e1d1f journal=/sessions/alpha.journal events=7 pending=0 pushed=- nodes=5 edges=8 parses=0 replays=0 every=5m skew=30s clip-every=15m clip-after=3 retain=48h index-cache=64 page-bytes=262144 page-records=512 closed-window=24h max-bytes=1048576 max-depth=64 max-nodes=100000 boundary=f3d2e1c findings=0 build=devel emitted=934"

// fakeSession is the S1 endpoint's stand-in while the Lisp endpoint lands in
// parallel: a Unix socket that reads ONE request line and answers ONE reply
// line, newline-terminated both ways.
//
// The socket is bound and dialled by RELATIVE name with this process's cwd
// inside the test's own t.TempDir(), the house way around sun_path: a
// Unix-domain socket's path is capped (104 bytes on darwin, 107 on linux) and
// the absolute spelling of a t.TempDir() outgrows it, so an absolute bind
// fails with "invalid argument" -- the same reason cmd/nova-sandbox's tests
// bind and dial the agent socket by relative name. t.Chdir restores the cwd
// before the directory is removed. The client is handed the same relative
// spelling, which is how a caller sitting in that directory would name it.
func fakeSession(t *testing.T, reply string) (socket string, requests <-chan string) {
	t.Helper()
	t.Chdir(t.TempDir())
	ln, err := net.Listen("unix", "session.sock")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ch := make(chan string, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return
				}
				ch <- strings.TrimSuffix(line, "\n")
				c.Write([]byte(reply + "\n"))
			}(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return "session.sock", ch
}

func awaitRequest(t *testing.T, requests <-chan string) string {
	t.Helper()
	select {
	case r := <-requests:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("no request reached the session")
		return ""
	}
}

func TestVersionPrintsTheBuildIdentity(t *testing.T) {
	for _, verb := range []string{"version", "--version"} {
		t.Run(verb, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{verb}, &stdout, &stderr, "v1.2.3-rc1+build.7"); code != 0 {
				t.Fatalf("version exit = %d, stderr = %s", code, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("version wrote stderr: %q", stderr.String())
			}
			line := stdout.String()
			if strings.Count(line, "\n") != 1 {
				t.Fatalf("version is not one line: %q", line)
			}
			fields := strings.Fields(strings.TrimSuffix(line, "\n"))
			if len(fields) != 4 {
				t.Fatalf("version fields = %d, want 4: %q", len(fields), line)
			}
			if fields[0] != "nova-work" || fields[1] != "v1.2.3-rc1+build.7" {
				t.Fatalf("version identity = %q, want nova-work v1.2.3-rc1+build.7", line)
			}
			if fields[2] != runtime.GOOS+"/"+runtime.GOARCH || fields[3] != runtime.Version() {
				t.Fatalf("version platform = %q", line)
			}
		})
	}
}

func TestSessionStatusPrintsTheSessionsLineByteForByte(t *testing.T) {
	socket, requests := fakeSession(t, sessionOKLine)
	var stdout, stderr bytes.Buffer
	code := run([]string{"session", "status", "--session", socket}, &stdout, &stderr, "")
	if code != 0 {
		t.Fatalf("session status exit = %d, stderr = %s", code, stderr.String())
	}
	if got, want := stdout.String(), sessionOKLine+"\n"; got != want {
		t.Fatalf("session status stdout = %q, want byte for byte %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("session status wrote stderr: %q", stderr.String())
	}
	if got, want := awaitRequest(t, requests), "session status --session "+socket; got != want {
		t.Fatalf("request line = %q, want %q", got, want)
	}
}

func TestRefusalLinesExitOne(t *testing.T) {
	// A refusal is the session answering NO -- the spec's exit 1 ("it ran and
	// said no", a refused take among them) -- and the card's test says so: a
	// refusal line exits 1, on stderr, byte for byte.
	refusals := []string{
		"SESSION REFUSED session=/sessions/alpha.sock: journal held by pid 4242 on /sessions/alpha.journal",
		"SESSION FAIL session=/sessions/alpha.sock owner=rowan generation=3: socket held by pid 4242",
	}
	for _, reply := range refusals {
		t.Run(strings.Fields(reply)[1], func(t *testing.T) {
			socket, _ := fakeSession(t, reply)
			var stdout, stderr bytes.Buffer
			code := run([]string{"session", "status", "--session", socket}, &stdout, &stderr, "")
			if code != 1 {
				t.Fatalf("refusal exit = %d, want 1", code)
			}
			if stdout.Len() != 0 {
				t.Fatalf("refusal wrote stdout: %q", stdout.String())
			}
			if got, want := stderr.String(), reply+"\n"; got != want {
				t.Fatalf("refusal stderr = %q, want byte for byte %q", got, want)
			}
		})
	}
}

func TestMissingSocketExitsTwoWithTheSpecsRemedy(t *testing.T) {
	// The spec's exit 2 is "could not run (... no such session ...), which
	// costs one line ending `run: nova-work help`" -- that line is the remedy.
	// The relative name never exists, so the dial is an honest missing socket.
	absent := "absent.sock"
	var stdout, stderr bytes.Buffer
	code := run([]string{"session", "status", "--session", absent}, &stdout, &stderr, "")
	if code != 2 {
		t.Fatalf("missing socket exit = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("missing socket wrote stdout: %q", stdout.String())
	}
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "run: nova-work help") {
		t.Fatalf("missing socket refusal = %q, want one line ending \"run: nova-work help\"", stderr.String())
	}
	if !strings.Contains(lines[0], "no such session") {
		t.Fatalf("missing socket refusal = %q, want it naming no such session", lines[0])
	}
}

func TestHelpListsEveryVerbTheSwitchAccepts(t *testing.T) {
	verbs := switchVerbs(t)
	want := []string{"help", "query", "render", "session start", "session status", "session stop", "version"}
	if got := strings.Join(verbs, ","); got != strings.Join(want, ",") {
		t.Fatalf("the switch accepts %q, want exactly %q", got, strings.Join(want, ","))
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("help exit = %d, stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("help wrote stderr: %q", stderr.String())
	}
	help := stdout.String()
	for _, v := range verbs {
		if !strings.Contains(help, "nova-work "+v) {
			t.Errorf("help does not list the switch's verb %q", v)
		}
	}
}

// switchVerbs walks the verb switch in main.go the way a reader does: the
// first switch statement in run's body is the verb switch; the case whose
// value is "session" holds the sub-verb switch; alias spellings (--help, -h)
// share their case's first value and are not verbs of their own.
func switchVerbs(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	var runBody ast.Node
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "run" && fn.Body != nil {
			runBody = fn.Body
		}
	}
	if runBody == nil {
		t.Fatal("no function run in main.go")
	}
	top := firstSwitch(runBody)
	if top == nil {
		t.Fatal("run's body holds no verb switch")
	}
	var verbs []string
	for _, c := range top.Body.List {
		cl, ok := c.(*ast.CaseClause)
		if !ok {
			continue
		}
		names := caseStrings(cl)
		if len(names) == 0 {
			continue
		}
		if names[0] == "session" {
			if inner := firstSwitch(cl); inner != nil {
				for _, c2 := range inner.Body.List {
					if cl2, ok := c2.(*ast.CaseClause); ok {
						for _, n := range caseStrings(cl2) {
							verbs = append(verbs, "session "+n)
						}
					}
				}
			}
			continue
		}
		verbs = append(verbs, names[0])
	}
	sort.Strings(verbs)
	return verbs
}

func firstSwitch(body ast.Node) *ast.SwitchStmt {
	var found *ast.SwitchStmt
	ast.Inspect(body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if sw, ok := n.(*ast.SwitchStmt); ok {
			found = sw
			return false
		}
		return true
	})
	return found
}

func caseStrings(cl *ast.CaseClause) []string {
	var out []string
	for _, e := range cl.List {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		if s, err := strconv.Unquote(lit.Value); err == nil {
			out = append(out, s)
		}
	}
	return out
}

func TestSessionStartAndStopSpellTheirRequestLines(t *testing.T) {
	socket, requests := fakeSession(t, sessionOKLine)

	var stdout, stderr bytes.Buffer
	code := run([]string{"session", "start",
		"--session", socket,
		"--as", "Rowan Jr",
		"--file", "snapshot.sexp",
		"--render-root", "docs=mas-bandwidth/nova-tools:docs",
		"--render-root", "spec=mas-bandwidth/nova-tools:spec",
		"--resolver", "git=./fetch.sh",
		"--git-timeout", "30",
		"--repair",
	}, &stdout, &stderr, "")
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("session start exit=%d stderr=%q", code, stderr.String())
	}
	if got, want := stdout.String(), sessionOKLine+"\n"; got != want {
		t.Fatalf("session start stdout = %q, want the session's own line %q", got, want)
	}
	want := "session start --session " + socket +
		" --as Rowan\\x20Jr --file snapshot.sexp" +
		" --render-root docs\\x3dmas-bandwidth/nova-tools:docs" +
		" --render-root spec\\x3dmas-bandwidth/nova-tools:spec" +
		" --resolver git\\x3d./fetch.sh --git-timeout 30 --repair true"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("start request line = %q, want %q", got, want)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"session", "stop", "--session", socket, "--git-timeout", "45", "--no-clip"}, &stdout, &stderr, "")
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("session stop exit=%d stderr=%q", code, stderr.String())
	}
	if got, want := awaitRequest(t, requests), "session stop --session "+socket+" --git-timeout 45 --no-clip true"; got != want {
		t.Fatalf("stop request line = %q, want %q", got, want)
	}
}

func TestCLIDocCarriesTheHelpBlockByteForByte(t *testing.T) {
	if strings.TrimSpace(usage) == "" {
		t.Fatal("the usage block is empty")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatalf("read docs/CLI.md: %v", err)
	}
	i := strings.Index(string(raw), "## nova-work")
	if i < 0 {
		t.Fatal("docs/CLI.md has no nova-work section")
	}
	section := string(raw[i:])
	if j := strings.Index(section, "\n## "); j >= 0 {
		section = section[:j]
	}
	if !strings.Contains(section, usage) {
		t.Fatal("docs/CLI.md's nova-work section does not carry the help block byte for byte")
	}
}

func TestUnusableInvocationsAreRefusedAtTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no arguments at all", nil, "a verb is required"},
		{"unknown verb", []string{"nodes"}, "unknown verb"},
		{"unknown session verb", []string{"session", "bogus"}, "unknown session verb"},
		{"session without a subverb", []string{"session"}, "start, status or stop"},
		{"missing --session refuses to guess", []string{"session", "status"}, "refusing to guess"},
		{"unknown flag", []string{"session", "status", "--session", "x", "--bogus"}, "flag provided but not defined"},
		{"positional arguments", []string{"session", "status", "--session", "x", "extra"}, "no positional arguments"},
		{"version takes no arguments", []string{"version", "extra"}, "version takes no arguments"},
		{"help takes no arguments", []string{"help", "extra"}, "help takes no arguments"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(c.args, &stdout, &stderr, "")
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (stderr %q)", code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("refusal wrote stdout: %q", stdout.String())
			}
			lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
			if len(lines) != 1 {
				t.Fatalf("refusal is %d lines, want 1: %q", len(lines), stderr.String())
			}
			if !strings.HasSuffix(lines[0], "run: nova-work help") {
				t.Fatalf("refusal %q does not end with the remedy", lines[0])
			}
			if !strings.Contains(lines[0], c.want) {
				t.Fatalf("refusal %q does not name %q", lines[0], c.want)
			}
		})
	}
}

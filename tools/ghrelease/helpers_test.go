package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeRunner answers the programs a verb runs without starting one.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []command
	output func(c command) (string, int)
	stream func(c command, stdout, stderr io.Writer) int
}

func (f *fakeRunner) Output(c command) (string, int) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if f.output == nil {
		return "", 0
	}
	return f.output(c)
}

func (f *fakeRunner) Stream(c command, stdout, stderr io.Writer) int {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if f.stream == nil {
		return 0
	}
	return f.stream(c, stdout, stderr)
}

func (f *fakeRunner) called() []command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]command(nil), f.calls...)
}

// fakeGH answers `gh api` from a function and records every call, `gh release`
// calls included; it never reaches GitHub and never uploads anything.
type fakeGH struct {
	mu        sync.Mutex
	api       func(args []string) (string, int)
	releaseRC int
	apis      [][]string
	releases  [][]string
}

func (g *fakeGH) API(args ...string) (string, int) {
	g.mu.Lock()
	g.apis = append(g.apis, args)
	g.mu.Unlock()
	if g.api == nil {
		return "", 1
	}
	return g.api(args)
}

func (g *fakeGH) Release(args ...string) int {
	g.mu.Lock()
	g.releases = append(g.releases, args)
	g.mu.Unlock()
	return g.releaseRC
}

// harness is one verb run against a temp directory and a fake environment.
type harness struct {
	t      *testing.T
	e      env
	dir    string
	out    *bytes.Buffer
	errb   *bytes.Buffer
	vars   map[string]string
	gh     *fakeGH
	runner *fakeRunner
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), out: &bytes.Buffer{}, errb: &bytes.Buffer{}, vars: map[string]string{}, gh: &fakeGH{}, runner: &fakeRunner{}}
	h.e = env{
		stdout:  h.out,
		stderr:  h.errb,
		getenv:  func(k string) string { return h.vars[k] },
		dir:     h.dir,
		run:     h.runner,
		gh:      h.gh,
		targets: []target{{"linux", "amd64"}},
	}
	return h
}

// do runs a verb and returns its exit code.
func (h *harness) do(args ...string) int { return run(args, h.e) }

// all is everything the run printed, both streams.
func (h *harness) all() string { return h.out.String() + h.errb.String() }

func (h *harness) write(rel, content string, mode os.FileMode) string {
	h.t.Helper()
	p := filepath.Join(h.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		h.t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		h.t.Fatal(err)
	}
	return p
}

// tools makes cmd/<name>/ directories in the harness's tree.
func (h *harness) tools(names ...string) {
	h.t.Helper()
	for _, n := range names {
		h.write("cmd/"+n+"/main.go", "package main\n", 0o644)
	}
}

func (h *harness) mustContain(s string) {
	h.t.Helper()
	if !strings.Contains(h.all(), s) {
		h.t.Fatalf("output does not contain %q:\n%s", s, h.all())
	}
}

func (h *harness) mustNotContain(s string) {
	h.t.Helper()
	if strings.Contains(h.all(), s) {
		h.t.Fatalf("output contains %q:\n%s", s, h.all())
	}
}

func (h *harness) wantRC(got, want int) {
	h.t.Helper()
	if got != want {
		h.t.Fatalf("exit %d, want %d; output:\n%s", got, want, h.all())
	}
}

func fixture(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// httpAnswer is what `gh api -i` prints: one status line, headers, a blank line,
// the body.
func httpAnswer(code, reason, body string) string {
	return "HTTP/2 " + code + " " + reason + "\r\ndate: a-fake-gh-does-not-have-dates\r\n\r\n" + body
}

func removeFile(p string) error { return os.Remove(p) }

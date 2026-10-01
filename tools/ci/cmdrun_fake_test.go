package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeCmdRunner is the one fake cmdRunner: it records every command a verb runs
// and answers it from answer, so a test drives a verb with no program run, no
// package installed and no network reached. A command answer does not know
// exits 0 with no output; answer may write c.Stderr itself.
type fakeCmdRunner struct {
	mu       sync.Mutex
	calls    []cmdSpec
	answer   func(c cmdSpec) (stdout string, code int, err error)
	onPath   map[string]string // program name -> its path; a name not here is not on PATH
	prepends []string
}

func (f *fakeCmdRunner) Run(c cmdSpec) (int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	answer := f.answer
	f.mu.Unlock()
	if answer == nil {
		return 0, nil
	}
	out, code, err := answer(c)
	if out != "" && c.Stdout != nil {
		io.WriteString(c.Stdout, out)
	}
	if err != nil {
		return -1, err
	}
	return code, nil
}

func (f *fakeCmdRunner) LookPath(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.onPath[name]; ok {
		return p, nil
	}
	return "", fmt.Errorf("%s: not found in PATH", name)
}

func (f *fakeCmdRunner) PrependPath(dir string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepends = append(f.prepends, dir)
}

// lines is every command run, one "name arg arg" string each.
func (f *fakeCmdRunner) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(append([]string{c.Name}, c.Args...), " "))
	}
	return out
}

func (f *fakeCmdRunner) ran(prefix string) bool {
	for _, l := range f.lines() {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func TestCaptureTrimsTrailingNewlinesAndPassesTheCode(t *testing.T) {
	t.Parallel()
	f := &fakeCmdRunner{answer: func(c cmdSpec) (string, int, error) { return "a\nb\n\n", 3, nil }}
	out, code, err := capture(f, cmdLine("", nil, nil, nil, "prog", "x"))
	if out != "a\nb" || code != 3 || err != nil {
		t.Fatalf("capture: %q %d %v", out, code, err)
	}
	if got := f.lines(); len(got) != 1 || got[0] != "prog x" {
		t.Fatalf("capture ran %v", got)
	}
}

func TestAppendGitHubFileAppendsALineOrSaysTheNameIsUnset(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "out")
	get := func(k string) string {
		if k == "GITHUB_OUTPUT" {
			return p
		}
		return ""
	}
	for _, l := range []string{"a=1", "b=2"} {
		if err := appendGitHubFile(get, "GITHUB_OUTPUT", l); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(p); string(b) != "a=1\nb=2\n" {
		t.Fatalf("GITHUB_OUTPUT holds %q", b)
	}
	err := appendGitHubFile(get, "GITHUB_PATH", "/x")
	if err == nil || !strings.Contains(err.Error(), "GITHUB_PATH is not set") {
		t.Fatalf("unset name: %v", err)
	}
}

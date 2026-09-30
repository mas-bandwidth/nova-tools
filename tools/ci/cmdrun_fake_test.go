package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// fakeCmdRunner records every command a verb runs and answers it from a script,
// so a test drives a verb with no program run, no package installed and no
// network reached. A command the script does not answer exits 0 with no output.
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
	f.mu.Unlock()
	if f.answer == nil {
		return 0, nil
	}
	out, code, err := f.answer(c)
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

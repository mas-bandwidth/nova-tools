// A fake for every program nova-pulse starts -- gh, git, nova-swarm, nova-bus and
// nova-pulse itself. A test that shells out to the REAL gh is a test with a forge and a
// credential in it: on this bench it passes because the bench is logged in, and on a
// hosted windows-latest runner it fails with `gh auth` (#windows-leg). So every child is
// this program, and nothing here reaches a network.
//
// It is a Go program rather than a POSIX sh script because this repo's CI runs on Windows,
// where `#!/bin/sh` is not a thing a file can be: the shell fixtures this replaced were
// simply skipped by the PATH lookup and the real tool answered instead. That silence is
// the whole bug, so the fake is LOUD: a name it has no spec for exits 97 and says so,
// which no real gh or git ever does.
//
// One directory, named in NOVA_PULSE_FAKE_DIR. The behaviour of the program invoked as
// <name> is read from <dir>/<name>.json:
//
//	{
//	  "log":   "<path>",        // every invocation appended as "<name> <args...>"
//	  "rules": [ {"arg":2, "equals":"view", "stdout":"...", "exit":0}, ... ],
//	  "default": {"exit":0}
//	}
//
// `arg` indexes os.Args, so `arg:1` is a shell fixture's $1 and `arg:2` its $2; `arg:0`
// matches anything. The first matching rule answers; `default` answers when none does.
// `stdout` is written with a trailing newline (what `echo` does), `stdoutFile` byte for
// byte (what `cat` does).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type rule struct {
	Arg        int    `json:"arg,omitempty"`
	Equals     string `json:"equals,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	StdoutFile string `json:"stdoutFile,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	Exit       int    `json:"exit,omitempty"`
}

type spec struct {
	Log     string `json:"log,omitempty"`
	Rules   []rule `json:"rules,omitempty"`
	Default rule   `json:"default"`
}

// noSpec is the exit code for a fake nobody configured. It is deliberately not 0, 1 or 2:
// a test that sees it is looking at the fake, not at the tool under test.
const noSpec = 97

func main() {
	name := toolName()
	dir := os.Getenv("NOVA_PULSE_FAKE_DIR")
	if dir == "" {
		fmt.Fprintf(os.Stderr, "the fake %s wants NOVA_PULSE_FAKE_DIR\n", name)
		os.Exit(noSpec)
	}
	path := filepath.Join(dir, name+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the fake %s has no spec at %s: %v\n", name, path, err)
		os.Exit(noSpec)
	}
	// A per-clone override, read from <cwd>/.fake/<name>.json, lets two jobs in
	// one run answer differently through identical argv: the caller's Dir is the
	// job's own clone, so the cwd is the only thing that differs per job.
	if cwd, werr := os.Getwd(); werr == nil {
		if over, rerr := os.ReadFile(filepath.Join(cwd, ".fake", name+".json")); rerr == nil {
			raw = over
		}
	}
	var s spec
	if err := json.Unmarshal(raw, &s); err != nil {
		fmt.Fprintf(os.Stderr, "the fake %s cannot read %s: %v\n", name, path, err)
		os.Exit(noSpec)
	}

	args := os.Args[1:]
	if s.Log != "" {
		appendLine(s.Log, strings.TrimRight(name+" "+strings.Join(args, " "), " "))
	}

	r := s.Default
	for _, cand := range s.Rules {
		if cand.Arg <= 0 || (cand.Arg < len(os.Args) && os.Args[cand.Arg] == cand.Equals) {
			r = cand
			break
		}
	}
	if r.StdoutFile != "" {
		out, err := os.ReadFile(r.StdoutFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "the fake %s cannot read %s: %v\n", name, r.StdoutFile, err)
			os.Exit(noSpec)
		}
		os.Stdout.Write(out)
	}
	if r.Stdout != "" {
		out := r.Stdout
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		fmt.Print(out)
	}
	if r.Stderr != "" {
		out := r.Stderr
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		fmt.Fprint(os.Stderr, out)
	}
	os.Exit(r.Exit)
}

// toolName is the name the caller started this program by, with Windows's extension
// removed: one binary, copied under five names, answers as whichever of them it was
// invoked as. The suffix is trimmed case-INSENSITIVELY, because Windows resolves a bare
// `gh` through PATHEXT, whose entries are conventionally spelt .EXE.
func toolName() string {
	name := ""
	if exe, err := os.Executable(); err == nil {
		name = filepath.Base(exe)
	}
	if name == "" || name == "." {
		name = filepath.Base(os.Args[0])
	}
	if ext := filepath.Ext(name); strings.EqualFold(ext, ".exe") {
		name = name[:len(name)-len(ext)]
	}
	return name
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

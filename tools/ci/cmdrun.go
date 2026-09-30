package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cmdSpec is one command a verb runs: the program, its arguments, the
// directory it runs in ("" is the process's own), the variables added to the
// inherited environment (KEY=VALUE, a later one wins), and where its output
// goes (a nil writer discards that stream).
type cmdSpec struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string
	Stdout io.Writer
	Stderr io.Writer
}

// cmdRunner is every way a verb touches the machine outside itself: the
// programs it runs, the PATH it searches, and the PATH later steps of its
// process inherit. A verb that installs or pushes takes one, so a test drives
// the verb with a fake that records each command and never runs one.
type cmdRunner interface {
	// Run runs c and returns its exit code. err is non-nil only when the
	// program could not be started at all (not found, not executable); the
	// code is then -1.
	Run(c cmdSpec) (code int, err error)
	// LookPath finds a program on PATH.
	LookPath(name string) (string, error)
	// PrependPath puts dir first on the PATH this process and its children
	// see.
	PrependPath(dir string)
}

// osCmdRunner is the real cmdRunner: os/exec and the process's own PATH.
type osCmdRunner struct{}

func (osCmdRunner) Run(c cmdSpec) (int, error) {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr
	cmd.Stdin = nil
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), nil
	}
	return -1, err
}

func (osCmdRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (osCmdRunner) PrependPath(dir string) {
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// capture runs c with its stdout captured and returns it with the trailing
// newlines taken off, the way a shell command substitution reads it. stderr
// goes where c.Stderr says.
func capture(r cmdRunner, c cmdSpec) (out string, code int, err error) {
	var b strings.Builder
	c.Stdout = &b
	code, err = r.Run(c)
	return strings.TrimRight(b.String(), "\n"), code, err
}

// appendPathFile adds dir as a line of the file GITHUB_PATH names, which is how
// a step hands a directory to the steps after it. An unset name is not an
// error: outside a workflow there is no later step to hand it to.
func appendPathFile(getenv func(string) string, dir string) error {
	name := getenv("GITHUB_PATH")
	if name == "" {
		return nil
	}
	f, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(f, dir+"\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// dirOf is the directory of a program path.
func dirOf(p string) string { return filepath.Dir(p) }

package main

// cmdrun.go is the one way a verb of this tool reaches outside itself: the
// programs it runs (cmdRunner, with its one real form osCmdRunner and the one
// fake in cmdrun_fake_test.go), the one capture of a program's output, and the
// one writer of the files GitHub Actions reads back between steps. Every verb
// takes a cmdRunner, so a test drives it with no program started.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
// process inherit.
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
	if c.Name == "" {
		return -1, errors.New("no command")
	}
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit):
		return exit.ExitCode(), nil
	}
	return -1, err
}

func (osCmdRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (osCmdRunner) PrependPath(dir string) {
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// cmdLine is the cmdSpec of a command line: argv[0] the program, the rest its
// arguments.
func cmdLine(dir string, env []string, stdout, stderr io.Writer, args ...string) cmdSpec {
	c := cmdSpec{Dir: dir, Env: env, Stdout: stdout, Stderr: stderr}
	if len(args) > 0 {
		c.Name, c.Args = args[0], args[1:]
	}
	return c
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

// errNoGitHubFile is appendGitHubFile's answer when the variable naming the
// file is unset: outside a workflow there is no later step to hand a value to.
var errNoGitHubFile = errors.New("not set")

// appendGitHubFile appends line to the file the environment variable name
// points at, which is how a step hands a value (GITHUB_OUTPUT, GITHUB_ENV) or
// a directory (GITHUB_PATH) to the steps after it. An unset name is
// errNoGitHubFile, wrapped with the name; the caller decides whether that is a
// refusal or nothing to do.
func appendGitHubFile(getenv func(string) string, name, line string) error {
	path := getenv(name)
	if path == "" {
		return fmt.Errorf("%s is %w: there is no file to hand %q to the next steps through", name, errNoGitHubFile, line)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(f, line+"\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

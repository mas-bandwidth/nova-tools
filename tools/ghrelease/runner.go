package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
)

// command is one program run: where, with which added variables, what.
type command struct {
	dir  string
	env  []string // KEY=VALUE pairs added to the process's own environment
	name string
	args []string
}

// runner starts the programs a verb runs. Production runs them through the
// operating system; a test hands in a fake that answers without starting one.
type runner interface {
	// Output runs c and returns its standard output and standard error
	// together and its exit code. A program that cannot be started is exit
	// code 127 with the reason as its output.
	Output(c command) (out string, rc int)
	// Stream runs c with its output on the given writers and returns its exit
	// code, 127 when the program cannot be started.
	Stream(c command, stdout, stderr io.Writer) (rc int)
}

type osRunner struct{}

// cmd prepares c through the one door every child of this repository goes
// through (pkg/subproc): the budget of its kind (git, gh, go, any other
// tool) and a bounded wait for its pipes once it ends.
func (osRunner) cmd(c command) subproc.Bounded {
	b := subproc.Prepare(context.Background(), subproc.BudgetOf(c.name, c.args), c.name, c.args...)
	b.Cmd.Dir = c.dir
	if len(c.env) > 0 {
		b.Cmd.Env = append(os.Environ(), c.env...)
	}
	return b
}

// finish is a run's exit code, and what to say when it had none: a program
// that could not start is 127 with the reason, a child its budget ended is 124.
func finish(b subproc.Bounded, err error) (int, string) {
	err = b.Wrap(b.Cmd.Path, err)
	b.Cancel()
	if err == nil {
		return 0, ""
	}
	var te *subproc.TimeoutError
	if errors.As(err, &te) {
		return 124, err.Error()
	}
	if rc := exitCode(err); rc != 127 {
		return rc, ""
	}
	return 127, err.Error()
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 127
}

func (r osRunner) Output(c command) (string, int) {
	b := r.cmd(c)
	var out bytes.Buffer
	b.Cmd.Stdout, b.Cmd.Stderr = &out, &out
	rc, why := finish(b, b.Cmd.Run())
	out.WriteString(why)
	return out.String(), rc
}

func (r osRunner) Stream(c command, stdout, stderr io.Writer) int {
	b := r.cmd(c)
	b.Cmd.Stdout, b.Cmd.Stderr = stdout, stderr
	rc, why := finish(b, b.Cmd.Run())
	if why != "" {
		_, _ = io.WriteString(stderr, why+"\n") // ignored: the exit code is the report
	}
	return rc
}

func (e env) runner() runner {
	if e.run != nil {
		return e.run
	}
	return osRunner{}
}

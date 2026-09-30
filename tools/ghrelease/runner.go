package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
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

func (osRunner) cmd(c command) *exec.Cmd {
	cmd := exec.Command(c.name, c.args...)
	cmd.Dir = c.dir
	if len(c.env) > 0 {
		cmd.Env = append(os.Environ(), c.env...)
	}
	return cmd
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
	cmd := r.cmd(c)
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	if rc := exitCode(err); rc == 127 {
		b.WriteString(err.Error())
		return b.String(), rc
	} else {
		return b.String(), rc
	}
}

func (r osRunner) Stream(c command, stdout, stderr io.Writer) int {
	cmd := r.cmd(c)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	rc := exitCode(err)
	if rc == 127 {
		io.WriteString(stderr, err.Error()+"\n")
	}
	return rc
}

func (e env) runner() runner {
	if e.run != nil {
		return e.run
	}
	return osRunner{}
}

package main

// sel_host.go is what the package-selection verbs reach outside themselves
// through: the processes they start, the core count, the HTTP client and the
// files GitHub Actions reads back (GITHUB_OUTPUT, GITHUB_ENV, GITHUB_PATH).
// A verb takes a selHost, so a test runs it with none of the machine's own.

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

// selStream runs argv in dir with env added to the process's own and streams
// its output to stdout and stderr. The error is only for a command that could
// not be started.
type selStream func(dir string, env []string, stdout, stderr io.Writer, argv ...string) (int, error)

// selHTTP is the one method of *http.Client a verb uses.
type selHTTP interface {
	Do(*http.Request) (*http.Response, error)
}

// selHost is everything a selection verb reaches outside itself through.
type selHost struct {
	stream   selStream
	run      pkgselect.Runner
	cores    func() int
	lookPath func(string) (string, error)
	client   selHTTP
}

func selExecStream(dir string, env []string, stdout, stderr io.Writer, argv ...string) (int, error) {
	if len(argv) == 0 {
		return -1, errors.New("no command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
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

// selCapture is a pkgselect.Runner over a stream: what the command printed on
// each stream is returned, not shown.
func selCapture(stream selStream) pkgselect.Runner {
	return func(dir string, env []string, argv ...string) (pkgselect.Result, error) {
		var out, errb bytes.Buffer
		code, err := stream(dir, env, &out, &errb, argv...)
		return pkgselect.Result{Stdout: out.String(), Stderr: errb.String(), Code: code}, err
	}
}

func selRealHost() selHost {
	return selHost{
		stream:   selExecStream,
		run:      selCapture(selExecStream),
		cores:    runtime.NumCPU,
		lookPath: exec.LookPath,
		client:   http.DefaultClient,
	}
}

// selRoot is the directory a verb works in: the env's, else the process's own.
func selRoot(e env) string {
	if e.dir != "" {
		return e.dir
	}
	return "."
}

// selAppend appends line to the file the environment variable name points at:
// the way a step hands a value to the steps after it (GITHUB_OUTPUT, GITHUB_ENV)
// or a directory to PATH (GITHUB_PATH).
func selAppend(e env, name, line string) error {
	path := e.getenv(name)
	if path == "" {
		return fmt.Errorf("%s is not set: there is no file to hand %q to the next steps through", name, line)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// selFlags parses a verb's flags. It returns the exit code to end with and
// whether to end: -h prints the verb's help and ends 0; a bad flag is a
// refusal, 2.
func selFlags(e env, name string, fs *flag.FlagSet, args []string) (int, bool) {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	err := fs.Parse(args)
	switch {
	case err == nil:
		return 0, false
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(e.stdout, registry[name].help)
		return 0, true
	}
	fmt.Fprintf(e.stderr, "%s %s: %v; run: go run ./tools/ci help %s\n", tool, name, err, name)
	return 2, true
}

// selRefuse prints a refusal and returns 2.
func selRefuse(e env, name, why string) int {
	fmt.Fprintf(e.stderr, "%s %s: %s; run: go run ./tools/ci help %s\n", tool, name, why, name)
	return 2
}

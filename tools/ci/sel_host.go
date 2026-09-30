package main

// sel_host.go is what the package-selection verbs reach outside themselves
// through: the one cmdRunner (cmdrun.go) for every process they start, the core
// count, and the HTTP client. A verb takes a selHost, so a test runs it with
// none of the machine's own.

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"runtime"

	"github.com/mas-bandwidth/nova-tools/internal/pkgselect"
)

// selHTTP is the one method of *http.Client a verb uses.
type selHTTP interface {
	Do(*http.Request) (*http.Response, error)
}

// selHost is everything a selection verb reaches outside itself through.
type selHost struct {
	r      cmdRunner
	cores  func() int
	client selHTTP
}

// stream runs a command line in dir through the host's runner, its output
// going to stdout and stderr.
func (h selHost) stream(dir string, env []string, stdout, stderr io.Writer, args ...string) (int, error) {
	return h.r.Run(cmdLine(dir, env, stdout, stderr, args...))
}

// run is internal/pkgselect's Runner over the host's runner: what the command
// printed on each stream is returned whole, not shown, because the selection
// reads both (a go list failure is judged on its stderr).
func (h selHost) run(dir string, env []string, args ...string) (pkgselect.Result, error) {
	var out, errb bytes.Buffer
	code, err := h.stream(dir, env, &out, &errb, args...)
	return pkgselect.Result{Stdout: out.String(), Stderr: errb.String(), Code: code}, err
}

func selRealHost() selHost {
	return selHost{r: osCmdRunner{}, cores: runtime.NumCPU, client: http.DefaultClient}
}

// selRoot is the directory a verb works in: the env's, else the process's own.
func selRoot(e env) string {
	if e.dir != "" {
		return e.dir
	}
	return "."
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

package main

import (
	"io"
	"strings"
	"unicode"
)

// ghClient is the GitHub a verb asks. Its production form is the gh command
// line; a test hands in a fake that answers from a fixture.
type ghClient interface {
	// API runs `gh api args...` and returns gh's standard output and standard
	// error together with its exit code. A caller that wants the HTTP status
	// passes -i and reads the status line (see hasStatus): gh exits 1 on a 404
	// and on no answer at all alike, so the exit code cannot say which.
	API(args ...string) (out string, rc int)
	// Release runs `gh release args...` with gh's output on the verb's own
	// streams and returns its exit code.
	Release(args ...string) (rc int)
}

// ghCLI is the production ghClient: the gh command, run in dir, carrying the
// process's environment (GH_TOKEN and the rest).
type ghCLI struct {
	run    runner
	dir    string
	stdout io.Writer
	stderr io.Writer
}

func (g ghCLI) API(args ...string) (string, int) {
	return g.run.Output(command{dir: g.dir, name: "gh", args: append([]string{"api"}, args...)})
}

func (g ghCLI) Release(args ...string) int {
	return g.run.Stream(command{dir: g.dir, name: "gh", args: append([]string{"release"}, args...)}, g.stdout, g.stderr)
}

func (e env) client() ghClient {
	if e.gh != nil {
		return e.gh
	}
	return ghCLI{run: e.runner(), dir: e.dir, stdout: e.stdout, stderr: e.stderr}
}

// hasStatus reports whether the first line of `gh api -i` output is an HTTP
// status line carrying the code: "HTTP/2.0 200 OK". The code must be followed
// by a space, as it is in every status line that has a reason phrase, and a
// line that is not a status line at all (a gh error, an empty answer) carries
// no code.
func hasStatus(out string, code string) bool {
	first, _, _ := strings.Cut(out, "\n")
	rest, ok := strings.CutPrefix(first, "HTTP/")
	return ok && strings.Contains(rest, " "+code+" ")
}

// answerBody is what `gh api -i` printed after its headers: everything after
// the first blank line that follows the status line. A line of only white
// space is blank, and an answer with no blank line has no body.
func answerBody(out string) string {
	lines := strings.Split(out, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimFunc(lines[i], unicode.IsSpace) == "" {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return ""
}

// hasSpace reports whether s carries white space: the class that would split a
// linker flag or a path.
func hasSpace(s string) bool {
	return strings.IndexFunc(s, unicode.IsSpace) >= 0
}

// chomp is what a shell command substitution does to output: the trailing
// newlines go.
func chomp(s string) string { return strings.TrimRight(s, "\n") }

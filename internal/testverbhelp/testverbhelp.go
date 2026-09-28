// Package testverbhelp is the per-tool check of the verb-help rule
// (the CLI style's rule (b), #4505; internal/nsprint/verbflag is the one seam that
// implements it): `<tool> <verb> -h` and `--help` print that verb's help on
// stdout and exit 0, with nothing on stderr, no file written and no dial.
//
// Each tool's test names its verbs and, for each, the flags that would point
// the verb at a place: a path flag gets {dir}/..., a store address gets
// {addr}. They are given BEFORE -h, so the parser has taken them when it
// meets -h, and the check then holds that nothing was created under {dir}
// and nothing connected to {addr}, a listener the check owns.
package testverbhelp

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Run is the tool, in process: args after the tool's name, the two streams,
// the exit code.
type Run func(args []string, stdout, stderr io.Writer) int

// Case is one verb ("send", "slots take") and the flags given before -h.
// "{dir}" and "{addr}" in a flag are replaced by the case's temp directory
// and listener address.
type Case struct {
	Verb  string
	Flags []string
}

// Budget is how long help may take. Help is a print; anything that took
// longer than this dialed, waited or walked something.
const Budget = 50 * time.Millisecond

// Check runs every case with -h and with --help, in parallel subtests.
func Check(t *testing.T, run Run, cases []Case) {
	t.Helper()
	if len(cases) == 0 {
		t.Fatal("no verbs named; a check over no verbs would pass by checking nothing")
	}
	for _, c := range cases {
		for _, spelling := range []string{"-h", "--help"} {
			c, spelling := c, spelling
			t.Run(c.Verb+" "+spelling, func(t *testing.T) {
				t.Parallel()
				One(t, run, c, spelling)
			})
		}
	}
}

// One runs one case with one spelling of help and asserts the rule.
func One(t *testing.T, run Run, c Case, spelling string) {
	t.Helper()
	for _, p := range Problems(run, c, spelling, t.TempDir()) {
		t.Error(p)
	}
}

// Problems runs one case with one spelling of help, with {dir} as dir, and
// returns every way it broke the rule; none is a pass.
func Problems(run Run, c Case, spelling, dir string) []string {
	var problems []string
	fail := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	// The listener is opened only for a case that names {addr}: a bench short of
	// ephemeral ports should cost the cases that dial-check, not every case.
	var ln net.Listener
	var dialed atomic.Int64
	done := make(chan struct{})
	if strings.Contains(strings.Join(c.Flags, " "), "{addr}") {
		var err error
		if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			return []string{err.Error()}
		}
		go func() {
			defer close(done)
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				dialed.Add(1)
				_ = conn.Close()
			}
		}()
	} else {
		close(done)
	}
	args := strings.Fields(c.Verb)
	for _, f := range c.Flags {
		f = strings.ReplaceAll(f, "{dir}", dir)
		if ln != nil {
			f = strings.ReplaceAll(f, "{addr}", ln.Addr().String())
		}
		args = append(args, f)
	}
	args = append(args, spelling)
	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := run(args, &stdout, &stderr)
	took := time.Since(start)
	// A loaded bench can stall any 50 ms; help that dials or waits is slow every
	// time. So an overrun is measured up to four more times and the fastest counts.
	for i := 0; i < 4 && took > Budget; i++ {
		start = time.Now()
		run(args, io.Discard, io.Discard)
		took = min(took, time.Since(start))
	}
	if ln != nil {
		_ = ln.Close()
	}
	<-done
	if code != 0 {
		fail("%q exited %d, want 0; stderr: %s", args, code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) == "" {
		fail("%q printed nothing on stdout; help is the verb's usage, on stdout", args)
	}
	if stderr.Len() != 0 {
		fail("%q wrote to stderr: %q; help is not a refusal", args, stderr.String())
	}
	if took > Budget {
		fail("%q took %v, over %v: help ran something", args, took, Budget)
	}
	if n := dialed.Load(); n != 0 {
		fail("%q connected to %s %d time(s); help dials nothing", args, ln.Addr(), n)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		fail("%q left %v under its temp dir (err %v); help writes nothing", args, names, err)
	}
	return problems
}

// HelpVerb checks `<tool> help <verb>` for a tool whose help verb takes one:
// the same help `<verb> -h` prints, on stdout, at exit 0, nothing on stderr.
func HelpVerb(t *testing.T, run Run, tool string, verbs ...string) {
	t.Helper()
	for _, verb := range verbs {
		verb := verb
		t.Run("help "+verb, func(t *testing.T) {
			t.Parallel()
			var viaHelp, viaFlag, stderr bytes.Buffer
			args := append([]string{"help"}, strings.Fields(verb)...)
			start := time.Now()
			code := run(args, &viaHelp, &stderr)
			if took := time.Since(start); took > Budget {
				t.Errorf("%q took %v, over %v", args, took, Budget)
			}
			if code != 0 || stderr.Len() != 0 {
				t.Errorf("%q: exit %d stderr %q, want 0 and nothing", args, code, stderr.String())
			}
			if want := "usage: " + tool + " " + verb; !strings.HasPrefix(viaHelp.String(), want) {
				t.Errorf("%q printed %q, want it to begin %q", args, viaHelp.String(), want)
			}
			run(append(strings.Fields(verb), "-h"), &viaFlag, &stderr)
			if viaHelp.String() != viaFlag.String() {
				t.Errorf("`help %s` and `%s -h` differ:\n%s\n---\n%s", verb, verb, viaHelp.String(), viaFlag.String())
			}
		})
	}
}

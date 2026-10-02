// Package testverbhelp is the per-tool check of the verb-help rule
// (the CLI style's rule (b), #4505; internal/nsprint/verbflag is the one seam that
// implements it): `<tool> <verb> -h` and `--help` print that verb's help on
// stdout and exit 0, with nothing on stderr, no file written and no dial.
// The check opens no socket: it is a unit-tier check.
//
// Each tool's test names its verbs and, for each, the flags that would point
// the verb at a place: a path flag gets {dir}/..., a store address gets
// {addr}. They are given BEFORE -h, so the parser has taken them when it
// meets -h, and the check then holds that nothing was created under {dir}.
// {addr} is RefusedAddr, a loopback port nothing listens on: the check owns
// no socket, and a verb that dials before it answers -h meets a refusal and
// fails the exit, stderr or budget clause.
package testverbhelp

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Run is the tool, in process: args after the tool's name, the two streams,
// the exit code.
type Run func(args []string, stdout, stderr io.Writer) int

// Case is one verb ("send", "slots take") and the flags given before -h.
// "{dir}" and "{addr}" in a flag are replaced by the case's temp directory
// and RefusedAddr.
type Case struct {
	Verb  string
	Flags []string
}

// Budget is how long help may take. Help is a print; anything that took
// longer than this dialed, waited or walked something.
const Budget = 50 * time.Millisecond

// RefusedAddr is what {addr} becomes: loopback port 1, where nothing listens,
// so a dial is refused at once and the check needs no listener of its own.
const RefusedAddr = "127.0.0.1:1"

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
		c := c
		t.Run(c.Verb+" "+UnknownFlag, func(t *testing.T) {
			t.Parallel()
			for _, p := range RefusalProblems(run, c, t.TempDir()) {
				t.Error(p)
			}
		})
	}
}

// UnknownFlag is a flag no verb defines. Every verb that is handed it refuses.
const UnknownFlag = "--no-such-flag-breadcrumb"

// RefusalProblems runs one case with UnknownFlag and returns every way the
// refusal broke the rule: a tool or verb never fails silently, and every
// refusal carries a breadcrumb to the fix. It is the runtime half of
// internal/ci's remedy and
// no-ok-on-failure rules, run through the same seam every verb parses its flags
// with:
//
//   - the exit is not 0: an invocation the verb cannot parse did not succeed;
//   - something is said on stderr, and it names the flag or the usage, or a
//     remedy in the house form (oneline.HasRemedy): never silent, and a
//     breadcrumb to the fix;
//   - no line on stdout closes with OK: the last word of a failed run is never
//     OK;
//   - nothing is written under {dir} and nothing dials: the refusal comes
//     before the work.
func RefusalProblems(run Run, c Case, dir string) []string {
	var problems []string
	fail := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	args := strings.Fields(c.Verb)
	for _, f := range c.Flags {
		f = strings.ReplaceAll(f, "{dir}", dir)
		f = strings.ReplaceAll(f, "{addr}", RefusedAddr)
		args = append(args, f)
	}
	args = append(args, UnknownFlag)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	said := stderr.String()
	if code == 0 {
		fail("%q exited 0 on a flag it does not define; an invocation that cannot be parsed is refused, exit 2", args)
	}
	if strings.TrimSpace(said) == "" {
		fail("%q exited %d and said nothing on stderr; a refusal names what is wrong and how to fix it", args, code)
	} else if !breadcrumb(said) {
		fail("%q refused with no breadcrumb: stderr %q names neither the flag, the usage nor a remedy (run: <tool> <verb> -h)", args, said)
	}
	if okClosing(stdout.String()) {
		fail("%q exited %d and its last stdout line says OK: %q", args, code, stdout.String())
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		fail("%q left entries under its temp dir (err %v); a refusal writes nothing", args, err)
	}
	return problems
}

// breadcrumb reports a refusal that points somewhere: the unknown flag named,
// the verb's usage printed, or a remedy in the house form.
func breadcrumb(said string) bool {
	flagName := strings.TrimLeft(UnknownFlag, "-")
	return strings.Contains(said, flagName) || strings.Contains(strings.ToLower(said), "usage") || oneline.HasRemedy(said)
}

// okClosing reports output whose last non-empty line is an OK event line.
func okClosing(out string) bool {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.Fields(lines[len(lines)-1])
	for _, w := range last {
		if w == "OK" {
			return true
		}
		if strings.ToUpper(w) != w || strings.Contains(w, "=") {
			return false
		}
	}
	return false
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
	args := strings.Fields(c.Verb)
	for _, f := range c.Flags {
		f = strings.ReplaceAll(f, "{dir}", dir)
		f = strings.ReplaceAll(f, "{addr}", RefusedAddr)
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
			took := time.Since(start)
			// The same four re-measures as Check: a loaded bench stalls any
			// 50 ms; help that runs something is slow every time.
			for i := 0; i < 4 && took > Budget; i++ {
				start = time.Now()
				run(args, io.Discard, io.Discard)
				took = min(took, time.Since(start))
			}
			if took > Budget {
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

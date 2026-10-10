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
// fails the exit or stderr clause. How long help takes is a performance
// check, not a unit one: it is held only under -tags perf (budget_perf.go), and
// the perf job runs it through each tool's perf-only TestEveryVerbsHelpIsWithinTheBudget.
package testverbhelp

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

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

// RefusedAddr is what {addr} becomes: loopback port 1, where nothing listens,
// so a dial is refused at once and the check needs no listener of its own.
const RefusedAddr = "127.0.0.1:1"

// Check runs every case with -h and with --help, in parallel subtests.
func Check(t *testing.T, run Run, cases []Case) {
	t.Helper()
	if len(cases) == 0 {
		t.Fatal("no verbs named; a check over no verbs would pass by checking nothing")
	}
	seen := make([]captured, len(cases))
	for i, c := range cases {
		for _, spelling := range []string{"-h", "--help"} {
			c, spelling := c, spelling
			r := run
			if spelling == "-h" {
				r = seen[i].tee(run, true)
			}
			t.Run(c.Verb+" "+spelling, func(t *testing.T) {
				t.Parallel()
				One(t, r, c, spelling)
			})
		}
		c, r := c, seen[i].tee(run, false)
		t.Run(c.Verb+" "+UnknownFlag, func(t *testing.T) {
			t.Parallel()
			for _, p := range RefusalProblems(r, c, t.TempDir()) {
				t.Error(p)
			}
		})
	}
	// after every parallel subtest: the same -h and refusal they ran, judged for completeness
	t.Cleanup(func() { Complete(t, cases, seen) })
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
	code := run(args, &stdout, &stderr)
	if code != 0 {
		fail("%q exited %d, want 0; stderr: %s", args, code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) == "" {
		fail("%q printed nothing on stdout; help is the verb's usage, on stdout", args)
	}
	if stderr.Len() != 0 {
		fail("%q wrote to stderr: %q; help is not a refusal", args, stderr.String())
	}
	if p := overBudget(run, args); p != "" {
		fail("%s", p)
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
			code := run(args, &viaHelp, &stderr)
			if p := overBudget(run, args); p != "" {
				t.Error(p)
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

// The completeness rule (card tdocs-verb-help-complete): a stranger, human or
// AI, can use a verb from its -h alone, so -h prints, for every verb,
//
//   - a usage line (the first line, `usage: <tool> <verb> ...`);
//   - a description: a line of prose that says what the verb does;
//   - every flag the verb registers, each with a non-empty description;
//   - at least one `example:` line;
//   - an `exit codes:` line.
//
// The flags are the verb's FlagSet's, never a hand list: -h prints them from
// the set (internal/nsprint/verbflag), and the refusal of an unknown flag
// names the set's flags as well, so a flag the refusal names and -h omits is
// a gap too. What a verb lacks and is not yet fixed is a line of
// internal/ci/testdata/help-complete-ledger.txt, `<tool> <verb> <parts>`,
// shrink-only: a gap not in the ledger fails, and so does a ledger line whose
// gap is fixed (or only partly there).

// Gap names, as the ledger spells them.
const (
	GapUsage       = "usage"
	GapDescription = "description"
	GapExample     = "example"
	GapExitCodes   = "exit-codes"
	// GapFlagPrefix is followed by a flag's name: the flag has no description.
	GapFlagPrefix = "flag-undescribed:"
	// GapMissingPrefix is followed by a flag's name: the refusal names it, -h does not print it.
	GapMissingPrefix = "flag-missing:"
)

// LedgerPath is the shrink-only ledger, found from this source file; a build
// with -trimpath names that file by its import path, so the ledger is then
// found from the checkout the test's working directory sits in.
func LedgerPath() string {
	_, file, _, _ := runtime.Caller(0)
	if p := filepath.Join(filepath.Dir(file), "..", "ci", "testdata", "help-complete-ledger.txt"); filepath.IsAbs(p) {
		return p
	}
	dir, err := os.Getwd()
	if err != nil {
		return filepath.Join("internal", "ci", "testdata", "help-complete-ledger.txt")
	}
	for {
		p := filepath.Join(dir, "internal", "ci", "testdata", "help-complete-ledger.txt")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Join("internal", "ci", "testdata", "help-complete-ledger.txt")
		}
		dir = parent
	}
}

var (
	flagLineRe    = regexp.MustCompile(`^  --([A-Za-z0-9][A-Za-z0-9_.-]*)(?: <[^>]*>)?(?:  (.*))?$`)
	refusedFlagRe = regexp.MustCompile(`--[A-Za-z0-9][A-Za-z0-9_.-]*`)
)

// Gaps is what the help text lacks of the rule, in the ledger's spelling,
// sorted. refusal is the stderr of the verb handed UnknownFlag ("" when not
// known); the flags it lists are the set's.
func Gaps(help, refusal string) []string {
	var gaps []string
	lines := strings.Split(strings.TrimRight(help, "\n"), "\n")
	tool := ""
	if f := strings.Fields(lines[0]); len(f) >= 2 && f[0] == "usage:" {
		tool = f[1]
	} else {
		gaps = append(gaps, GapUsage)
	}
	var example, exit, prose bool
	printed := map[string]bool{}
	inFlags := false
	for _, l := range lines[1:] {
		t := strings.TrimSpace(l)
		switch {
		case l == "flags:":
			inFlags = true
			continue
		case strings.HasPrefix(t, "example:"):
			example = true
		case strings.HasPrefix(t, "exit codes:"):
			exit = true
		}
		if m := flagLineRe.FindStringSubmatch(l); inFlags && m != nil {
			printed[m[1]] = true
			if strings.TrimSpace(m[2]) == "" {
				gaps = append(gaps, GapFlagPrefix+m[1])
			}
			continue
		}
		inFlags = false
		if t != "" && !structural(t, tool) {
			prose = true
		}
	}
	if !prose {
		gaps = append(gaps, GapDescription)
	}
	if !example {
		gaps = append(gaps, GapExample)
	}
	if !exit {
		gaps = append(gaps, GapExitCodes)
	}
	if i := strings.Index(refusal, "flags of "); i >= 0 {
		for _, f := range refusedFlagRe.FindAllString(refusal[i:], -1) {
			if name := strings.TrimPrefix(f, "--"); !printed[name] {
				gaps = append(gaps, GapMissingPrefix+name)
			}
		}
	}
	sort.Strings(gaps)
	return gaps
}

// structural is a help line that is not prose: a header, a synopsis or example
// (it opens with the tool's name), the effect line, the exit codes.
func structural(t, tool string) bool {
	for _, p := range []string{"usage:", "from `", "example:", "exit codes:", "effect:"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return tool != "" && strings.HasPrefix(t, tool+" ")
}

// captured is what one case printed on its -h run (stdout) and on its
// unknown-flag run (stderr), kept from the runs Check already makes.
type captured struct {
	mu            sync.Mutex
	help, refusal string
}

// tee is run with its output remembered: stdout when help, stderr otherwise.
func (c *captured) tee(run Run, help bool) Run {
	return func(args []string, stdout, stderr io.Writer) int {
		var out, errOut bytes.Buffer
		code := run(args, &out, &errOut)
		c.mu.Lock()
		if help {
			c.help = out.String()
		} else {
			c.refusal = errOut.String()
		}
		c.mu.Unlock()
		// ignored: the copies go to the caller's in-memory sinks, which cannot fail; the verb's exit code is what is returned
		_, _ = stdout.Write(out.Bytes())
		// ignored: same in-memory sink as the line above
		_, _ = stderr.Write(errOut.Bytes())
		return code
	}
}

// Completeness runs one case with -h, and with an unknown flag to read the
// set's flags, and returns the verb's tool and gaps.
func Completeness(run Run, c Case, dir string) (tool string, gaps []string) {
	args := strings.Fields(c.Verb)
	for _, f := range c.Flags {
		f = strings.ReplaceAll(f, "{dir}", dir)
		f = strings.ReplaceAll(f, "{addr}", RefusedAddr)
		args = append(args, f)
	}
	var h, r captured
	help, refuse := h.tee(run, true), r.tee(run, false)
	var sink bytes.Buffer
	help(append(append([]string(nil), args...), "-h"), &sink, &sink)
	refuse(append(append([]string(nil), args...), UnknownFlag), &sink, &sink)
	return judged(h.help, r.refusal)
}

func judged(help, refusal string) (string, []string) {
	tool := strings.TrimSuffix(filepath.Base(os.Args[0]), ".test")
	if f := strings.Fields(strings.SplitN(help, "\n", 2)[0]); len(f) >= 2 && f[0] == "usage:" {
		tool = f[1]
	}
	return tool, Gaps(help, refusal)
}

// shipped reports whether tool is one of the repository's (a directory under
// cmd/): the ledger governs those, and a fake tool a test hands the seam is not one.
func shipped(tool string) bool {
	st, err := os.Stat(filepath.Join(filepath.Dir(LedgerPath()), "..", "..", "..", "cmd", tool))
	return err == nil && st.IsDir()
}

// Ledger is the ledger's rows: "<tool> <verb>" to its gaps. A malformed line is an error.
func Ledger(text string) (map[string][]string, error) {
	rows := map[string][]string{}
	for n, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		if len(f) < 3 {
			return nil, fmt.Errorf("ledger line %d %q: want `<tool> <verb> <missing parts>`", n+1, l)
		}
		parts := strings.Split(f[len(f)-1], ",")
		key := strings.Join(f[:len(f)-1], " ")
		if _, dup := rows[key]; dup {
			return nil, fmt.Errorf("ledger line %d: %q appears twice", n+1, key)
		}
		sort.Strings(parts)
		rows[key] = parts
	}
	return rows, nil
}

// Judge compares one verb's gaps with its ledger row (nil: none), and returns
// the problems: a gap not in the ledger, a ledger part already fixed.
func Judge(key string, gaps, ledgered []string) []string {
	var problems []string
	have := map[string]bool{}
	for _, g := range gaps {
		have[g] = true
	}
	in := map[string]bool{}
	for _, g := range ledgered {
		in[g] = true
		if !have[g] {
			problems = append(problems, fmt.Sprintf("%s: the ledger lists %q, which is fixed; remove it from %s (the ledger only shrinks)", key, g, "internal/ci/testdata/help-complete-ledger.txt"))
		}
	}
	for _, g := range gaps {
		if !in[g] {
			problems = append(problems, fmt.Sprintf("%s: -h lacks %q; complete the verb's help (its verbhelp.go or the verb's own help text); a new gap is never added to the ledger", key, g))
		}
	}
	return problems
}

// Complete holds every case's captured help to the rule, against the ledger.
func Complete(t *testing.T, cases []Case, seen []captured) {
	t.Helper()
	text, err := os.ReadFile(LedgerPath())
	if err != nil {
		t.Fatalf("the help-complete ledger is unreadable: %v", err)
	}
	rows, err := Ledger(string(text))
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		tool, gaps := judged(seen[i].help, seen[i].refusal)
		if !shipped(tool) {
			continue
		}
		key := tool + " " + c.Verb
		for _, p := range Judge(key, gaps, rows[key]) {
			t.Error(p)
		}
	}
}

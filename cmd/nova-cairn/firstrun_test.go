// First-run tests for nova-cairn: the usage banner's `example:` block and the
// docs/TESTS.md `### First run` transcript are RUN here rather than read. An
// example that has drifted out of the flag set teaches the wrong invocation
// to exactly the reader who cannot tell, and a transcript line no run prints
// is a promise the tool never made.
package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

func runCLI(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// localize points the documented store at the run's directory, so the
// transcript a stranger types against ./cairns executes here in a fresh one.
func localize(store, cmd string) ([]string, error) {
	fields, err := splitShell(strings.ReplaceAll(cmd, "./cairns", store))
	if err != nil {
		return nil, err
	}
	return fields, nil
}

// splitShell splits a documented command line the way a POSIX shell would
// for the quotes this repo's examples use: double quotes group, backslash
// escapes the next character inside them. The examples never nest quotes,
// so anything fancier is a test bug, not a feature.
func splitShell(cmd string) ([]string, error) {
	var fields []string
	var cur strings.Builder
	inQuote := false
	escaped := false
	has := false
	for _, r := range cmd {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && inQuote:
			escaped = true
		case r == '"':
			inQuote = !inQuote
			has = true
		case r == ' ' || r == '\t':
			if inQuote {
				cur.WriteRune(r)
				break
			}
			if has {
				fields = append(fields, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if inQuote || escaped {
		return nil, errors.New("unterminated quote")
	}
	if has {
		fields = append(fields, cur.String())
	}
	return fields, nil
}

// usageExamples returns the command lines under the banner's `example:`
// heading. It asks for the banner, because a bare invocation is a refusal
// and this reads what is behind the door the refusal names.
func usageExamples(t *testing.T) []string {
	t.Helper()
	exit, stdout, stderr := runCLI(t, "", "help")
	if exit != 0 {
		t.Fatalf("`nova-cairn help` must be exit 0, got %d; stderr: %s", exit, stderr)
	}
	examples, err := onboarding.ExampleLines(stdout, "nova-cairn")
	if err != nil {
		t.Fatal(err)
	}
	return examples
}

// The usage banner ends in one example per verb, in the order a first run
// types them: the append needs the record the open created.
func TestUsageBannerExamplesRun(t *testing.T) {
	examples := usageExamples(t)
	if len(examples) != 4 {
		t.Fatalf("want an open, an append, an index and a receipt example under `example:`, got %d: %q", len(examples), examples)
	}
	for i, want := range []string{
		"nova-cairn open ", "nova-cairn append ", "nova-cairn index ", "nova-cairn receipt ",
	} {
		if !strings.HasPrefix(examples[i], want) {
			t.Errorf("example %d is not %q: %q", i, want, examples[i])
		}
	}
	// One store for the whole first run: the examples are a sitting, not four.
	store := filepath.Join(t.TempDir(), "cairns")
	for _, ex := range examples {
		fields, err := splitShell(strings.ReplaceAll(ex, "./cairns", store))
		if err != nil {
			t.Fatalf("cannot split the usage example %q: %v", ex, err)
		}
		exit, stdout, stderr := runCLI(t, "", fields[1:]...)
		if exit != 0 {
			t.Fatalf("the usage example %q does not run: exit %d, stderr: %s", ex, exit, stderr)
		}
		if stdout == "" {
			t.Errorf("the usage example %q printed nothing", ex)
		}
	}
}

// The `### First run` block of docs/TESTS.md is EXECUTED: every documented
// command is run, in order, in one directory, and its whole output is compared
// with the block written under it -- same number of lines, same lines, same
// order.
//
// WHAT THIS REPLACES. The old test collected the SHAPES a command printed into
// a `printed map[string]bool` and asked whether each documented line was in it,
// with the VALUES deliberately not compared. Under that comparison an abridged
// block passes (a dropped line removes a lookup, not an assertion), a reordered
// pair is never looked at, and a wrong stamp, a wrong byte count or a wrong
// `duplicate=` is invisible -- which for a tool whose whole job is a durable
// receipt is most of what the transcript is for.
//
// NOTHING IS NORMALISED, and that is a property of this transcript rather than
// a shortcut. The stamps come from `--now`, the ids and the store are named on
// the command line, and the byte count is of the text typed there, so every
// value on every line reproduces. onboarding.Execute is told so by being handed
// no Norm, and it says as much under any line that disagrees.
//
// The store is typed as written. The documented `./cairns` is relative and the
// tool PRINTS IT BACK on every line, so the test runs in a directory of its own
// rather than rewriting the path: a rewritten one is no longer the line the
// document promised, which is what the old test's `localize` gave up.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-cairn")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-cairn", lines)
	if err != nil {
		t.Fatal(err)
	}
	// The sitting is the whole tool: a record opened, a line appended to it,
	// the index that shows it and the receipt that proves it. A block that has
	// quietly lost one of the four verbs is short of a first run, and no
	// per-line comparison would say so -- the lines that remain would match.
	verbs := map[string]bool{}
	for _, s := range steps {
		verbs[s.Args[0]] = true
	}
	for _, verb := range []string{"open", "append", "index", "receipt"} {
		if !verbs[verb] {
			t.Errorf("the `### First run` block never runs `nova-cairn %s`; the first sitting is all four verbs", verb)
		}
	}

	// ONE store for the whole sitting: the transcript opens a record and then
	// appends to it, and a fresh directory per line would unmake that.
	t.Chdir(t.TempDir())
	for _, p := range onboarding.Execute(steps, runDocumented) {
		t.Error(p)
	}
}

// runDocumented calls this binary's own entry point with the documented
// arguments. nova-cairn's first run reads nothing on stdin.
func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	if s.Stdin != "" {
		return onboarding.Result{}, errReadsNothing
	}
	var out, errb bytes.Buffer
	code := run(s.Args, strings.NewReader(""), &out, &errb)
	return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
}

type readsNothing struct{}

func (readsNothing) Error() string {
	return "nova-cairn's first run reads no stdin; a `< path` in its transcript is the document's bug"
}

var errReadsNothing = readsNothing{}

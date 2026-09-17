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

// The TESTS.md First run block is the first thing a stranger copies, so
// every transcript line must match a line the tool actually printed — the
// event prefix and the field names, in order. Values are a run's own
// business and are deliberately NOT compared.
func TestTESTSFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-cairn")
	if err != nil {
		t.Fatal(err)
	}
	var printed map[string]bool
	seen := map[string]int{}
	// One store for the whole sitting: the transcript opens a record and
	// then appends to it, and a fresh directory per line would unmake that.
	store := filepath.Join(t.TempDir(), "cairns")
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ nova-cairn "); ok {
			fields, err := localize(store, cmd)
			if err != nil {
				t.Fatalf("cannot split the TESTS.md command %q: %v", line, err)
			}
			exit, stdout, stderr := runCLI(t, "", fields...)
			if exit != 0 {
				t.Fatalf("the TESTS.md command %q does not run: exit %d, stderr: %s", line, exit, stderr)
			}
			printed = map[string]bool{}
			for _, out := range strings.Split(stdout, "\n") {
				if s := onboarding.Shape(out); s != "" {
					printed[s] = true
				}
			}
			continue
		}
		s := onboarding.Shape(line)
		if s == "" {
			continue
		}
		if printed == nil {
			t.Fatalf("transcript line before any command: %q", line)
		}
		if !printed[s] {
			t.Errorf("TESTS.md line\n  %s\nhas shape %q, which this tool never prints. Re-run the command and paste what it said.", line, s)
		}
		seen[strings.Join(strings.Fields(s)[:2], " ")]++
	}
	for prefix, want := range map[string]int{
		"OPEN OK": 1, "APPEND OK": 1, "INDEX ENTRY": 1,
		"INDEX COVERAGE": 1, "RECEIPT OK": 1,
	} {
		if seen[prefix] != want {
			t.Errorf("TESTS.md First run shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}

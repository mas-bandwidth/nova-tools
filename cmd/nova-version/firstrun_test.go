package main

import (
	"bytes"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/update"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutableFirstRun(t *testing.T) {
	t.Chdir("../..")
	var banner bytes.Buffer
	update.Main("nova-version", []string{"help"}, "", &banner, &banner)
	examples, err := onboarding.ExampleLines(banner.String(), "nova-version")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range examples {
		var out, errs bytes.Buffer
		code := update.Main("nova-version", strings.Fields(line)[1:], "", &out, &errs)
		if code == 2 {
			t.Fatalf("%s refused: %s", line, errs.String())
		}
	}
	doc, err := os.ReadFile("docs/TESTS.md")
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := onboarding.FirstRun(string(doc), "nova-version")
	if err != nil {
		t.Fatal(err)
	}
	var wanted, actual []string
	var out, errs bytes.Buffer
	for _, line := range transcript {
		if strings.HasPrefix(line, "$ ") {
			if c := update.Main("nova-version", strings.Fields(line)[2:], "", &out, &errs); c != 0 {
				t.Fatalf("first run: %d %s", c, errs.String())
			}
		} else if s := firstRunShape(line); s != "" {
			wanted = append(wanted, s)
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if s := firstRunShape(line); s != "" {
			actual = append(actual, s)
		}
	}
	if strings.Join(wanted, "\n") != strings.Join(actual, "\n") {
		t.Fatalf("document shape %v differs from run %v", wanted, actual)
	}
}
func TestMissingIndependentFlagsAreNamedTogether(t *testing.T) {
	var out, errs bytes.Buffer
	c := update.Main("nova-version", []string{"report", "--send"}, "", &out, &errs)
	if c != 2 {
		t.Fatal(c)
	}
	for _, flag := range []string{"--file", "--as", "--to", "--bus", "--remote", "--branch"} {
		if !strings.Contains(errs.String(), flag) {
			t.Fatal("missing " + flag + ": " + errs.String())
		}
	}
	if strings.Count(errs.String(), "\n") > 2 {
		t.Fatal("refusal printed a banner")
	}
}

// REPORT has a dynamic timestamp as its second token, unlike two-word events.
// Remove only that value; keep the field and every other output shape check.
func firstRunShape(line string) string {
	if strings.HasPrefix(line, "REPORT at=") {
		fields := strings.Fields(line)
		fields[1] = "at="
		line = strings.Join(fields, " ")
	}
	return onboarding.Shape(line)
}

// TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine executes the
// `### First run` block of docs/TESTS.md for nova-version -- every command, in
// order -- and compares each command's whole output with the block written
// under it: same number of lines, same lines, same order. The existing
// TestExecutableFirstRun asks whether each line is the right SHAPE, which keeps
// the words and drops the count and the order; this one keeps the whole
// promise.
func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	t.Chdir(repoRoot(t))
	raw, err := os.ReadFile(filepath.Join("docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-version")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-version", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("the `### First run` block holds no nova-version command; this test would pass by running nothing")
	}
	// The transcript declares four values as owned by the RUN or the BENCH
	// rather than by the document, and nothing else: the instant of the run
	// (`at=`) and its duration (`took=`), and the three fields of the
	// `REPORT TOOL` line that say which Go THIS machine runs (`version=`),
	// what its `version` command printed (`raw=`) and where it is installed
	// (`path=`). The document records the Studio's arm64 Go; a bench runs its
	// own, so those three cannot be compared as written. Every other value --
	// the file, the counts, the kinds, the bounds -- is compared exactly.
	norms := []onboarding.Norm{
		onboarding.Instant("at"),
		elide(t, "took= (the duration of this run)", `took=[^ ]+`, "took=<the duration of this run>"),
		elide(t, "version= (the Go this bench runs)", `version=[^ ]+`, "version=<the Go this bench runs>"),
		elide(t, "raw= (what this bench's version command printed)", `raw=[^ ]+`, "raw=<what this bench's version command printed>"),
		elide(t, "path= (where this bench's Go is installed)", `path=[^ ]+`, "path=<where this bench's Go is installed>"),
	}
	for _, p := range onboarding.Execute(steps, runVersionDocumented(t), norms...) {
		t.Error(p)
	}
}

// elide declares one normalisation this package has no constructor for. The
// name is what a reader of a failing test is told is not compared.
func elide(t *testing.T, name, pattern, as string) onboarding.Norm {
	t.Helper()
	n, err := onboarding.Elide(name, pattern, as)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// runVersionDocumented calls this tool's own entry point with the documented
// arguments. A redirect would be a transcript for a tool that reads stdin, and
// nova-version reads none: it is refused here rather than silently dropped, so
// a future document that adds one fails loudly instead of running a different
// command than the reader typed.
func runVersionDocumented(t *testing.T) onboarding.Runner {
	t.Helper()
	return func(s onboarding.Step) (onboarding.Result, error) {
		if s.Stdin != "" {
			return onboarding.Result{}, fmt.Errorf("the transcript redirects %q into nova-version, which reads no stdin", s.Stdin)
		}
		var out, errb bytes.Buffer
		code := update.Main("nova-version", s.Args, "", &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
}

// repoRoot is the checkout root: this package sits two directories under it.
// It is resolved rather than assumed so that a failure names a path.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "TESTS.md")); err != nil {
		t.Fatalf("docs/TESTS.md is not under %s: %v", root, err)
	}
	return root
}

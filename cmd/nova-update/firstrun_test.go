package main

import (
	"bytes"
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
	update.Main("nova-update", []string{"help"}, "", &banner, &banner)
	examples, err := onboarding.ExampleLines(banner.String(), "nova-update")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range examples {
		var out, errs bytes.Buffer
		code := update.Main("nova-update", strings.Fields(line)[1:], "", &out, &errs)
		if code == 2 {
			t.Fatalf("%s refused: %s", line, errs.String())
		}
	}
	doc, err := os.ReadFile("docs/TESTS.md")
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := onboarding.FirstRun(string(doc), "nova-update")
	if err != nil {
		t.Fatal(err)
	}
	var wanted, actual []string
	var out, errs bytes.Buffer
	for _, line := range transcript {
		if strings.HasPrefix(line, "$ ") {
			if c := update.Main("nova-update", strings.Fields(line)[2:], "", &out, &errs); c != 0 {
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
	c := update.Main("nova-update", []string{"report", "--send"}, "", &out, &errs)
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

// The `### First run` block under docs/TESTS.md's `## nova-update` section is
// EXECUTED here: the documented command is run and its whole output is compared
// with the block written under it -- same number of lines, same lines, same
// order. TestExecutableFirstRun above reduces each line to its field names, so
// it passes on an abridged or reordered transcript; this test keeps the whole
// promise.
func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	t.Chdir(repoRoot(t))
	raw, err := os.ReadFile(filepath.Join("docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-update")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-update", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("the `### First run` block holds no nova-update command; this test would pass by running nothing")
	}
	for _, p := range onboarding.Execute(steps, runDocumented(t), firstRunNorms(t)...) {
		t.Error(p)
	}
}

// firstRunNorms declares the five values in this transcript that belong to the
// RUN or to the BENCH rather than to the document. Every other value on every
// line is compared as written.
//
//   - `at=` is the instant the report was taken. It goes through
//     onboarding.Instant, which PARSES the value, so an impossible stamp the
//     tool printed is still a finding rather than a value a norm erased.
//   - `took=` is how long this run's version reads took.
//   - `version=`, `raw=` and `path=` are the `go version` THIS bench answers.
//     The documented transcript was cut on the Studio (go1.27.1, darwin/arm64,
//     /opt/homebrew/bin/go); the same command on a Linux bench answers a
//     different release, a different GOOS/GOARCH and a different executable
//     path. The fixture (`testdata/example.tsv`) declares `go version` as the
//     installed command, so these three fields are the bench's by construction
//     and no run of this test on any machine could reproduce the Studio's.
//
// What is NOT declared is the point of comparing line for line: the number of
// lines, their order, and `entries=`, `kinds=`, `checked=`, `known=`,
// `unknown=`, `changed=`, `sent=` and every file name all stay compared, so a
// dropped, reordered or abridged transcript is red.
func firstRunNorms(t *testing.T) []onboarding.Norm {
	t.Helper()
	duration, err := onboarding.Elide(
		"took= (the wall time this run's reads took)",
		`took=[0-9][^\s]*`,
		"took=<the wall time this run's reads took>",
	)
	if err != nil {
		t.Fatal(err)
	}
	version, err := onboarding.Elide(
		"version= (the release this bench's version command answers)",
		`version=[^\s]+`,
		"version=<the release this bench's version command answers>",
	)
	if err != nil {
		t.Fatal(err)
	}
	rawVersion, err := onboarding.Elide(
		"raw= (this bench's version command printed)",
		`raw=[^\s]+`,
		"raw=<this bench's version command printed>",
	)
	if err != nil {
		t.Fatal(err)
	}
	path, err := onboarding.Elide(
		"path= (the executable this bench found on PATH)",
		`path=[^\s]+`,
		"path=<the executable this bench found on PATH>",
	)
	if err != nil {
		t.Fatal(err)
	}
	return []onboarding.Norm{onboarding.Instant("at"), duration, version, rawVersion, path}
}

// runDocumented calls this binary's own entry point with the documented
// arguments. nova-update reads no stdin and this transcript has no `< path`
// redirect, so there is no stream to open.
func runDocumented(t *testing.T) onboarding.Runner {
	t.Helper()
	return func(s onboarding.Step) (onboarding.Result, error) {
		var out, errb bytes.Buffer
		code := update.Main("nova-update", s.Args, "", &out, &errb)
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

//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/redis/go-redis/v9"
)

// firstrun_test.go pins the onboarding standard for this binary: the
// docs/TESTS.md and docs/CLI.md `### First run` transcripts are RUN, line for
// line and in order, through the one comparator (onboarding.CompareTranscript,
// SPEC-TOOLWORK.md documents rule 2), and the usage banner's `example:` block is that
// same sitting. The sitting moves a member between two cells (ns_oset_move,
// a function of the nova_sprint library), so it runs on a throwaway
// redis-server with the library loaded: the functional tier's.
//
// NOTHING IS NORMALISED: every value on every line reproduces (the counts,
// the names, the render), so the comparator is handed no field of the
// onboarding.Volatile table. The documented lines name no --redis: on a
// bench the seat's address is the default; here each step is run with
// --redis <the throwaway server> appended, which changes what the tool
// dials and nothing it prints.

// firstRunStore is a throwaway redis-server with the library loaded.
func firstRunStore(t *testing.T) string {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return addr
}

// runDocumented calls this binary's own entry point with the documented
// arguments, the throwaway store's address appended.
func runDocumented(addr string) onboarding.Runner {
	return func(s onboarding.Step) (onboarding.Result, error) {
		var out, errb bytes.Buffer
		code := run(append(append([]string{}, s.Args...), "--redis", addr), &out, &errb)
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
}

func readDoc(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// executeFirstRun runs the `### First run` block of doc's nova-table section
// on a fresh store and hands the steps and the results to the comparator.
func executeFirstRun(t *testing.T, doc string) []onboarding.Step {
	t.Helper()
	lines, err := onboarding.FirstRun(readDoc(t, doc), "nova-table")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-table", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatalf("the `### First run` block of docs/%s holds no nova-table command; this test would pass by running nothing", doc)
	}
	// the verbs a first run is: a table made, a row in it, members in a
	// cell, one moved to another, the table shown and rendered; a
	// transcript that has quietly lost one still matches line for line, so
	// the sitting is held to them by name
	verbsOfTheSitting := []string{
		"nova-table create", "nova-table row add", "nova-table cell add", "nova-table cell move", "nova-table show", "nova-table render",
	}
	for _, verb := range verbsOfTheSitting {
		found := false
		for _, s := range steps {
			if strings.HasPrefix(strings.Join(append([]string{"nova-table"}, s.Args...), " "), verb+" ") {
				found = true
			}
		}
		if !found {
			t.Errorf("the `### First run` block of docs/%s never runs `%s`; the first sitting is every verb of verbsOfTheSitting", doc, verb)
		}
	}
	run := runDocumented(firstRunStore(t))
	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		res, err := run(s)
		if err != nil {
			t.Fatalf("the documented command\n  %s\ncould not be run: %v", s.Line, err)
		}
		got = append(got, res)
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		t.Errorf("docs/%s: %s", doc, p)
	}
	return steps
}

// TestTESTSFirstRunIsWhatTheToolPrints executes docs/TESTS.md's section, and
// holds the usage banner's `example:` block to be that same sitting, line for
// line: the examples a stranger pastes are the transcript a build checks.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	steps := executeFirstRun(t, "TESTS.md")
	code, banner, stderr := runTable("help")
	if code != 0 {
		t.Fatalf("`nova-table help` exits %d, want 0; stderr: %s", code, stderr)
	}
	examples, err := onboarding.ExampleLines(banner, "nova-table")
	if err != nil {
		t.Fatalf("%v\n\nwhat the banner printed:\n%s", err, banner)
	}
	documented := make([]string, 0, len(steps))
	for _, s := range steps {
		documented = append(documented, strings.TrimPrefix(s.Line, "$ "))
	}
	if strings.Join(examples, "\n") != strings.Join(documented, "\n") {
		t.Fatalf("the banner's example block is not docs/TESTS.md's first run\nbanner:\n  %s\ntranscript:\n  %s",
			strings.Join(examples, "\n  "), strings.Join(documented, "\n  "))
	}
}

// TestTheCommandReferenceFirstRunMatchesWhatTheToolPrints executes
// docs/CLI.md's `### First run` the same way (SPEC-TOOLWORK.md documents rule 6: the
// reference is a document a stranger pastes from).
func TestTheCommandReferenceFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	t.Parallel()

	executeFirstRun(t, "CLI.md")
}

// TestTESTSNamesNovaTableOnce: onboarding.Section reads the FIRST `##
// nova-table` section and stops, so a second would be read by no test.
func TestTESTSNamesNovaTableOnce(t *testing.T) {
	t.Parallel()

	sections := 0
	for _, name := range onboarding.SectionNames(readDoc(t, "TESTS.md")) {
		if name == "nova-table" {
			sections++
		}
	}
	if sections != 1 {
		t.Fatalf("docs/TESTS.md heads %d `## nova-table` sections, want exactly 1", sections)
	}
}

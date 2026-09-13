package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// ONBOARDING.md, pinned for this binary: the example lines are EXECUTED against the
// fixture, every refusal says what the input WANTS and one run names every independent
// problem, and the TESTS.md transcript is compared against what the tool actually prints.

// fixtureIn copies cmd/nova-tokens/testdata/example-bench into t.TempDir(), makes the
// output directory the examples write to, and moves the test into it. Nothing here
// reaches outside t.TempDir(): a first run WRITES, so the fixture is copied rather than
// run in place.
func fixtureIn(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join("testdata", "example-bench")
	root, err := filepath.Abs(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dst)
	return dst
}

// firstRunStamp is the clock the transcript in TESTS.md was produced under.
var firstRunStamp = time.Date(2026, 9, 11, 23, 55, 2, 0, time.UTC)

func TestTheExampleLinesRun(t *testing.T) {
	fixtureIn(t)
	var banner bytes.Buffer
	if exit := run([]string{"help"}, &banner, io.Discard, firstRunStamp); exit != 0 {
		t.Fatalf("`nova-tokens help` exits %d, want 0", exit)
	}
	examples, err := onboarding.ExampleLines(banner.String(), "nova-tokens")
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) == 0 {
		t.Fatal("the example: block holds no line")
	}
	for _, line := range examples {
		args := strings.Fields(line)[1:]
		var out, errb bytes.Buffer
		exit := run(args, &out, &errb, firstRunStamp)
		// A line that RUNS answers 0 or 1. Exit 2 is "could not run", and an example
		// exiting 2 is a broken example.
		if exit == 2 {
			t.Errorf("the example `%s` could not run (exit 2):\n%s", line, errb.String())
		}
	}
}

func TestEveryRefusalSaysWhatTheInputWantsAndOneRunNamesEveryProblem(t *testing.T) {
	dir := t.TempDir()
	// Three independent problems, three lines, one run.
	r := invoke(t, "fold", "--day", "2026-09-11")
	wantExit(t, r, 2)
	if n := strings.Count(r.stderr, "\n"); n != 3 {
		t.Errorf("%d refusal lines for three independent problems:\n%s", n, r.stderr)
	}
	for _, want := range []string{"refusing to guess", "it wants the directory", "it wants a file of", "it wants --claude"} {
		wantContains(t, r.stderr, want)
	}
	// A flag typo costs ONE line and names the door, never the banner.
	r = invoke(t, "fold", "--ou", dir)
	wantExit(t, r, 2)
	if n := strings.Count(strings.TrimSuffix(r.stderr, "\n"), "\n"); n != 0 {
		t.Errorf("a flag typo cost %d lines; the banner is behind `nova-tokens help`:\n%s", n+1, r.stderr)
	}
	wantContains(t, r.stderr, "run: nova-tokens help")
	// An unknown verb, and a bare invocation, do the same.
	r = invoke(t, "collate")
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "run: nova-tokens help")
	r = invoke(t)
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "run: nova-tokens help")
	if r.stdout != "" {
		t.Errorf("a bare invocation wrote to stdout: %q", r.stdout)
	}
	// And the door opens on stdout at exit 0.
	r = invoke(t, "help")
	wantExit(t, r, 0)
	if r.stderr != "" {
		t.Errorf("`help` wrote to stderr: %q", r.stderr)
	}
}

// There is no quickstart verb, and docs/ONBOARDING.md point 4 wants that said rather than
// guessed at. Every verb here needs a path this tool must not invent: an output
// directory, a rules file, a source. A quickstart would have to write state nobody asked
// for, in a directory nobody named.
func TestThereIsNoQuickstartVerbAndTheCommandReferenceSaysWhy(t *testing.T) {
	r := invoke(t, "quickstart")
	wantExit(t, r, 2)
	wantContains(t, r.stderr, "unknown subcommand")
	cli := readRepoFile(t, filepath.Join("docs", "CLI.md"))
	if !strings.Contains(cli, "no `quickstart`") {
		t.Error("docs/CLI.md does not say why there is no quickstart verb (docs/ONBOARDING.md point 4)")
	}
}

// The TESTS.md transcript is compared by SHAPE -- the two-token event prefix and the field
// names in order -- and deliberately not by value, so the transcript stays a document
// instead of becoming a fixture.
func TestTheTranscriptIsWhatTheToolPrints(t *testing.T) {
	doc := readRepoFile(t, filepath.Join("docs", "TESTS.md"))
	lines, err := onboarding.FirstRun(doc, "nova-tokens")
	if err != nil {
		t.Fatal(err)
	}
	fixtureIn(t)
	var want []string
	var got []string
	var pending []string
	flush := func() {
		got = append(got, pending...)
		pending = nil
	}
	for _, line := range lines {
		if args, ok := strings.CutPrefix(line, "$ nova-tokens "); ok {
			flush()
			var out, errb bytes.Buffer
			run(strings.Fields(args), &out, &errb, firstRunStamp)
			for _, printed := range strings.Split(out.String()+errb.String(), "\n") {
				if shape := onboarding.Shape(printed); shape != "" {
					pending = append(pending, shape)
				}
			}
			continue
		}
		if shape := onboarding.Shape(line); shape != "" {
			want = append(want, shape)
		}
	}
	flush()
	if len(want) == 0 {
		t.Fatal("the transcript holds no event line")
	}
	for i, w := range want {
		if i >= len(got) {
			t.Fatalf("the transcript has a line the tool does not print: %q", w)
		}
		if got[i] != w {
			t.Errorf("line %d of the transcript is\n  %s\nand the tool prints\n  %s", i+1, w, got[i])
		}
	}
	if len(got) > len(want) {
		t.Errorf("the tool prints %d event lines and the transcript shows %d; the first missing is %q", len(got), len(want), got[len(want)])
	}
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// First-run tests for nova-work: the usage banner's `example:` block and the
// docs/TESTS.md `### First run` transcript are RUN here rather than read, so an
// example that has drifted out of the flag set is caught by a build.
//
// firstrun_test.go pins the onboarding standard (docs/ONBOARDING.md) for this binary:
// the examples at the foot of the usage banner RUN, and a bare command refuses in one
// line that names the door. The example's redis address is pointed at a miniredis, so
// nothing here reaches a real instance.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// firstRunPlan is the plan the first run writes: one valid node with a known
// kind, small enough to read in one line.
const firstRunPlan = "(:plan :version 1 (:node :id \"n1\" :kind docs))\n"

// firstRunSet is the work set the first run reads: the OTHER top form of the same
// language, small enough to read in one line and clean enough that the example
// exits 0 -- one closed unit and one that needs it, so --ready has a row to print
// and the SET OK line's arithmetic is visible in the transcript.
const firstRunSet = `(work-set "first-run"
  :title "the smallest work set"
  :units ((unit "u0" :status "closed" :title "the need that has landed")
          (unit "u1" :needs ("u0") :owner "Rowan" :title "the unit that is ready")))
`

// firstRunUnits is the set the attempt and next examples read and WRITE: one
// ready unit in one lane, owned by the coordinator's child, so `next --take`
// has exactly one answer and the `attempt record` line after it closes the very
// attempt that take opened. The two lines are a sequence, like the graph lines
// above them, and they run in the order the banner writes them.
const firstRunUnits = `(work-set "first-run-units"
  :title "the smallest set a mind can be handed work from"
  :units ((unit "certify:verb" :lane "pulse" :owner "rowan-child" :needs ()
            :title "the one ready unit")))
`

// firstRunLanes is the lanes table those units name: a lane is a resource of
// capacity 1 over an area of the tree, and the file is the map (A6).
const firstRunLanes = "pulse\tcmd/nova-pulse internal/pulse\n"

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// firstRunDir is the working directory ONE transcript runs in: a temp dir holding
// the plan the `./work.work` lines read. It is made once per test rather than once
// per line because the lines are a sequence -- `dependencies --node b`, then
// `--node a --needs b`, then `ready` -- and the graph they build has to survive
// from one line to the next.
func firstRunDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "work.work"), []byte(firstRunPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work-set.lisp"), []byte(firstRunSet), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "units.lisp"), []byte(firstRunUnits), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lanes.tsv"), []byte(firstRunLanes), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// localize rewrites EVERY `./x` token in an example into dir, rather than the one
// `./work.work` this used to know about. The one it did not know about was
// `./deps.json`, which the `dependencies` verb WRITES: relative to the test's own
// working directory, every run of this package left a deps.json in
// cmd/nova-work/. The examples are still run verbatim in shape -- what changes is
// the directory the paths in them point at, so a relative path added to the usage
// banner or to docs/TESTS.md tomorrow lands in the temp dir too.
func localize(t *testing.T, dir, line string) []string {
	t.Helper()
	fields := strings.Fields(line)
	for i, f := range fields {
		if rest, ok := strings.CutPrefix(f, "./"); ok {
			fields[i] = filepath.Join(dir, rest)
		}
	}
	return fields
}

// TestBareNovaWorkRefusesInOneLine: no verb is not an invocation, and the refusal says
// where the usage is.
func TestBareNovaWorkRefusesInOneLine(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb, production()); code != 2 {
		t.Fatalf("a bare nova-work exits %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Errorf("a bare nova-work wrote to stdout: %q", out.String())
	}
	if !strings.Contains(errb.String(), "run: nova-work help") {
		t.Errorf("a bare nova-work names no door: %q", errb.String())
	}
	if lines := strings.Split(strings.TrimSuffix(errb.String(), "\n"), "\n"); len(lines) > 2 {
		t.Errorf("a bare nova-work printed %d lines, want 1: %q", len(lines), errb.String())
	}
}

// TestUsageBannerExamplesRun executes each line under `example:` with the test's deps.
func TestUsageBannerExamplesRun(t *testing.T) {
	var help, errb bytes.Buffer
	if code := run([]string{"help"}, &help, &errb, production()); code != 0 {
		t.Fatalf("nova-work help exit = %d, stderr=%s", code, errb.String())
	}
	examples, err := onboarding.ExampleLines(help.String(), "nova-work")
	if err != nil {
		t.Fatalf("%s\n\n%s", err, help.String())
	}
	mr := miniredis.RunT(t)
	deps := Deps{
		Now:  func() time.Time { return time.Now().UTC() },
		Dial: func(addr string) *redis.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
		Forge: func(_, _ string, _ time.Duration) ci.Forge {
			return &fakeForge{}
		},
	}
	dir := firstRunDir(t)
	for _, ex := range examples {
		fields := localize(t, dir, ex)
		if len(fields) < 2 || fields[0] != "nova-work" {
			t.Fatalf("usage example %q is not a nova-work command", ex)
		}
		args := fields[1:]
		// The events example names the default redis address. This run points it at
		// the test's miniredis, so the example runs as written and reaches no network.
		for i, a := range args {
			if a == "127.0.0.1:6379" {
				args[i] = mr.Addr()
			}
		}
		var out, errs bytes.Buffer
		code := run(args, &out, &errs, deps)
		if code == 2 {
			t.Errorf("the usage example %q does not run: exit 2\nstderr: %s", ex, errs.String())
		}
		if out.Len() == 0 && errs.Len() == 0 {
			t.Errorf("the usage example %q printed nothing", ex)
		}
	}
}

func TestTESTSFirstRunMatchesWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	dir := firstRunDir(t)
	var printed map[string]bool
	for _, line := range lines {
		if cmd, ok := strings.CutPrefix(line, "$ nova-work "); ok {
			fields := localize(t, dir, cmd)
			code, stdout, stderr := runCLI(t, fields...)
			if code != 0 {
				t.Fatalf("the TESTS.md command %q does not run: exit %d, stderr: %s", line, code, stderr)
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
		if !printed[s] {
			t.Errorf("TESTS.md line\n  %s\nhas shape %q, which this tool never prints", line, s)
		}
	}
}

// TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine is the comparator half of
// the check above. TestTESTSFirstRunMatchesWhatTheToolPrints asks whether each
// documented line RESEMBLES a line the tool printed; this runs every `$` line in
// order and compares the whole output block with what docs/TESTS.md writes --
// same number of lines, same lines, same order.
//
// The first run is all local: `dependencies` writes the graph it is given,
// `ready` reads it back, and `plan check` reads the `work.work` this test wrote,
// so every value reproduces and NO normalisation is declared. The commands run
// from the temp dir firstRunDir made, which is where the document's `./deps.json`
// and `./work.work` point for whoever typed them.
func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-work", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("the `### First run` block holds no nova-work command; this test would pass by running nothing")
	}
	t.Chdir(firstRunDir(t))
	for _, p := range onboarding.Execute(steps, runDocumented(t)) {
		t.Error(p)
	}
}

// runDocumented calls this binary's own entry point with the documented
// arguments. The first-run verbs are local -- no redis, no forge, no store --
// so production's outside edges are never reached.
func runDocumented(t *testing.T) onboarding.Runner {
	t.Helper()
	return func(s onboarding.Step) (onboarding.Result, error) {
		var out, errb bytes.Buffer
		code := run(s.Args, &out, &errb, production())
		return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
	}
}

// TestTESTSRefusalsAreWhatTheToolPrints executes the `### Refusals` block of this
// tool's docs/TESTS.md section, which nothing executed until 2026-09-19. A
// refusal line is compared WHOLE rather than by shape: a refusal is a sentence a
// person reads at a prompt, and its wording is the promise. The first-run lines
// run first, in the same directory, because the cycle refusal reads the graph
// those lines build.
//
// This is the test the two-bench dogfood run was doing by hand. The bare-command
// line in that block said `nova-work: no verb given` while the binary had said
// `WORK REFUSED: a verb is required` since it grew the client spec's refusal
// shape, and it said so from a second `## nova-work` section that
// onboarding.Section could not reach.
func TestTESTSRefusalsAreWhatTheToolPrints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	dir := firstRunDir(t)
	firstRun, err := onboarding.FirstRun(string(raw), "nova-work")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range firstRun {
		if cmd, ok := strings.CutPrefix(line, "$ nova-work "); ok {
			if code, _, stderr := runCLI(t, localize(t, dir, cmd)...); code != 0 {
				t.Fatalf("setting up from the first run: %q exits %d: %s", line, code, stderr)
			}
		}
	}

	refusals, err := onboarding.Transcript(string(raw), "nova-work", "Refusals")
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for i := 0; i < len(refusals); i++ {
		cmd, ok := strings.CutPrefix(refusals[i], "$ nova-work")
		if !ok {
			continue
		}
		if i+1 >= len(refusals) {
			t.Fatalf("the refusal %q is the last line of the block; the line it prints is missing", refusals[i])
		}
		want := refusals[i+1]
		code, stdout, stderr := runCLI(t, localize(t, dir, strings.TrimSpace(cmd))...)
		ran++
		if code != 2 {
			t.Errorf("%q exits %d, want 2 (a refusal is what could not run)", refusals[i], code)
		}
		if stdout != "" {
			t.Errorf("%q wrote to stdout: %q; a refusal belongs on stderr", refusals[i], stdout)
		}
		if got := strings.TrimSuffix(stderr, "\n"); got != want {
			t.Errorf("docs/TESTS.md promises\n  %s\nand %q printed\n  %s", want, refusals[i], got)
		}
	}
	if ran == 0 {
		t.Fatal("the `### Refusals` block holds no nova-work command; this test passed by running nothing")
	}
}

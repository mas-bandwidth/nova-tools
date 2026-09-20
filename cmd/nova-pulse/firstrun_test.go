// firstrun_test.go EXECUTES this binary's two docs/TESTS.md transcripts --
// `### First run` and `### Cutting cards, and the pool` -- through
// internal/onboarding's harness: every documented command is run and its whole
// output is compared with the block under it, same number of lines, same lines,
// same order.
//
// nova-pulse was one of the eight transcripts no test ran, and the 2026-09-19
// dogfood rerun found its POOL OK line short of `prs=` and `work=` on both
// benches (the fix is in PR #1534, unmerged and conflicting). This executes the
// block instead of correcting it once.
//
// STANDARD ERROR IS THIS TOOL'S STRUCTURED LOG, and the transcript is not.
// nova-pulse writes one JSON object per event to standard error -- `{"level":
// "INFO","msg":"launch: pulse ...` -- on every run, and a transcript that listed
// them would carry a timestamp, a pid and a guid on every line and be stale
// before it was committed. So the runner below drops the JSON objects and keeps
// everything else, which is what the PULSE lines are: the refusal on standard
// error, the two OK lines on standard output, all compared as written.
//
// TWO VALUES BELONG TO THE RUN and are DECLARED: the pulse id (a stamp and a
// hash of the launch) and `took=`. Everything else on every line -- the counts,
// the slot arithmetic, the deadline, the route names, the output paths -- is
// compared byte for byte.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// pulseID is the id a launch invents: a UTC stamp and six hex of the launch.
// It reproduces on no second run, and is the only field of a PULSE OK line that
// does not.
var pulseID = mustElide("id= (the pulse this run launched)", `id=[0-9]{8}T[0-9]{6}Z-pulse-[0-9a-f]{6}`, "id=<the pulse this run launched>")

// pulseTook is how long the run took. It is `0s` on every bench measured so
// far and is still the run's, not the document's.
var pulseTook = mustElide("took= (how long this run took)", `took=[0-9]+(\.[0-9]+)?[a-z]+`, "took=<how long this run took>")

func mustElide(name, pattern, as string) onboarding.Norm {
	norm, err := onboarding.Elide(name, pattern, as)
	if err != nil {
		panic(err)
	}
	return norm
}

// TestTESTSFirstRunIsWhatTheToolPrints runs the three launches of the first
// sitting in a copy of the fixture the section names, with the fixture's own
// stub `nova-swarm` on PATH -- so no model is called and no card is dispatched.
func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	doc := transcriptDoc(t)
	dir := t.TempDir()
	copyTree(t, filepath.Join("testdata", "example-pulse"), dir)
	t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(dir)
	executeTranscript(t, doc, "First run", pulseID)
}

// TestTESTSCutAndPoolAreWhatTheToolPrints runs the other block. Its commands
// name the fixture from the root of the checkout, which is where a reader
// typing them stands, so the fixture is copied to that same relative path in a
// directory of this test's own rather than the paths being rewritten: a
// rewritten path is no longer the line the document promised, and `out=` prints
// the path back.
func TestTESTSCutAndPoolAreWhatTheToolPrints(t *testing.T) {
	doc := transcriptDoc(t)
	dir := t.TempDir()
	copyTree(t, "testdata", filepath.Join(dir, "cmd", "nova-pulse", "testdata"))
	t.Chdir(dir)
	executeTranscript(t, doc, "Cutting cards, and the pool", pulseTook)
}

func executeTranscript(t *testing.T, doc, heading string, norms ...onboarding.Norm) {
	t.Helper()
	lines, err := onboarding.Transcript(doc, "nova-pulse", heading)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-pulse", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatalf("`### %s` holds no nova-pulse command; this test would pass by running nothing", heading)
	}
	for _, p := range onboarding.Execute(steps, runDocumented, norms...) {
		t.Error(p)
	}
}

// runDocumented calls this binary's own entry point, and returns what a reader
// saw WITHOUT this tool's structured log -- see the file comment.
func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	if s.Stdin != "" {
		return onboarding.Result{}, errReadsNothing
	}
	var out, errb bytes.Buffer
	code := run(s.Args, &out, &errb, time.Now().UTC())
	return onboarding.Result{Code: code, Stdout: out.String(), Stderr: withoutTheLog(errb.String())}, nil
}

// withoutTheLog drops the JSON objects nova-pulse writes to standard error and
// keeps every other line, so a refusal printed there is still compared as
// written and a NEW non-JSON line appearing there is still red.
func withoutTheLog(stderr string) string {
	var kept []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, `{"`) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

type readsNothing struct{}

func (readsNothing) Error() string {
	return "nova-pulse reads no stdin; a `< path` in its transcript is the document's bug"
}

var errReadsNothing = readsNothing{}

// transcriptDoc is docs/TESTS.md, read from two directories up -- BEFORE the
// test moves into a directory of its own, which is where the documented paths
// are written from and where the fixture is rebuilt.
func transcriptDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// copyTree copies a fixture directory, preserving the executable bit the
// fixture's stub `nova-swarm` needs.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	from, err := filepath.Abs(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatal(err)
	}
	err = filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, body, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

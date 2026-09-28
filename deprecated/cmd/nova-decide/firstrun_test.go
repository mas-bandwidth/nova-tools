// firstrun_test.go EXECUTES this binary's `### First run` docs/TESTS.md
// transcript -- command lines and the ladder of minds under one heading --
// through internal/onboarding's harness: every documented command is run and
// its whole output is compared with the block under it, same number of lines,
// same lines, same order. A second test counts the event lines, so the ladder
// moving back behind a heading of its own is red rather than silent
// (nova-tools #1425).
//
// nova-decide was one of the eight transcripts no test ran, and the 2026-09-19
// dogfood rerun found its ROUTE lines short of `next=` and `steps=` on all three
// benches. Running them here found one more the bench readings let through: the
// document said `floor=0.90` and the tool prints `floor=0.65`. A bench card's
// rule is that VALUES may differ between a card's run and the page; the page's
// own promise is that they do not, and that is what this file holds.
//
// THE KEYED LINE IS NOT IN EITHER BLOCK, and the section's prose says so. Asking
// a model is the one thing here that needs a key and a network; a test can only
// run that by reaching a model from `go test`, which this repository does not
// do, or by skipping it -- and the skipped form could never have passed anyway,
// because no questions file and no state file were ever written for it. A
// precondition may not hide an unrunnable step. Everything in the block runs
// with `--no-jev` and reaches no network.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// firstRunLog is the row the block's first `route` line writes when it is given
// `--log ./decide.jsonl`, which is what the section's prose now says the `log`
// line reads. It is pasted rather than produced by an extra run, because a step
// this test invented would be a step the document does not show.
const firstRunLog = `{"time":"2026-09-19T13:05:22Z","unit":"card-41","kind":"rebase","evidence":{"id":"card-41","kind":"rebase","files":2,"packages":1},"rung_tried":"flash","height":0,"confidence":0.9,"floor":0.65,"stepped_up":false,"escalated":false,"source":"rules","rowan_pick":"flash","reason":"kind rebase starts at rung flash","wait":"-","down_checked":false}
`

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	mr := miniredis.RunT(t)
	t.Setenv("NOVA_REDIS_ADDR", mr.Addr())
	executeTranscript(t, "First run")
}

// TestFirstRunHoldsTheWholeLadder counts the event lines a stranger walks
// through first, so the ladder cannot drift back behind a heading of its own:
// onboarding.FirstRun stops at the next `### `, and a ladder under `### The
// ladder of minds` is seven lines no first run reaches and no cage keeps
// (nova-tools #1425). The count is the cage: route, help and log must be here,
// not anywhere else the section might grow.
func TestFirstRunHoldsTheWholeLadder(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-decide")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, line := range lines {
		if s := onboarding.Shape(line); s != "" {
			seen[strings.Fields(s)[0]]++
		}
	}
	for prefix, want := range map[string]int{"DECIDE": 1, "ROUTE": 3, "HELP": 1, "LOG": 2} {
		if seen[prefix] != want {
			t.Errorf("`### First run` under `## nova-decide` shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}

// executeTranscript runs one named block of this tool's section as a sitting.
func executeTranscript(t *testing.T, heading string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.Transcript(string(raw), "nova-decide", heading)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-decide", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatalf("`### %s` holds no nova-decide command; this test would pass by running nothing", heading)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "decide.jsonl"), []byte(firstRunLog), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	problems, skips := onboarding.ExecuteWith(steps, runDocumented, conditions(), onboarding.Version(), onboarding.GoBuild())
	for _, p := range problems {
		t.Error(p)
	}
	// A skip is printed, never swallowed: a green whose skips are invisible
	// says less than it looks like, and the reason is the document's own words.
	for _, s := range skips {
		t.Log(s)
	}
	if len(skips) == len(steps) {
		t.Errorf("every step of `### %s` was skipped; this bench proved nothing about the block", heading)
	}
}

// conditions is what this bench can offer: the platform, and NOTHING ELSE.
//
// It used to answer for `JEV_API_KEY` too, because the `### First run` block
// carried a `# Requires: JEV_API_KEY` step. That step could never pass -- no
// questions file and no state file were ever written, so with a key it failed
// on `bad-questions` and without one it was skipped, which is how it looked
// green. It has been taken out of the block and put in the section's prose
// instead, where a reader can still see what the keyed line prints and nobody
// mistakes it for a line a test runs. There is no `Requires:` left here, so a
// Have that answers about a key would be a promise nothing asks for.
//
// This is still ExecuteWith rather than Execute, deliberately: this test wants
// to say for itself what it does with a skip, and to keep its own all-skipped
// check next to the block it is about.
func conditions() onboarding.Conditions {
	return onboarding.Conditions{GOOS: runtime.GOOS}
}

// runDocumented calls this binary's own entry point with the documented
// arguments. nova-decide reads no stdin, so a `< path` in its transcript would
// be a line this runner cannot honour, and it says so rather than running the
// command without its input.
func runDocumented(s onboarding.Step) (onboarding.Result, error) {
	if s.Stdin != "" {
		return onboarding.Result{}, errReadsNothing
	}
	var out, errb bytes.Buffer
	code := run(s.Args, &out, &errb)
	return onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()}, nil
}

type readsNothing struct{}

func (readsNothing) Error() string {
	return "nova-decide reads no stdin; a `< path` in its transcript is the document's bug"
}

var errReadsNothing = readsNothing{}

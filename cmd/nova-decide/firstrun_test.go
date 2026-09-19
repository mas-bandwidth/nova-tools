// firstrun_test.go EXECUTES this binary's two docs/TESTS.md transcripts --
// `### First run` and `### The ladder of minds` -- through internal/onboarding's
// harness: every documented command is run and its whole output is compared with
// the block under it, same number of lines, same lines, same order.
//
// nova-decide was one of the eight transcripts no test ran, and the 2026-09-19
// dogfood rerun found its ROUTE lines short of `next=` and `steps=` on all three
// benches. Running them here found one more the bench readings let through: the
// document said `floor=0.90` and the tool prints `floor=0.65`. A bench card's
// rule is that VALUES may differ between a card's run and the page; the page's
// own promise is that they do not, and that is what this file holds.
//
// TWO PRECONDITIONS ARE STATED, PER STEP, IN THE DOCUMENT ITSELF. The third line
// of `### First run` asks a model and carries `# Requires: JEV_API_KEY`; a bench
// that has no key skips that one line by name instead of reading it as drift,
// which is how the same run mis-scored it. Everything else in both blocks runs
// with `--no-jev` and reaches no network.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// firstRunLog is the row the block's first `route` line writes when it is given
// `--log ./decide.jsonl`, which is what the section's prose now says the `log`
// line reads. It is pasted rather than produced by an extra run, because a step
// this test invented would be a step the document does not show.
const firstRunLog = `{"time":"2026-09-19T13:05:22Z","unit":"card-41","kind":"rebase","evidence":{"id":"card-41","kind":"rebase","files":2,"packages":1},"rung_tried":"flash","height":0,"confidence":0.9,"floor":0.65,"stepped_up":false,"escalated":false,"source":"rules","rowan_pick":"flash","reason":"kind rebase starts at rung flash","wait":"-"}
`

func TestTESTSFirstRunIsWhatTheToolPrints(t *testing.T) {
	executeTranscript(t, "First run")
}

func TestTESTSLadderOfMindsIsWhatTheToolPrints(t *testing.T) {
	executeTranscript(t, "The ladder of minds")
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

	problems, skips := onboarding.ExecuteWith(steps, runDocumented, conditions(), onboarding.GoBuild())
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

// conditions is what this bench can offer. The key is read from the environment
// and NEVER from a file, a flag or this test: the only question asked here is
// whether the launcher already put it in the environment, and its value is not
// read, printed or copied.
func conditions() onboarding.Conditions {
	return onboarding.Conditions{
		GOOS: runtime.GOOS,
		Have: func(requirement string) bool {
			switch requirement {
			case "JEV_API_KEY", "TYPESAFE_API_KEY":
				_, set := os.LookupEnv(requirement)
				return set
			}
			return false
		},
	}
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

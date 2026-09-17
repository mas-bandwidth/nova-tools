package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// classifyFloor is the floor `classify` applies to the provider's own confidence: a
// decision below it is a suggestion, and the caller keeps today's behaviour.
const classifyFloor = 0.90

// cmdClassify asks one typed decision about one failed merge-group run: flaky under the
// queue's load, the pull request's own change, or the environment. The decision ADVISES:
// above the floor it prints the classification and the action it drives; below the floor
// the kind is unknown, neither action is taken, and the line names the raw answer.
func cmdClassify(args []string, stdout, stderr io.Writer, deps Deps) int {
	f := laneSet("classify")
	run := f.fs.Int64("run", 0, "")
	baseURL := f.fs.String("base-url", decide.DefaultBaseURL, "")
	keyEnv := f.fs.String("key-env", decide.DefaultKeyEnv, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.check()
	if *run <= 0 {
		f.problem("--run is the id of the merge-group run to classify, a positive number; refusing to guess")
	}
	if !f.done(stderr) {
		return 2
	}
	st, code := openLane("classify", *f.lane, stderr)
	if st == nil {
		return code
	}
	mr, err := deps.NewHost(st.Repo, f.dur()).MergeGroupRun(int(*run))
	if err != nil {
		fmt.Fprintf(stderr, "CLASSIFY REFUSED run=%d: %s\n", *run, oneline.Err(err))
		return 2
	}
	client, err := decide.New(*baseURL, *keyEnv)
	if err != nil {
		fmt.Fprintf(stderr, "CLASSIFY REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	answers, _, err := client.Decide(context.Background(), classifyState(mr), classifyQuestions(mr))
	if err != nil {
		fmt.Fprintf(stderr, "CLASSIFY REFUSED run=%d: %s\n", *run, oneline.Err(err))
		return 2
	}
	fmt.Fprintln(stdout, classifyLine(mr, answers, classifyFloor))
	return 0
}

// classifyState is the bounded, public evidence the decision reads: the run and its
// failing tests, their packages, whether the pull request changed those packages, and the
// runner. It is a run id and public CI identifiers, never a secret and never a body.
func classifyState(mr merge.MergeRun) string {
	s := fmt.Sprintf("merge-group run %d", mr.ID)
	if mr.PR > 0 {
		s += fmt.Sprintf(" for pull request %d", mr.PR)
	}
	s += " failed.\n"
	for _, j := range mr.Jobs {
		s += fmt.Sprintf("job %s runner=%s package=%s changed_by_pr=%t tests=%s\n",
			oneline.Field(j.Name), oneline.Field(j.Runner), oneline.Field(j.Package),
			j.Changed, oneline.Field(strings.Join(j.Tests, ",")))
	}
	return s
}

// classifyQuestions asks the one typed choice. The features of the ask -- the test names,
// the package they live in, whether the pull request changed that package, and the runner
// -- are the question's instructions.
func classifyQuestions(mr merge.MergeRun) map[string]decide.Question {
	instructions := "A merge-group run failed. Decide what the failure is, choosing exactly one option. " +
		"flaky-under-load: a test that passed on its own and failed under the merge queue's load; " +
		"environment: the runner or the environment failed, not the test; " +
		"own-change: the failing test lives in a package this pull request changed, so the change is the cause. " +
		"Evidence: " + classifyState(mr)
	return map[string]decide.Question{
		"kind": {
			Instructions: instructions,
			Choice: map[string]string{
				"flaky-under-load": "a test that passed on its own and failed under the merge queue's load",
				"own-change":       "the failing test lives in a package this pull request changed",
				"environment":      "the runner or the environment failed, not the test",
			},
		},
	}
}

// classifyLine renders the one line `classify` prints. Above the floor the kind drives the
// action: flaky-under-load and environment re-run, own-change parks. Below the floor the
// kind is unknown, both actions are no, and below= names the raw answer.
func classifyLine(mr merge.MergeRun, answers map[string]decide.Answer, floor float64) string {
	a := answers["kind"]
	raw := strings.TrimSpace(a.Choice)
	kind, rerun, park := "unknown", "no", "no"
	if a.Confidence >= floor {
		switch raw {
		case "flaky-under-load", "environment":
			kind, rerun, park = raw, "yes", "no"
		case "own-change":
			kind, rerun, park = raw, "no", "yes"
		}
	}
	line := fmt.Sprintf("CLASSIFY run=%d pr=%d kind=%s conf=%.2f floor=%.2f rerun=%s park=%s",
		mr.ID, mr.PR, oneline.Field(kind), a.Confidence, floor, oneline.Field(rerun), oneline.Field(park))
	if a.Confidence < floor {
		if raw == "" {
			raw = "-"
		}
		line += " below=" + oneline.Field(raw)
	}
	return line
}

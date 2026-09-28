package merge

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// Merge-queue events: docs/SPEC-LOGS.md, "The merge queue":
//
//	"`enqueue` with the PR and head sha, `group_start` with the group name and
//	the run id, `group_verdict` with the conclusion, the failing test and the
//	poison verdict, and `park` when a PR is set aside with the reason and the age."
//
// One JSON object per state change, one line, written BESIDE the durable store
// (queue.json and the lane records) and never instead of it: the queue stays
// the record these lines point at, and a nil sink (or a nil Events) writes
// nothing at all.

// The four merge-queue event names, the spec's own nouns.
const (
	EventEnqueue      = "enqueue"
	EventGroupStart   = "group_start"
	EventGroupVerdict = "group_verdict"
	EventPark         = "park"
)

// Events is the merge queue's structured log sink: one JSON object per state
// change, one line, on the writer the caller hands it. It is additive: the
// enqueue, group and park call sites write their durable records exactly as
// before and then tell the sink, so a caller with no sink sees no change.
type Events struct {
	Sink io.Writer
}

// DefaultEvents is the production sink: stderr, where docs/SPEC-LOGS.md puts a
// verb's JSON lines (on a bench a unit's stderr is the journal Alloy reads).
// NewEnqueuer and UpdateQueue use it, so every production enqueue through the
// one door and every park written to queue.json emits its line with no caller
// wiring. A test swaps it for a buffer; nil silences it.
var DefaultEvents = &Events{Sink: os.Stderr}

// emit writes one event line. A nil Events or a nil Sink is silent: the
// emitter is additive, never a replacement.
func (e *Events) emit(fields map[string]any) {
	if e == nil || e.Sink == nil {
		return
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return
	}
	fmt.Fprintf(e.Sink, "%s\n", raw)
}

// Enqueue logs one admission to the merge queue: the PR and its head sha.
func (e *Events) Enqueue(pr int, head string) {
	e.emit(map[string]any{"event": EventEnqueue, "pr": pr, "head": head})
}

// GroupStart logs one merge-group run starting: the group name and the run id.
func (e *Events) GroupStart(group string, run int64) {
	e.emit(map[string]any{"event": EventGroupStart, "group": group, "run": run})
}

// GroupVerdict logs one merge-group run judged: the conclusion, the failing
// test and the poison verdict.
func (e *Events) GroupVerdict(conclusion, failingTest, poisonVerdict string) {
	e.emit(map[string]any{"event": EventGroupVerdict, "conclusion": conclusion,
		"failing_test": failingTest, "poison_verdict": poisonVerdict})
}

// Park logs one PR set aside: the reason and the age.
func (e *Events) Park(reason, age string) {
	e.emit(map[string]any{"event": EventPark, "reason": reason, "age": age})
}

// GroupVerdictResult is one merge-group run judged: the conclusion the run
// carried, the failing test the poison detector armed on ("" when none
// armed), the poison verdict (the classify decision for this run), and
// whether the run's pull request was parked.
type GroupVerdictResult struct {
	Group         string
	Run           int64
	Conclusion    string
	FailingTest   string
	PoisonVerdict string
	Parked        bool
}

// RecordGroupVerdict judges one merge-group run over a lane and records what
// it decided: a group_start line, a group_verdict line, and -- when the
// poison detector arms (a test that failed at least twice in a package the
// pull request changed, whose classify for the head is own-change) -- the
// park in queue.json plus the park line. The queue stays the durable store;
// the lines point at it.
func RecordGroupVerdict(lane, group string, run MergeRun, failures []Failure, changed map[string]bool, classifyVerdict string, now time.Time, ev *Events) (GroupVerdictResult, error) {
	ev.GroupStart(group, run.ID)
	conclusion := "success"
	for _, j := range run.Jobs {
		if len(j.Tests) > 0 {
			conclusion = "failure"
			break
		}
	}
	failingTest, pkg, count := armedFailure(failures, changed)
	out := GroupVerdictResult{Group: group, Run: run.ID, Conclusion: conclusion,
		FailingTest: failingTest, PoisonVerdict: classifyVerdict}
	ev.GroupVerdict(conclusion, failingTest, classifyVerdict)
	if failingTest == "" || classifyVerdict != ClassOwnChange || run.PR < 1 {
		return out, nil
	}
	p := Park{PR: run.PR, Test: failingTest, Package: pkg, Runs: count, At: now.UTC().Format(Stamp)}
	if err := PutParkWithEvents(lane, p, now, ev); err != nil {
		return out, err
	}
	out.Parked = true
	return out, nil
}

// armedFailure is the poison detector's arm: a test that failed at least
// twice, in a package the pull request changed. The first armed failure wins,
// most runs first and then by test name, so two reads of one forge name the
// same one.
func armedFailure(failures []Failure, changed map[string]bool) (test, pkg string, count int) {
	ordered := append([]Failure(nil), failures...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Count != ordered[j].Count {
			return ordered[i].Count > ordered[j].Count
		}
		return ordered[i].Test < ordered[j].Test
	})
	for _, f := range ordered {
		if f.Count < 2 || !changed[f.Package] {
			continue
		}
		return f.Test, f.Package, f.Count
	}
	return "", "", 0
}

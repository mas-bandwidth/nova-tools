package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// A FAKE gh with no network anywhere: the merge queue's contents, the open pull requests
// and their head runs are fields a test sets, and every mutation the sweep would make is
// recorded. This is the whole point of the injected SweepHost -- the pass is a function of
// what the forge said, and a test that cannot say what the forge said cannot test it.
type fakeSweep struct {
	queue []merge.QueueEntry
	prs   []merge.SweepPR
	runs  map[string]merge.SweepRun

	enqueued []int
	rerun    []int64
	dequeued []int

	enqueueErr map[int]error
	rerunErr   map[int64]error
	dequeueErr map[int]error

	repo, branch string
}

func (f *fakeSweep) Queue() ([]merge.QueueEntry, error) { return f.queue, nil }
func (f *fakeSweep) OpenPRs() ([]merge.SweepPR, error)  { return f.prs, nil }

func (f *fakeSweep) HeadRun(branch string) (merge.SweepRun, error) {
	return f.runs[branch], nil
}

func (f *fakeSweep) Enqueue(pr int) error {
	if err := f.enqueueErr[pr]; err != nil {
		return err
	}
	f.enqueued = append(f.enqueued, pr)
	return nil
}

func (f *fakeSweep) Rerun(run int64) error {
	if err := f.rerunErr[run]; err != nil {
		return err
	}
	f.rerun = append(f.rerun, run)
	return nil
}

func (f *fakeSweep) Dequeue(pr int) error {
	if err := f.dequeueErr[pr]; err != nil {
		return err
	}
	f.dequeued = append(f.dequeued, pr)
	return nil
}

// sweepDeps hands the verb the fake forge and records the repo, branch and timeout it was
// handed, so the flags are under test and not only the pass.
func sweepDeps(f *fakeSweep) Deps {
	return Deps{NewSweepHost: func(repo, branch string, timeout time.Duration) merge.SweepHost {
		f.repo, f.branch = repo, branch
		return f
	}}
}

func (f *fakeSweep) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	exit := run(args, &out, &errb, sweepDeps(f))
	return exit, out.String(), errb.String()
}

// A green pull request off the queue is enqueued; a queued one is left alone.
func TestSweepEnqueuesGreenUnqueuedRowanPRs(t *testing.T) {
	f := &fakeSweep{
		prs: []merge.SweepPR{
			{Number: 101, HeadRef: "rowan/impl-merge-sweep", MergeState: "CLEAN"},
			{Number: 102, HeadRef: "rowan/already-queued", MergeState: "CLEAN"},
		},
		queue: []merge.QueueEntry{{PR: 102, State: "MERGEABLE"}},
		runs: map[string]merge.SweepRun{
			"rowan/impl-merge-sweep": {ID: 7, Status: "completed", Conclusion: "success"},
			"rowan/already-queued":   {ID: 8, Status: "completed", Conclusion: "success"},
		},
	}
	exit, stdout, stderr := f.run(t, "sweep", "--repo", "mas-bandwidth/nova-tools", "--branch", "dev", "--once")
	if exit != 0 {
		t.Fatalf("sweep: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	// Already queued is not re-enqueued; the green unqueued one is.
	if len(f.enqueued) != 1 || f.enqueued[0] != 101 {
		t.Fatalf("want exactly [101] enqueued, got %v", f.enqueued)
	}
	// One line, and only one, with the counts the card names.
	contains(t, stdout, "SWEEP queued=1 reruns=0 dequeued=0 inqueue=1\n")
	if lines := strings.Count(stdout, "\n"); lines != 1 {
		t.Fatalf("sweep prints one line; got %d:\n%s", lines, stdout)
	}
	if f.repo != "mas-bandwidth/nova-tools" || f.branch != "dev" {
		t.Fatalf("the fake forge was handed repo=%q branch=%q", f.repo, f.branch)
	}
}

// A failed or cancelled head run is rerun ONLY while the queue holds five entries or
// fewer; at six the pass leaves it entirely alone.
func TestSweepRerunsFailedRunsOnlyWhenTheQueueIsShallow(t *testing.T) {
	failed := []merge.SweepPR{{Number: 201, HeadRef: "rowan/red", MergeState: "CLEAN"}}
	run := map[string]merge.SweepRun{"rowan/red": {ID: 55, Status: "completed", Conclusion: "failure"}}

	t.Run("depth six leaves the run alone", func(t *testing.T) {
		f := &fakeSweep{prs: failed, runs: run, queue: queueOf(6)}
		exit, stdout, stderr := f.run(t, "sweep", "--repo", "o/n", "--branch", "dev", "--once")
		if exit != 0 {
			t.Fatalf("sweep: exit %d\n%s\n%s", exit, stdout, stderr)
		}
		if len(f.rerun) != 0 {
			t.Fatalf("a queue of six holds no rerun, got %v", f.rerun)
		}
		contains(t, stdout, "SWEEP queued=0 reruns=0 dequeued=0 inqueue=6\n")
	})

	t.Run("depth five reruns the run once", func(t *testing.T) {
		f := &fakeSweep{prs: failed, runs: run, queue: queueOf(5)}
		exit, stdout, stderr := f.run(t, "sweep", "--repo", "o/n", "--branch", "dev", "--once")
		if exit != 0 {
			t.Fatalf("sweep: exit %d\n%s\n%s", exit, stdout, stderr)
		}
		if len(f.rerun) != 1 || f.rerun[0] != 55 {
			t.Fatalf("want exactly [55] rerun, got %v", f.rerun)
		}
		contains(t, stdout, "SWEEP queued=0 reruns=1 dequeued=0 inqueue=5\n")
	})
}

// An UNMERGEABLE queue entry is dequeued, and it is touched in no other way: not
// enqueued, not rerun, even when its checks are green or its head run is red.
func TestSweepDequeuesOnlyUnmergeableEntries(t *testing.T) {
	f := &fakeSweep{
		queue: []merge.QueueEntry{
			{PR: 301, State: "UNMERGEABLE"},
			{PR: 302, State: "MERGEABLE"},
		},
		prs: []merge.SweepPR{
			{Number: 301, HeadRef: "rowan/stuck", MergeState: "CLEAN"},
			{Number: 302, HeadRef: "rowan/fine", MergeState: "CLEAN"},
		},
		runs: map[string]merge.SweepRun{
			"rowan/stuck": {ID: 71, Status: "completed", Conclusion: "failure"},
			"rowan/fine":  {ID: 72, Status: "completed", Conclusion: "success"},
		},
	}
	exit, stdout, stderr := f.run(t, "sweep", "--repo", "o/n", "--branch", "dev", "--once")
	if exit != 0 {
		t.Fatalf("sweep: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if len(f.dequeued) != 1 || f.dequeued[0] != 301 {
		t.Fatalf("want exactly [301] dequeued, got %v", f.dequeued)
	}
	if len(f.enqueued) != 0 {
		t.Fatalf("a queued entry is not enqueued again, got %v", f.enqueued)
	}
	// 301's head run is red and the queue is shallow, so it WOULD rerun if being
	// queued did not already take it out of the pass: the UNMERGEABLE entry is
	// dequeued and otherwise untouched, never enqueued and never rerun.
	if len(f.rerun) != 0 {
		t.Fatalf("a queued entry is not rerun, got %v", f.rerun)
	}
	contains(t, stdout, "SWEEP queued=0 reruns=0 dequeued=1 inqueue=2\n")
}

// DIRTY pull requests and heads that are not rowan/* are not swept at all.
func TestSweepSkipsDirtyAndNonRowanPRs(t *testing.T) {
	f := &fakeSweep{
		prs: []merge.SweepPR{
			{Number: 401, HeadRef: "rowan/conflicted", MergeState: "DIRTY"},
			{Number: 402, HeadRef: "glenn/handwritten", MergeState: "CLEAN"},
			{Number: 403, HeadRef: "rowan/pending", MergeState: "CLEAN"},
		},
		runs: map[string]merge.SweepRun{
			"rowan/conflicted":  {ID: 81, Status: "completed", Conclusion: "success"},
			"glenn/handwritten": {ID: 82, Status: "completed", Conclusion: "success"},
			"rowan/pending":     {ID: 83, Status: "in_progress"},
		},
	}
	exit, stdout, stderr := f.run(t, "sweep", "--repo", "o/n", "--branch", "dev", "--once")
	if exit != 0 {
		t.Fatalf("sweep: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if len(f.enqueued) != 0 || len(f.rerun) != 0 || len(f.dequeued) != 0 {
		t.Fatalf("dirty, foreign and unfinished heads are left alone: enqueued=%v reran=%v dequeued=%v",
			f.enqueued, f.rerun, f.dequeued)
	}
	contains(t, stdout, "SWEEP queued=0 reruns=0 dequeued=0 inqueue=0\n")
}

// A sweep with no --once is refused: this is one pass of the old loop, and a loop with no
// deadline is a tool that has stopped saying anything.
func TestSweepRefusesWithoutOnce(t *testing.T) {
	f := &fakeSweep{}
	exit, stdout, stderr := f.run(t, "sweep", "--repo", "o/n", "--branch", "dev")
	if exit != 2 {
		t.Fatalf("a sweep that is not one pass is refused: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "--once")
}

func queueOf(n int) []merge.QueueEntry {
	out := make([]merge.QueueEntry, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, merge.QueueEntry{PR: 1000 + i, State: "MERGEABLE"})
	}
	return out
}

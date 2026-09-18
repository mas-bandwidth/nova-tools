package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// The queue section (docs/SPEC-MERGE.md, "The merge queue (#1142), 2026-09-17") and its
// nine red tests. Every fake here is a fake host and a fake clock; nothing reaches a
// network.

func (l *lab) loadQueue() merge.Queue {
	l.t.Helper()
	st, err := merge.Load(l.lane)
	if err != nil {
		l.t.Fatal(err)
	}
	q, err := merge.LoadQueue(l.lane, st)
	if err != nil {
		l.t.Fatal(err)
	}
	return *q
}

func eqInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// 1. hold then a sweep: the sweep is exit 2 with the release remedy, the fake host
// receives no enqueue, and <lane>/hold's first line is the reason.
func TestQueueHoldRefusesTheSweepAndItsFirstLineIsTheReason(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	exit, stdout, stderr := l.run("queue", "--lane", l.lane, "hold", "the base is frozen", "--who", "rowan")
	if exit != 0 {
		t.Fatalf("hold: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "QUEUE HOLD reason=the\\x20base\\x20is\\x20frozen")
	contains(t, stdout, "by=rowan")
	raw, err := os.ReadFile(filepath.Join(l.lane, merge.HoldName))
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(string(raw), "\n", 2)[0]
	if first != "the base is frozen" {
		t.Fatalf("the hold file's first line is the reason, got %q", first)
	}
	l.host.OpenQueue = []merge.PR{{Number: 11, Author: "pat", Base: "main", HeadRef: "f11",
		HeadOID: strings.Repeat("a", 40), Mergeable: "MERGEABLE"}}
	l.host.SetChecks(strings.Repeat("a", 40), 3, 0)
	exit, stdout, stderr = l.run("queue", "--lane", l.lane, "sweep", "--window", "1h")
	if exit != 2 {
		t.Fatalf("a held sweep is exit 2: %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "QUEUE REFUSED")
	contains(t, stderr, "nova-merge queue release")
	q := l.loadQueue()
	if len(q.Queued) != 0 {
		t.Errorf("a held sweep enqueues nothing, got %v", q.Queued)
	}
}

// 2. skip 12 13, then unskip 13: the order omits both, skipped holds 12, and a sweep
// over a fake window with 12 green does not enqueue it.
func TestQueueSkipThenUnskip(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	for _, n := range []int{12, 13} {
		if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", itoa(n)); exit != 0 {
			t.Fatalf("add: %s", errb)
		}
	}
	if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "skip", "12", "13"); exit != 0 {
		t.Fatalf("skip: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if q := l.loadQueue(); len(q.Queued) != 0 || !eqInts(q.Skipped, []int{12, 13}) {
		t.Fatalf("after skip, queued=%v skipped=%v", q.Queued, q.Skipped)
	}
	if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "unskip", "13"); exit != 0 {
		t.Fatalf("unskip: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	q := l.loadQueue()
	if !eqInts(q.Queued, []int{13}) || !eqInts(q.Skipped, []int{12}) {
		t.Fatalf("after unskip 13, queued=%v skipped=%v", q.Queued, q.Skipped)
	}
	// A sweep with 12 green does not enqueue it: it is skipped.
	l.host.OpenQueue = []merge.PR{{Number: 12, Author: "pat", Base: "main", HeadRef: "f12",
		HeadOID: strings.Repeat("b", 40), Mergeable: "MERGEABLE"}}
	l.host.SetChecks(strings.Repeat("b", 40), 3, 0)
	if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "sweep", "--window", "1h"); exit != 0 {
		t.Fatalf("sweep: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if q := l.loadQueue(); !eqInts(q.Queued, []int{13}) {
		t.Errorf("a skipped pull request is never swept; queued=%v", q.Queued)
	}
}

// 3. front 7 on a four-entry queue: one QUEUE FRONT, the order [7, 4, 5, 6], and after
// the host reports 7 merged the rest keep their old order.
func TestQueueFrontPuts7FirstAndTheRestKeepTheirOrder(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	l.host.SetChecks(l.baseSHA(), 3, 0)
	for _, n := range []int{4, 5, 6} {
		if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", itoa(n)); exit != 0 {
			t.Fatalf("add: %s", errb)
		}
	}
	oid := l.branch("feature-7", "seven.txt", "the seven change\n", "a change on seven")
	l.host.PRs[7] = merge.PR{Number: 7, Author: "pat", Base: "main", HeadRef: "feature-7",
		HeadOID: oid, Mergeable: "MERGEABLE", URL: "https://example.invalid/seven"}
	l.host.SetChecks(oid, 3, 0)
	if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "7", "--needs-read"); exit != 0 {
		t.Fatalf("add: %s", errb)
	}
	exit, stdout, stderr := l.run("queue", "--lane", l.lane, "front", "7")
	if exit != 0 {
		t.Fatalf("front: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "QUEUE FRONT entry=7 position=1 queued=4 displaced=3")
	if q := l.loadQueue(); !eqInts(q.Queued, []int{7, 4, 5, 6}) {
		t.Fatalf("front order: %v", q.Queued)
	}
	// The pass retires 7 first; gate and read it, then a pass lands it.
	_, build, _ := l.run("run", "--lane", l.lane, "--once")
	m := mergeSHAOf(t, build, "7")
	base := l.baseSHA()
	if exit, _, errb := l.run("gate", "--lane", l.lane, "--pr", "7", "--head", oid,
		"--base-sha", base, "--merge", m, "--verdict", "green", "--summary", l.summary("q7")); exit != 0 {
		t.Fatalf("gate: %s", errb)
	}
	if exit, _, errb := l.run("read", "--lane", l.lane, "--pr", "7", "--who", "emma",
		"--head", oid, "--verdict", "approve"); exit != 0 {
		t.Fatalf("read: %s", errb)
	}
	exit, stdout, stderr = l.run("run", "--lane", l.lane, "--once")
	if exit != 0 {
		t.Fatalf("run: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "MERGE OK entry=7")
	if q := l.loadQueue(); !eqInts(q.Queued, []int{4, 5, 6}) {
		t.Errorf("after 7 landed the rest keep their old order, got %v", q.Queued)
	}
}

// 4. A sweep over a fake host of ten open pull requests -- green, stale red, current
// red, dirty, skipped, parked and already queued -- counts each bucket and enqueues only
// the green one.
func TestQueueSweepCountsEachBucketAndEnqueuesOnlyGreen(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	// The lane's queue already holds six entries, so the stale-red bound does not
	// re-enqueue; 4 is skipped and 5 is parked (skipped plus a park record). The rest
	// exist only at the fake host.
	for _, n := range []int{6, 11, 12, 13, 14, 15} {
		if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", itoa(n)); exit != 0 {
			t.Fatalf("add: %s", errb)
		}
	}
	if exit, _, errb := l.run("queue", "--lane", l.lane, "skip", "4"); exit != 0 {
		t.Fatalf("skip: %s", errb)
	}
	if exit, _, errb := l.run("queue", "--lane", l.lane, "skip", "5"); exit != 0 {
		t.Fatalf("skip: %s", errb)
	}
	green := strings.Repeat("1", 40)
	staleHead := strings.Repeat("2", 40)
	staleOld := strings.Repeat("9", 40)
	currentRed := strings.Repeat("3", 40)
	dirty := strings.Repeat("4", 40)
	l.host.OpenQueue = []merge.PR{
		{Number: 1, Base: "main", HeadRef: "f1", HeadOID: green, Mergeable: "MERGEABLE"},
		{Number: 2, Base: "main", HeadRef: "f2", HeadOID: staleHead, Mergeable: "MERGEABLE"},
		{Number: 3, Base: "main", HeadRef: "f3", HeadOID: currentRed, Mergeable: "MERGEABLE"},
		{Number: 4, Base: "main", HeadRef: "f4", HeadOID: strings.Repeat("4", 40), Mergeable: "MERGEABLE"},
		{Number: 5, Base: "main", HeadRef: "f5", HeadOID: strings.Repeat("5", 40), Mergeable: "MERGEABLE"},
		{Number: 6, Base: "main", HeadRef: "f6", HeadOID: strings.Repeat("6", 40), Mergeable: "MERGEABLE"},
		{Number: 7, Base: "main", HeadRef: "f7", HeadOID: dirty, Mergeable: "CONFLICTING"},
		{Number: 8, Base: "main", HeadRef: "f8", HeadOID: strings.Repeat("8", 40), Mergeable: "MERGEABLE"},
		{Number: 9, Base: "main", HeadRef: "f9", HeadOID: strings.Repeat("9", 40), Mergeable: "MERGEABLE"},
		{Number: 10, Base: "main", HeadRef: "fa", HeadOID: strings.Repeat("a", 40), Mergeable: "MERGEABLE"},
	}
	l.host.SetCheckRuns(green, merge.CheckDetail{Name: "ci-ok", Conclusion: "success", SHA: green})
	l.host.SetCheckRuns(currentRed, merge.CheckDetail{Name: "ci-ok", Conclusion: "failure", SHA: currentRed})
	// A stale red: a failure recorded against a sha the head has moved past.
	l.host.SetCheckRuns(staleHead, merge.CheckDetail{Name: "old-ci", Conclusion: "failure", SHA: staleOld})
	// 3, 8 and 10 are current reds: nothing to enqueue. 9 is stale red too.
	l.host.SetCheckRuns(strings.Repeat("8", 40), merge.CheckDetail{Name: "ci-ok", Conclusion: "failure", SHA: strings.Repeat("8", 40)})
	l.host.SetCheckRuns(strings.Repeat("a", 40), merge.CheckDetail{Name: "ci-ok", Conclusion: "failure", SHA: strings.Repeat("a", 40)})
	l.host.SetCheckRuns(strings.Repeat("9", 40), merge.CheckDetail{Name: "old-ci", Conclusion: "failure", SHA: strings.Repeat("b", 40)})
	l.host.SetChecks(dirty, 2, 0)
	// Park 5 with a record so it is counted parked, not skipped.
	if err := merge.PutPark(l.lane, merge.Park{PR: 5, Test: "TestX", Package: "pkg/x", Runs: 2, At: "2026-09-17T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "sweep", "--window", "1h"); exit != 0 {
		t.Fatalf("sweep: exit %d\n%s\n%s", exit, stdout, stderr)
	} else {
		contains(t, stdout, "QUEUE SWEEP")
		contains(t, stdout, "scanned=10")
		contains(t, stdout, "green=1")
		contains(t, stdout, "queued=1")
		contains(t, stdout, "already=1")
		contains(t, stdout, "parked=1")
		contains(t, stdout, "skipped=1")
		contains(t, stdout, "dirty=1")
		contains(t, stdout, "stale_red=2")
		contains(t, stdout, "rerun=0")
	}
}

// 5. A sweep with a stale red and a queue of six does not re-enqueue it; the same sweep
// with a queue of five does.
func TestQueueSweepStaleRedBound(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		entries int
		want    string
	}{{"six", 6, "rerun=0"}, {"five", 5, "rerun=1"}} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := newLab(t)
			l.init("main")
			for n := 1; n <= tc.entries; n++ {
				if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", itoa(n)); exit != 0 {
					t.Fatalf("add: %s", errb)
				}
			}
			head := strings.Repeat("d", 40)
			old := strings.Repeat("e", 40)
			l.host.OpenQueue = []merge.PR{{Number: 99, Base: "main", HeadRef: "f99", HeadOID: head, Mergeable: "MERGEABLE"}}
			l.host.SetCheckRuns(head, merge.CheckDetail{Name: "old-ci", Conclusion: "failure", SHA: old})
			if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "sweep", "--window", "1h"); exit != 0 {
				t.Fatalf("sweep: exit %d\n%s\n%s", exit, stdout, stderr)
			} else {
				contains(t, stdout, tc.want)
			}
		})
	}
}

// 6. The detector with a fake host: one test failed twice in a changed package plus an
// own-change classify parks the pull request, a green re-sweep does not enqueue it, and
// a flaky-under-load classify never parks.
func TestQueuePoisonDetectorParksOnOwnChangeOnly(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	head := strings.Repeat("f", 40)
	l.host.OpenQueue = []merge.PR{{Number: 21, Base: "main", HeadRef: "f21", HeadOID: head, Mergeable: "MERGEABLE"}}
	l.host.SetCheckRuns(head, merge.CheckDetail{Name: "TestFlaky", Conclusion: "failure", SHA: head})
	l.host.Failures = map[int][]merge.Failure{
		21: {{Test: "TestFlaky", Package: "pkg/x", Count: 2}},
	}
	l.host.Changed = map[int][]string{21: {"pkg/x"}}
	l.host.Issues = map[int]string{21: "https://example.invalid/issues/21"}
	if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", "21"); exit != 0 {
		t.Fatalf("add: %s", errb)
	}
	// flaky-under-load does not arm the detector.
	if exit, stdout, stderr := l.run("queue", "classify", "--lane", l.lane, "--run", "run-1", "--head", head,
		"--verdict", "flaky-under-load"); exit != 0 {
		t.Fatalf("classify: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "sweep", "--window", "1h"); exit != 0 {
		t.Fatalf("sweep: exit %d\n%s\n%s", exit, stdout, stderr)
	} else {
		absent(t, stdout, "QUEUE PARK")
	}
	// own-change arms it: the sweep parks, names the test and the issue.
	l.now = l.now.Add(2 * 1e9) // +2s
	if exit, stdout, stderr := l.run("queue", "classify", "--lane", l.lane, "--run", "run-1", "--head", head,
		"--verdict", "own-change"); exit != 0 {
		t.Fatalf("classify: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "sweep", "--window", "1h"); exit != 0 {
		t.Fatalf("sweep: exit %d\n%s\n%s", exit, stdout, stderr)
	} else {
		contains(t, stdout, "QUEUE PARK entry=21")
		contains(t, stdout, "test=TestFlaky")
		contains(t, stdout, "package=pkg/x")
		contains(t, stdout, "issue=https://example.invalid/issues/21")
	}
	// A green re-sweep does not enqueue it: it is parked.
	l.host.SetCheckRuns(head, merge.CheckDetail{Name: "ci-ok", Conclusion: "success", SHA: head})
	if exit, stdout, stderr := l.run("queue", "--lane", l.lane, "sweep", "--window", "1h"); exit != 0 {
		t.Fatalf("sweep: exit %d\n%s\n%s", exit, stdout, stderr)
	} else {
		absent(t, stdout, "QUEUE PARK")
	}
	if q := l.loadQueue(); len(q.Queued) != 0 {
		t.Errorf("a parked pull request is never re-enqueued, queued=%v", q.Queued)
	}
}

// 7. queue classify --run against a fake remote: one immutable record, CLASSIFY OK pushed=true,
// a second classification for the same run is a second file with the newest at winning,
// and an unknown --verdict is exit 2 naming the three classes.
func TestQueueClassifyRecordsAndRefusesUnknownVerdict(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	head := strings.Repeat("c", 40)
	if exit, stdout, stderr := l.run("queue", "classify", "--lane", l.lane, "--run", "run-9", "--head", head,
		"--verdict", "environment"); exit != 0 {
		t.Fatalf("classify: exit %d\n%s\n%s", exit, stdout, stderr)
	} else {
		contains(t, stdout, "CLASSIFY OK run=run-9")
		contains(t, stdout, "verdict=environment")
		contains(t, stdout, "pushed=true")
	}
	first, err := filepath.Glob(filepath.Join(l.lane, merge.ClassifyDir, "*.json"))
	if err != nil || len(first) != 1 {
		t.Fatalf("one immutable record, got %v (%v)", first, err)
	}
	l.now = l.now.Add(3 * 1e9)
	if exit, _, errb := l.run("queue", "classify", "--lane", l.lane, "--run", "run-9", "--head", head,
		"--verdict", "own-change"); exit != 0 {
		t.Fatalf("classify: %s", errb)
	}
	files, _ := filepath.Glob(filepath.Join(l.lane, merge.ClassifyDir, "*.json"))
	if len(files) != 2 {
		t.Fatalf("a second classification is a second file, got %v", files)
	}
	recs, err := merge.LoadClassifies(l.lane)
	if err != nil {
		t.Fatal(err)
	}
	if got := recs["run-9"].Verdict; got != "own-change" {
		t.Errorf("the newest at for a run wins, got %q", got)
	}
	exit, _, stderr := l.run("queue", "classify", "--lane", l.lane, "--run", "run-9", "--head", head,
		"--verdict", "banana")
	if exit != 2 {
		t.Fatalf("an unknown verdict is exit 2, got %d", exit)
	}
	contains(t, stderr, "flaky-under-load")
	contains(t, stderr, "own-change")
	contains(t, stderr, "environment")
}

// 8. queue with no subverb, hold "", skip with no <pr>, front on a missing pull request,
// sweep with no --window and classify with no --run: each exit 2 with its one remedy
// line, and the fake remote sees no push.
func TestQueueRefusalsCarryOneRemedyLine(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	pushedBefore := len(l.pushes())
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no subverb", []string{"queue", "--lane", l.lane}, "hold, release, skip, unskip, front, sweep"},
		{"empty reason", []string{"queue", "--lane", l.lane, "hold", "", "--who", "x"}, "nova-merge queue hold"},
		{"skip no pr", []string{"queue", "--lane", l.lane, "skip"}, "nova-merge queue skip <pr>..."},
		{"front missing", []string{"queue", "--lane", l.lane, "front", "404"}, "nova-merge add --lane " + l.lane + " --pr 404"},
		{"sweep no window", []string{"queue", "--lane", l.lane, "sweep"}, "--window <duration>"},
		{"classify no run", []string{"queue", "classify", "--lane", l.lane, "--verdict", "own-change"}, "--run"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			exit, stdout, stderr := l.run(tc.args...)
			if exit != 2 {
				t.Fatalf("exit %d, want 2\n%s\n%s", exit, stdout, stderr)
			}
			contains(t, stderr, "REFUSED")
			contains(t, stderr, tc.want)
			if len(l.pushes()) != pushedBefore {
				t.Errorf("a refusal pushes nothing; the remote saw %v", l.pushes())
			}
		})
	}
}

// 9. Two concurrent hold/skip writers: every write lands and queue.json parses at every
// read. The sweep window is measured by the fake clock, never the wall clock.
//
// NOT parallel, and on the MACHINE's clock: the injected lock clock is process-wide and
// every waiter's poll advances it, so this test's own three waiting writers move the
// deadline past themselves and are refused before the holder has finished its write. That
// was green on CI and red on the Studio the runners share, which is a test asserting the
// machine. realLockClock puts the real clock back for this test alone, and running alone
// is what makes that safe.
func TestQueueWritesAreWholeUnderConcurrentWriters(t *testing.T) {
	realLockClock(t)
	l := newLab(t)
	l.init("main")
	for n := 1; n <= 8; n++ {
		if exit, _, errb := l.run("add", "--lane", l.lane, "--pr", itoa(n)); exit != 0 {
			t.Fatalf("add: %s", errb)
		}
	}
	done := make(chan int, 4)
	for i := 0; i < 4; i++ {
		go func(i int) {
			var code int
			if i%2 == 0 {
				code, _, _ = l.run("queue", "--lane", l.lane, "skip", itoa(i+1))
			} else {
				code, _, _ = l.run("queue", "--lane", l.lane, "hold", "held by writer", "--who", "x")
			}
			done <- code
		}(i)
	}
	for i := 0; i < 4; i++ {
		if code := <-done; code != 0 {
			t.Fatalf("a concurrent writer failed with exit %d", code)
		}
	}
	// queue.json parses at this read.
	st, err := merge.Load(l.lane)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := merge.LoadQueue(l.lane, st); err != nil {
		t.Fatalf("queue.json stopped parsing: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(l.lane, merge.QueueName))
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("queue.json is not whole JSON: %v\n%s", err, raw)
	}
}

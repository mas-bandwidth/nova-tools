package main

// The merge lane's own structured events (#1326's emitter, this slice's verbs). Every test
// here asserts the SHAPE of the lines a verb wrote -- the kind, the labels a LogQL query
// selects on, and the fields the dashboard's panels read out of the message -- and none of
// them opens a socket: batch drives a real git against the bare fixture, queue drives a
// lane and a fake host, and react drives miniredis.
//
// They were seen red before cmd/nova-merge/events.go existed.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
)

// emitted is one structured line, decoded. The fields are the spec's fifteen; a test names
// only the ones it is about.
type emitted struct {
	Level  string `json:"level"`
	Source string `json:"source"`
	Bench  string `json:"bench"`
	Verb   string `json:"verb"`
	Card   string `json:"card"`
	PR     int    `json:"pr"`
	Event  string `json:"event"`
	Msg    string `json:"msg"`
	Err    string `json:"err"`
	TS     string `json:"ts"`
	GUID   string `json:"guid"`
}

// readEvents reads the file --log named. Every line must be one JSON object: a log with a
// half-written line in it is a log Alloy's json stage drops, so the decode is the assertion.
func readEvents(t *testing.T, path string) []emitted {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the --log file was not written: %v", err)
	}
	var out []emitted
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e emitted
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("a logged line is not one JSON object: %v\n%s", err, line)
		}
		out = append(out, e)
	}
	return out
}

// ofKind is every line of one kind, in the order they were written.
func ofKind(lines []emitted, event string) []emitted {
	var out []emitted
	for _, l := range lines {
		if l.Event == event {
			out = append(out, l)
		}
	}
	return out
}

// onlyKind is the one line of a kind a verb writes exactly once.
func onlyKind(t *testing.T, lines []emitted, event string) emitted {
	t.Helper()
	got := ofKind(lines, event)
	if len(got) != 1 {
		t.Fatalf("want exactly one %s line, got %d of them in %d lines", event, len(got), len(lines))
	}
	return got[0]
}

// wantLabels is the four labels Alloy promotes out of the JSON plus the bench, which is
// what every one of the dashboard's queries selects on.
func wantLabels(t *testing.T, l emitted, verb string) {
	t.Helper()
	if l.Source != "nova-merge" {
		t.Errorf("source = %q, want nova-merge", l.Source)
	}
	if l.Verb != verb {
		t.Errorf("verb = %q, want %q", l.Verb, verb)
	}
	if l.Bench != "hulk" {
		t.Errorf("bench = %q, want the --bench given", l.Bench)
	}
	if l.TS == "" || l.GUID == "" {
		t.Errorf("ts and guid are never absent: %+v", l)
	}
}

// ---- batch ----

// THE RED BATCH, AS EVENTS: one batch-start, one batch-member per pull request offered
// (merged or dropped by name with the reason), one batch-verdict naming the failing step
// and its packages and tests, and NO batch-enqueued -- a red batch enqueues nothing, and a
// dashboard that showed one would be showing a landing that never happened.
func TestBatchEmitsStartMembersAndARedVerdictAndNothingEnqueued(t *testing.T) {
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	logPath := filepath.Join(l.dir, "nova-events-batch.log")

	exit, _, stderr := l.run("batch", "--name", "integration-1", "--pr", "1,2,3",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--log", logPath, "--bench", "hulk")
	if exit != 1 {
		t.Fatalf("a red batch is exit 1, got %d\n%s", exit, stderr)
	}

	lines := readEvents(t, logPath)
	start := onlyKind(t, lines, "batch-start")
	wantLabels(t, start, "batch")
	if !strings.Contains(start.Msg, "name=integration-1") || !strings.Contains(start.Msg, "prs=3") {
		t.Errorf("batch-start does not name the batch and its members: %q", start.Msg)
	}

	members := ofKind(lines, "batch-member")
	if len(members) != 3 {
		t.Fatalf("want one batch-member per pull request offered, got %d", len(members))
	}
	byPR := map[int]emitted{}
	for _, m := range members {
		byPR[m.PR] = m
	}
	for _, n := range []int{1, 3} {
		if !strings.Contains(byPR[n].Msg, "state=merged") {
			t.Errorf("#%d merged, and its line says %q", n, byPR[n].Msg)
		}
	}
	if !strings.Contains(byPR[2].Msg, "state=dropped") {
		t.Errorf("#2 conflicts and is dropped by name, and its line says %q", byPR[2].Msg)
	}
	if !strings.Contains(byPR[2].Msg, "reason=") {
		t.Errorf("a drop with no reason is a silent skip: %q", byPR[2].Msg)
	}

	verdict := onlyKind(t, lines, "batch-verdict")
	wantLabels(t, verdict, "batch")
	if verdict.Level != "ERROR" {
		t.Errorf("a red verdict is ERROR so a panel selects it by label, got %q", verdict.Level)
	}
	for _, want := range []string{"verdict=FAIL", "step=test", "packages=", "tests="} {
		if !strings.Contains(verdict.Msg, want) {
			t.Errorf("batch-verdict does not carry %q: %q", want, verdict.Msg)
		}
	}
	if got := ofKind(lines, "batch-enqueued"); len(got) != 0 {
		t.Errorf("a red batch enqueues nothing, got %d batch-enqueued lines", len(got))
	}
}

// THE GREEN BATCH: the verdict is OK and the batch is enqueued -- one line naming the
// branch it built and the head a caller pushes.
func TestBatchGreenEmitsAnOKVerdictAndTheEnqueuedBranch(t *testing.T) {
	l := batchRepo(t)
	root := filepath.Join(l.dir, "batch")
	logPath := filepath.Join(l.dir, "nova-events-batch.log")

	exit, stdout, stderr := l.run("batch", "--name", "integration-2", "--pr", "1",
		"--repo", "o/n", "--root", root, "--base", "dev", "--timeout", "5m",
		"--log", logPath, "--bench", "hulk")
	if exit != 0 {
		t.Fatalf("a green batch is exit 0, got %d\n%s\n%s", exit, stdout, stderr)
	}

	lines := readEvents(t, logPath)
	verdict := onlyKind(t, lines, "batch-verdict")
	if !strings.Contains(verdict.Msg, "verdict=OK") {
		t.Errorf("batch-verdict does not carry the green verdict: %q", verdict.Msg)
	}
	if verdict.Level != "INFO" {
		t.Errorf("a green verdict is INFO, got %q", verdict.Level)
	}
	enq := onlyKind(t, lines, "batch-enqueued")
	wantLabels(t, enq, "batch")
	if !strings.Contains(enq.Msg, "branch=rowan/integration-2") {
		t.Errorf("batch-enqueued does not name the branch it built: %q", enq.Msg)
	}
	if !strings.Contains(enq.Msg, "head=") || !strings.Contains(enq.Msg, "members=1") {
		t.Errorf("batch-enqueued does not name the head and the members: %q", enq.Msg)
	}
}

// A batch that could not run at all says so as an event too: a refusal is the verb's spine
// and never a silent exit.
func TestBatchRefusalIsOneRefuseEvent(t *testing.T) {
	l := newLab(t)
	logPath := filepath.Join(l.dir, "nova-events-batch.log")
	exit, _, _ := l.run("batch", "--name", "integration-3", "--pr", "1",
		"--repo", "o/n", "--root", filepath.Join(l.dir, "batch"), "--base", "no-such-base",
		"--timeout", "5m", "--log", logPath, "--bench", "hulk")
	if exit != 2 {
		t.Fatalf("a batch that could not run is exit 2, got %d", exit)
	}
	refuse := onlyKind(t, readEvents(t, logPath), "refuse")
	wantLabels(t, refuse, "batch")
	if refuse.Level != "ERROR" || refuse.Err == "" {
		t.Errorf("a refusal carries the error at ERROR: %+v", refuse)
	}
}

// ---- queue ----

// EVERY QUEUE READ EMITS THE DEPTH. The panel that replaces the metrics.tsv one reads
// running, waiting and unmergeable out of this message, so every subverb that opens the
// queue writes one.
func TestQueueEmitsTheDepthOnEveryRead(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	logPath := filepath.Join(l.dir, "nova-events-queue.log")

	// Two reads: a skip and an unskip. Each writes its own depth line.
	for _, args := range [][]string{
		{"queue", "--lane", l.lane, "skip", "11", "--log", logPath, "--bench", "hulk"},
		{"queue", "--lane", l.lane, "unskip", "11", "--log", logPath, "--bench", "hulk"},
	} {
		if exit, out, errb := l.run(args...); exit != 0 {
			t.Fatalf("%v: exit %d\n%s\n%s", args, exit, out, errb)
		}
	}

	lines := readEvents(t, logPath)
	depths := ofKind(lines, "queue-depth")
	if len(depths) != 2 {
		t.Fatalf("one queue-depth per read, got %d in %d lines", len(depths), len(lines))
	}
	wantLabels(t, depths[0], "queue")
	for _, want := range []string{"running=", "waiting=", "unmergeable="} {
		if !strings.Contains(depths[0].Msg, want) {
			t.Fatalf("queue-depth does not carry %q: %q", want, depths[0].Msg)
		}
	}
	// The skip put #11 in the unmergeable set; the unskip took it out and left it waiting.
	if !strings.Contains(depths[0].Msg, "unmergeable=1") {
		t.Errorf("after a skip the entry is unmergeable: %q", depths[0].Msg)
	}
	if !strings.Contains(depths[1].Msg, "unmergeable=0") || !strings.Contains(depths[1].Msg, "waiting=") {
		t.Errorf("after an unskip the entry is back in the queue: %q", depths[1].Msg)
	}
}

// ONE QUEUE-AUDIT PER ENTRY WHOSE AUTOMATIC MERGE THE QUEUE TURNED OFF. A skip is the
// hand-written one; the reason is on the line, because an entry that stopped merging for a
// reason nobody wrote down is the thing this event exists to prevent.
func TestQueueEmitsAnAuditPerEntryItDisables(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	logPath := filepath.Join(l.dir, "nova-events-queue.log")

	if exit, out, errb := l.run("queue", "--lane", l.lane, "skip", "11", "12",
		"--log", logPath, "--bench", "hulk"); exit != 0 {
		t.Fatalf("skip: exit %d\n%s\n%s", exit, out, errb)
	}
	audits := ofKind(readEvents(t, logPath), "queue-audit")
	if len(audits) != 2 {
		t.Fatalf("one queue-audit per entry disabled, got %d", len(audits))
	}
	wantLabels(t, audits[0], "queue")
	seen := map[int]bool{}
	for _, a := range audits {
		seen[a.PR] = true
		if !strings.Contains(a.Msg, "action=skip") || !strings.Contains(a.Msg, "reason=") {
			t.Errorf("a queue-audit names the action and its reason: %q", a.Msg)
		}
	}
	if !seen[11] || !seen[12] {
		t.Errorf("both entries are audited, got %v", seen)
	}
}

// A HOLD IS THE LANE-WIDE ONE: every entry stops merging at once, so the audit carries no
// pull request number and the reason is the hold's own.
func TestQueueHoldEmitsALaneWideAudit(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	logPath := filepath.Join(l.dir, "nova-events-queue.log")

	if exit, out, errb := l.run("queue", "--lane", l.lane, "hold", "the base is frozen",
		"--who", "rowan", "--log", logPath, "--bench", "hulk"); exit != 0 {
		t.Fatalf("hold: exit %d\n%s\n%s", exit, out, errb)
	}
	audit := onlyKind(t, readEvents(t, logPath), "queue-audit")
	wantLabels(t, audit, "queue")
	if audit.PR != 0 {
		t.Errorf("a hold is the whole lane and names no entry, got pr=%d", audit.PR)
	}
	if !strings.Contains(audit.Msg, "action=hold") || !strings.Contains(audit.Msg, "the base is frozen") {
		t.Errorf("the hold's audit does not carry the hold's reason: %q", audit.Msg)
	}
}

// A queue refusal is one refuse event, the same spine every other verb writes.
func TestQueueRefusalIsOneRefuseEvent(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	logPath := filepath.Join(l.dir, "nova-events-queue.log")
	exit, _, _ := l.run("queue", "--lane", l.lane, "front", "not-a-number",
		"--log", logPath, "--bench", "hulk")
	if exit != 2 {
		t.Fatalf("a queue refusal is exit 2, got %d", exit)
	}
	refuse := onlyKind(t, readEvents(t, logPath), "refuse")
	if refuse.Level != "ERROR" || refuse.Err == "" {
		t.Errorf("a refusal carries the error at ERROR: %+v", refuse)
	}
}

// Without --log the lines go to stderr, which under systemd is the unit's journal: a bench
// needs no flag to be observable. The human QUEUE line is untouched beside it.
func TestQueueWithoutALogWritesTheEventsToStderr(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.init("main")
	exit, stdout, stderr := l.run("queue", "--lane", l.lane, "skip", "11", "--bench", "hulk")
	if exit != 0 {
		t.Fatalf("skip: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stdout, "QUEUE SKIP entry=11") {
		t.Errorf("the human line on stdout is untouched: %q", stdout)
	}
	if !strings.Contains(stderr, `"event":"queue-depth"`) {
		t.Errorf("the structured line does not reach stderr: %q", stderr)
	}
}

// ---- react ----

// THE REACTOR EMITS WHAT IT REACTS TO: the kind is the channel name the message arrived
// on, so the vocabulary a query selects on is the vocabulary the bus carries, and the
// message says what the reactor did about it.
func TestReactEmitsTheMessageItReactedTo(t *testing.T) {
	mr := miniredis.RunT(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "nova-events-react.log")
	deps := Deps{
		Now:   func() time.Time { return time.Now().UTC() },
		Dial:  func(addr string) *redis.Client { return redis.NewClient(&redis.Options{Addr: addr}) },
		Forge: func(_, _ string, _ time.Duration) ci.Forge { return &reactFakeForge{} },
	}
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"react", "--redis", mr.Addr(), "--once", "--deadline", "60",
			"--log", logPath, "--bench", "hulk"}, &out, &errb, deps)
	}()

	payload := `{"number":42,"head":"a1b2","conclusion":"SUCCESS"}`
	deadline := time.After(reactWait())
	for {
		if rdb.SIsMember(ctx, "merge:queue", "42").Val() {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("react never enqueued PR 42")
		default:
		}
		if err := rdb.Publish(ctx, ci.ChannelPRChecksDone, payload).Err(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code := <-done; code != 0 {
		t.Fatalf("react --once exit = %d, stderr=%s", code, errb.String())
	}

	lines := readEvents(t, logPath)
	reacted := ofKind(lines, ci.ChannelPRChecksDone)
	if len(reacted) == 0 {
		t.Fatalf("the reactor emitted nothing for the message it reacted to: %d lines", len(lines))
	}
	wantLabels(t, reacted[0], "react")
	if reacted[0].PR != 42 {
		t.Errorf("the reaction does not name the pull request: %+v", reacted[0])
	}
	if !strings.Contains(reacted[0].Msg, "action=enqueue") {
		t.Errorf("the reaction does not say what it did: %q", reacted[0].Msg)
	}
	if len(ofKind(lines, "start")) != 1 || len(ofKind(lines, "done")) != 1 {
		t.Errorf("the verb's own spine is one start and one done: %d lines", len(lines))
	}
}

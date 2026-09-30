package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// where --watch and the frame it draws: docs/SPEC-SPRINT.md section 1 (the
// view) and section 11 (the machine's state line). No test opens a terminal
// or a socket: the store is the in-memory one, the clock and the sleep are the
// test app's, and the screen is a recording writer.

var updateWhereGolden = flag.Bool("update-golden-where", false, "write the where frame and its watch bytes from the fixture to their golden files")

// golden compares got with testdata/<name>; with -update-golden-where it
// writes the file from got instead.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateWhereGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s differs from the golden:\ngot  %q\nwant %q", name, got, string(want))
	}
}

// writeLog is a screen that keeps each write whole. after, when set, is
// called with the number of writes so far and the write just kept: a test
// ends its watch from it.
type writeLog struct {
	mu     sync.Mutex
	writes []string
	after  func(n int, w string)
}

func (l *writeLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	l.writes = append(l.writes, string(p))
	n := len(l.writes)
	l.mu.Unlock()
	if l.after != nil {
		l.after(n, string(p))
	}
	return len(p), nil
}

// whereFixture is a sprint with every table showing: three primaries in one
// stream, one of them read and accepted into merging, and a second stream
// whose only primary was dropped, so it has no cards in any column.
func whereFixture(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1,m2")
	ta.ok("add --stream s1 --count 3")
	ta.ok("add --stream s2 --count 1")
	ta.ok("drop s2-1 --reason obsolete")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 1")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	taken := ""
	for _, c := range q.Cards {
		if c.Col == "working" {
			taken = c.ID
		}
	}
	if taken == "" {
		t.Fatalf("m1 took nothing: %+v", q.Cards)
	}
	ta.ok("finish --as m1 " + taken + "@1")
	ta.ok("ask")
	ta.ok("read --as reader-a --ok --limit 10")
	ta.ok("read --as reader-b --ok --limit 10")
	ta.ok("accept --read-ok")
	return ta
}

// whereRun is the run of a where on the test app's store.
func (ta *testApp) whereRun(watch bool) whereRun {
	return whereRun{c: common{verb: "where", redis: "mem:0", prefix: "t-", epoch: -1}, watch: watch, every: time.Second, stale: defaultStale, atEpoch: -1}
}

// tableOf is the table of the frame titled name: its lines up to the next
// blank line, or "" when the frame shows none.
func tableOf(frame, name string) string {
	for _, block := range strings.Split(frame, "\n\n") {
		if strings.HasPrefix(block, name+" ") {
			return block
		}
	}
	return ""
}

// rowsOf is the first cell of each row of a table's block: its identity.
func rowsOf(block string) []string {
	var rows []string
	for _, l := range strings.Split(block, "\n")[2:] {
		if strings.HasPrefix(l, "-") {
			break // the rule above the footer
		}
		first, _, _ := strings.Cut(l, " | ")
		rows = append(rows, strings.TrimSpace(first))
	}
	return rows
}

// The frame is the golden, byte for byte: the plain text where prints, and
// the bytes the watch writes for the same frame.
func TestWhereFrameIsTheGolden(t *testing.T) {
	t.Parallel()
	ta := whereFixture(t)
	golden(t, "where_frame.golden", ta.ok("where"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	screen := &writeLog{after: func(n int, _ string) {
		if n == 2 { // the cursor hidden, then the frame
			cancel()
		}
	}}
	var errb bytes.Buffer
	if code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb); code != 0 || errb.Len() > 0 {
		t.Fatalf("watch: exit %d: %s", code, errb.String())
	}
	if len(screen.writes) != 3 {
		t.Fatalf("writes: %q", screen.writes)
	}
	golden(t, "where_watch_frame.golden", screen.writes[1])
}

// A frame holds the time, the words SPRINT TABLE, the machine's state line
// and the tables, and nothing else: no pending line, no stalled line, no line
// about the people, no coordinator; the merge table has no since column; and a
// row's first cell is its identity. What the frame leaves out is in --json.
func TestWhereFrameHoldsOnlyTheHeaderAndTheTables(t *testing.T) {
	t.Parallel()
	ta := whereFixture(t)
	dir := t.TempDir()
	text := filepath.Join(dir, "goal.txt")
	if err := os.WriteFile(text, []byte("keep going\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ta.ok("goal set friend-a --file " + text + " --to file:" + filepath.Join(dir, "reminder.txt"))
	// a pending operation in the fence, and a stream with no progress for hours
	ctx := context.Background()
	f, err := ta.m.ReadFence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := ta.m.Acquire(ctx, f.Gen, store.OpRecord{ID: "op-left", Verb: "add", At: t0}); err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	ta.mu.Lock()
	ta.now = ta.now.Add(3 * time.Hour)
	ta.mu.Unlock()

	frame := ta.ok("where")
	lines := strings.Split(frame, "\n")
	if got, want := lines[:6], []string{"2030-01-02 06:04:05 UTC", "", "SPRINT TABLE", "", "STOPPED (no tick for 10800s)", ""}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the head of the frame:\n%q\nwant\n%q", got, want)
	}
	for _, l := range lines[6:] {
		if l != "" && !strings.Contains(l, " | ") && !strings.Contains(l, "-+-") {
			t.Errorf("a line that is not a table's: %q\n%s", l, frame)
		}
	}
	for _, gone := range []string{"pending", "stalled", "REMINDERS", "friend-a", "coordinator", "since", "op-left"} {
		if strings.Contains(frame, gone) {
			t.Errorf("the frame shows %q:\n%s", gone, frame)
		}
	}
	// the identity of a row is its first cell
	for name, want := range map[string]string{"work": "s1", "readers": "reader-a,reader-b", "merge": "s1", "fleet": "m1,m2"} {
		if got := strings.Join(rowsOf(tableOf(frame, name)), ","); got != want {
			t.Errorf("table %s: rows %q, want %q:\n%s", name, got, want, frame)
		}
	}
	// the view for a program keeps all of it
	var w whereView
	ta.json("where", &w)
	if w.Pending != "op-left" || len(w.Stalled) == 0 || len(w.Goals) != 1 || w.Coordinator == "" {
		t.Errorf("where --json: pending=%q stalled=%v goals=%v coordinator=%q", w.Pending, w.Stalled, w.Goals, w.Coordinator)
	}
}

// A table with no rows is not in the frame, and a stream with no cards in any
// column is not in its tables; each is there again when it has a row.
func TestWhereShowsAnEmptyTableAndAnEmptyStreamOnlyWithRows(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s2 --count 1")
	ta.ok("drop s2-1 --reason obsolete")
	frame := ta.ok("where")
	for _, name := range []string{"readers", "merge"} {
		if tableOf(frame, name) != "" {
			t.Errorf("table %s has no rows and shows:\n%s", name, frame)
		}
	}
	if got := rowsOf(tableOf(frame, "work")); strings.Join(got, ",") != "s1" {
		t.Errorf("work rows %q: s2 has no cards in any column:\n%s", got, frame)
	}

	// the readers come, and a card comes to s2
	ta.ok("reader add reader-a reader-b")
	ta.ok("add --stream s2 s2-2")
	frame = ta.ok("where")
	if got := rowsOf(tableOf(frame, "readers")); strings.Join(got, ",") != "reader-a,reader-b" {
		t.Errorf("readers rows %q:\n%s", got, frame)
	}
	if got := rowsOf(tableOf(frame, "work")); strings.Join(got, ",") != "s1,s2" {
		t.Errorf("work rows %q:\n%s", got, frame)
	}
	if tableOf(frame, "merge") != "" {
		t.Errorf("merge has no cards yet and shows:\n%s", frame)
	}

	// a card comes to merge
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 1")
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	for _, c := range q.Cards {
		if c.Col == "working" {
			ta.ok("finish --as m1 " + c.ID + "@1")
		}
	}
	ta.ok("ask")
	ta.ok("read --as reader-a --ok --limit 10")
	ta.ok("read --as reader-b --ok --limit 10")
	ta.ok("accept --read-ok")
	frame = ta.ok("where")
	if got := rowsOf(tableOf(frame, "merge")); len(got) != 1 {
		t.Errorf("merge rows %q:\n%s", got, frame)
	}
}

// Each frame of a watch is one write, drawn from the top of the screen, every
// line cleared to its end, everything below it cleared, and no newline after
// the last line, so nothing scrolls and a shorter frame leaves nothing behind.
func TestWatchDrawsEachFrameInPlaceWithOneWrite(t *testing.T) {
	t.Parallel()
	ta := whereFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	screen := &writeLog{after: func(n int, _ string) {
		if n == 3 { // the cursor hidden, then two frames
			cancel()
		}
	}}
	var errb bytes.Buffer
	if code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb); code != 0 {
		t.Fatalf("watch: exit %d: %s", code, errb.String())
	}
	if len(screen.writes) != 4 {
		t.Fatalf("two frames, the cursor hidden and restored: %d writes: %q", len(screen.writes), screen.writes)
	}
	if screen.writes[0] != "\x1b[?25l" || screen.writes[3] != "\x1b[?25h" {
		t.Fatalf("the cursor: %q ... %q", screen.writes[0], screen.writes[3])
	}
	for i, w := range screen.writes[1:3] {
		if !strings.HasPrefix(w, "\x1b[H") || !strings.HasSuffix(w, "\x1b[K\x1b[J") {
			t.Errorf("frame %d does not draw from home and clear below: %q", i+1, w)
		}
		if strings.Contains(w, "\x1b[2J") {
			t.Errorf("frame %d clears the whole screen: %q", i+1, w)
		}
		// every line is cleared to its end; the last has no newline after it
		lines := strings.Count(w, "\n") + 1
		if got := strings.Count(w, "\x1b[K"); got != lines {
			t.Errorf("frame %d: %d lines, %d cleared to their end", i+1, lines, got)
		}
		if got := strings.Count(w, "\x1b[K\n"); got != lines-1 {
			t.Errorf("frame %d: %d newlines, %d after a cleared line", i+1, lines-1, got)
		}
		if strings.Contains(w[strings.LastIndex(w, "\x1b[K"):], "\n") {
			t.Errorf("frame %d has a newline after its last line: %q", i+1, w)
		}
	}
	// the second frame is a new read: the clock ran one second between them
	if !strings.Contains(screen.writes[1], "2030-01-02 03:04:05 UTC") || !strings.Contains(screen.writes[2], "2030-01-02 03:04:06 UTC") {
		t.Errorf("the clock of the frames:\n%q\n%q", screen.writes[1], screen.writes[2])
	}
}

// The writer's sequences, and its one write to a frame: two frames are two
// writes, and the cursor is a write of its own each way.
func TestWatchWriterWritesOncePerFrame(t *testing.T) {
	t.Parallel()
	screen := &writeLog{}
	w := newWatchWriter(screen)
	if err := w.frame("one\ntwo\nthree\n"); err != nil {
		t.Fatal(err)
	}
	if err := w.frame("one\n"); err != nil { // a shorter frame
		t.Fatal(err)
	}
	if len(screen.writes) != 2 {
		t.Fatalf("two frames are %d writes: %q", len(screen.writes), screen.writes)
	}
	if want := "\x1b[Hone\x1b[K\ntwo\x1b[K\nthree\x1b[K\x1b[J"; screen.writes[0] != want {
		t.Errorf("the first frame:\n%q\nwant\n%q", screen.writes[0], want)
	}
	if want := "\x1b[Hone\x1b[K\x1b[J"; screen.writes[1] != want {
		t.Errorf("the shorter frame clears what is below it:\n%q\nwant\n%q", screen.writes[1], want)
	}
	w.hideCursor()
	w.showCursor()
	if len(screen.writes) != 4 || screen.writes[2] != "\x1b[?25l" || screen.writes[3] != "\x1b[?25h" {
		t.Errorf("the cursor: %q", screen.writes[2:])
	}
}

// The cursor comes back however the watch ends: its context cancelled while
// it sleeps, cancelled before it starts, or a read the store refuses.
func TestWatchRestoresTheCursor(t *testing.T) {
	t.Parallel()
	hidden, shown := "\x1b[?25l", "\x1b[?25h"
	t.Run("cancelled while it sleeps", func(t *testing.T) {
		t.Parallel()
		ta := whereFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ta.a.sleep = func(time.Duration) { cancel() }
		screen := &writeLog{}
		var errb bytes.Buffer
		if code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb); code != 0 || errb.Len() > 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		w := screen.writes
		if len(w) != 3 || w[0] != hidden || w[2] != shown || !strings.HasPrefix(w[1], "\x1b[H") {
			t.Fatalf("writes: %q", w)
		}
	})
	t.Run("cancelled before it starts", func(t *testing.T) {
		t.Parallel()
		ta := whereFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		screen := &writeLog{}
		var errb bytes.Buffer
		if code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb); code != 0 || errb.Len() > 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		w := screen.writes
		if len(w) < 2 || w[0] != hidden || w[len(w)-1] != shown || strings.Count(strings.Join(w, ""), hidden) != 1 {
			t.Fatalf("writes: %q", w)
		}
	})
	t.Run("a read the store refuses", func(t *testing.T) {
		t.Parallel()
		ta := whereFixture(t)
		r := ta.whereRun(true)
		r.atEpoch = 99
		screen := &writeLog{}
		var errb bytes.Buffer
		if code := ta.a.whereLoop(context.Background(), r, screen, &errb); code != 1 || !strings.Contains(errb.String(), "EPOCHAHEAD") {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		if got := strings.Join(screen.writes, "|"); got != hidden+"|"+shown {
			t.Fatalf("writes: %q", screen.writes)
		}
	})
	t.Run("no store to read", func(t *testing.T) {
		t.Parallel()
		ta := whereFixture(t)
		r := ta.whereRun(true)
		r.c.redis = ""
		screen := &writeLog{}
		var errb bytes.Buffer
		if code := ta.a.whereLoop(context.Background(), r, screen, &errb); code != 2 || !strings.Contains(errb.String(), "--redis") {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		if got := strings.Join(screen.writes, "|"); got != hidden+"|"+shown {
			t.Fatalf("writes: %q", screen.writes)
		}
	})
}

// --json --watch is a line for a program each time, with nothing drawn: no
// cursor and no escape.
func TestWatchJSONIsOneObjectAFrameAndNoEscape(t *testing.T) {
	t.Parallel()
	ta := whereFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	screen := &writeLog{after: func(n int, _ string) {
		if n == 2 {
			cancel()
		}
	}}
	r := ta.whereRun(true)
	r.c.json = true
	var errb bytes.Buffer
	if code := ta.a.whereLoop(ctx, r, screen, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if len(screen.writes) != 2 {
		t.Fatalf("writes: %q", screen.writes)
	}
	for _, w := range screen.writes {
		var v whereView
		if err := json.Unmarshal([]byte(w), &v); err != nil || strings.Contains(w, "\x1b") || !strings.HasSuffix(w, "\n") {
			t.Errorf("not one object for a program: %q (%v)", w, err)
		}
	}
}

// --every is bounded with --watch: a redraw with no wait is a loop on the store.
func TestWatchRefusesAnEveryOutOfRange(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	for _, every := range []string{"0s", "-1s", "2h"} {
		code, out, errs := ta.do("where --watch --every " + every)
		if code != 2 || out != "" || !strings.Contains(errs, "--every wants a duration between 1ms and 1h") {
			t.Errorf("--every %s: exit %d %q %q", every, code, out, errs)
		}
	}
}

// pause sleeps the interval in steps and stops as soon as its context is done.
func TestPauseSleepsInStepsAndStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	a := &app{}
	var slept []time.Duration
	a.sleep = func(d time.Duration) { slept = append(slept, d) }
	if !a.pause(context.Background(), 250*time.Millisecond) {
		t.Fatal("a pause nothing interrupted says the watch is over")
	}
	if want := []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 50 * time.Millisecond}; len(slept) != len(want) || slept[0] != want[0] || slept[1] != want[1] || slept[2] != want[2] {
		t.Fatalf("slept %v, want %v", slept, want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.sleep = func(time.Duration) { cancel() }
	slept = nil
	if a.pause(ctx, time.Hour) {
		t.Fatal("a cancelled pause says the watch goes on")
	}
	cancelled := 0
	a.sleep = func(time.Duration) { cancelled++ }
	if a.pause(ctx, time.Hour) || cancelled != 0 {
		t.Fatalf("a pause that begins cancelled slept %d times", cancelled)
	}
}

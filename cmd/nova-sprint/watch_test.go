package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
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
// whose only primary was dropped, so it has no cards in any column and its row
// shows, at zero.
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
	ta.ok("tick") // the pump drains the accept the machine queued
	return ta
}

// whereRun is the run of a where on the test app's store.
func (ta *testApp) whereRun(watch bool) whereRun {
	return whereRun{c: common{verb: "where", redis: "mem:0", epoch: -1}, watch: watch, every: time.Second, stale: defaultStale, atEpoch: -1}
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
	if got, want := lines[:4], []string{"SPRINT TABLE", "", "STOPPED", ""}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the head of the frame:\n%q\nwant\n%q", got, want)
	}
	for _, l := range lines[4:] {
		if l != "" && !strings.Contains(l, " | ") && !strings.Contains(l, "-+-") {
			t.Errorf("a line that is not a table's: %q\n%s", l, frame)
		}
	}
	for _, gone := range []string{"pending", "stalled", "REMINDERS", "friend-a", "coordinator", "since", "op-left"} {
		if strings.Contains(frame, gone) {
			t.Errorf("the frame shows %q:\n%s", gone, frame)
		}
	}
	// the identity of a row is its first cell; the readers and merge tables are
	// one row, the sum of all (the owner, 2026-10-01)
	for name, want := range map[string]string{"work": "s1,s2", "readers": allRow, "merge": allRow, "fleet": "m1,m2"} {
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
	assert.ElementsMatch(t, []string{"reader-a", "reader-b"}, slices.Collect(maps.Keys(w.Tables[sprint.Readers])), "where --json keeps each reader's row")
	assert.ElementsMatch(t, []string{"s1", "s2"}, slices.Collect(maps.Keys(w.Tables[sprint.Merge])), "where --json keeps each stream's merge row")
}

// Every table is in the frame, with no rows when it has none, and a stream with
// no cards in any column is in the work and merge tables, at zero; the rows of
// a table come when it has them.
func TestWhereShowsEveryTableAndEveryStream(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s2 --count 1")
	ta.ok("drop s2-1 --reason obsolete")
	frame := ta.ok("where")
	for _, name := range []string{"work", "readers", "merge", "fleet"} {
		if !strings.Contains(frame, "\n"+name+" ") {
			t.Errorf("table %s is not in the frame:\n%s", name, frame)
		}
	}
	// the readers table is one row, the sum of all readers, at zero with none
	if got := rowsOf(tableOf(frame, "readers")); strings.Join(got, ",") != allRow {
		t.Errorf("readers rows %q, want %s alone:\n%s", got, allRow, frame)
	}
	assert.Equal(t, []string{"s1", "s2"}, rowsOf(tableOf(frame, "work")), "s2 has no cards in any column and shows:\n%s", frame)
	// the merge table is one row, the sum of all streams; --json keeps each
	assert.Equal(t, []string{allRow}, rowsOf(tableOf(frame, "merge")), frame)
	assert.ElementsMatch(t, []string{"s1", "s2"}, ta.mergeRows(), "where --json keeps each stream's merge row, s2 at zero")

	// the readers come, and a card comes to s2
	ta.ok("reader add reader-a reader-b")
	ta.ok("add --stream s2 s2-2")
	frame = ta.ok("where")
	if got := rowsOf(tableOf(frame, "readers")); strings.Join(got, ",") != allRow {
		t.Errorf("readers rows %q, want %s alone:\n%s", got, allRow, frame)
	}
	assert.ElementsMatch(t, []string{"reader-a", "reader-b"}, ta.readerRows(), "where --json keeps each reader's row")
	if got := rowsOf(tableOf(frame, "work")); strings.Join(got, ",") != "s1,s2" {
		t.Errorf("work rows %q:\n%s", got, frame)
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
	assert.Equal(t, []string{allRow}, rowsOf(tableOf(frame, "merge")), frame)
	assert.ElementsMatch(t, []string{"s1", "s2"}, ta.mergeRows(), "where --json keeps both streams' merge rows")
}

// mergeRows is the merge table's row keys, as where --json lists them.
func (ta *testApp) mergeRows() []string {
	ta.t.Helper()
	var w whereView
	ta.json("where", &w)
	return slices.Collect(maps.Keys(w.Tables[sprint.Merge]))
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
		// and the last line is not an empty one, which is a newline after
		// the line before it
		if last := strings.TrimSuffix(w[strings.LastIndex(w, "\n")+1:], "\x1b[K\x1b[J"); last == "" {
			t.Errorf("frame %d ends in an empty line: %q", i+1, w)
		}
	}
	// no frame shows a time: the view's first line is its title
	if strings.Contains(screen.writes[1], "2030-01-02") || !strings.Contains(screen.writes[1], "\x1b[HSPRINT TABLE") {
		t.Errorf("the head of a frame:\n%q", screen.writes[1])
	}
}

// The writer's sequences, and its one write to a frame: two frames are two
// writes, and the cursor is a write of its own each way.
func TestWatchWriterWritesOncePerFrame(t *testing.T) {
	t.Parallel()
	screen := &writeLog{}
	w := newWatchWriter(screen, screenOf(0, 0))
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

// --every wants a duration above 0 with --watch (a redraw with no wait is a
// loop on the store), and nothing else: 1ns, 1ms and 2h are all taken.
func TestWatchRefusesAnEveryOfZeroOrLess(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	for _, every := range []string{"0s", "-1s"} {
		code, out, errs := ta.do("where --watch --every " + every)
		if code != 2 || out != "" || !strings.Contains(errs, "--every wants a duration above 0") {
			t.Errorf("--every %s: exit %d %q %q", every, code, out, errs)
		}
	}
}

func TestWatchTakesAnyEveryAboveZero(t *testing.T) {
	t.Parallel()
	for _, every := range []string{"1ns", "1ms", "2h"} {
		t.Run(every, func(t *testing.T) {
			t.Parallel()
			ta := whereFixture(t)
			in := ta.interruptible()
			ta.a.sleep = func(time.Duration) { in.now(t) } // the first step of the wait
			screen := &writeLog{}
			var errb bytes.Buffer
			if code := ta.a.cmdWhere(split("--watch --every "+every), screen, &errb); code != 0 || errb.Len() > 0 {
				t.Fatalf("--every %s: exit %d: %s", every, code, errb.String())
			}
			if len(screen.writes) != 3 || !strings.HasPrefix(screen.writes[1], "\x1b[H") {
				t.Fatalf("--every %s: the cursor hidden, one frame, the cursor restored: %q", every, screen.writes)
			}
		})
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

// screenOf is the size of a screen, read by a watchWriter: rows by cols, 0 for
// what is not known.
func screenOf(rows, cols int) func() (int, int) { return func() (int, int) { return rows, cols } }

// drawn is the lines a frame draws, read back from its bytes: the cursor home
// and the clear below taken off, and the clear to the end of each line. A frame
// with any other sequence in it fails the test.
func drawn(t *testing.T, w string) []string {
	t.Helper()
	body, ok := strings.CutPrefix(w, "\x1b[H")
	body, ok2 := strings.CutSuffix(body, "\x1b[J")
	if !ok || !ok2 {
		t.Fatalf("not a frame drawn in place: %q", w)
	}
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		l, ok := strings.CutSuffix(l, "\x1b[K")
		if !ok || strings.Contains(l, "\x1b") {
			t.Fatalf("line %d is not cleared to its end, or holds a sequence: %q", i, lines[i])
		}
		lines[i] = l
	}
	return lines
}

// plainLines is the lines of the frame where prints.
func plainLines(text string) []string { return strings.Split(strings.TrimSuffix(text, "\n"), "\n") }

// Nothing scrolls at any size of the screen: a frame taller than the screen is
// cut at the bottom and nothing is added to say so; a frame that fits, or a
// screen whose height is not known, is written whole.
func TestWatchCutsAFrameTallerThanTheScreenAtTheBottom(t *testing.T) {
	t.Parallel()
	text := "one\ntwo\nthree\nfour\nfive\n"
	whole := "\x1b[Hone\x1b[K\ntwo\x1b[K\nthree\x1b[K\nfour\x1b[K\nfive\x1b[K\x1b[J"
	for _, tc := range []struct {
		name string
		rows int
		want string
	}{
		{"taller than the screen", 3, "\x1b[Hone\x1b[K\ntwo\x1b[K\nthree\x1b[K\x1b[J"},
		{"one line taller", 4, "\x1b[Hone\x1b[K\ntwo\x1b[K\nthree\x1b[K\nfour\x1b[K\x1b[J"},
		{"a screen of one line", 1, "\x1b[Hone\x1b[K\x1b[J"},
		{"as tall as the screen", 5, whole},
		{"shorter than the screen", 24, whole},
		{"height not known", 0, whole},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			screen := &writeLog{}
			if err := newWatchWriter(screen, screenOf(tc.rows, 0)).frame(text); err != nil {
				t.Fatal(err)
			}
			if len(screen.writes) != 1 || screen.writes[0] != tc.want {
				t.Fatalf("%d rows: %q\nwant one write of %q", tc.rows, screen.writes, tc.want)
			}
		})
	}
}

// Through where: a watch reads the height of the screen it draws on for every
// frame, and draws the first lines of the frame that fit it. Nothing is added
// to the frame to say lines were cut.
func TestWhereWatchDrawsOnlyTheLinesThatFitTheScreen(t *testing.T) {
	t.Parallel()
	ta := whereFixture(t)
	plain := plainLines(ta.ok("where"))
	if len(plain) < 20 {
		t.Fatalf("the fixture's frame is %d lines, too few to cut:\n%s", len(plain), strings.Join(plain, "\n"))
	}
	rows := 10
	var asked []io.Writer
	ta.a.screen = func(w io.Writer) (int, int) { asked = append(asked, w); return rows, 0 }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	screen := &writeLog{after: func(n int, _ string) {
		switch n {
		case 2: // the first frame is drawn: the terminal grows
			rows = len(plain) + 5
		case 3:
			cancel()
		}
	}}
	var errb bytes.Buffer
	if code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb); code != 0 || errb.Len() > 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if len(screen.writes) != 4 {
		t.Fatalf("the cursor hidden, two frames, the cursor restored: %q", screen.writes)
	}
	first := drawn(t, screen.writes[1])
	if strings.Join(first, "\n") != strings.Join(plain[:10], "\n") {
		t.Errorf("a screen of 10 rows draws the first 10 lines of the frame:\n%s\nwant\n%s", strings.Join(first, "\n"), strings.Join(plain[:10], "\n"))
	}
	second := drawn(t, screen.writes[2])
	if len(second) != len(plain) || strings.Join(second[1:], "\n") != strings.Join(plain[1:], "\n") { // the clock is the first line
		t.Errorf("a screen taller than the frame draws all of it:\n%s\nwant\n%s", strings.Join(second, "\n"), strings.Join(plain, "\n"))
	}
	if len(asked) != 2 {
		t.Errorf("the height is read for every frame: read %d times for 2 frames", len(asked))
	}
	for _, w := range asked {
		if w != io.Writer(screen) {
			t.Errorf("the size was read of %v, not of the screen it draws on", w)
		}
	}
}

// A line is cut to one column less than the screen is wide, so that the last
// character of a line as wide as the screen never waits at the edge; where the
// width is not known a line is whole.
func TestWatchCutsEachLineToOneLessThanTheWidth(t *testing.T) {
	t.Parallel()
	text := "abcd\nabcde\nabcdef\nééééé\n\nend\n"
	for _, tc := range []struct {
		name       string
		rows, cols int
		want       []string
	}{
		{"width 5", 0, 5, []string{"abcd", "abcd", "abcd", "éééé", "", "end"}},
		{"width 4", 0, 4, []string{"abc", "abc", "abc", "ééé", "", "end"}},
		{"a screen one column wide holds nothing", 0, 1, []string{"", "", "", "", "", ""}},
		{"width not known", 0, 0, []string{"abcd", "abcde", "abcdef", "ééééé", "", "end"}},
		{"cut both ways", 2, 4, []string{"abc", "abc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			screen := &writeLog{}
			if err := newWatchWriter(screen, screenOf(tc.rows, tc.cols)).frame(text); err != nil {
				t.Fatal(err)
			}
			if len(screen.writes) != 1 {
				t.Fatalf("writes: %q", screen.writes)
			}
			if got := drawn(t, screen.writes[0]); strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("%d columns: %q, want %q", tc.cols, got, tc.want)
			}
		})
	}
}

// Through where: every line of the frame fits one column short of the width.
func TestWhereWatchCutsItsLinesToTheWidthOfTheScreen(t *testing.T) {
	t.Parallel()
	ta := whereFixture(t)
	plain := plainLines(ta.ok("where"))
	const cols = 24
	ta.a.screen = func(io.Writer) (int, int) { return 0, cols }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	screen := &writeLog{after: func(n int, _ string) {
		if n == 2 {
			cancel()
		}
	}}
	var errb bytes.Buffer
	if code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb); code != 0 || errb.Len() > 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	got := drawn(t, screen.writes[1])
	if len(got) != len(plain) {
		t.Fatalf("a screen with no height known draws every line: %d of %d", len(got), len(plain))
	}
	cut := 0
	for i, l := range got {
		want := plain[i]
		if r := []rune(want); len(r) > cols-1 {
			want, cut = string(r[:cols-1]), cut+1
		}
		if l != want {
			t.Errorf("line %d is %q, want %q", i, l, want)
		}
		if utf8.RuneCountInString(l) > cols-1 {
			t.Errorf("line %d is %d columns on a screen %d wide: %q", i, utf8.RuneCountInString(l), cols, l)
		}
	}
	if cut == 0 {
		t.Fatalf("no line of the frame is wider than the screen, so nothing shows the cut:\n%s", strings.Join(plain, "\n"))
	}
}

// One write per frame at any size: a frame of a sprint with many streams, over
// 8 KiB, is still one write, not the several a buffered writer would make.
func TestWatchWritesAFrameOverFourKiBInOneWrite(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1")
	var streams []string
	for i := 0; i < 200; i++ {
		streams = append(streams, fmt.Sprintf("stream-%03d-of-the-sprint", i))
	}
	ta.ok("add --stream " + strings.Join(streams, ",") + " --count 2")
	plain := ta.ok("where")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	screen := &writeLog{after: func(n int, _ string) {
		if n == 2 {
			cancel()
		}
	}}
	var errb bytes.Buffer
	if code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb); code != 0 || errb.Len() > 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if len(screen.writes) != 3 {
		t.Fatalf("the cursor hidden, ONE frame, the cursor restored: %d writes of %v bytes", len(screen.writes), writeSizes(screen.writes))
	}
	frame := screen.writes[1]
	if len(frame) <= 8192 {
		t.Fatalf("the frame is %d bytes, not over 8 KiB", len(frame))
	}
	if got := drawn(t, frame); strings.Join(got, "\n") != strings.Join(plainLines(plain), "\n") {
		t.Errorf("the frame is not the whole of the view")
	}
}

func writeSizes(writes []string) []int {
	var sizes []int
	for _, w := range writes {
		sizes = append(sizes, len(w))
	}
	return sizes
}

// interrupt is the test's hand on the interrupt of a where --watch: its app's
// notify gives the command a context that now ends.
type interrupt struct {
	asked, released int
	fire            context.CancelFunc
}

// interruptible puts the interrupt of the test app in the test's hand.
func (ta *testApp) interruptible() *interrupt {
	in := &interrupt{}
	ta.a.notify = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		in.asked++
		in.fire = cancel
		return ctx, func() { in.released++; cancel() }
	}
	return in
}

// now is the interrupt arriving. The test fails if the command never asked for
// one to reach it: an interrupt would then kill it with the cursor hidden.
func (in *interrupt) now(t *testing.T) {
	t.Helper()
	if in.fire == nil {
		t.Fatalf("where --watch did not ask to be interrupted, so an interrupt would not reach it")
	}
	in.fire()
}

// An interrupt ends the watch through the command: cmdWhere hands the loop the
// context the interrupt ends, the watch stops, the cursor is restored, and the
// exit is 0.
func TestAnInterruptEndsTheWatchThroughTheCommandAndRestoresTheCursor(t *testing.T) {
	t.Parallel()
	ta := whereFixture(t)
	in := ta.interruptible()
	sleeps := 0
	ta.a.sleep = func(time.Duration) {
		if sleeps++; sleeps > 3 {
			t.Fatalf("the watch went on after the interrupt")
		}
		if sleeps == 3 { // the interrupt arrives while it waits, after three frames
			in.now(t)
		}
	}
	screen := &writeLog{}
	var errb bytes.Buffer
	if code := ta.a.cmdWhere(split("--watch --every 100ms"), screen, &errb); code != 0 || errb.Len() > 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	w := screen.writes
	if len(w) != 5 || w[0] != "\x1b[?25l" || w[4] != "\x1b[?25h" || !strings.HasPrefix(w[3], "\x1b[H") {
		t.Fatalf("the cursor hidden, three frames, the cursor restored: %q", w)
	}
	if in.asked != 1 || in.released != 1 {
		t.Errorf("the interrupt was asked for %d times and let go %d times", in.asked, in.released)
	}
}

// cutStore is the store, with an interrupt arriving inside one of its reads:
// the read the interrupt arrives in, and only once the watch has drawn a
// frame. A read made in a context that is done is refused, as a real store's
// is.
type cutStore struct {
	store.Backend
	at      string // "dial", "Epoch" (opening the store) or "Shapes" (reading the view)
	armed   bool
	fire    func()
	sawDone bool            // the read the interrupt arrived in was made in the command's context
	dialCtx context.Context // the context the store was last opened in
}

func (b *cutStore) cut(ctx context.Context, read string) error {
	if b.armed && read == b.at {
		b.armed = false
		b.fire()
		b.sawDone = ctx.Err() != nil
	}
	return ctx.Err()
}

func (b *cutStore) Epoch(ctx context.Context) (store.EpochState, error) {
	if err := b.cut(ctx, "Epoch"); err != nil {
		return store.EpochState{}, err
	}
	return b.Backend.Epoch(ctx)
}

func (b *cutStore) Shapes(ctx context.Context, tables []string) ([]ntable.Table, error) {
	if err := b.cut(ctx, "Shapes"); err != nil {
		return nil, err
	}
	return b.Backend.Shapes(ctx, tables)
}

// An interrupt that cuts a read short ends the watch as any interrupt does:
// exit 0, nothing said, the cursor restored, and no frame drawn after it. The
// store is opened and read in the command's context, so the interrupt reaches
// the read it arrives in.
func TestAnInterruptThatCutsAReadShortEndsTheWatchWithExitZero(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, at string }{
		{"while the store is dialed", "dial"},
		{"while the store is opened", "Epoch"},
		{"while the view is read", "Shapes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta := whereFixture(t)
			in := ta.interruptible()
			cut := &cutStore{Backend: ta.m, at: tc.at, fire: func() { in.now(t) }}
			ta.a.backend = func(ctx context.Context, _ string, _ sprint.Names) (store.Backend, error) {
				cut.dialCtx = ctx
				if err := cut.cut(ctx, "dial"); err != nil {
					return nil, err
				}
				return cut, nil
			}
			sleeps := 0
			ta.a.sleep = func(time.Duration) {
				cut.armed = true // the first frame is drawn: the interrupt comes in the next read
				if sleeps++; sleeps > 3 {
					t.Fatalf("the watch went on after the interrupt")
				}
			}
			screen := &writeLog{}
			var errb bytes.Buffer
			if code := ta.a.cmdWhere(split("--watch --every 100ms"), screen, &errb); code != 0 || errb.Len() > 0 {
				t.Fatalf("exit %d: %s", code, errb.String())
			}
			if w := screen.writes; len(w) != 3 || w[0] != "\x1b[?25l" || !strings.HasPrefix(w[1], "\x1b[H") || w[2] != "\x1b[?25h" {
				t.Fatalf("the cursor hidden, the one frame, the cursor restored: %q", w)
			}
			if !cut.sawDone {
				t.Errorf("the read the interrupt arrived in was not made in the command's context: the interrupt cannot cut it short")
			}
			if cut.dialCtx == nil || cut.dialCtx.Err() == nil {
				t.Errorf("the store was opened in a context the interrupt does not end")
			}
		})
	}
}

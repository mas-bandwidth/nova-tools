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
	"github.com/stretchr/testify/require"

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
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), got, "%s differs from the golden", name)
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
	require.NotEmpty(t, taken, "m1 took nothing: %+v", q.Cards)
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
	code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb)
	require.Equal(t, 0, code, "watch: exit %d: %s", code, errb.String())
	require.Zero(t, errb.Len(), "watch: exit %d: %s", code, errb.String())
	require.Len(t, screen.writes, 3, "writes: %q", screen.writes)
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
	require.NoError(t, os.WriteFile(text, []byte("keep going\n"), 0o644))
	ta.ok("goal set friend-a --file " + text + " --to file:" + filepath.Join(dir, "reminder.txt"))
	// a pending operation in the fence, and a stream with no progress for hours
	ctx := context.Background()
	f, err := ta.m.ReadFence(ctx)
	require.NoError(t, err)
	ok, err := ta.m.Acquire(ctx, f.Gen, store.OpRecord{ID: "op-left", Verb: "add", At: t0})
	require.NoError(t, err, "acquire: %v %v", ok, err)
	require.True(t, ok, "acquire: %v %v", ok, err)
	ta.mu.Lock()
	ta.now = ta.now.Add(3 * time.Hour)
	ta.mu.Unlock()

	frame := ta.ok("where")
	lines := strings.Split(frame, "\n")
	require.Equal(t, []string{"SPRINT TABLE", "", "STOPPED", ""}, lines[:4], "the head of the frame")
	for _, l := range lines[4:] {
		// a table's line holds a cell divider or a rule's joint; the friends table's
		// summary row is a blank label and its blank cell, "<label> |"
		assert.True(t, l == "" || strings.Contains(l, " | ") || strings.Contains(l, "-+-") || strings.HasSuffix(l, " |"), "a line that is not a table's: %q\n%s", l, frame)
	}
	for _, gone := range []string{"pending", "stalled", "REMINDERS", "friend-a", "coordinator", "since", "op-left"} {
		assert.NotContains(t, frame, gone, "the frame shows %q", gone)
	}
	// the identity of a row is its first cell; the readers and merge tables are
	// one row, the sum of all (the owner, 2026-10-01)
	for name, want := range map[string]string{"work": "s1,s2", "readers": allRow, "merge": allRow, "fleet": "m1,m2"} {
		got := strings.Join(rowsOf(tableOf(frame, name)), ",")
		assert.Equal(t, want, got, "table %s: rows, in the frame:\n%s", name, frame)
	}
	// the fixture has no friend: the friends table is its header, one rule and its summary row
	assert.Equal(t, "friends | status\n--------+-------\n        |", tableOf(frame, "friends"), "the frame:\n%s", frame)
	// the view for a program keeps all of it
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, "op-left", w.Pending, "where --json: pending=%q stalled=%v goals=%v coordinator=%q", w.Pending, w.Stalled, w.Goals, w.Coordinator)
	assert.NotEmpty(t, w.Stalled, "where --json: pending=%q stalled=%v goals=%v coordinator=%q", w.Pending, w.Stalled, w.Goals, w.Coordinator)
	assert.Len(t, w.Goals, 1, "where --json: pending=%q stalled=%v goals=%v coordinator=%q", w.Pending, w.Stalled, w.Goals, w.Coordinator)
	assert.NotEmpty(t, w.Coordinator, "where --json: pending=%q stalled=%v goals=%v coordinator=%q", w.Pending, w.Stalled, w.Goals, w.Coordinator)
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
	// a table with no row is its header, one rule and the summary row (the owner,
	// 2026-10-02: "when the work stream table is empty, please just show the summary row")
	for _, name := range []string{"work", "readers", "merge"} {
		lines := strings.Split(strings.TrimRight(tableOf(ta.ok("where"), name), "\n"), "\n")
		require.Len(t, lines, 3, "table %s, empty: header, rule, summary row", name)
		assert.Equal(t, "", strings.TrimSpace(strings.Split(lines[2], " | ")[0]), "table %s: the summary row is unlabelled: %q", name, lines[2])
	}
	ta.ok("add --stream s1 --count 2")
	ta.ok("add --stream s2 --count 1")
	ta.ok("drop s2-1 --reason obsolete")
	frame := ta.ok("where")
	for _, name := range []string{"work", "readers", "merge", "fleet"} {
		assert.Contains(t, frame, "\n"+name+" ", "table %s is not in the frame", name)
	}
	// the readers table is one row, the sum of all readers, at zero with none
	assert.Equal(t, allRow, strings.Join(rowsOf(tableOf(frame, "readers")), ","), "readers rows, %s alone:\n%s", allRow, frame)
	assert.Equal(t, []string{"s1", "s2"}, rowsOf(tableOf(frame, "work")), "s2 has no cards in any column and shows:\n%s", frame)
	// the merge table is one row, the sum of all streams; --json keeps each
	assert.Equal(t, []string{allRow}, rowsOf(tableOf(frame, "merge")), frame)
	assert.ElementsMatch(t, []string{"s1", "s2"}, ta.mergeRows(), "where --json keeps each stream's merge row, s2 at zero")

	// the readers come, and a card comes to s2
	ta.ok("reader add reader-a reader-b")
	ta.ok("add --stream s2 s2-2")
	frame = ta.ok("where")
	assert.Equal(t, allRow, strings.Join(rowsOf(tableOf(frame, "readers")), ","), "readers rows, %s alone:\n%s", allRow, frame)
	assert.ElementsMatch(t, []string{"reader-a", "reader-b"}, ta.readerRows(), "where --json keeps each reader's row")
	assert.Equal(t, "s1,s2", strings.Join(rowsOf(tableOf(frame, "work")), ","), "work rows:\n%s", frame)

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
	code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb)
	require.Equal(t, 0, code, "watch: exit %d: %s", code, errb.String())
	require.Len(t, screen.writes, 4, "two frames, the cursor hidden and restored: %d writes: %q", len(screen.writes), screen.writes)
	require.Equal(t, "\x1b[?25l", screen.writes[0], "the cursor: %q ... %q", screen.writes[0], screen.writes[3])
	require.Equal(t, "\x1b[?25h", screen.writes[3], "the cursor: %q ... %q", screen.writes[0], screen.writes[3])
	for i, w := range screen.writes[1:3] {
		assert.True(t, strings.HasPrefix(w, "\x1b[H"), "frame %d does not draw from home and clear below: %q", i+1, w)
		assert.True(t, strings.HasSuffix(w, "\x1b[K\x1b[J"), "frame %d does not draw from home and clear below: %q", i+1, w)
		assert.NotContains(t, w, "\x1b[2J", "frame %d clears the whole screen", i+1)
		// every line is cleared to its end; the last has no newline after it
		lines := strings.Count(w, "\n") + 1
		assert.Equal(t, lines, strings.Count(w, "\x1b[K"), "frame %d: %d lines cleared to their end", i+1, lines)
		assert.Equal(t, lines-1, strings.Count(w, "\x1b[K\n"), "frame %d: %d newlines after a cleared line", i+1, lines-1)
		assert.NotContains(t, w[strings.LastIndex(w, "\x1b[K"):], "\n", "frame %d has a newline after its last line: %q", i+1, w)
		// and the last line is not an empty one, which is a newline after
		// the line before it
		assert.NotEmpty(t, strings.TrimSuffix(w[strings.LastIndex(w, "\n")+1:], "\x1b[K\x1b[J"), "frame %d ends in an empty line: %q", i+1, w)
	}
	// no frame shows a time: the view's first line is its title
	assert.NotContains(t, screen.writes[1], "2030-01-02", "the head of a frame")
	assert.Contains(t, screen.writes[1], "\x1b[HSPRINT TABLE", "the head of a frame")
}

// The writer's sequences, and its one write to a frame: two frames are two
// writes, and the cursor is a write of its own each way.
func TestWatchWriterWritesOncePerFrame(t *testing.T) {
	t.Parallel()
	screen := &writeLog{}
	w := newWatchWriter(screen, screenOf(0, 0))
	require.NoError(t, w.frame("one\ntwo\nthree\n"))
	require.NoError(t, w.frame("one\n")) // a shorter frame
	require.Len(t, screen.writes, 2, "two frames are %d writes: %q", len(screen.writes), screen.writes)
	assert.Equal(t, "\x1b[Hone\x1b[K\ntwo\x1b[K\nthree\x1b[K\x1b[J", screen.writes[0], "the first frame")
	assert.Equal(t, "\x1b[Hone\x1b[K\x1b[J", screen.writes[1], "the shorter frame clears what is below it")
	w.hideCursor()
	w.showCursor()
	if assert.Len(t, screen.writes, 4, "the cursor: %q", screen.writes[2:]) {
		assert.Equal(t, "\x1b[?25l", screen.writes[2], "the cursor: %q", screen.writes[2:])
		assert.Equal(t, "\x1b[?25h", screen.writes[3], "the cursor: %q", screen.writes[2:])
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
		code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb)
		require.Equal(t, 0, code, "exit %d: %s", code, errb.String())
		require.Zero(t, errb.Len(), "exit %d: %s", code, errb.String())
		w := screen.writes
		require.Len(t, w, 3, "writes: %q", w)
		require.Equal(t, hidden, w[0], "writes: %q", w)
		require.Equal(t, shown, w[2], "writes: %q", w)
		require.True(t, strings.HasPrefix(w[1], "\x1b[H"), "writes: %q", w)
	})
	t.Run("cancelled before it starts", func(t *testing.T) {
		t.Parallel()
		ta := whereFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		screen := &writeLog{}
		var errb bytes.Buffer
		code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb)
		require.Equal(t, 0, code, "exit %d: %s", code, errb.String())
		require.Zero(t, errb.Len(), "exit %d: %s", code, errb.String())
		w := screen.writes
		require.GreaterOrEqual(t, len(w), 2, "writes: %q", w)
		require.Equal(t, hidden, w[0], "writes: %q", w)
		require.Equal(t, shown, w[len(w)-1], "writes: %q", w)
		require.Equal(t, 1, strings.Count(strings.Join(w, ""), hidden), "writes: %q", w)
	})
	t.Run("a read the store refuses", func(t *testing.T) {
		t.Parallel()
		ta := whereFixture(t)
		r := ta.whereRun(true)
		r.atEpoch = 99
		screen := &writeLog{}
		var errb bytes.Buffer
		code := ta.a.whereLoop(context.Background(), r, screen, &errb)
		require.Equal(t, 1, code, "exit %d: %s", code, errb.String())
		require.Contains(t, errb.String(), "EPOCHAHEAD", "exit %d: %s", code, errb.String())
		require.Equal(t, hidden+"|"+shown, strings.Join(screen.writes, "|"), "writes: %q", screen.writes)
	})
	t.Run("no store to read", func(t *testing.T) {
		t.Parallel()
		ta := whereFixture(t)
		r := ta.whereRun(true)
		r.c.redis = ""
		screen := &writeLog{}
		var errb bytes.Buffer
		code := ta.a.whereLoop(context.Background(), r, screen, &errb)
		require.Equal(t, 2, code, "exit %d: %s", code, errb.String())
		require.Contains(t, errb.String(), "--redis", "exit %d: %s", code, errb.String())
		require.Equal(t, hidden+"|"+shown, strings.Join(screen.writes, "|"), "writes: %q", screen.writes)
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
	code := ta.a.whereLoop(ctx, r, screen, &errb)
	require.Equal(t, 0, code, "exit %d: %s", code, errb.String())
	require.Len(t, screen.writes, 2, "writes: %q", screen.writes)
	for _, w := range screen.writes {
		var v whereView
		err := json.Unmarshal([]byte(w), &v)
		assert.NoError(t, err, "not one object for a program: %q (%v)", w, err)
		assert.NotContains(t, w, "\x1b", "not one object for a program: %q (%v)", w, err)
		assert.True(t, strings.HasSuffix(w, "\n"), "not one object for a program: %q (%v)", w, err)
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
		assert.Equal(t, 2, code, "--every %s: exit %d %q %q", every, code, out, errs)
		assert.Empty(t, out, "--every %s: exit %d %q %q", every, code, out, errs)
		assert.Contains(t, errs, "--every wants a duration above 0", "--every %s: exit %d %q %q", every, code, out, errs)
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
			code := ta.a.cmdWhere(split("--watch --every "+every), screen, &errb)
			require.Equal(t, 0, code, "--every %s: exit %d: %s", every, code, errb.String())
			require.Zero(t, errb.Len(), "--every %s: exit %d: %s", every, code, errb.String())
			require.Len(t, screen.writes, 3, "--every %s: the cursor hidden, one frame, the cursor restored: %q", every, screen.writes)
			require.True(t, strings.HasPrefix(screen.writes[1], "\x1b[H"), "--every %s: the cursor hidden, one frame, the cursor restored: %q", every, screen.writes)
		})
	}
}

// pause sleeps the interval in steps and stops as soon as its context is done.
func TestPauseSleepsInStepsAndStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	a := &app{}
	var slept []time.Duration
	a.sleep = func(d time.Duration) { slept = append(slept, d) }
	require.True(t, a.pause(context.Background(), 250*time.Millisecond), "a pause nothing interrupted says the watch is over")
	require.Equal(t, []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 50 * time.Millisecond}, slept, "slept")
	ctx, cancel := context.WithCancel(context.Background())
	a.sleep = func(time.Duration) { cancel() }
	slept = nil
	require.False(t, a.pause(ctx, time.Hour), "a cancelled pause says the watch goes on")
	cancelled := 0
	a.sleep = func(time.Duration) { cancelled++ }
	paused := a.pause(ctx, time.Hour)
	require.False(t, paused, "a pause that begins cancelled slept %d times", cancelled)
	require.Zero(t, cancelled, "a pause that begins cancelled slept %d times", cancelled)
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
	require.True(t, ok, "not a frame drawn in place: %q", w)
	require.True(t, ok2, "not a frame drawn in place: %q", w)
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		l, ok := strings.CutSuffix(l, "\x1b[K")
		require.True(t, ok, "line %d is not cleared to its end, or holds a sequence: %q", i, lines[i])
		require.NotContains(t, l, "\x1b", "line %d is not cleared to its end, or holds a sequence: %q", i, lines[i])
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
			require.NoError(t, newWatchWriter(screen, screenOf(tc.rows, 0)).frame(text))
			require.Len(t, screen.writes, 1, "%d rows: %q\nwant one write of %q", tc.rows, screen.writes, tc.want)
			require.Equal(t, tc.want, screen.writes[0], "%d rows: %q\nwant one write of %q", tc.rows, screen.writes, tc.want)
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
	require.GreaterOrEqual(t, len(plain), 20, "the fixture's frame is %d lines, too few to cut:\n%s", len(plain), strings.Join(plain, "\n"))
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
	code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb)
	require.Equal(t, 0, code, "exit %d: %s", code, errb.String())
	require.Zero(t, errb.Len(), "exit %d: %s", code, errb.String())
	require.Len(t, screen.writes, 4, "the cursor hidden, two frames, the cursor restored: %q", screen.writes)
	first := drawn(t, screen.writes[1])
	assert.Equal(t, strings.Join(plain[:10], "\n"), strings.Join(first, "\n"), "a screen of 10 rows draws the first 10 lines of the frame")
	second := drawn(t, screen.writes[2])
	assert.Len(t, second, len(plain), "a screen taller than the frame draws all of it:\n%s\nwant\n%s", strings.Join(second, "\n"), strings.Join(plain, "\n"))
	// the clock is the first line
	assert.Equal(t, strings.Join(plain[1:], "\n"), strings.Join(second[1:], "\n"), "a screen taller than the frame draws all of it:\n%s\nwant\n%s", strings.Join(second, "\n"), strings.Join(plain, "\n"))
	assert.Len(t, asked, 2, "the height is read for every frame: read %d times for 2 frames", len(asked))
	for _, w := range asked {
		assert.Same(t, screen, w, "the size was read of another writer, not of the screen it draws on")
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
			require.NoError(t, newWatchWriter(screen, screenOf(tc.rows, tc.cols)).frame(text))
			require.Len(t, screen.writes, 1, "writes: %q", screen.writes)
			require.Equal(t, tc.want, drawn(t, screen.writes[0]), "%d columns", tc.cols)
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
	code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb)
	require.Equal(t, 0, code, "exit %d: %s", code, errb.String())
	require.Zero(t, errb.Len(), "exit %d: %s", code, errb.String())
	got := drawn(t, screen.writes[1])
	require.Len(t, got, len(plain), "a screen with no height known draws every line: %d of %d", len(got), len(plain))
	cut := 0
	for i, l := range got {
		want := plain[i]
		if r := []rune(want); len(r) > cols-1 {
			want, cut = string(r[:cols-1]), cut+1
		}
		assert.Equal(t, want, l, "line %d", i)
		assert.LessOrEqual(t, utf8.RuneCountInString(l), cols-1, "line %d is %d columns on a screen %d wide: %q", i, utf8.RuneCountInString(l), cols, l)
	}
	require.NotZero(t, cut, "no line of the frame is wider than the screen, so nothing shows the cut:\n%s", strings.Join(plain, "\n"))
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
	code := ta.a.whereLoop(ctx, ta.whereRun(true), screen, &errb)
	require.Equal(t, 0, code, "exit %d: %s", code, errb.String())
	require.Zero(t, errb.Len(), "exit %d: %s", code, errb.String())
	require.Len(t, screen.writes, 3, "the cursor hidden, ONE frame, the cursor restored: %d writes of %v bytes", len(screen.writes), writeSizes(screen.writes))
	frame := screen.writes[1]
	require.Greater(t, len(frame), 8192, "the frame is %d bytes, not over 8 KiB", len(frame))
	assert.Equal(t, strings.Join(plainLines(plain), "\n"), strings.Join(drawn(t, frame), "\n"), "the frame is not the whole of the view")
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
	require.NotNil(t, in.fire, "where --watch did not ask to be interrupted, so an interrupt would not reach it")
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
		sleeps++
		require.LessOrEqual(t, sleeps, 3, "the watch went on after the interrupt")
		if sleeps == 3 { // the interrupt arrives while it waits, after three frames
			in.now(t)
		}
	}
	screen := &writeLog{}
	var errb bytes.Buffer
	code := ta.a.cmdWhere(split("--watch --every 100ms"), screen, &errb)
	require.Equal(t, 0, code, "exit %d: %s", code, errb.String())
	require.Zero(t, errb.Len(), "exit %d: %s", code, errb.String())
	w := screen.writes
	require.Len(t, w, 5, "the cursor hidden, three frames, the cursor restored: %q", w)
	require.Equal(t, "\x1b[?25l", w[0], "the cursor hidden, three frames, the cursor restored: %q", w)
	require.Equal(t, "\x1b[?25h", w[4], "the cursor hidden, three frames, the cursor restored: %q", w)
	require.True(t, strings.HasPrefix(w[3], "\x1b[H"), "the cursor hidden, three frames, the cursor restored: %q", w)
	assert.Equal(t, 1, in.asked, "the interrupt was asked for %d times and let go %d times", in.asked, in.released)
	assert.Equal(t, 1, in.released, "the interrupt was asked for %d times and let go %d times", in.asked, in.released)
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
				sleeps++
				require.LessOrEqual(t, sleeps, 3, "the watch went on after the interrupt")
			}
			screen := &writeLog{}
			var errb bytes.Buffer
			code := ta.a.cmdWhere(split("--watch --every 100ms"), screen, &errb)
			require.Equal(t, 0, code, "exit %d: %s", code, errb.String())
			require.Zero(t, errb.Len(), "exit %d: %s", code, errb.String())
			w := screen.writes
			require.Len(t, w, 3, "the cursor hidden, the one frame, the cursor restored: %q", w)
			require.Equal(t, "\x1b[?25l", w[0], "the cursor hidden, the one frame, the cursor restored: %q", w)
			require.True(t, strings.HasPrefix(w[1], "\x1b[H"), "the cursor hidden, the one frame, the cursor restored: %q", w)
			require.Equal(t, "\x1b[?25h", w[2], "the cursor hidden, the one frame, the cursor restored: %q", w)
			assert.True(t, cut.sawDone, "the read the interrupt arrived in was not made in the command's context: the interrupt cannot cut it short")
			if assert.True(t, cut.dialCtx != nil, "the store was opened in a context the interrupt does not end") {
				assert.Error(t, cut.dialCtx.Err(), "the store was opened in a context the interrupt does not end")
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// screen is the writer the in-place tests hand the loop: it keeps what was
// drawn and says when each draw landed, so a test steps the loop by draws
// and never by the wall clock.
type screen struct {
	buf   bytes.Buffer
	drawn chan struct{}
}

func newScreen() *screen { return &screen{drawn: make(chan struct{}, 64)} }

func (s *screen) Write(p []byte) (int, error) {
	n, err := s.buf.Write(p)
	s.drawn <- struct{}{}
	return n, err
}

type brokenWatchOutput struct{}

func (brokenWatchOutput) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

type cancelWatchStderr struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelWatchStderr) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.cancel()
	return n, err
}

// demoTable is a rendered table for the watch tests, its cells filled in
// memory: the loop is tested with an injected read, ticker and clock, no
// store and no wall clock.
func demoTable(ready int64) ntable.Table {
	cols, err := ntable.ParseColumns("ready,working")
	if err != nil {
		panic(err)
	}
	t := ntable.Table{Name: "demo", Columns: cols}
	r := ntable.NewRow(t, "build")
	r.Cells[0].Count = ready
	t.Rows = []ntable.Row{r}
	return t
}

// TestWatchDrawsInPlaceTheTableAndNothingElse: two ticks produce two clear
// sequences and two renders, each byte-equal to the render of that tick's
// table; the screen holds no clock, no key, no status line (Glenn: "it
// should only contain that table data, no bullshit around it").
func TestWatchDrawsInPlaceTheTableAndNothingElse(t *testing.T) {
	t.Parallel()

	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	out := newScreen()
	var errOut bytes.Buffer
	tables := []ntable.Table{demoTable(1), demoTable(2)}
	reads := 0
	read := func(context.Context) (string, error) {
		reads++
		return renderAll("", []ntable.Table{tables[min(reads, 2)-1]}, ntable.RenderOpts{}), nil
	}
	clock := time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	done := make(chan int, 1)
	go func() { done <- watchLoop(ctx, out, &errOut, read, ticks, now, "") }()
	// the first draw is on entry; one tick draws again; the signal ends it
	<-out.drawn
	ticks <- clock.Add(time.Second)
	<-out.drawn
	cancel()
	{
		code := <-done
		require.EqualValues(t, 0, code, "watch exited %d", code)
	}
	want := clearScreen + ntable.Render(tables[0], ntable.RenderOpts{Title: tables[0].Name}) +
		clearScreen + ntable.Render(tables[1], ntable.RenderOpts{Title: tables[1].Name})
	{
		got := out.buf.String()
		require.Equal(t, want, got, "watched output:\n%q\nwant:\n%q", got, want)
	}
	require.EqualValues(t, 0, errOut.Len(), "stderr: %q", errOut.String())
}

// TestWatchKeepsTheLastGoodTableWithOneUnreachableLine: a read that fails
// leaves the last good text standing with one `store unreachable since
// <time>` line under it (the time of the first failed read, the same on every
// failing tick), says so on stderr once, and says once more when the store
// answers again; a successful tick draws the table and no counter.
func TestWatchKeepsTheLastGoodTableWithOneUnreachableLine(t *testing.T) {
	t.Parallel()

	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	out := newScreen()
	var errOut bytes.Buffer
	answers := []error{errors.New("dial tcp: connection refused"), nil, errors.New("dial tcp: connection refused"), errors.New("dial tcp: connection refused"), nil}
	reads := 0
	table := ntable.Render(demoTable(3), ntable.RenderOpts{})
	read := func(context.Context) (string, error) {
		err := answers[reads]
		reads++
		if err != nil {
			return "", err
		}
		return table, nil
	}
	base := time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC)
	clock := base
	now := func() time.Time { return clock }
	done := make(chan int, 1)
	go func() { done <- watchLoop(ctx, out, &errOut, read, ticks, now, "") }()
	<-out.drawn
	for i := 1; i < len(answers); i++ {
		clock = base.Add(time.Duration(i) * 7 * time.Second)
		ticks <- clock
		<-out.drawn
	}
	cancel()
	{
		code := <-done
		require.EqualValues(t, 0, code, "watch exited %d", code)
	}
	want := clearScreen + "store unreachable since 03:00:00\n" +
		clearScreen + table +
		clearScreen + table + "store unreachable since 03:00:14\n" +
		clearScreen + table + "store unreachable since 03:00:14\n" +
		clearScreen + table
	require.NotContains(t, out.buf.String(), "stale", "watched output holds a stale counter:\n%q", out.buf.String())
	{
		got := out.buf.String()
		require.Equal(t, want, got, "watched output:\n%q\nwant:\n%q", got, want)
	}
	wantErr := "nova-table watch: dial tcp: connection refused; the last good table stands until it answers\n" +
		"nova-table watch: Redis answers again\n" +
		"nova-table watch: dial tcp: connection refused; the last good table stands until it answers\n" +
		"nova-table watch: Redis answers again\n"
	{
		got := errOut.String()
		require.Equal(t, wantErr, got, "stderr:\n%q\nwant:\n%q", got, wantErr)
	}
}

// TestWatchPublishesToAFileByRename: with --out every tick writes the text
// to a temp file beside it and renames it over the file, so the file holds
// one whole table and no temp file is left.
func TestWatchPublishesToAFileByRename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := filepath.Join(dir, "TABLE.txt")
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	var stdout, errOut bytes.Buffer
	table := ntable.Render(demoTable(4), ntable.RenderOpts{})
	read := func(context.Context) (string, error) { return table, nil }
	done := make(chan int, 1)
	go func() { done <- watchLoop(ctx, &stdout, &errOut, read, ticks, time.Now, out) }()
	ticks <- time.Time{}
	cancel()
	{
		code := <-done
		require.EqualValues(t, 0, code, "watch exited %d", code)
	}
	body, err := os.ReadFile(out)
	require.NoError(t, err, "published file: %q %v; want the table", body, err)
	require.Equal(t, table, string(body), "published file: %q %v; want the table", body, err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "the directory holds %d entries (%v); want the one file, no temp", len(entries), err)
	require.Len(t, entries, 1, "the directory holds %d entries (%v); want the one file, no temp", len(entries), err)
	require.EqualValues(t, 0, stdout.Len(), "stdout %q stderr %q; --out draws nothing on the terminal", stdout.String(), errOut.String())
	require.EqualValues(t, 0, errOut.Len(), "stdout %q stderr %q; --out draws nothing on the terminal", stdout.String(), errOut.String())
}

// An output sink failure is terminal on the first frame. No timer, Redis
// service, or cancellation can turn a failed publication into exit 0.
func TestWatchOutputFailureStopsWithSinkRemedy(t *testing.T) {
	t.Parallel()
	read := func(context.Context) (string, error) { return "frame\n", nil }
	ticks := make(chan time.Time)
	now := func() time.Time { return time.Time{} }

	t.Run("stdout", func(t *testing.T) {
		var stderr bytes.Buffer
		code := watchLoop(context.Background(), brokenWatchOutput{}, &stderr, read, ticks, now, "")
		require.EqualValues(t, 1, code, "exit %d, want 1", code)
		for _, want := range []string{"stdout: broken pipe", "next: repair or replace the stdout consumer"} {
			require.Contains(t, stderr.String(), want, "stderr %q lacks %q", stderr.String(), want)
		}
	})

	t.Run("out file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "missing-directory", "table.txt")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var stdout bytes.Buffer
		stderr := &cancelWatchStderr{cancel: cancel}
		code := watchLoop(ctx, &stdout, stderr, read, ticks, now, out)
		require.EqualValues(t, 1, code, "exit %d, want 1", code)
		require.EqualValues(t, 0, stdout.Len(), "--out wrote to stdout: %q", stdout.String())
		for _, want := range []string{"--out:", out, "next: make --out", "parent directory present and writable"} {
			require.Contains(t, stderr.String(), want, "stderr %q lacks %q", stderr.String(), want)
		}
		{
			_, err := os.Stat(out)
			require.ErrorIs(t, err, os.ErrNotExist, "failed publication left target: %v", err)
		}
	})
}

// publish is the one-shot watch output path. A failed sink must report a
// publication failure, not a usage refusal after a successful read.
func TestWatchOnceOutputFailureUsesWatchSinkResult(t *testing.T) {
	t.Parallel()
	t.Run("stdout", func(t *testing.T) {
		var stderr bytes.Buffer
		code := publish("", "frame\n", brokenWatchOutput{}, &stderr, "watch")
		require.Equal(t, 1, code, "output failure exit")
		for _, want := range []string{"stdout: broken pipe", "--out <file>"} {
			assert.Contains(t, stderr.String(), want)
		}
	})
	t.Run("out file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "missing-directory", "table.txt")
		var stdout, stderr bytes.Buffer
		code := publish(out, "frame\n", &stdout, &stderr, "watch")
		require.Equal(t, 1, code, "output failure exit")
		assert.Empty(t, stdout.String(), "--out wrote to stdout")
		for _, want := range []string{"--out:", out, "next: make --out"} {
			assert.Contains(t, stderr.String(), want)
		}
		_, err := os.Stat(out)
		assert.ErrorIs(t, err, os.ErrNotExist, "failed publication left target")
	})
}

// TestRenderAllJoinsTablesWithOneBlankLine: the view's title first, every
// table as a block headed by its name (Glenn 2026-09-27: "tables need a
// title"), one blank line between blocks, and an empty table a block too,
// its header and footer (the owner's ruling, 2026-09-30).
func TestRenderAllJoinsTablesWithOneBlankLine(t *testing.T) {
	t.Parallel()

	a, b := demoTable(1), demoTable(2)
	b.Name = "other"
	empty := ntable.Table{Name: "empty", Columns: a.Columns}
	ra, rb := ntable.Render(a, ntable.RenderOpts{Title: "demo"}), ntable.Render(b, ntable.RenderOpts{Title: "other"})
	re := ntable.Render(empty, ntable.RenderOpts{Title: "empty"})
	{
		got := renderAll("", []ntable.Table{a, empty, b}, ntable.RenderOpts{})
		require.Equal(t, ra+"\n"+re+"\n"+rb, got, "two tables and an empty one:\n%q", got)
	}
	{
		got := renderAll("SPRINT", []ntable.Table{a}, ntable.RenderOpts{})
		require.Equal(t, "SPRINT\n\n"+ra, got, "with a title:\n%q", got)
	}
	{
		got := renderAll("", []ntable.Table{empty}, ntable.RenderOpts{})
		require.Equal(t, re, got, "an empty table alone renders %q, want its header and footer %q", got, re)
		require.NotEmpty(t, re, "an empty table alone renders %q, want its header and footer %q", got, re)
	}
	require.NotContains(t, ra, "\n\n", "%v", "a render holds a blank line")
}

func TestViewSummaryUsesAllKnownCounts(t *testing.T) {
	t.Parallel()
	{
		got := ntable.SummaryLine(ntable.View{Summary: "ready"}, demoTable(0))
		require.Equal(t, "0/0 0.0% -> ETA", got, "empty known counts: %s", got)
	}
	tb := demoTable(3)
	tb.Hidden = []string{"ready"}
	tb.Rows[0].Hidden = true
	{
		got := ntable.SummaryLine(ntable.View{Summary: "ready"}, tb)
		require.Equal(t, "3/3 100.0% -> ETA", got, "hidden counts: %s", got)
	}
	tb.Rows[0].Cells[1].Unread = true
	{
		got := ntable.SummaryLine(ntable.View{Summary: "ready"}, tb)
		require.Equal(t, "?/? ? -> ETA", got, "unknown count: %s", got)
	}
	tb.Rows[0].Cells[1].Unread = false
	{
		got := ntable.SummaryLine(ntable.View{Summary: "missing"}, tb)
		require.Equal(t, "?/? ? -> ETA", got, "missing column: %s", got)
	}
}

// A view's frame: while the view has a state, the summary line is that state
// alone, with no counts, percent or ETA; without one, the counts.
func TestAViewsFrameShowsItsStateAloneAsTheSummaryLine(t *testing.T) {
	t.Parallel()
	tb := demoTable(3)
	for _, c := range []struct{ state, want string }{
		{"STOPPED", "SPRINT TABLE\n\nSTOPPED\n\n"},
		{"", "SPRINT TABLE\n\n3/3 100.0% -> ETA\n\n"},
	} {
		viewGet := func(context.Context, redis.Cmdable, string) (ntable.View, error) {
			return ntable.View{Name: "sprint", Title: "SPRINT TABLE", Tables: []string{"demo"}, Summary: "ready", State: c.state}, nil
		}
		snapshotter := func(redis.Cmdable, []string) func(context.Context) ([]ntable.Table, error) {
			return func(context.Context) ([]ntable.Table, error) { return []ntable.Table{tb}, nil }
		}
		got, err := viewReaderWith(nil, "sprint", ntable.RenderOpts{}, false, viewGet, snapshotter, nil)(context.Background())
		require.NoError(t, err, "%v", err)
		_, frame, _ := strings.Cut(got, "\n\n") // the clock line first
		require.True(t, strings.HasPrefix(frame, c.want), "state %q: frame\n%s\nwant it to open with %q", c.state, frame, c.want)
		require.True(t, c.state == "" || !strings.Contains(frame, "ETA"), "state %q: the frame still counts:\n%s", c.state, frame)
	}
}

func TestWatchCheckTableFailureProducesStallRow(t *testing.T) {
	t.Parallel()
	tb := demoTable(1)
	snapshots := func(context.Context) ([]ntable.Table, error) {
		return []ntable.Table{tb}, nil
	}
	checker := func(ctx context.Context, c redis.Cmdable, name string) (ntable.CheckReport, error) {
		return ntable.CheckReport{}, fmt.Errorf("table %s: %w: [build ready job duplicate place]; run: nova-table show '%s'", name, ntable.ErrDrift, name)
	}

	read := tablesReaderWith(nil, []string{"demo"}, "", ntable.RenderOpts{}, true, snapshots, checker)
	got, err := read(context.Background())
	require.NoError(t, err, "read: %v", err)
	wantTable := ntable.Render(tb, ntable.RenderOpts{Title: tb.Name})
	wantStall := "stall: demo: member record and owned set disagree: [build ready job duplicate place]\n"
	require.True(t, strings.HasPrefix(got, wantTable), "expected table prefix:\n%s\ngot:\n%s", wantTable, got)
	require.True(t, strings.HasSuffix(got, wantStall), "expected stall row suffix:\n%s\ngot:\n%s", wantStall, got)
}

func TestWatchCheckViewFailureProducesStallRow(t *testing.T) {
	t.Parallel()
	tb := demoTable(1)
	viewGet := func(ctx context.Context, c redis.Cmdable, name string) (ntable.View, error) {
		return ntable.View{Name: name, Title: "Work View", Tables: []string{"demo"}}, nil
	}
	snapshotter := func(c redis.Cmdable, names []string) func(context.Context) ([]ntable.Table, error) {
		return func(context.Context) ([]ntable.Table, error) {
			return []ntable.Table{tb}, nil
		}
	}
	checker := func(ctx context.Context, c redis.Cmdable, name string) (ntable.CheckReport, error) {
		return ntable.CheckReport{}, fmt.Errorf("table %s: %w: requested 1, active 2; run: nova-table show '%s'", name, ntable.ErrStale, name)
	}

	read := viewReaderWith(nil, "myview", ntable.RenderOpts{}, true, viewGet, snapshotter, checker)
	got, err := read(context.Background())
	require.NoError(t, err, "read: %v", err)
	wantStall := "stall: demo: requested epoch is stale, not the active epoch: requested 1, active 2\n"
	require.Contains(t, got, "Work View", "expected view title in output:\n%s", got)
	require.True(t, strings.HasSuffix(got, wantStall), "expected stall row suffix:\n%s\ngot:\n%s", wantStall, got)
}

func TestWatchCheckSuccessProducesNoStallRow(t *testing.T) {
	t.Parallel()
	tb := demoTable(1)
	snapshots := func(context.Context) ([]ntable.Table, error) {
		return []ntable.Table{tb}, nil
	}
	checkCalled := false
	checker := func(ctx context.Context, c redis.Cmdable, name string) (ntable.CheckReport, error) {
		checkCalled = true
		return ntable.CheckReport{Epoch: 1, Revision: 1, Members: 1, Cells: 1}, nil
	}

	read := tablesReaderWith(nil, []string{"demo"}, "", ntable.RenderOpts{}, true, snapshots, checker)
	got, err := read(context.Background())
	require.NoError(t, err, "read: %v", err)
	require.True(t, checkCalled, "%v", "expected check to be called")
	require.NotContains(t, got, "stall:", "expected no stall row on successful check, got:\n%s", got)
	wantTable := ntable.Render(tb, ntable.RenderOpts{Title: tb.Name})
	require.Equal(t, wantTable, got, "got %q, want %q", got, wantTable)
}

func TestWatchCheckDisabledDoesNotRunCheck(t *testing.T) {
	t.Parallel()
	tb := demoTable(1)
	snapshots := func(context.Context) ([]ntable.Table, error) {
		return []ntable.Table{tb}, nil
	}
	checkCalled := false
	checker := func(ctx context.Context, c redis.Cmdable, name string) (ntable.CheckReport, error) {
		checkCalled = true
		return ntable.CheckReport{}, errors.New("should not be called")
	}

	read := tablesReaderWith(nil, []string{"demo"}, "", ntable.RenderOpts{}, false, snapshots, checker)
	got, err := read(context.Background())
	require.NoError(t, err, "read: %v", err)
	require.False(t, checkCalled, "%v", "expected ntable.Check NOT to be called when check is false")
	require.NotContains(t, got, "stall:", "expected no stall row, got:\n%s", got)
}

func TestWatchCheckFlagWired(t *testing.T) {
	t.Parallel()
	code, stdout, errout := runTable("watch", "--help")
	require.EqualValues(t, 0, code, "watch --help: code %d errout %q", code, errout)
	require.Empty(t, errout, "watch --help: code %d errout %q", code, errout)
	require.Contains(t, stdout, "-check", "expected --check flag in watch help, got:\n%s", stdout)
	require.Contains(t, stdout, "run table check every tick", "expected --check flag in watch help, got:\n%s", stdout)
}

func TestFormatStall(t *testing.T) {
	t.Parallel()
	err1 := fmt.Errorf("table demo: %w: [row col id duplicate place]; run: nova-table show 'demo'", ntable.ErrDrift)
	{
		got := formatStall("demo", err1)
		require.Equal(t, "stall: demo: member record and owned set disagree: [row col id duplicate place]", got, "unexpected format: %q", got)
	}
	err2 := fmt.Errorf("table demo: %w: requested 1, active 2; run: nova-table show 'demo'", ntable.ErrStale)
	{
		got := formatStall("demo", err2)
		require.Equal(t, "stall: demo: requested epoch is stale, not the active epoch: requested 1, active 2", got, "unexpected format: %q", got)
	}
	err3 := errors.New("connection failed")
	{
		got := formatStall("demo", err3)
		require.Equal(t, "stall: demo: connection failed", got, "unexpected format: %q", got)
	}
}

func TestAppendStalls(t *testing.T) {
	t.Parallel()
	{
		got := appendStalls("table\n", nil)
		require.Equal(t, "table\n", got, "empty stalls: got %q", got)
	}
	{
		got := appendStalls("table\n", []string{"stall: a: b"})
		require.Equal(t, "table\nstall: a: b\n", got, "with trailing newline: got %q", got)
	}
	{
		got := appendStalls("table", []string{"stall: a: b"})
		require.Equal(t, "table\nstall: a: b\n", got, "without trailing newline: got %q", got)
	}
	{
		got := appendStalls("", []string{"stall: a: b"})
		require.Equal(t, "stall: a: b\n", got, "empty text: got %q", got)
	}
}

// A stored view's frame draws every table and every row, all-zero or not.
func TestViewReaderDrawsZeroRowsOfEveryTable(t *testing.T) {
	t.Parallel()
	zero, kept := demoTable(0), demoTable(0)
	kept.Name = "kept"
	viewGet := func(context.Context, redis.Cmdable, string) (ntable.View, error) {
		return ntable.View{Name: "v", Tables: []string{"demo", "kept"}}, nil
	}
	snapshotter := func(redis.Cmdable, []string) func(context.Context) ([]ntable.Table, error) {
		return func(context.Context) ([]ntable.Table, error) { return []ntable.Table{zero, kept}, nil }
	}
	got, err := viewReaderWith(nil, "v", ntable.RenderOpts{}, false, viewGet, snapshotter, nil)(context.Background())
	require.NoError(t, err, "%v", err)
	require.Contains(t, got, "demo", "demo (all zero) and kept are both drawn:\n%s", got)
	require.Contains(t, got, "kept", "demo (all zero) and kept are both drawn:\n%s", got)
	require.Contains(t, got, "build", "demo (all zero) and kept are both drawn:\n%s", got)
}

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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

// demoTable is a rendered table for the watch tests, its cells filled in
// memory: the loop is tested with an injected read, ticker and clock, no
// store and no wall clock.
func demoTable(ready int64) ntable.Table {
	cols, err := ntable.ParseColumns("job:text:none,ready,working")
	if err != nil {
		panic(err)
	}
	t := ntable.Table{Name: "demo", Columns: cols}
	r := ntable.NewRow(t, "build")
	r.Cells[1].Count = ready
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
	if code := <-done; code != 0 {
		t.Fatalf("watch exited %d", code)
	}
	want := clearScreen + ntable.Render(tables[0], ntable.RenderOpts{Title: tables[0].Name}) +
		clearScreen + ntable.Render(tables[1], ntable.RenderOpts{Title: tables[1].Name})
	if got := out.buf.String(); got != want {
		t.Fatalf("watched output:\n%q\nwant:\n%q", got, want)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr: %q", errOut.String())
	}
}

// TestWatchKeepsTheLastGoodTableWithOneStaleLine: a read that fails leaves
// the last good text standing with one `stale: <n>s` line under it, says
// so on stderr once, and says once more when the store answers again; a
// failure before any read draws `stale: never read`.
func TestWatchKeepsTheLastGoodTableWithOneStaleLine(t *testing.T) {
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
	if code := <-done; code != 0 {
		t.Fatalf("watch exited %d", code)
	}
	want := clearScreen + "stale: never read\n" +
		clearScreen + table +
		clearScreen + table + "stale: 7s\n" +
		clearScreen + table + "stale: 14s\n" +
		clearScreen + table
	if got := out.buf.String(); got != want {
		t.Fatalf("watched output:\n%q\nwant:\n%q", got, want)
	}
	wantErr := "nova-table watch: dial tcp: connection refused; the last good table stands until it answers\n" +
		"nova-table watch: Redis answers again\n" +
		"nova-table watch: dial tcp: connection refused; the last good table stands until it answers\n" +
		"nova-table watch: Redis answers again\n"
	if got := errOut.String(); got != wantErr {
		t.Fatalf("stderr:\n%q\nwant:\n%q", got, wantErr)
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
	if code := <-done; code != 0 {
		t.Fatalf("watch exited %d", code)
	}
	body, err := os.ReadFile(out)
	if err != nil || string(body) != table {
		t.Fatalf("published file: %q %v; want the table", body, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the directory holds %d entries (%v); want the one file, no temp", len(entries), err)
	}
	if stdout.Len() != 0 || errOut.Len() != 0 {
		t.Fatalf("stdout %q stderr %q; --out draws nothing on the terminal", stdout.String(), errOut.String())
	}
}

// TestRenderAllJoinsTablesWithOneBlankLine: the view's title first, every
// table as a block headed by its name (Glenn 2026-09-27: "tables need a
// title"), one blank line between blocks, and an empty table a block that
// says (no rows), so nothing on the screen is anonymous.
func TestRenderAllJoinsTablesWithOneBlankLine(t *testing.T) {
	t.Parallel()

	a, b := demoTable(1), demoTable(2)
	b.Name = "other"
	empty := ntable.Table{Name: "empty", Columns: a.Columns}
	ra, rb := ntable.Render(a, ntable.RenderOpts{Title: "demo"}), ntable.Render(b, ntable.RenderOpts{Title: "other"})
	if got := renderAll("", []ntable.Table{a, empty, b}, ntable.RenderOpts{}); got != ra+"\nempty\n(no rows)\n\n"+rb {
		t.Fatalf("two tables and an empty one:\n%q", got)
	}
	if got := renderAll("SPRINT", []ntable.Table{a}, ntable.RenderOpts{}); got != "SPRINT\n\n"+ra {
		t.Fatalf("with a title:\n%q", got)
	}
	if got := renderAll("", []ntable.Table{empty}, ntable.RenderOpts{}); got != "empty\n(no rows)\n" {
		t.Fatalf("an empty table alone renders %q, want its name and (no rows)", got)
	}
	if strings.Contains(ra, "\n\n") {
		t.Fatal("a render holds a blank line")
	}
}

func TestViewSummaryUsesAllKnownCounts(t *testing.T) {
	t.Parallel()
	tb := demoTable(3)
	tb.Hidden = []string{"ready"}
	tb.Rows[0].Hidden = true
	if got := viewSummary(tb, "ready"); got != "3/3 100.0% -> ETA -" {
		t.Fatalf("hidden counts: %s", got)
	}
	tb.Rows[0].Cells[2].Unread = true
	if got := viewSummary(tb, "ready"); got != "?/? ? -> ETA -" {
		t.Fatalf("unknown count: %s", got)
	}
	tb.Rows[0].Cells[2].Unread = false
	if got := viewSummary(tb, "missing"); got != "?/? ? -> ETA -" {
		t.Fatalf("missing column: %s", got)
	}
}

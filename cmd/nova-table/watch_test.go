package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
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
	if code := <-done; code != 0 {
		t.Fatalf("watch exited %d", code)
	}
	want := clearScreen + "store unreachable since 03:00:00\n" +
		clearScreen + table +
		clearScreen + table + "store unreachable since 03:00:14\n" +
		clearScreen + table + "store unreachable since 03:00:14\n" +
		clearScreen + table
	if strings.Contains(out.buf.String(), "stale") {
		t.Fatalf("watched output holds a stale counter:\n%q", out.buf.String())
	}
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
// title"), one blank line between blocks, and an empty table no block at
// all and no gap (Glenn 2026-09-27: "When a table has no rows, it should
// automatically hide. When it has rows again, it should show").
func TestRenderAllJoinsTablesWithOneBlankLine(t *testing.T) {
	t.Parallel()

	a, b := demoTable(1), demoTable(2)
	b.Name = "other"
	empty := ntable.Table{Name: "empty", Columns: a.Columns}
	ra, rb := ntable.Render(a, ntable.RenderOpts{Title: "demo"}), ntable.Render(b, ntable.RenderOpts{Title: "other"})
	if got := renderAll("", []ntable.Table{a, empty, b}, ntable.RenderOpts{}); got != ra+"\n"+rb {
		t.Fatalf("two tables and an empty one:\n%q", got)
	}
	if got := renderAll("SPRINT", []ntable.Table{a}, ntable.RenderOpts{}); got != "SPRINT\n\n"+ra {
		t.Fatalf("with a title:\n%q", got)
	}
	if got := renderAll("", []ntable.Table{empty}, ntable.RenderOpts{}); got != "" {
		t.Fatalf("an empty table alone renders %q, want nothing", got)
	}
	if strings.Contains(ra, "\n\n") {
		t.Fatal("a render holds a blank line")
	}
}

func TestViewSummaryUsesAllKnownCounts(t *testing.T) {
	t.Parallel()
	if got := ntable.SummaryLine(ntable.View{Summary: "ready"}, demoTable(0)); got != "0/0 0.0% -> ETA" {
		t.Fatalf("empty known counts: %s", got)
	}
	tb := demoTable(3)
	tb.Hidden = []string{"ready"}
	tb.Rows[0].Hidden = true
	if got := ntable.SummaryLine(ntable.View{Summary: "ready"}, tb); got != "3/3 100.0% -> ETA" {
		t.Fatalf("hidden counts: %s", got)
	}
	tb.Rows[0].Cells[1].Unread = true
	if got := ntable.SummaryLine(ntable.View{Summary: "ready"}, tb); got != "?/? ? -> ETA" {
		t.Fatalf("unknown count: %s", got)
	}
	tb.Rows[0].Cells[1].Unread = false
	if got := ntable.SummaryLine(ntable.View{Summary: "missing"}, tb); got != "?/? ? -> ETA" {
		t.Fatalf("missing column: %s", got)
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
		if err != nil {
			t.Fatal(err)
		}
		_, frame, _ := strings.Cut(got, "\n\n") // the clock line first
		if !strings.HasPrefix(frame, c.want) {
			t.Fatalf("state %q: frame\n%s\nwant it to open with %q", c.state, frame, c.want)
		}
		if c.state != "" && strings.Contains(frame, "ETA") {
			t.Fatalf("state %q: the frame still counts:\n%s", c.state, frame)
		}
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
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	wantTable := ntable.Render(tb, ntable.RenderOpts{Title: tb.Name})
	wantStall := "stall: demo: member record and owned set disagree: [build ready job duplicate place]\n"
	if !strings.HasPrefix(got, wantTable) {
		t.Fatalf("expected table prefix:\n%s\ngot:\n%s", wantTable, got)
	}
	if !strings.HasSuffix(got, wantStall) {
		t.Fatalf("expected stall row suffix:\n%s\ngot:\n%s", wantStall, got)
	}
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
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	wantStall := "stall: demo: requested epoch is stale, not the active epoch: requested 1, active 2\n"
	if !strings.Contains(got, "Work View") {
		t.Fatalf("expected view title in output:\n%s", got)
	}
	if !strings.HasSuffix(got, wantStall) {
		t.Fatalf("expected stall row suffix:\n%s\ngot:\n%s", wantStall, got)
	}
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
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !checkCalled {
		t.Fatal("expected check to be called")
	}
	if strings.Contains(got, "stall:") {
		t.Fatalf("expected no stall row on successful check, got:\n%s", got)
	}
	wantTable := ntable.Render(tb, ntable.RenderOpts{Title: tb.Name})
	if got != wantTable {
		t.Fatalf("got %q, want %q", got, wantTable)
	}
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
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if checkCalled {
		t.Fatal("expected ntable.Check NOT to be called when check is false")
	}
	if strings.Contains(got, "stall:") {
		t.Fatalf("expected no stall row, got:\n%s", got)
	}
}

func TestWatchCheckFlagWired(t *testing.T) {
	t.Parallel()
	code, stdout, errout := runTable("watch", "--help")
	if code != 0 || errout != "" {
		t.Fatalf("watch --help: code %d errout %q", code, errout)
	}
	if !strings.Contains(stdout, "-check") || !strings.Contains(stdout, "run table check every tick") {
		t.Fatalf("expected --check flag in watch help, got:\n%s", stdout)
	}
}

func TestFormatStall(t *testing.T) {
	t.Parallel()
	err1 := fmt.Errorf("table demo: %w: [row col id duplicate place]; run: nova-table show 'demo'", ntable.ErrDrift)
	if got := formatStall("demo", err1); got != "stall: demo: member record and owned set disagree: [row col id duplicate place]" {
		t.Fatalf("unexpected format: %q", got)
	}
	err2 := fmt.Errorf("table demo: %w: requested 1, active 2; run: nova-table show 'demo'", ntable.ErrStale)
	if got := formatStall("demo", err2); got != "stall: demo: requested epoch is stale, not the active epoch: requested 1, active 2" {
		t.Fatalf("unexpected format: %q", got)
	}
	err3 := errors.New("connection failed")
	if got := formatStall("demo", err3); got != "stall: demo: connection failed" {
		t.Fatalf("unexpected format: %q", got)
	}
}

func TestAppendStalls(t *testing.T) {
	t.Parallel()
	if got := appendStalls("table\n", nil); got != "table\n" {
		t.Fatalf("empty stalls: got %q", got)
	}
	if got := appendStalls("table\n", []string{"stall: a: b"}); got != "table\nstall: a: b\n" {
		t.Fatalf("with trailing newline: got %q", got)
	}
	if got := appendStalls("table", []string{"stall: a: b"}); got != "table\nstall: a: b\n" {
		t.Fatalf("without trailing newline: got %q", got)
	}
	if got := appendStalls("", []string{"stall: a: b"}); got != "stall: a: b\n" {
		t.Fatalf("empty text: got %q", got)
	}
}

// A stored view's HideZero reaches the frame: the named table hides a row whose
// counts are all zero, and a table the view does not name keeps it.
func TestViewReaderHidesZeroRowsOfTheTablesTheViewNames(t *testing.T) {
	t.Parallel()
	hidden, kept := demoTable(0), demoTable(0)
	kept.Name = "kept"
	viewGet := func(context.Context, redis.Cmdable, string) (ntable.View, error) {
		return ntable.View{Name: "v", Tables: []string{"demo", "kept"}, HideZero: []string{"demo"}}, nil
	}
	snapshotter := func(redis.Cmdable, []string) func(context.Context) ([]ntable.Table, error) {
		return func(context.Context) ([]ntable.Table, error) { return []ntable.Table{hidden, kept}, nil }
	}
	got, err := viewReaderWith(nil, "v", ntable.RenderOpts{}, false, viewGet, snapshotter, nil)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "demo") || !strings.Contains(got, "kept") || !strings.Contains(got, "build") {
		t.Fatalf("demo (all zero) is not drawn, kept is:\n%s", got)
	}
}

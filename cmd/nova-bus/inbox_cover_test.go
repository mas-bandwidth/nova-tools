package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inboxCoverEntry builds the OpenEntry an open list, a body item or a body page carries.
// Values are free of whitespace and "=", so oneline.Field passes them through verbatim and
// the expected lines below are the bytes the printers wrote.
func inboxCoverEntry(kind bus.OpenKind, path string) bus.OpenEntry {
	return bus.OpenEntry{
		ID:      "ada-0123456789ab",
		Kind:    kind,
		From:    "Ada",
		Addr:    "to",
		Date:    "2026-03-04T05:06:07Z",
		Subject: "Covering the shape",
		Path:    path,
	}
}

// TestInboxCoverOpenAfterBodiesKeepsTheEmittedPrefix pins openAfterBodies on both branches.
// A full read derives OPEN anew: prior rows survive only where the fresh listing still holds
// them, and everything through the page's SafeFrontier is re-added, so a two-note commit
// persists both notes and a later page never replaces an earlier one. An incremental read
// keeps current minus fresh and re-adds the same emitted prefix. The refusal row is the one
// where no item reaches the frontier: the carried list is kept and nothing is added.
func TestInboxCoverOpenAfterBodiesKeepsTheEmittedPrefix(t *testing.T) {
	t.Parallel()
	a := inboxCoverEntry(bus.OpenNote, "from-ada/1.md")
	b := inboxCoverEntry(bus.OpenNote, "from-ada/2.md")
	c := inboxCoverEntry(bus.OpenNote, "from-ada/3.md")
	d := inboxCoverEntry(bus.OpenNote, "from-ada/4.md")
	p1 := inboxCoverEntry(bus.OpenNote, "from-ada/p1.md")
	p2 := inboxCoverEntry(bus.OpenNote, "from-ada/p2.md")
	p3 := inboxCoverEntry(bus.OpenNote, "from-ada/p3.md")
	items := []bus.BodyItem{
		{Commit: "f1", Path: p1.Path, Entry: p1},
		{Commit: "f1", Path: p2.Path, Entry: p2},
		{Commit: "f2", Path: p3.Path, Entry: p3},
	}
	cases := []struct {
		name                        string
		current, fresh, prior, want []bus.OpenEntry
		full                        bool
		items                       []bus.BodyItem
		frontier                    string
	}{
		{
			name:     "a full read derives open anew from prior survivors and the emitted prefix",
			current:  []bus.OpenEntry{a, b, c},
			prior:    []bus.OpenEntry{b, d},
			full:     true,
			items:    items,
			frontier: "f1",
			want:     []bus.OpenEntry{b, p1, p2},
		},
		{
			name:     "an incremental read keeps current minus fresh and re-adds the prefix",
			current:  []bus.OpenEntry{a, b, c},
			fresh:    []bus.OpenEntry{a},
			items:    items,
			frontier: "f1",
			want:     []bus.OpenEntry{b, c, p1, p2},
		},
		{
			name:     "an item the carried list already holds is not added twice",
			current:  []bus.OpenEntry{b, c},
			prior:    []bus.OpenEntry{b},
			full:     true,
			items:    []bus.BodyItem{{Commit: "f1", Path: b.Path, Entry: b}},
			frontier: "f1",
			want:     []bus.OpenEntry{b},
		},
		{
			name:     "a page whose last item is past the frontier adds nothing",
			current:  []bus.OpenEntry{b},
			items:    items,
			frontier: "f0",
			want:     []bus.OpenEntry{b},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := openAfterBodies(tc.current, tc.fresh, tc.prior, tc.items, bus.BodyPage{SafeFrontier: tc.frontier}, tc.full)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestInboxCoverWalkProgressNarratesThrottledThenCloses pins the walk narration through its
// own struct, in process: the throttle refuses a line under a second (last is set fresh, so
// the refusal is decided by the field and not by the clock), a forced emit always writes,
// advance records and stays under the throttle, and finish stops the ticker and writes the
// one closing line. The elapsed value is machine time and is never asserted. No sleeps.
func TestInboxCoverWalkProgressNarratesThrottledThenCloses(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := newWalkProgress(&buf, 10)
	p.last = time.Now()
	p.advance(3, 2)
	assert.Empty(t, buf.String(), "a line under a second after the last one is refused")
	emit := func(force bool) string {
		p.emit(force)
		out := buf.String()
		buf.Reset()
		return out
	}
	first := emit(true)
	assert.True(t, strings.HasPrefix(first, "INBOX WALK commits=3/10 notes=2 elapsed="), "got %q", first)
	assert.True(t, strings.HasSuffix(first, "\n"), "one line")
	p.advance(4, 2)
	assert.Empty(t, buf.String(), "advance stays under the throttle")
	p.finish(5, 2)
	closing := buf.String()
	buf.Reset()
	assert.True(t, strings.HasPrefix(closing, "INBOX WALK commits=5/10 notes=2 elapsed="), "got %q", closing)
	assert.Equal(t, 1, strings.Count(closing, "\n"), "finish writes exactly one closing line")
}

// TestInboxCoverWalkProgressAbortStopsSilently pins abort: it stops the ticker and waits
// for it, writing nothing, so a since-walk that ends in a refusal is one line.
func TestInboxCoverWalkProgressAbortStopsSilently(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := newWalkProgress(&buf, 3)
	p.abort()
	assert.Empty(t, buf.String())
}

// TestInboxCoverBodyLimitRefusesZeroAndOverTheCeiling pins bodyLimit: 1..ceiling passes
// with nothing written; zero (unlimited is not a thing) and over-ceiling each print the
// INBOX REFUSED shape naming the flag, the value and the ceiling, and are false.
func TestInboxCoverBodyLimitRefusesZeroAndOverTheCeiling(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		value      int64
		want       bool
		wantInLine string
	}{
		{"in range", 500, true, ""},
		{"the floor", 1, true, ""},
		{"zero is not unlimited", 0, false, "INBOX REFUSED: --body-notes 0 is not unlimited; give 1 to 1000"},
		{"negative is not unlimited", -3, false, "INBOX REFUSED: --body-notes -3 is not unlimited; give 1 to 1000"},
		{"over the ceiling", 1001, false, "INBOX REFUSED: --body-notes 1001 is over the ceiling 1000; run: nova-bus inbox -h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			got := bodyLimit(&buf, "--body-notes", tc.value, 1000)
			assert.Equal(t, tc.want, got)
			if tc.wantInLine == "" {
				assert.Empty(t, buf.String())
			} else {
				assert.Contains(t, buf.String(), tc.wantInLine)
			}
		})
	}
}

// TestInboxCoverRefuseContinuationNamesEachReason pins refuseContinuation: one shape, exit
// 2, and a sentence per reason -- the moved-cursor mismatch names both cursors, the
// no-item token its own sentence, and anything else says what did not match. Every line
// ends with the same remedy: rerun without --after.
func TestInboxCoverRefuseContinuationNamesEachReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		assert func(t *testing.T, line string)
	}{
		{
			name: "a cursor another read moved names both cursors",
			err:  &bus.BodyCursorMismatchError{Token: "tok-1", Persisted: "cur-1"},
			assert: func(t *testing.T, line string) {
				assert.Contains(t, line, "INBOX REFUSED: --after names cursor tok-1 and this reader's cursor is cur-1; rerun without --after")
			},
		},
		{
			name: "a token that names no item says so",
			err:  bus.ErrBodyTokenNoItem,
			assert: func(t *testing.T, line string) {
				assert.Equal(t, "INBOX REFUSED: --after <token> names no item in this range; rerun without --after\n", line)
			},
		},
		{
			name: "anything else says what did not match",
			err:  errors.New("body page token too long"),
			assert: func(t *testing.T, line string) {
				assert.Equal(t, "INBOX REFUSED: --after <token> is not a continuation for this read: body page token too long; rerun without --after\n", line)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			code := refuseContinuation(&buf, tc.err)
			assert.Equal(t, 2, code)
			tc.assert(t, buf.String())
		})
	}
}

// TestInboxCoverPrintBodyPagePrintsGroupsThenGaps pins printBodyPage: the display follows
// the NOTE, HEARD, RECEIPT groups whatever the snapshot order was, gaps are accounting
// events printed after the grouped rows in canonical order, and a page holding an emission
// with neither item nor gap is an error, the one refusal this printer has.
func TestInboxCoverPrintBodyPagePrintsGroupsThenGaps(t *testing.T) {
	t.Parallel()
	note := inboxCoverEntry(bus.OpenNote, "from-ada/note.md")
	heard := inboxCoverEntry(bus.OpenNote, "from-ada/heard.md")
	heard.Heard = true
	receipt := inboxCoverEntry(bus.OpenReceipt, "from-ada/receipt.md")
	page := bus.BodyPage{
		SafeFrontier: "f2",
		Emissions: []bus.BodyEmission{
			{Item: &bus.BodyItem{Commit: "f2", Path: receipt.Path, Entry: receipt, Body: []byte("ack\n")}},
			{Item: &bus.BodyItem{Commit: "f1", Path: note.Path, Entry: note, Body: []byte("body\n")}},
			{Item: &bus.BodyItem{Commit: "f1", Path: heard.Path, Entry: heard}},
			{Gap: &bus.BodyGap{ID: "big-0123456789ab", Commit: "f1", Path: "from-ada/big.md", Bytes: 100}},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, printBodyPage(&buf, page, 64))
	assert.Equal(t, ""+
		"INBOX NOTE id=ada-0123456789ab from=Ada addr=to at=2026-03-04T05:06:07Z path=from-ada/note.md: Covering the shape\n"+
		"INBOX BODY id=ada-0123456789ab bytes=5\n"+
		"body\n"+
		"INBOX BODY END id=ada-0123456789ab\n"+
		"INBOX HEARD id=ada-0123456789ab from=Ada addr=to at=2026-03-04T05:06:07Z path=from-ada/heard.md: Covering the shape\n"+
		"INBOX RECEIPT id=ada-0123456789ab from=Ada addr=to at=2026-03-04T05:06:07Z path=from-ada/receipt.md: Covering the shape\n"+
		"INBOX BODY OVERSIZE id=big-0123456789ab bytes=100 max-bytes=64 path=from-ada/big.md\n",
		buf.String())

	var empty bytes.Buffer
	err := printBodyPage(&empty, bus.BodyPage{Emissions: []bus.BodyEmission{{}}}, 64)
	require.Error(t, err)
	assert.EqualError(t, err, "body page has an empty emission")
	assert.Empty(t, empty.String())
}

// TestInboxCoverHiddenReasonNamesTheLineAndTheInstant pins hiddenReason's one sentence:
// the line as the reader drew it, the instant written now, and the read-once remedy.
func TestInboxCoverHiddenReasonNamesTheLineAndTheInstant(t *testing.T) {
	t.Parallel()
	line, err := bus.NewLegacyLine("2030-01-02")
	require.NoError(t, err)
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	got := hiddenReason(line, now)
	assert.Contains(t, got, "your switch-day line is 2030-01-02, which is after a note written now (2026-03-04T05:06:07Z)")
	assert.Contains(t, got, "read once with `--full --legacy-before 2026-03-04T05:06:07Z --advance`")
}

// TestInboxCoverUTCDayStartsTheUTCCalendarDay pins utcDay: the day is read in UTC and not
// in the local zone, so an evening local is still the same UTC calendar day, and the
// moment returned is that day's start.
func TestInboxCoverUTCDayStartsTheUTCCalendarDay(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("east", 2*3600)
	now := time.Date(2026, 3, 4, 1, 30, 0, 0, zone) // 2026-03-03T23:30Z
	assert.Equal(t, time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC), utcDay(now))
}

// TestInboxCoverOrPlaceholderKeepsAValueOrNamesTheMissingOne pins orPlaceholder on both
// branches: a value this run has is kept as written, a missing one is its angle-bracket
// placeholder and never an invented default.
func TestInboxCoverOrPlaceholderKeepsAValueOrNamesTheMissingOne(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "origin/main", orPlaceholder("origin/main", "<remote>"))
	assert.Equal(t, "<remote>", orPlaceholder("", "<remote>"))
	assert.Equal(t, "<you>", orPlaceholder("", "<you>"))
}

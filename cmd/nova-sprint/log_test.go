package main

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// log prints every line of the epoch in local time, filtered by card,
// member, stream and time; --json gives the lines; a clear keeps the old
// epoch's log readable with --at-epoch.
func TestLogPrintsTheEpochsLinesFiltered(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.loc = time.UTC
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2 --brief-file " + writeBrief(t, "handle the empty case"))
	ta.deal(2)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'the tests went red'")
	out := ta.ok("log --card s1-1")
	for _, want := range []string{
		"03:04:05  s1-1 added to s1 by coordinator (in a set of 2)", // a set move as the card's own line (log_card_test.go)
		" bytes, shown by nova-sprint card s1-1",                    // a brief is said, not printed (log_brief_test.go)
		"attempt 1 dealt to m1",
		"m1 took attempt 1",
		"m1 finished attempt 1: FAILED",
		"    report: the tests went red",
		"judgment: work came back failed",
	} {
		assert.Contains(t, out, want, "log --card s1-1 has no %q", want)
	}
	assert.NotContains(t, out, "s1-2.w1", "log --card s1-1 shows s1-2's work")
	out = ta.ok("log --member m1")
	assert.True(t, strings.Contains(out, "s1-2.w1: attempt 2") || strings.Contains(out, "attempt 1 dealt to m1"), "log --member m1:\n%s", out)
	ta.mu.Lock()
	ta.now = ta.now.Add(time.Hour)
	ta.mu.Unlock()
	assert.Contains(t, ta.ok("log --since 10m"), "LOG OK lines=0", "log --since 10m an hour later")
	var j struct {
		Lines []struct {
			Kind, Card string
			Cards      []string
		} `json:"lines"`
	}
	ta.json("log --card s1-2", &j)
	if assert.NotEmpty(t, j.Lines, "log --json: %+v", j) {
		assert.Equal(t, "move", j.Lines[0].Kind, "log --json: %+v", j)
		if assert.Len(t, j.Lines[0].Cards, 2, "log --json: %+v", j) {
			assert.Equal(t, "s1-2", j.Lines[0].Cards[1], "log --json: %+v", j)
		}
	}
	ta.ok("clear --confirm sprint")
	assert.Contains(t, ta.ok("log --at-epoch 0 --card s1-1"), "m1 finished attempt 1: FAILED", "the old epoch's log after a clear")
	assert.Contains(t, ta.ok("log --card s1-1"), "LOG OK lines=0", "the new epoch's log of s1-1")
}

// card tells the card's story: a card in flight says first what holds it
// and the commands that answer it; the timeline goes an attempt at a time,
// one line per event with the worker's and the readers' words; a landed
// card ends with one line.
func TestCardTellsTheStory(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.loc = time.UTC
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "handle the empty case, tier: pro")) // pro: two readers
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --failed --report 'the tests went red'")
	out := ta.ok("card s1-1")
	now := strings.Index(out, "now:")
	brief := strings.Index(out, "brief:")
	require.GreaterOrEqual(t, now, 0, "a card in flight, what holds it first:\n%s", out)
	require.GreaterOrEqual(t, brief, 0, "a card in flight, what holds it first:\n%s", out)
	require.LessOrEqual(t, now, brief, "a card in flight, what holds it first:\n%s", out)
	require.Contains(t, out, "waits on your judgment: work came back failed", "a card in flight, what holds it first")
	require.Contains(t, out, "rework with a fix: nova-sprint rework", "a card in flight, what holds it first")
	ta.ok("rework s1-1")
	ta.deal(1)
	ta.ok("take --as m1 s1-1.w2@1")
	ta.ok("finish --as m1 s1-1.w2@1 --head h2 --report 'handled; tests green'")
	ta.ok("ask") // the pair: a card's reads are asked together
	ta.ok("read --as reader-a --ok s1-1.r2.reader-a --finding 'the empty case is tested'")
	ta.ok("read --as reader-b --ok s1-1.r2.reader-b --finding 'fine'")
	ta.ok("accept s1-1")
	ta.ok("merge --stream s1")
	out = ta.ok("card s1-1")
	for _, want := range []string{
		"s1-1   stream s1   landed   attempt 2   head h2",
		"  attempt 1\n",
		`m1 finished attempt 1: FAILED. "the tests went red"`,
		"  attempt 2, because attempt 1 failed\n",
		`s1-1 reworked by coordinator: attempt 2; answers "work came back failed"`,
		`m1 finished attempt 2: ok, head h2. "handled; tests green"`,
		"reader-a and reader-b asked to read attempt 2 by coordinator", // reads asked together: one ask
		`reader-a read attempt 2: ok. "the empty case is tested"`,
		"merged into s1 and landed by coordinator",
		"attempt 1, report by m1 (failed):\n    the tests went red",
		"now:\n  landed; nothing waits",
	} {
		assert.Contains(t, out, want, "the story has no %q", want)
	}
	for _, not := range []string{"work came back ok", "the fix this attempt was given", "score"} {
		assert.NotContains(t, out, not, "the story of a landed card says %q", not)
	}
}

// The worker's packet: take hands the brief, this attempt's fix, the notes,
// the epoch and generation and the branch to work on (a rework starts from
// the attempt before's branch); queue --as shows the same; finish takes
// --branch and --base; a reader's packet carries the work it reads, its
// branch and the worker's report. No actor needs card to learn its task.
func TestTakeAndQueueHandTheirPackets(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "handle the empty case"))
	ta.deal(1)
	out := ta.ok("take --as m1 s1-1.w1@1")
	for _, want := range []string{"PACKET s1-1.w1 attempt=1 gen=1 epoch=0", "  branch: sprint/s1-1.w1.g1.e0", "  brief:\n    handle the empty case", "  notes: none",
		"  report it: nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --branch sprint/s1-1.w1.g1.e0"} {
		assert.Contains(t, out, want, "take has no %q", want)
	}
	ta.ok("finish --as m1 s1-1.w1@1 --failed --branch feature/empty --report 'the tests went red'")
	ta.ok("rework s1-1 --fix 'check the nil slice too'")
	ta.deal(1)
	var j struct {
		Packets []struct {
			Card, Branch, Base, Brief, Fix string
			Gen                            int
		} `json:"packets"`
	}
	ta.json("take --as m1 s1-1.w2@1", &j)
	require.Len(t, j.Packets, 1, "take --json packets: %+v", j)
	require.Equal(t, "check the nil slice too", j.Packets[0].Fix, "take --json packets: %+v", j)
	require.Equal(t, "feature/empty", j.Packets[0].Base, "take --json packets: %+v", j)
	require.True(t, strings.HasPrefix(j.Packets[0].Brief, "handle the empty case"), "take --json packets: %+v", j)
	out = ta.ok("queue --as m1")
	require.Contains(t, out, "  fix (this attempt):\n    check the nil slice too", "queue --as m1")
	require.Contains(t, out, "  base: feature/empty", "queue --as m1")
	// the packet says why the attempt exists, and card tells it per attempt
	assert.Contains(t, ta.ok("queue --as m1"), "  why this attempt exists:\n    attempt 1 failed: the tests went red")
	assert.Equal(t, 1, strings.Count(ta.ok("card s1-1"), "check the nil slice too"), "card says the fix once")
	assert.Contains(t, ta.ok("card s1-1"), "attempt 2 was given:\n  because:\n    attempt 1 failed: the tests went red\n  the fix:\n    check the nil slice too")
	ta.ok("finish --as m1 s1-1.w2@1 --head h2 --branch feature/empty-2 --base feature/empty --report 'handled; tests green'")
	ta.ok("ask")
	out = ta.ok("queue --as reader-a")
	for _, want := range []string{"PACKET s1-1.r2.reader-a attempt=2", "  work: attempt 2 by m1", "  head: h2", "  branch: feature/empty-2", "  base: feature/empty",
		"  report:\n    handled; tests green", "  report it: nova-sprint read --as reader-a (--ok | --broken) s1-1.r2.reader-a --epoch 0"} {
		assert.Contains(t, out, want, "the reader's queue has no %q", want)
	}
}

// where does not show the merge table's since column: the machine keeps
// the cell, the view hides it, on a store made before the rule too. The
// table shows once a stream has a card in it (whereFixture has one queued).
func TestWhereHidesTheMergeTablesSince(t *testing.T) {
	t.Parallel()
	ta := whereFixture(t)
	out := ta.ok("where --all")
	block := tableOf(out, "merge")
	require.NotEmpty(t, block, "no merge table:\n%s", out)
	require.NotContains(t, block, "since", "where shows since:\n%s", out)
}

// log --json --since takes a window wider than about 22 h in every shape a
// caller types: a Go duration, a whole number of days and a date or an
// instant, each wider than a page, and returns every entry at or after the
// window's time. The window is the instant it names, compared in UTC; the
// clock is the twin store's, injected, so the test reads no real time.
func TestLogJsonSinceWithAWindowWiderThan22HoursReturnsEveryEntry(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.loc = time.UTC
	ta.ok("init --readers reader-a --members m1")
	ta.mu.Lock()
	t0 := ta.now
	ta.mu.Unlock()
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "early card"))
	ta.mu.Lock()
	ta.now = ta.now.Add(25 * time.Hour) // s1-1 is now 25 h old: wider than 22 h
	ta.mu.Unlock()
	ta.ok("add --stream s1 --count 1 --one --after s1-1 --brief-file " + writeBrief(t, "later card"))

	// cards is the card ids the verb returned for a --since window.
	cards := func(line string) map[string]bool {
		t.Helper()
		var j struct {
			Lines []sprint.Line `json:"lines"`
		}
		ta.json(line, &j)
		got := map[string]bool{}
		for _, l := range j.Lines {
			if l.Card != "" {
				got[l.Card] = true
			}
		}
		return got
	}
	// 26 h back is wider than 22 h: both entries are in the window.
	wide := cards("log --since 26h")
	assert.True(t, wide["s1-1"], "26h: s1-1, 25 h old, is in a 26 h window")
	assert.True(t, wide["s1-2"], "26h: s1-2 is in a 26 h window")
	// A whole number of days: the unit time.ParseDuration has none for.
	day := cards("log --since 1d")
	assert.False(t, day["s1-1"], "1d: s1-1, 25 h old, is before a 24 h window")
	assert.True(t, day["s1-2"], "1d: s1-2 is in a 24 h window")
	twoDays := cards("log --since 2d")
	assert.True(t, twoDays["s1-1"], "2d: s1-1 is in a 48 h window")
	assert.True(t, twoDays["s1-2"], "2d: s1-2 is in a 48 h window")
	// A date, in the run's own zone.
	date := cards("log --since " + t0.Format(time.DateOnly))
	assert.True(t, date["s1-1"], "the date: s1-1 is at or after its midnight")
	assert.True(t, date["s1-2"], "the date: s1-2 is at or after its midnight")
	// A date and time without seconds, the shape a person pastes.
	space := cards("log --since '" + t0.Format("2006-01-02 15:04Z07:00") + "'")
	assert.True(t, space["s1-1"], "the date and time without seconds: s1-1 is in the window")
	assert.True(t, space["s1-2"], "the date and time without seconds: s1-2 is in the window")
	// An instant in RFC 3339, the form the child cut its window to.
	instant := cards("log --since " + t0.Format(time.RFC3339))
	assert.True(t, instant["s1-1"], "the instant: s1-1 is at or after it")
	assert.True(t, instant["s1-2"], "the instant: s1-2 is at or after it")
}

// A full RFC 3339 cutoff names its year, and year zero is a year the parser
// holds: it must not be read as a year left out and moved to the run's
// current one. The entry here is written in the last hour of 2029, the run's
// clock moves into 2030, and a cutoff of 0000-01-01T00:00:00Z still admits it;
// reading year zero as absent moved the cutoff to 2030-01-01 and hid every
// historical entry, the same zero-results symptom a wide window had.
func TestLogSinceKeepsAnExplicitYearZeroCutoff(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.a.loc = time.UTC
	ta.mu.Lock()
	ta.now = time.Date(2029, 12, 31, 23, 0, 0, 0, time.UTC)
	ta.mu.Unlock()
	ta.ok("init --readers reader-a --members m1")
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "a historical card"))
	ta.mu.Lock()
	ta.now = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) // the run's clock moves into the next year
	ta.mu.Unlock()
	var j struct {
		Lines []sprint.Line `json:"lines"`
	}
	ta.json("log --card s1-1 --since 0000-01-01T00:00:00Z", &j)
	require.NotEmpty(t, j.Lines, "a cutoff that names year zero must keep an entry written in 2029")
}

// parseSince names the instant a value gives: a value that names its year is
// kept exactly, the valid year zero included; a shape that omits the year is
// completed with the run's current year in the run's zone before it is
// parsed, so a yearless 02-29 is refused in a year that has none instead of
// being normalized into March.
func TestParseSinceHoldsAnExplicitYearAndCompletesAYearlessDate(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 6, 1, 12, 0, 0, 0, time.UTC)
	zero, err := parseSince("0000-01-01T00:00:00Z", now, time.UTC)
	require.NoError(t, err)
	assert.Equal(t, 0, zero.Year(), "a named year zero is a year, not a year left out")
	assert.Equal(t, time.January, zero.Month())
	assert.Equal(t, 1, zero.Day())
	day, err := parseSince("01-02", now, time.UTC)
	require.NoError(t, err)
	assert.Equal(t, 2030, day.Year(), "a yearless date is read in the run's current year")
	assert.Equal(t, time.January, day.Month())
	assert.Equal(t, 2, day.Day())
	leap, err := parseSince("02-29", time.Date(2028, 6, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	require.NoError(t, err, "2028 has a February 29")
	assert.Equal(t, 2028, leap.Year())
	assert.Equal(t, time.February, leap.Month())
	assert.Equal(t, 29, leap.Day())
	_, err = parseSince("02-29", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	assert.Error(t, err, "a yearless 02-29 is not a date in a year without one")
}

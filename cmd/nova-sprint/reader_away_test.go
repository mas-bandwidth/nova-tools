package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The readers' state (docs/SPEC-SPRINT.md section 6; tla/DirtyTick.tla, the
// readers update): a reader is up, away or down; the ask never asks one that
// is not up; a read asked of one that goes away is asked again; fewer than two
// up is one judgment per tick-end; reader remove takes a row that holds no
// read off the table.

// readerState is a reader's state as the store derives it, after the beat of
// every reader that beats (the view shows none).
func (ta *testApp) readerState(reader string) string {
	ta.t.Helper()
	ta.beat()
	st := &store.Store{B: ta.m, Names: sprint.Names{}, Now: ta.a.now}
	states, err := st.ReaderStates(context.Background(), []string{reader}, ta.a.now())
	require.NoError(ta.t, err)
	return states[reader]
}

// readers is the readers' row keys, as where shows them.
func (ta *testApp) readerRows() []string {
	ta.t.Helper()
	var v struct {
		Tables map[string]map[string]map[string]string
	}
	ta.json("where", &v)
	var out []string
	for r := range v.Tables[sprint.Readers] {
		out = append(out, r)
	}
	return out
}

// inReview is n pro primaries of stream s1 finished and in review, the machine
// STOPPED (the ask is the coordinator's by hand).
func (ta *testApp) inReview(n int) {
	ta.t.Helper()
	ta.ok(fmt.Sprintf("add --stream s1 --count %d --one --brief-file %s", n, proBriefFile(ta.t))) // pro: two readers each
	ta.deal(n)
	ta.ok(fmt.Sprintf("take --as m1 --limit %d", n))
	var words []string
	for i := 1; i <= n; i++ {
		words = append(words, fmt.Sprintf("s1-%d.w1@1", i))
	}
	ta.ok("finish --as m1 " + strings.Join(words, " "))
}

// askedOf is the read cards placed on a reader's row (a friend's or a member's), by id.
func (ta *testApp) askedOf(reader string) []string {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	s, err := st.Load(context.Background(), []string{sprint.Fleet}, nil)
	require.NoError(ta.t, err)
	var ids []string
	for _, row := range []string{sprint.FriendRow(reader), reader} {
		for _, c := range s.Fleet.Column(sprint.Ready, sprint.Working) {
			if c.Row == row && c.F("kind") == "read" {
				ids = append(ids, c.ID)
			}
		}
	}
	return ids
}

// A reader's own queue is its beat: with no beat it is down, a beat makes it
// up, and past the beat bound it is away.
func TestAReaderIsUpWhileItBeatsAndAwayWhenItLapses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.quiet = map[string]bool{"reader-c": true}
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	assert.Equal(t, "up", ta.readerState("reader-a"))
	assert.Equal(t, "down", ta.readerState("reader-c"), "a row no reader process has beaten for is down")
	ta.ok("queue --as reader-c")
	assert.Equal(t, "up", ta.readerState("reader-c"), "queue --as is the reader's beat")
	ta.a.sleep(sprint.ReaderBeatBound + time.Second)
	assert.Equal(t, "away", ta.readerState("reader-c"), "no beat past the bound is away")
	assert.Equal(t, "up", ta.readerState("reader-a"), "a reader that keeps asking for its queue stays up")
	ta.ok("queue --as reader-c")
	assert.Equal(t, "up", ta.readerState("reader-c"))
}

// reader away holds a reader away whatever it beats; reader up releases it.
func TestReaderAwayAndUp(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	out := ta.ok("reader away reader-c")
	assert.Contains(t, out, "READER-AWAY OK readers=reader-c")
	ta.ok("queue --as reader-c") // it still beats
	assert.Equal(t, "held", ta.readerState("reader-c"), "reader away is hold --return in the old words: its state reads held")
	assert.Equal(t, "up", ta.readerState("reader-a"))
	out = ta.ok("reader up reader-c")
	assert.Contains(t, out, "READER-UP OK readers=reader-c")
	assert.Equal(t, "up", ta.readerState("reader-c"))
}

// A reader with no row is refused, exit 1, and nothing is written; no name is
// refused at the arguments.
func TestReaderAwayRefusesAnUnknownReader(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, out, errs := ta.do("reader away reader-a reader-z")
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, errs, "no reader reader-z")
	assert.Equal(t, "up", ta.readerState("reader-a"), "reader-a was named with an unknown reader: nothing was written")
	code, _, errs = ta.do("reader up")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants at least one reader")
	code, _, errs = ta.do("reader up bad.name")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "a reader name wants letters, digits, _ and -: bad.name")
}

// reader remove takes the rows off the readers table, in one write.
func TestReaderRemove(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.ok("reader away reader-c")
	out := ta.ok("reader remove reader-c")
	assert.Contains(t, out, "READER-REMOVE OK readers=reader-c")
	assert.ElementsMatch(t, []string{"reader-a", "reader-b"}, ta.readerRows())
	out = ta.ok("reader remove reader-a reader-b")
	assert.Contains(t, out, "READER-REMOVE OK readers=reader-a,reader-b")
	assert.Empty(t, ta.readerRows())
	ta.ok("reader add reader-c")
	assert.Equal(t, "up", ta.readerState("reader-c"), "a reader added again starts clean: its hold went with its row")
}

func TestReaderRemoveRefusesAnUnknownReader(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, out, errs := ta.do("reader remove reader-a reader-z")
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, errs, "no reader reader-z")
	assert.ElementsMatch(t, []string{"reader-a", "reader-b"}, ta.readerRows(), "nothing was written")
}

func TestReaderRemoveWantsANameAndAValidName(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, _, errs := ta.do("reader remove")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants at least one reader")
	code, _, errs = ta.do("reader remove 'bad name'")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "a reader name wants letters, digits, _ and -: bad name")
	assert.Len(t, ta.readerRows(), 2)
}

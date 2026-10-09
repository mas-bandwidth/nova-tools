package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The verb priority (docs/SPEC-SPRINT.md section 1, "Priority"; the owner, 2026-10-06): it sets
// a card's level, or every card of a stream in one call and the stream's default, each change on
// the card's timeline with the actor and the reason; it prints a card's level; card, queue and
// where show it.

// cardPriority is the card's level and its source as card --json says them.
func (ta *testApp) cardPriority(id string) (string, string) {
	ta.t.Helper()
	var v struct {
		Priority       string `json:"priority"`
		PrioritySource string `json:"priority_source"`
	}
	ta.json("card "+id, &v)
	return v.Priority, v.PrioritySource
}

func TestPrioritySetsACardAndItsStream(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 3")
	l, src := ta.cardPriority("s1-1")
	assert.Equal(t, [2]string{sprint.PriorityNormal, "default"}, [2]string{l, src}, "a card is normal by default")

	ta.ok("priority s1-1 --high --reason 'the release waits on it'")
	l, src = ta.cardPriority("s1-1")
	assert.Equal(t, [2]string{sprint.PriorityHigh, "set"}, [2]string{l, src})
	assert.Contains(t, ta.ok("priority s1-1"), "s1-1 priority=high source=set")
	assert.Contains(t, ta.ok("card s1-1"), "priority=high", "the CARD OK line carries it")
	logged := ta.ok("log --card s1-1")
	assert.Contains(t, logged, "s1-1 priority normal -> high: the release waits on it", "the change is on its timeline with the reason")
	assert.Contains(t, logged, "coordinator", "and the actor")

	// one call sets every card of the stream, whatever its column, and the stream's default
	ta.ok("hold s1 --reason 'the base is red'")
	held := ta.ok("priority --stream s1 --low --reason 'after the release'")
	assert.Contains(t, held, "stream s1 is held: its cards are set, and none is dealt until nova-sprint unhold s1", "a held stream is set and said so")
	assert.Contains(t, ta.ok("log --stream s1"), "stream s1 priority default normal -> low (the cards added later): after the release", "the default's change is on the stream's timeline")
	ta.ok("unhold s1 --reason 'the base is green'")
	ta.deal(1)
	for _, id := range []string{"s1-1", "s1-2", "s1-3"} {
		l, _ := ta.cardPriority(id)
		assert.Equal(t, sprint.PriorityLow, l, id)
	}
	ta.ok("tick") // the where record is the tick's
	var v whereView
	ta.json("where", &v)
	assert.ElementsMatch(t, []string{"s1-1", "s1-2", "s1-3"}, v.Priorities[sprint.PriorityLow])
	assert.Equal(t, map[string]string{"s1": sprint.PriorityLow}, v.StreamPriorities)
	assert.Equal(t, sprint.PriorityLow, v.Tables["work"]["s1"]["priority"], "the stream's default beside the stream")
	assert.Contains(t, ta.ok("where"), "priority: low s1-1, s1-2, s1-3; stream defaults s1 low")

	// a work card carries its primary's level to the worker's queue
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	require.NotEmpty(t, q.Cards)
	for _, c := range q.Cards {
		assert.NotEmpty(t, c.Priority, "%s carries a level", c.ID)
	}

	// one level at a time, and a level or a card to print
	code, _, errs := ta.do("priority s1-1 --high --low --reason r")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "one level at a time")
	// a reason is required to set, as drop's is
	code, _, errs = ta.do("priority s1-1 --high")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants --reason <text>")
}

func TestAStreamsDefaultSeedsNewCards(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("priority --stream s1 --low --reason 'a background stream'")
	ta.ok("add --stream s1 --count 1 --one")
	l, src := ta.cardPriority("s1-2")
	assert.Equal(t, [2]string{sprint.PriorityLow, "set"}, [2]string{l, src}, "a card added later takes its stream's default")

	// its own PRIORITY line wins over its stream's
	ta.ok("add --stream s1 --count 1 --one --brief-file " + writeBrief(t, "an urgent card\nPRIORITY: high"))
	l, _ = ta.cardPriority("s1-3")
	assert.Equal(t, sprint.PriorityHigh, l)

	// and a stream set back to normal seeds nothing
	ta.ok("priority --stream s1 --normal --reason back")
	ta.ok("add --stream s1 --count 1 --one")
	l, src = ta.cardPriority("s1-4")
	assert.Equal(t, [2]string{sprint.PriorityNormal, "default"}, [2]string{l, src}, "a stream back to normal seeds nothing")
	l, _ = ta.cardPriority("s1-3")
	assert.Equal(t, sprint.PriorityNormal, l, "the call set every card of the stream, its own line's too")
	var v whereView
	ta.json("where", &v)
	assert.Empty(t, v.StreamPriorities["s1"], "no default but normal")

	// a brief line seeds a card in a stream with no default at all
	ta.ok("add --stream s2 --count 1 --one --brief-file " + writeBrief(t, "the block\nPRIORITY: blocker"))
	l, _ = ta.cardPriority("s2-1")
	assert.Equal(t, sprint.PriorityBlocker, l)
}

func TestPriorityRefusesAReadCard(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 1 --one")
	code, out, errs := ta.do("priority s1-1.r1.reader-a --high --reason r")
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, out+errs, "a read card always has reader priority; change its producer urgency on its primary: nova-sprint priority s1-1 --high")
	l, _ := ta.cardPriority("s1-1")
	assert.Equal(t, sprint.PriorityNormal, l, "nothing is written")
	assert.Contains(t, ta.ok("priority s1-1.r1.reader-a"), "priority=reader source=read", "a normal primary's read prints reader")
	ta.ok("priority s1-1 --high --reason urgent")
	assert.Contains(t, ta.ok("priority s1-1.r1.reader-a"), "priority=reader source=read", "producer urgency never changes the read role")
	code, out, errs = ta.do("priority s1-1.w1 --high --reason r")
	assert.Equal(t, 1, code)
	assert.True(t, strings.Contains(out+errs, "set its primary: nova-sprint priority s1-1 --high"), "%s%s", out, errs)
}

// readersReadPro names flash and pro on every reader row, as the seat's reader set does for
// a row that reads pro: a fleet row that names no tier reads flash alone while the store
// holds routes (sprint fleetReadsFlashOnly).
func (ta *testApp) readersReadPro() {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	rows, err := st.ReaderRows(context.Background())
	require.NoError(ta.t, err)
	if len(rows) > 0 {
		ta.ok("reader set " + strings.Join(rows, " ") + " --tiers flash,pro")
	}
}

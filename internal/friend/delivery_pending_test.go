package friend

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rules of docs/SPEC-FRIEND.md, "The loop": a message never waits behind
// every older one. When the session is free the daemon delivers every pending
// message as ONE envelope, oldest first, each with its id, from, time and age;
// the envelope is acked at exit 0 and not at all on a failure.

// advance moves both the daemon's clock and the store's by d, as a long turn does.
func (r *rig) advance(d time.Duration) {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
	r.store.Advance(d)
}

func envelopeLine(m bus.Message) *regexp.Regexp {
	return regexp.MustCompile(`\[\d+/\d+\] ` + regexp.QuoteMeta(m.ID) + ` from=` + m.From + ` at=\S+ age=\d+m subject=` + regexp.QuoteMeta(m.Subject) + `\n`)
}

// Three messages queued during a 6-minute turn arrive as one next turn, oldest
// first, every one acked at exit 0.
func TestATurnEndDeliversEveryPendingMessageAsOneTurnOldestFirst(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.hold, r.releaseAt = make(chan struct{}), 30
	r.send(t, "ada", "long", "a long task")
	var m [3]bus.Message
	for i, step := range []int{3, 5, 7} {
		r.at[step] = func() { m[i] = r.send(t, "ada", fmt.Sprintf("later-%d", i+1), fmt.Sprintf("body %d", i+1)) }
	}
	r.at[9] = func() { r.advance(6 * time.Minute) }
	r.run(t, 40)
	require.Len(t, r.delivered, 2, "the long task, then one turn for the three: %v", r.delivered)
	text := r.delivered[1]
	last := -1
	for i := range m {
		loc := envelopeLine(m[i]).FindStringIndex(text)
		require.NotNil(t, loc, "message %d has its envelope line: %q", i+1, text)
		assert.Contains(t, text[loc[0]:], fmt.Sprintf("[%d/3] ", i+1))
		assert.Greater(t, loc[0], last, "oldest first")
		last = loc[0]
		assert.Contains(t, text, fmt.Sprintf("body %d", i+1))
	}
	assert.Regexp(t, `age=[56]m `, text, "the age is the wait in minutes")
	pending, fresh := r.pending(t)
	assert.Empty(t, pending, "all acked at exit 0")
	assert.Empty(t, fresh)
}

// A turn that fails acks nothing of its envelope.
func TestAFailedEnvelopeAcksNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.exit = 1
	for i := 1; i <= 3; i++ {
		r.send(t, "ada", fmt.Sprintf("s%d", i), "x")
	}
	r.run(t, 3)
	require.NotEmpty(t, r.delivered)
	assert.Contains(t, r.delivered[0], "[3/3] ")
	pending, _ := r.pending(t)
	assert.Len(t, pending, 3, "exit 1 acks none of the three")
	assert.Equal(t, 0, r.last().Delivered)
}

// The daemon's own notices of which a newer one exists are dropped, and the
// dropped one is acked with its successor named.
func TestASupersededNoticeIsDroppedAndAckedWithItsSuccessor(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	old := r.send(t, "bob", "coordinator silent", "coordinator silent since 12:37")
	work := r.send(t, "ada", "work", "do it")
	newer := r.send(t, "bob", "coordinator back", "coordinator back")
	r.run(t, 4)
	require.Len(t, r.delivered, 1)
	assert.NotContains(t, r.delivered[0], old.ID, "the stale notice is not delivered")
	assert.Contains(t, r.delivered[0], work.ID)
	assert.Contains(t, r.delivered[0], newer.ID)
	require.NotEmpty(t, r.records)
	assert.Contains(t, strings.Join(r.records, "\n"), "superseded="+newer.ID)
	assert.Contains(t, strings.Join(r.records, "\n"), old.ID)
	pending, _ := r.pending(t)
	assert.Empty(t, pending)
}

// A ping the daemon has answered is acked and never a turn.
func TestAnAnsweredPingIsNeverATurn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	r.run(t, 4)
	assert.Empty(t, r.delivered)
	assert.Equal(t, []string{"daemon-pong: daemon-pong n1"}, r.adaGot(t))
	pending, fresh := r.pending(t)
	assert.Empty(t, pending, "acked once answered")
	assert.Empty(t, fresh)
}

// Any bus line the session sends ends an open challenge, not only a pong.
func TestAnyBusLineFromTheSessionIsItsProofOfLife(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	var spoke time.Time
	r.d.Spoke = func(since time.Time) (time.Time, bool, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		return spoke, !spoke.IsZero() && !spoke.Before(since), nil
	}
	var challenged Status
	r.at[3] = func() {
		challenged = r.last()
		r.mu.Lock()
		spoke = r.now
		r.mu.Unlock()
	}
	r.run(t, 6)
	assert.Equal(t, Challenged, challenged.Challenge)
	assert.Equal(t, Quiet, r.last().Challenge, "a line from the session answered the challenge")
	assert.Empty(t, r.delivered)
}

// What does not fit the text limit is named by id and stays pending.
func TestTheEnvelopeNamesWhatDidNotFit(t *testing.T) {
	t.Parallel()
	msgs := []bus.Message{
		{ID: "a-1", From: "ada", At: t0, Subject: "one", Body: strings.Repeat("x", 40)},
		{ID: "a-2", From: "ada", At: t0, Subject: "two", Body: strings.Repeat("y", 40)},
		{ID: "a-3", From: "ada", At: t0, Subject: "three", Body: "z"},
	}
	text, carried := Envelope(msgs, t0.Add(3*time.Minute), "bob", 300)
	require.Less(t, carried, 3)
	require.GreaterOrEqual(t, carried, 1)
	assert.LessOrEqual(t, len(text), 300)
	assert.Contains(t, text, "[1/")
	assert.Contains(t, text, "age=3m")
	assert.Contains(t, text, fmt.Sprintf("and %d more", 3-carried))
	assert.Contains(t, text, "a-3")
	assert.Contains(t, text, "nova-bus recv --as bob --all")
	all, n := Envelope(msgs, t0, "bob", 1<<20)
	assert.Equal(t, 3, n)
	assert.NotContains(t, all, "more")
}

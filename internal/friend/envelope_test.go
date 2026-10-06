package friend

import (
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The envelope and the supersede rule, docs/SPEC-FRIEND.md, the loop: a message does
// not wait behind every older one, a stale notice is never a turn, a ping is the
// daemon's, and the session's own lines are its proof of life. The rig's clock and
// the store's fake are the only time and the only store.

func envelopeLine(i, n int, m bus.Message) string {
	return "[" + string(rune('0'+i)) + "/" + string(rune('0'+n)) + "] " + m.ID + " from=" + m.From + " at="
}

func TestATurnEndDeliversEveryPendingMessageAsOneTurnOldestFirst(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.hold, r.releaseAt = make(chan struct{}), 360 // a six-minute turn
	r.send(t, "ada", "long", "a long task")
	var queued []bus.Message
	for i, step := range []int{3, 5, 7} {
		r.at[step] = func() {
			queued = append(queued, r.send(t, "ada", "q"+string(rune('a'+i)), "body "+string(rune('a'+i))))
		}
	}
	r.run(t, 370)
	require.Len(t, r.delivered, 2, "the long turn, then one turn for the three: %v", r.delivered)
	require.Len(t, queued, 3)
	text, last := r.delivered[1], -1
	for i, m := range queued {
		at := strings.Index(text, envelopeLine(i+1, 3, m))
		assert.Greater(t, at, last, "message %d after the one before it: %q", i+1, text)
		last = at
		assert.Contains(t, text, " subject="+m.Subject+"\n"+m.Body+"\n")
	}
	assert.Regexp(t, `age=\d+m subject=qa\n`, text)
	pending, fresh := r.pending(t)
	assert.Empty(t, pending, "all acked at exit 0")
	assert.Empty(t, fresh)
	assert.Equal(t, 4, r.last().Delivered)
}

func TestAFailedEnvelopeAcksNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.exit = 3
	for _, s := range []string{"one", "two", "three"} {
		r.send(t, "ada", s, "x")
	}
	r.run(t, 4)
	require.NotEmpty(t, r.delivered)
	assert.Contains(t, r.delivered[0], "[3/3] ")
	pending, _ := r.pending(t)
	assert.Len(t, pending, 3, "none acked when the turn fails")
	assert.Equal(t, 0, r.last().Delivered)
}

func TestASupersededNoticeIsDroppedAndAckedWithItsSuccessor(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	old, err := r.bus.Send(t.Context(), bus.Message{From: "bob", To: []string{"bob"}, Subject: "coordinator silent", Body: "silent since 12:37\n"})
	require.NoError(t, err)
	r.send(t, "ada", "work", "do it")
	newer, err := r.bus.Send(t.Context(), bus.Message{From: "bob", To: []string{"bob"}, Subject: "coordinator silent", Body: "silent since 12:50\n"})
	require.NoError(t, err)
	r.run(t, 4)
	require.Len(t, r.delivered, 1)
	assert.NotContains(t, r.delivered[0], old.ID, "the stale notice is not delivered")
	assert.Contains(t, r.delivered[0], newer.ID)
	assert.Contains(t, strings.Join(r.records, "\n"), "superseded="+newer.ID+" acked=true")
	pending, fresh := r.pending(t)
	assert.Empty(t, pending, "dropped and delivered are both acked")
	assert.Empty(t, fresh)
}

func TestAnAnsweredPingIsNeverATurn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
	r.run(t, 4)
	assert.Empty(t, r.delivered, "a ping spends no turn")
	assert.Equal(t, []string{"daemon-pong: daemon-pong n1"}, r.adaGot(t))
	pending, fresh := r.pending(t)
	assert.Empty(t, pending, "acked once answered")
	assert.Empty(t, fresh)
}

func TestAnyBusLineFromTheSessionIsItsProofOfLife(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		line    bool
		want    string
		wantNum int
	}{{"no line", false, Challenged, 0}, {"a real message from the session", true, Quiet, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			r.at[2] = func() { r.send(t, "ada", "PING n1", PingText("ada", t0, "n1")) }
			if tc.line {
				r.at[6] = func() {
					_, err := r.bus.Send(t.Context(), bus.Message{From: "bob", To: []string{"ada"}, Subject: "status", Body: "working on it\n"})
					require.NoError(t, err)
				}
			}
			r.run(t, 12)
			assert.Equal(t, tc.want, r.last().Challenge, "the daemon's own pong is no proof")
			assert.Equal(t, tc.wantNum, r.last().Pongs)
			assert.Empty(t, r.delivered)
		})
	}
}

func TestTheEnvelopeNamesWhatDidNotFit(t *testing.T) {
	t.Parallel()
	now := t0.Add(10 * time.Minute)
	var msgs []bus.Message
	for _, id := range []string{"m1", "m2", "m3", "m4"} {
		msgs = append(msgs, bus.Message{ID: id, From: "ada", Subject: "s" + id, At: t0, Body: strings.Repeat("x", 100)})
	}
	text, shown := Envelope(msgs, now, "bob", 500, "", "")
	assert.Equal(t, 2, shown)
	assert.LessOrEqual(t, len(text)-len("\nand 2 more (m3, m4): nova-bus recv --as bob --all\n"), 500)
	assert.Contains(t, text, "[1/4] m1 from=ada at="+t0.Format(time.RFC3339)+" age=10m subject=sm1\n")
	assert.Contains(t, text, "\nand 2 more (m3, m4): nova-bus recv --as bob --all\n")
	assert.NotContains(t, text, "[3/4]")
	full, all := Envelope(msgs, now, "bob", 0, "", "")
	assert.Equal(t, 4, all)
	assert.NotContains(t, full, " more ")
	_, one := Envelope(msgs, now, "bob", 1, "", "")
	assert.Equal(t, 1, one, "at least one message goes in, whatever the limit")
}

func TestSupersededNoticesKeepOnlyTheNewestOfTheDaemonsOwn(t *testing.T) {
	t.Parallel()
	msgs := []bus.Message{
		{ID: "1", From: "bob", Subject: "coordinator silent"},
		{ID: "2", From: "ada", Subject: "coordinator silent"},
		{ID: "3", From: "bob", Subject: "coordinator back"},
		{ID: "4", From: "bob", Subject: "other"},
		{ID: "5", From: "bob", Subject: "coordinator silent"},
	}
	assert.Equal(t, map[string]string{"1": "5", "3": "5"}, SupersededNotices(msgs, "bob"))
	assert.Empty(t, SupersededNotices(msgs[:1], "bob"))
}

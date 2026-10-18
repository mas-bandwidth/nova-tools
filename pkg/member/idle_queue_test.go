package member

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// THE IDLE LOAD. On 2026-10-17 nine idle benches sent the sprint server ~750 serial batches a
// minute: each ran a member and a reader, and each seat sent two verbs every 3 s pass (the
// member its fleet beat and its queue, the reader its beat's queue and its pass's queue).
// An idle member reads its queue every IdleQueueEvery instead (Tick, skipQueue); its beat
// keeps its own clock, so the server never marks it down. A card, an answer with a card or
// a Wake ends the backoff at once. A reader sends one queue an interval: its beat skips
// its own while the pass's ran within Config.Every (Beat). No test here reads a clock: the
// time is the tick's argument and Config.Now.

// idleRig is a member or a reader on a 3 s loop whose queue answers no card, on a clock
// the test moves.
func idleRig(reader bool) (g *rig, at func(sec int64)) {
	now := time.Unix(1000, 0)
	as := "m"
	if reader {
		as = "r"
	}
	g = newWidthRig(1, Config{As: as, Reader: reader, Every: 3 * time.Second, Now: func() time.Time { return now }})
	return g, func(sec int64) { now = time.Unix(sec, 0) }
}

func (g *rig) queues() int { return len(g.s.lines("queue")) }

// TestAnIdleMemberReadsItsQueueOnceAnIdleQueueEvery: a member whose last queue listed no card,
// with nothing running, sends no queue on the passes inside IdleQueueEvery, and one once it has
// passed; its beat goes on every interval meanwhile.
func TestAnIdleMemberReadsItsQueueOnceAnIdleQueueEvery(t *testing.T) {
	t.Parallel()
	g, at := idleRig(false)
	g.s.set("queue", 0, queueJSON(t, 7))
	for _, sec := range []int64{1000, 1003, 1006, 1009, 1012} {
		at(sec)
		_, err := g.m.Tick(time.Unix(sec, 0))
		require.NoError(t, err)
		require.NoError(t, g.m.Beat())
	}
	require.Equal(t, 1, g.queues(), "one queue in the first IdleQueueEvery: the first pass's")
	require.Len(t, g.s.lines("beat"), 5, "the beat keeps its own clock: one every interval")
	at(1015)
	_, err := g.m.Tick(time.Unix(1015, 0))
	require.NoError(t, err)
	require.Equal(t, 2, g.queues(), "IdleQueueEvery passed: the queue is read again")
}

// TestAWokenIdleMemberReadsItsQueueAtOnce: the Wake (a push ended, a child exited) ends the
// backoff: the next pass reads its queue whatever the idle clock says.
func TestAWokenIdleMemberReadsItsQueueAtOnce(t *testing.T) {
	t.Parallel()
	g, at := idleRig(false)
	g.s.set("queue", 0, queueJSON(t, 7))
	_, err := g.m.Tick(time.Unix(1000, 0))
	require.NoError(t, err)
	g.m.woken()
	at(1003)
	_, err = g.m.Tick(time.Unix(1003, 0))
	require.NoError(t, err)
	require.Equal(t, 2, g.queues(), "woken: the pass reads its queue at once")
	at(1006)
	_, err = g.m.Tick(time.Unix(1006, 0))
	require.NoError(t, err)
	require.Equal(t, 2, g.queues(), "the word is spent: idle again, the next pass waits the clock out")
}

// TestAnAnswerWithACardEndsTheBackoff: a queue that lists a card, taken or not, is not idle:
// the next pass reads its queue at the interval.
func TestAnAnswerWithACardEndsTheBackoff(t *testing.T) {
	t.Parallel()
	g, at := idleRig(false)
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	g.s.set("take", 0, takeJSON(t)) // dealt to another, or held: nothing handed
	_, err := g.m.Tick(time.Unix(1000, 0))
	require.NoError(t, err)
	at(1003)
	_, err = g.m.Tick(time.Unix(1003, 0))
	require.NoError(t, err)
	require.Equal(t, 2, g.queues(), "a card in the answer: the queue is read every pass")
}

// TestAWorkingMemberReadsItsQueueEveryPass: a member with a child running is unchanged: the
// queue is read every pass, as it was.
func TestAWorkingMemberReadsItsQueueEveryPass(t *testing.T) {
	t.Parallel()
	g, at := idleRig(false)
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	p := pk("c1")
	g.s.set("take", 0, takeJSON(t, p))
	_, err := g.m.Tick(time.Unix(1000, 0))
	require.NoError(t, err)
	require.Equal(t, 1, g.m.Running())
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	for _, sec := range []int64{1003, 1006, 1009} {
		at(sec)
		_, err = g.m.Tick(time.Unix(sec, 0))
		require.NoError(t, err)
	}
	require.Equal(t, 4, g.queues(), "a working member reads its queue every pass")
}

// TestAnIdleMemberStillAsksRoom: the skipped pass still asks Room, so the word the beat
// carries (--no-room) is this tick's and not the last queue's.
func TestAnIdleMemberStillAsksRoom(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	room := true
	g := newWidthRig(1, Config{As: "m", Every: 3 * time.Second, Now: func() time.Time { return now }, Room: func() (bool, string) { return room, "the disk is under its floor" }})
	g.s.set("queue", 0, queueJSON(t, 7))
	_, err := g.m.Tick(now)
	require.NoError(t, err)
	room = false
	now = time.Unix(1003, 0)
	_, err = g.m.Tick(now)
	require.NoError(t, err)
	require.Equal(t, 1, g.queues(), "the pass was skipped")
	require.NoError(t, g.m.Beat())
	require.Contains(t, g.s.lines("beat")[0], "--no-room", "the beat carries this tick's word")
}

// TestAMemberTickedByHandBacksOffNothing: with no loop interval (Config.Every 0, the tests'
// member) every pass reads its queue, idle or not.
func TestAMemberTickedByHandBacksOffNothing(t *testing.T) {
	t.Parallel()
	g := newWidthRig(1, Config{As: "m"})
	g.s.set("queue", 0, queueJSON(t, 7))
	for range 3 {
		_, err := g.tick(t)
		require.NoError(t, err)
	}
	require.Equal(t, 3, g.queues())
}

// TestAReaderSendsOneQueueAnInterval: a reader's beat is its queue, and so is its pass's: the
// beat sends none of its own while the pass's ran within the interval, and one once it is
// older, so the reader's read leases are renewed and it stays up on one queue an interval.
func TestAReaderSendsOneQueueAnInterval(t *testing.T) {
	t.Parallel()
	g, at := idleRig(true)
	g.s.set("queue", 0, queueJSON(t, 7))
	_, err := g.m.Tick(time.Unix(1000, 0))
	require.NoError(t, err)
	at(1001)
	require.NoError(t, g.m.Beat())
	require.Equal(t, 1, g.queues(), "the pass's queue ran within the interval: the beat sends none")
	at(1003)
	require.NoError(t, g.m.Beat())
	require.Equal(t, 2, g.queues(), "an interval later the beat's queue goes")
	require.Contains(t, g.s.lines("queue")[1], "--packets 0", "the beat's queue wants no packet")
	at(1006)
	require.NoError(t, g.m.Beat())
	require.Equal(t, 3, g.queues(), "the idle pass skips its queue: the beat alone keeps the reader up, one an interval")
}

// TestAReadersBeatWakesTheIdlePassWhenItSeesACard: the idle reader's pass reads its queue
// every IdleQueueEvery; a read asked meanwhile shows in the beat's answer, which wakes the
// pass, so it reads its queue and begins the read at the next pass, not the idle clock out.
func TestAReadersBeatWakesTheIdlePassWhenItSeesACard(t *testing.T) {
	t.Parallel()
	g, at := idleRig(true)
	g.s.set("queue", 0, queueJSON(t, 7))
	_, err := g.m.Tick(time.Unix(1000, 0))
	require.NoError(t, err)
	p := Packet{Card: "r1", Kind: "read", As: "r", Attempt: 1, Epoch: 7, Head: "h1"}
	g.s.set("queue", 0, queueJSON(t, 7, asked("r1", &p)))
	at(1003)
	require.NoError(t, g.m.Beat())
	select {
	case <-g.m.Wake():
	default:
		t.Fatal("the beat's answer carried a card and the pass was not woken")
	}
	at(1004)
	_, err = g.m.Tick(time.Unix(1004, 0))
	require.NoError(t, err)
	require.Equal(t, 3, g.queues(), "woken, the pass reads its queue before IdleQueueEvery")
	require.Equal(t, []string{"r1"}, g.r.started(), "and begins the read")
}

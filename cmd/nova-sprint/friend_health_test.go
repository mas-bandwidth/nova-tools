package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus2"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The seat's generation and a friend's health through the command (docs/SPEC-SPRINT.md
// section 1, "A friend's health"): `seat` is the small read the daemons call every
// second; `friend health` is the coordinator's observation, fenced by the seat; the
// table shows up, held or down and nothing else, with the reason when there is one.

// seatView is what seat --json carries.
type seatJSON struct {
	Holder     string `json:"holder"`
	Epoch      uint64 `json:"epoch"`
	Generation uint64 `json:"generation"`
}

func (ta *testApp) seatState() seatJSON {
	ta.t.Helper()
	var s seatJSON
	ta.json("seat", &s)
	return s
}

// The seat read: holder, epoch and generation, from the first init with no seat
// record; a handover takes the next generation; a clear keeps it.
func TestSeatReadsHolderEpochAndGeneration(t *testing.T) {
	t.Parallel()
	ta := seatSprint(t)
	assert.Equal(t, "SEAT holder=coordinator epoch=0 generation=1\n", ta.ok("seat"))
	assert.Equal(t, seatJSON{Holder: "coordinator", Epoch: 0, Generation: 1}, ta.seatState())
	ta.ok("coordinator rowan --reason 'handing over'")
	assert.Equal(t, seatJSON{Holder: "rowan", Epoch: 0, Generation: 2}, ta.seatState())
	var h handoverView
	ta.json("handover", &h)
	assert.Equal(t, uint64(2), h.Seat.Generation, "handover carries it too")
	ta.ok("clear --confirm sprint --actor rowan")
	assert.Equal(t, seatJSON{Holder: "rowan", Epoch: 1, Generation: 2}, ta.seatState(), "a clear moves the epoch, never the seat's generation")
}

// friend health by the holder at the seat's generation writes the observation; the
// table shows her up at once and down ten seconds later; another actor and a stale
// generation are refused with nothing written; the same observation again replays.
func TestFriendHealthIsTheSeatsAndFencedByItsGeneration(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync --root " + t.TempDir())
	seen := ta.now.UTC().Format(time.RFC3339)
	out := ta.ok("friend health amy --state up --seen " + seen + " --generation 1")
	assert.Equal(t, "FRIEND-HEALTH OK amy state=up seen="+seen+" generation=1 status=up\n", out)
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "amy     |     0 |       0 |     8 |    0 | 0.0% |       0 | up")

	out = ta.ok("friend health amy --state up --seen " + seen + " --generation 1")
	assert.Contains(t, out, "replayed=true", "the same proof again is answered as recorded")

	code, _, errs := ta.do("friend health amy --state up --seen " + seen + " --generation 1 --actor stella")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "friend health is the coordinator's alone: coordinator, not stella")

	ta.ok("coordinator stella --reason 'handing over'")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| down", "an old seat's proof never looks up under a new seat")
	ta.a.sleep(time.Second)
	later := ta.now.UTC().Format(time.RFC3339)
	code, _, errs = ta.do("friend health amy --state up --seen " + later + " --generation 1 --actor stella")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "the seat is stella's at generation 2, and this observation names generation 1: read the seat again (nova-sprint seat); nothing was changed")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| down", "a refusal writes nothing")
	ta.ok("friend health amy --state up --seen " + later + " --generation 2 --actor stella")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| up")
	code, _, errs = ta.do("friend health amy --state up --seen " + ta.now.Add(time.Hour).UTC().Format(time.RFC3339) + " --generation 2 --actor stella")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "after the server's clock", "a proof dated after the server's clock is refused")

	ta.a.sleep(sprint.FriendObservedDownAfter + time.Second)
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| down", "ten seconds without a newer proof")
	ta.ok("friend beat amy")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| down", "her own beat never makes an observed friend up again")

	// usage: the words it wants
	code, _, errs = ta.do("friend health amy --state sleepy --seen " + later + " --generation 2 --actor stella")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--state wants one of up, asleep, down")
	code, _, errs = ta.do("friend health amy --state up --seen yesterday --generation 2 --actor stella")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--seen wants an RFC3339 time")
	code, _, errs = ta.do("friend health amy --state up --seen " + later + " --actor stella")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--generation wants the seat's generation")
}

// The table's words are up, held and down and nothing else: asleep is kept on the row
// for the daemon and shown as down; a reason and an until from the observation, or
// from friend down, are shown in the status cell and on the row; held is the
// coordinator's hold alone, lifted by friend up.
func TestTheFriendsTableShowsUpHeldOrDownWithTheReason(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync --root " + t.TempDir())
	seen := ta.now.UTC().Format(time.RFC3339)
	ta.ok("friend health amy --state asleep --seen " + seen + " --generation 1")
	frame := tableOf(ta.frame(), sprint.Friends)
	assert.Contains(t, frame, "| down")
	assert.NotContains(t, frame, "asleep")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, sprint.Down, w.Tables[sprint.Friends]["amy"]["status"])

	ta.a.sleep(time.Second)
	later := ta.now.UTC().Format(time.RFC3339)
	back := ta.now.Add(2 * time.Hour)
	ta.ok("friend health amy --state down --seen " + later + " --generation 1 --reason 'opus rate limited' --until " + back.UTC().Format(time.RFC3339))
	frame = tableOf(ta.frame(), sprint.Friends)
	assert.Contains(t, frame, "down (opus rate limited, until "+ta.a.clock12(back, ta.now)+")", frame)

	ta.ok("friend down amy --reason 'resting her' --until " + back.UTC().Format(time.RFC3339))
	frame = tableOf(ta.frame(), sprint.Friends)
	assert.Contains(t, frame, "held (resting her, until "+ta.a.clock12(back, ta.now)+")", frame)
	ta.json("where", &w)
	assert.True(t, strings.HasPrefix(w.Tables[sprint.Friends]["amy"]["status"], sprint.Held+" ("), "where --json carries the cell as printed")
	ta.ok("friend up amy")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| down", "the hold lifted, the observation (down) stands")
	ta.a.sleep(time.Second)
	ta.ok("friend health amy --state up --seen " + ta.now.UTC().Format(time.RFC3339) + " --generation 1")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| up")
}

// A friend-card delivery wakes the friend: one bus message from the coordinator to her
// per card delivered, naming the card and its inbox path; a send that fails never fails
// the delivery, is said on sync's line and written on the card's story.
func TestFriendSyncWakesTheFriendWithOneBusMessagePerDelivery(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	ta.mu.Lock()
	sent := append([]bus2.Message(nil), ta.sent...)
	ta.mu.Unlock()
	require.Len(t, sent, 1, "one message per delivery")
	m := sent[0]
	assert.Equal(t, "coordinator", m.From)
	assert.Equal(t, []string{"amy"}, m.To)
	assert.Equal(t, "card s1-1.w1 dealt: FRIEND-CARD DELIVERED friend=amy card=s1-1.w1 job=s1-1.w1 branch=sprint/s1-1.w1.g1.e0", m.Subject)
	assert.Contains(t, m.Body, filepath.Join(root, "amy-working", "inbox", "s1-1.w1", "BRIEF.md"))
	assert.Empty(t, m.Re)
	ta.ok("friend sync --root " + root)
	ta.mu.Lock()
	n := len(ta.sent)
	ta.mu.Unlock()
	assert.Equal(t, 1, n, "a sync that delivers nothing sends nothing")

	// the bus is down: the delivery stands, sync says so, and the card's story has it
	ta2, root2 := friendCardApp(t, "friend amy", "amy")
	ta2.a.bus = func(_ context.Context, _ bus2.Message) error { return errors.New("dial tcp: connection refused") }
	ta2.ok("tick")
	out := ta2.ok("friend sync --root " + root2)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-1.w1")
	assert.Contains(t, out, "FRIEND-CARD NOTE friend=amy card=s1-1.w1: the bus message to her was not sent (dial tcp: connection refused); the inbox file stands, tell her by hand")
	_, err := os.Stat(filepath.Join(root2, "amy-working", "inbox", "s1-1.w1", "BRIEF.md"))
	require.NoError(t, err, "the inbox file is the record")
	story := ta2.ok("card s1-1")
	assert.Contains(t, story, "a friend was not told of her card", story)
	assert.True(t, strings.Contains(story, "nova-bus2 send --as coordinator --to amy"), story)
	ta.clean()
	ta2.clean()
}

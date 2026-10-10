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

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/mas-bandwidth/nova-tools/internal/config"
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
	ta.ok("friend beat amy") // her daemon beats: the half of up this test does not vary
	seen := ta.now.UTC().Format(time.RFC3339)
	assert.Contains(t, ta.dry("friend health amy --state up --seen "+seen+" --generation 1 --dry-run"), "FRIEND-HEALTH DRY-RUN amy state=up seen="+seen+" generation=1; nothing was changed")
	out := ta.ok("friend health amy --state up --seen " + seen + " --generation 1")
	assert.Equal(t, "FRIEND-HEALTH OK amy state=up seen="+seen+" generation=1 status=up\n", out)
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "amy     |     0 |       0 |     8 |    0 | 0.0% | up")

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

	ta.a.sleep(sprint.FriendPongWindow + time.Second)
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
	assert.True(t, strings.HasPrefix(cellText(w.Tables[sprint.Friends]["amy"]["status"]), sprint.Held+" ("), "where --json carries the cell as printed")
	ta.ok("friend up amy")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| down", "the hold lifted, the observation (down) stands")
	ta.a.sleep(time.Second)
	ta.ok("friend health amy --state up --seen " + ta.now.UTC().Format(time.RFC3339) + " --generation 1")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| down", "her session answered and her daemon is not beating: down")
	ta.ok("friend beat amy")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| up", "her daemon beats and her session answered: up")
}

// A one-shot friend's delivery wakes her once per card: one bus message from the
// coordinator, naming the card and its inbox path. A send that fails never fails
// the delivery, is said on sync's line and written on the card's story. Batch mode
// is TestFriendSyncWakesOncePerPassInBatchMode.
func TestFriendSyncWakesOneShotFriendWithOneBusMessagePerDelivery(t *testing.T) {
	t.Parallel()
	oneShot := func(ta *testApp) {
		read := ta.a.friends
		ta.a.friends = func(ctx context.Context, pg string) ([]config.Row, error) {
			rows, err := read(ctx, pg)
			for i := range rows {
				if rows[i].Fields == nil {
					rows[i].Fields = map[string]string{}
				}
				rows[i].Fields["mode"] = config.FriendModeOneShot
			}
			return rows, err
		}
	}
	ta, root := friendCardApp(t, "friend amy", "amy")
	oneShot(ta)
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	ta.mu.Lock()
	sent := append([]bus.Message(nil), ta.sent...)
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
	oneShot(ta2)
	ta2.a.bus = func(_ context.Context, _ bus.Message, _ func(string)) error {
		return errors.New("dial tcp: connection refused")
	}
	ta2.ok("tick")
	out := ta2.ok("friend sync --root " + root2)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-1.w1")
	assert.Contains(t, out, "FRIEND-CARD NOTE friend=amy card=s1-1.w1: the bus message to her was not sent (dial tcp: connection refused); the inbox file stands, tell her by hand")
	_, err := os.Stat(filepath.Join(root2, "amy-working", "inbox", "s1-1.w1", "BRIEF.md"))
	require.NoError(t, err, "the inbox file is the record")
	story := ta2.ok("card s1-1")
	assert.Contains(t, story, "a friend was not told of her card", story)
	assert.True(t, strings.Contains(story, "nova-bus send --as coordinator --to amy"), story)
	ta.clean()
	ta2.clean()
}

// friend sync's bus message goes through friend.Courier, its result watched: a store that
// refuses the server's login is one alarm, raised at the first failed send as one
// FRIEND-CARD BUS-ALARM line on sync's output and named in the note on the card's story,
// and cleared at the next send that succeeds (docs/SPEC-FRIEND.md, fr-delivery-receipts.w1).
// The store is bus's fake behind the app's dial (busOpen): no socket.
func TestFriendSyncSaysTheBusStoresAlarmAndTheNextSendClearsIt(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	env := ta.a.getenv
	ta.a.getenv = func(k string) string {
		return map[string]string{busRedisEnv: "bus.test:6379", busUserEnv: "sprint"}[k] + env(k)
	}
	fake := bustest.NewFake(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), "coordinator", "amy")
	fake.Friends = []string{"amy"}
	fake.Fail = errors.New("WRONGPASS invalid username-password pair or user is disabled.")
	var dialed []string
	ta.a.busOpen = func(_ context.Context, addr, user string) (*bus.Bus, func(), error) {
		dialed = append(dialed, addr+" as "+user)
		return &bus.Bus{Store: fake}, func() {}, nil
	}
	ta.a.bus = ta.a.sendBus
	ta.ok("tick")
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=amy card=s1-1.w1")
	assert.Contains(t, out, "FRIEND-CARD BUS-ALARM bus store bus.test:6379 refuses the login of user sprint: login refused (WRONGPASS)")
	_, alarm, _ := strings.Cut(out, "FRIEND-CARD BUS-ALARM")
	alarm, _, _ = strings.Cut(alarm, "\n")
	assert.NotContains(t, alarm, "invalid username-password", "the alarm carries the store's refusal word, never the rest of its text")
	assert.Contains(t, out, "the bus store's alarm is raised: FRIEND-CARD BUS-ALARM", "the note names the alarm")
	assert.Equal(t, 1, strings.Count(out, "BUS-ALARM bus store"), "raised once")

	var said []string
	say := func(line string) { said = append(said, line) }
	m := bus.Message{From: "coordinator", To: []string{"amy"}, Subject: "card c2 dealt", Body: "hello"}
	require.Error(t, ta.a.sendBus(context.Background(), m, say))
	assert.Empty(t, said, "a second failure counts in the raised alarm and says nothing new")

	fake.Fail = nil
	require.NoError(t, ta.a.sendBus(context.Background(), m, say))
	require.Len(t, said, 1)
	assert.Contains(t, said[0], "FRIEND-CARD BUS-ALARM bus store bus.test:6379 answers user sprint again: 2 sends failed (auth)")
	assert.Equal(t, 1, fake.Len(bus.StreamOf("amy")), "the message reached her stream once the store answered")
	assert.Equal(t, []string{"bus.test:6379 as sprint", "bus.test:6379 as sprint", "bus.test:6379 as sprint", "bus.test:6379 as sprint"}, dialed, "one connection per send, and one before the deal's to name her (enrollBus)")
}

// friend health --clear removes the coordinator's observation of a friend, so her status is
// her session's evidence alone: with the observation gone she is up only on a card of hers
// finished, and her beat never brings her up. --dry-run says what stood and writes nothing;
// the seat's holder alone clears; an observation's flag beside --clear is refused.
func TestFriendHealthClearLeavesHerOnHerSessionsEvidenceAlone(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync --root " + t.TempDir())
	ta.ok("friend health amy --state up --seen " + ta.now.UTC().Format(time.RFC3339) + " --generation 1")
	ta.a.sleep(sprint.FriendPongWindow + time.Second)
	ta.ok("friend beat amy")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| down", "observed: her own beat never brings her up")

	assert.Equal(t, "FRIEND-HEALTH DRY-RUN amy clear was=up; nothing was changed\n", ta.dry("friend health amy --clear --dry-run"))
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| down", "a dry run removes nothing")

	code, _, errs := ta.do("friend health amy --clear --actor stella")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "friend health is the coordinator's alone: coordinator, not stella")
	code, _, errs = ta.do("friend health amy --clear --state up")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "--clear removes her observation and takes no observation's flag")

	assert.Equal(t, "FRIEND-HEALTH OK amy cleared=true was=up status=down\n", ta.ok("friend health amy --clear"))
	ta.ok("friend beat amy")
	assert.Contains(t, tableOf(ta.frame(), sprint.Friends), "| down", "her beat is no evidence once the observation is gone")
	var w whereView
	ta.json("where", &w)
	assert.Equal(t, sprint.Down, w.Tables[sprint.Friends]["amy"]["status"])

	assert.Equal(t, "FRIEND-HEALTH OK amy cleared=true was=none status=down\n", ta.ok("friend health amy --clear"), "a friend with no observation clears all the same")

	code, _, errs = ta.do("friend health nobody --clear")
	assert.Equal(t, 1, code, errs)
	assert.Contains(t, errs, "no friend nobody on the friends table")
}

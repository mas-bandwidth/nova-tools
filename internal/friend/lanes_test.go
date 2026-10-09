package friend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The one-shot lanes (the owner, 2026-10-04: "we could have one shot
// friends, have one-shots, per-lane", and "so freddy can still be wide, it's
// just 8 freddys"): width lanes, each its own session of the friend, each
// handed one card per turn and waiting for that card's RESULT.md.

// cardDirFixture is a friend's working directory with a queue file of tasks
// (id and state), each delivered card's brief, her own identity files, and
// done cards' results.
func cardDirFixture(t *testing.T, tasks [][2]string, delivered, done []string) string {
	t.Helper()
	dir := t.TempDir()
	q := Queue{}
	for _, task := range tasks {
		q.Tasks = append(q.Tasks, Task{ID: task[0], State: task[1]})
	}
	raw, err := json.Marshal(q)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), raw, 0o644))
	for _, id := range delivered {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", id+"~15"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", id+"~15", "BRIEF.md"), []byte("RESULT: "+id+"\n"), 0o644))
	}
	for _, id := range done {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", id+"~15"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", id+"~15", "RESULT.md"), []byte("RESULT: "+id+"\n"), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bob", "memory"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bob", "AGENTS.md"), []byte("I am bob.\n"), 0o644))
	return dir
}

// lanesHarness is a harness that opens sessions ses_1, ses_2, ... and, for a
// card turn, writes the card's RESULT.md when finish says so; a turn of a
// card in reject says that permission line.
type lanesHarness struct {
	mu      sync.Mutex
	dir     string
	seeds   []string
	turns   []string // session: card
	texts   []string
	finish  map[string]bool
	reject  map[string]string
	active  map[string]int
	maxBusy int
	block   chan struct{}      // when set, a card turn waits for it
	onPong  func(nonce string) // when set, the session "runs" each pong line a turn carries: called with its nonce
	refuse  []error            // what the next message turns answer instead of running (a Deferred, a SessionRefused), in order
}

var cardOfText = regexp.MustCompile(`one card this turn, ([^ ]+)\. Do exactly`)
var nonceOfLine = regexp.MustCompile(`nova-friend pong --as bob --nonce ([^ \n]+)`)
var laneOfSeed = regexp.MustCompile(`this is lane (\d+),`)

func (h *lanesHarness) Deliver(context.Context, string) (int, error) { return 0, nil }

func (h *lanesHarness) OpenSession(_ context.Context, seed string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seeds = append(h.seeds, seed)
	return "ses_" + laneOfSeed.FindStringSubmatch(seed)[1], nil // named for its lane: the opens race, the names do not
}

func (h *lanesHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	id := "-" // a message turn: no card
	if m := cardOfText.FindStringSubmatch(text); m != nil {
		id = m[1]
	}
	if h.onPong != nil {
		for _, m := range nonceOfLine.FindAllStringSubmatch(text, -1) {
			h.onPong(m[1])
		}
	}
	h.mu.Lock()
	h.turns = append(h.turns, session+": "+id)
	h.texts = append(h.texts, text)
	if id == "-" && len(h.refuse) > 0 {
		err := h.refuse[0]
		h.refuse = h.refuse[1:]
		h.mu.Unlock()
		return LaneTurn{Exit: 1}, err
	}
	h.active[session]++
	h.maxBusy = max(h.maxBusy, h.active[session])
	block := h.block
	h.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active[session]--
	if h.finish[id] {
		out := filepath.Join(h.dir, "outbox", id+"~15")
		if err := os.MkdirAll(out, 0o755); err != nil {
			return LaneTurn{}, err
		}
		if err := os.WriteFile(filepath.Join(out, "RESULT.md"), []byte("RESULT: "+id+"\n"), 0o644); err != nil {
			return LaneTurn{}, err
		}
	}
	return LaneTurn{Exit: 0, Rejected: h.reject[id]}, nil
}

func (h *lanesHarness) got() (turns, texts, seeds []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.turns...), append([]string(nil), h.texts...), append([]string(nil), h.seeds...)
}

// laneRig is the daemon rig over a lanes harness in a fixture directory, in
// one-shot mode at width lanes, its lane state in memory.
func laneRig(t *testing.T, h *lanesHarness, width int) (*rig, *LaneState) {
	r := newRig(t)
	state := &LaneState{}
	r.d.Deliver, r.passive, r.d.Dir = h, true, h.dir
	r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
	r.d.Row = func() (string, int) { return ModeOneShot, width }
	r.d.Coordinator = "ada"
	r.d.CardDone = func(card, to string) string {
		return "nova-bus send --as bob --to " + to + " --subject 'card " + card + " done'"
	}
	r.d.LoadLanes = func() (LaneState, error) { return *state, nil }
	r.d.SaveLanes = func(s LaneState) error { *state = s; return nil }
	return r, state
}

// Two lanes, each its own session seeded from the friend's own files, each
// handed one card per turn: a card whose turn writes its RESULT.md is done and
// the lane takes the next; a card without one is handed again once, then set
// aside and reported to the coordinator with the reason (here the permission
// the harness refused). A bus message rides in the first card's turn and is
// acked with it; no turn is ever started without a card.
func TestOneShotLanesHandOneCardPerTurnEachInItsOwnSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c0", "working"}, {"c1", "queued"}, {"cx", "queued"}, {"c2", "queued"}, {"cd", "queued"}, {"c3", "queued"}},
			[]string{"c0", "c1", "c2", "cd", "c3"}, []string{"cd"})
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c3": true}, reject: map[string]string{"c2": "Permission to read /elsewhere was auto-rejected"}, active: map[string]int{}}
		r, state := laneRig(t, h, 2)
		hello := r.send(t, "ada", "hello", "for whichever lane is next")
		r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
		r.at[20] = func() { r.send(t, "ada", "later", "after every card") }
		r.run(t, 30)

		turns, texts, seeds := h.got()
		require.Len(t, seeds, 2, "one session per lane")
		assert.ElementsMatch(t, []string{"1", "2"}, []string{laneOfSeed.FindStringSubmatch(seeds[0])[1], laneOfSeed.FindStringSubmatch(seeds[1])[1]})
		for _, seed := range seeds {
			assert.Contains(t, seed, "Read "+filepath.Join(dir, "bob", "AGENTS.md")+" and every file under "+filepath.Join(dir, "bob", "memory")+"/ first")
		}
		// c0 is working, cx undelivered, cd done: each lane one card a turn, the
		// lane whose session opened first taking c1
		byLane := map[string][]string{}
		for _, tn := range turns {
			ses, card, _ := strings.Cut(tn, ": ")
			if card != "-" {
				byLane[ses] = append(byLane[ses], card)
			}
		}
		assert.ElementsMatch(t, [][]string{{"c1", "c3"}, {"c2", "c2"}}, [][]string{byLane["ses_1"], byLane["ses_2"]}, "%v", turns)
		c1Lane := "1"
		if byLane["ses_2"][0] == "c1" {
			c1Lane = "2"
		}
		c2Lane := map[string]string{"1": "2", "2": "1"}[c1Lane]
		assert.Equal(t, 1, h.maxBusy, "a lane never runs two turns at once")
		first := texts[0]
		assert.Contains(t, first, "lane "+c1Lane+" of 2: one card this turn, c1.")
		assert.Contains(t, first, "Its brief is "+filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"))
		assert.Contains(t, first, "2. Write "+filepath.Join(dir, "outbox", "c1~15")+"/REPORT.md and "+filepath.Join(dir, "outbox", "c1~15")+"/RESULT.md")
		assert.Contains(t, first, "3. Send one bus line: nova-bus send --as bob --to ada --subject 'card c1 done'")
		assert.Contains(t, first, Text(hello), "the waiting message rides with the first card")
		for _, text := range texts[1:] {
			assert.NotContains(t, text, "for whichever lane is next", "and only with it")
		}
		last := texts[len(texts)-1]
		assert.Contains(t, last, "after every card", "a message with no card to ride with goes in as a message turn of its own")
		assert.Contains(t, last, "no card this turn")
		assert.NotContains(t, last, "one card this turn")
		assert.Equal(t, "-", strings.SplitN(turns[len(turns)-1], ": ", 2)[1], "the message turn names no card: %v", turns)
		pending, fresh := r.pending(t)
		assert.Empty(t, pending, "the late message is acked by its message turn")
		assert.Empty(t, fresh)

		got := r.adaGot(t)
		require.Len(t, got, 2)
		assert.Equal(t, "daemon-pong: daemon-pong n1", got[0])
		assert.True(t, strings.HasPrefix(got[1], "friend bob: card c2 not finished after 2 turns (lane "+c2Lane+"): the harness refused a permission: Permission to read /elsewhere was auto-rejected"), got[1])
		assert.Equal(t, map[int]string{1: "ses_1", 2: "ses_2"}, state.Sessions, "each lane keeps its session")
		assert.Equal(t, []string{"c2~15"}, state.GivenUp)
		records := strings.Join(r.records, "\n")
		assert.Equal(t, 2, strings.Count(records, " card=done"), records)
		assert.Contains(t, records, "lane="+c2Lane+" session=ses_"+c2Lane+` subject="card c2" messages=0`)
		assert.Contains(t, records, `card=again turn=1/2 reason="the harness refused a permission: Permission to read /elsewhere was auto-rejected"`)
		assert.Contains(t, records, "card=set_aside turn=2/2")
		assert.Contains(t, records, `subject="later" messages=1`)
		s := r.last()
		assert.Equal(t, ModeOneShot, s.Mode)
		assert.Equal(t, "1:ses_1:- 2:ses_2:-", s.Lanes)
		assert.Equal(t, 2, s.Width)
	})
}

// A lane friend with no card answers a wake ping in a message turn of her own: the pong
// line goes into a free lane, the session runs it (its own bus message and pong file), and
// the challenge ends. Before this the line rode only with a card, and a lane friend between
// cards was deaf to every wake. The daemon's own daemon-pong ends nothing.
func TestALaneFriendWithNoCardAnswersAWakeInAMessageTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{}, active: map[string]int{}}
		r, _ := laneRig(t, h, 2)
		pongCommand := func(nonce string) string { return "/opt/nova/bin/nova-friend pong --as bob --nonce " + nonce }
		r.d.PongCommand = pongCommand
		h.onPong = func(nonce string) { // the session runs the line: its own bus message, and the pong file
			_, err := r.bus.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: "pong " + nonce, Body: "pong " + nonce})
			require.NoError(t, err)
			r.mu.Lock()
			r.pong, r.pongSet = Pong{Nonce: nonce, At: r.now, To: "ada"}, true
			r.mu.Unlock()
		}
		r.at[3] = func() { r.send(t, "ada", "PING w1", WakePingText("ada", t0, "w1")) }
		r.run(t, 12)
		turns, texts, _ := h.got()
		require.Len(t, turns, 1, "one message turn, no card: %v", turns)
		assert.Equal(t, "ses_1: -", turns[0])
		assert.True(t, strings.HasPrefix(texts[0], "Run this now, first, exactly as written: "+pongCommand("w1")+"\n"), texts[0])
		assert.Contains(t, texts[0], "lane 1 of 2 of bob: no card this turn")
		got := r.adaGot(t)
		assert.Contains(t, got, "daemon-pong: daemon-pong w1", "the daemon answers the ping at once, as ever")
		assert.Contains(t, got, "pong w1: pong w1", "and the session's own pong is on the bus")
		s := r.last()
		assert.Equal(t, Quiet, s.Challenge, "the session's pong ends the wake challenge")
		assert.Equal(t, 1, s.Pongs)
		assert.Contains(t, strings.Join(r.records, "\n"), `subject="wake w1" messages=0`)
	})
}

// A message turn the session could not take was never delivered, so it is neither a failure
// nor an ack: deferred (the harness cannot take a turn now) or refused (the session cannot
// take one at all), the turn stays whole in the daemon's hand, goes in again after
// RecheckEvery with the same messages, is counted toward nothing and never acked or given
// up, and is acked once when a turn succeeds; a newer message waits behind it (Stella's
// cold read of #5474, 2026-10-08: no bus message is discarded unread).
func TestAMessageTurnTheSessionCannotTakeStaysOwedAndIsNeverAckedUnread(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{}, active: map[string]int{}}
		h.refuse = []error{
			Deferred{Reason: "the app is busy"},
			SessionRefused{Session: "ses_1", Reason: "no provider key", Detail: "dsh has no key for the provider", Remedy: "seal it"},
			Deferred{Reason: "the app is busy"},
		}
		r, _ := laneRig(t, h, 1)
		hello := r.send(t, "ada", "hello", "the first thing")
		var mid Status
		var midPending int
		r.at[15] = func() {
			mid = r.last()
			pending, _ := r.pending(t)
			midPending = len(pending)
			r.send(t, "ada", "more", "the second thing, behind the owed turn")
		}
		r.run(t, 50)
		turns, texts, _ := h.got()
		require.GreaterOrEqual(t, len(turns), 4, "three turns the session could not take, then one it did: %v", turns)
		for _, tn := range turns {
			assert.Equal(t, "ses_1: -", tn)
		}
		for _, text := range texts[:4] {
			assert.Contains(t, text, Text(hello), "the same turn, the same message, each time")
		}
		assert.Equal(t, 1, midPending, "the message is pending in the store while the session cannot take it, never acked")
		assert.Equal(t, 0, mid.Delivered, "nothing counted delivered before a turn succeeds")
		pending, fresh := r.pending(t)
		assert.Empty(t, pending, "acked once the turn succeeded; the second message had its own turn")
		assert.Empty(t, fresh)
		assert.Equal(t, 2, r.last().Delivered)
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, `tries=1 deferred: the app is busy; the turn stays in hand, handed again every 10s`)
		assert.Contains(t, records, `tries=2 session=refused reason="no provider key"`)
		assert.NotContains(t, records, "given_up=true", "a message never delivered is never given up")
		assert.Equal(t, 2, strings.Count(records, " acked=true"), "the turn that succeeded is acked once, and the second message's once: %s", records)
	})
}

// An owed message turn is cleared from the daemon's hand immediately on take, so
// multiple idle lanes never take the same turn pointer concurrently, preventing
// data races and duplicate ACKs.
func TestOwedMessageTurnClearedOnTakePreventsDuplicateDispatchAcrossLanes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{}, active: map[string]int{}}
		h.refuse = []error{
			Deferred{Reason: "the app is busy"},
		}
		// 2 lanes configured; with no card queued, both are free to take message turns
		r, _ := laneRig(t, h, 2)
		r.send(t, "ada", "hello", "the message")
		r.run(t, 25)
		turns, _, _ := h.got()
		// Turn 0: initial deferred turn (exit 1)
		// Turn 1: retried turn after RecheckEvery (exit 0)
		// Without clearing l.owedMessage on take, both lane 1 and lane 2 grab the same
		// turn at step 12 simultaneously, producing 3 turns total and duplicate ACKs.
		require.Len(t, turns, 2, "initial deferred turn, then exactly one retry turn: %v", turns)
		assert.Equal(t, 1, r.last().Delivered, "the message is counted delivered exactly once")
		records := strings.Join(r.records, "\n")
		assert.Equal(t, 1, strings.Count(records, " acked=true"), "exactly one ACK sent for the turn: %s", records)
	})
}

// A deferred message in owedMessage is given its turn before a queued card (FIFO),
// rather than being starved indefinitely by incoming cards.
func TestDeferredMessagePrioritizedBeforeQueuedCardsFIFO(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		h.refuse = []error{
			Deferred{Reason: "the app is busy"},
		}
		r, _ := laneRig(t, h, 1)
		hello := r.send(t, "ada", "hello", "the deferred message")
		// At step 13, queue card c1 right before hello's RecheckEvery retry arrives at step 14
		r.at[13] = func() {
			raw, err := json.Marshal(Queue{Tasks: []Task{{ID: "c1", State: "queued"}}})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), raw, 0o644))
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "c1~15"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "c1~15", "BRIEF.md"), []byte("RESULT: c1\n"), 0o644))
		}
		r.run(t, 25)
		turns, texts, _ := h.got()
		// Turn 0: message turn (deferred)
		// Turn 1: retried message turn (succeeded)
		// Turn 2: card c1 turn
		// Without prioritizing owedMessage before nextCard, card c1 runs at step 14 instead of
		// the deferred message, starving the message indefinitely.
		require.GreaterOrEqual(t, len(turns), 3, "deferred turn, retried message turn, then card turn: %v", turns)
		assert.Equal(t, "ses_1: -", turns[0], "turn 0 is the initial message turn")
		assert.Equal(t, "ses_1: -", turns[1], "turn 1 is the retried message turn, prioritized before card c1")
		assert.Contains(t, texts[1], Text(hello), "turn 1 delivers the deferred message")
		assert.Equal(t, "ses_1: c1", turns[2], "turn 2 is card c1, running after the owed message succeeded")
		assert.Equal(t, 1, r.last().Delivered, "the message was delivered and acked")
	})
}

// A one-shot friend's push is proved from a lane: while unproven, the lane opens its
// session and hands the push check's own pong line as a message turn; the session's run of
// it is the proof, and only then does a card go in. The daemon writes no pong for it.
func TestAOneShotFriendProvesHerPushFromALaneAndCardsWaitForIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		r, _ := laneRig(t, h, 1)
		pongCommand := func(nonce string) string { return "/opt/nova/bin/nova-friend pong --as bob --nonce " + nonce }
		r.d.PongCommand = pongCommand
		var mu sync.Mutex
		proven := false
		r.d.Proof = func() (bool, string) {
			mu.Lock()
			defer mu.Unlock()
			if proven {
				return true, ""
			}
			return false, "chk1"
		}
		h.onPong = func(nonce string) { // the session runs the check's line: the check sees its pong on the bus
			if nonce != "chk1" {
				return
			}
			_, err := r.bus.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: "pong " + nonce, Body: "pong " + nonce})
			require.NoError(t, err)
			mu.Lock()
			proven = true
			mu.Unlock()
		}
		var before Status
		r.at[2] = func() { before = r.last() }
		r.run(t, 12)
		turns, texts, seeds := h.got()
		require.Len(t, seeds, 1, "the lane opens its session while the push is unproven")
		require.Len(t, turns, 2, "the check's turn, then the card: %v", turns)
		assert.Equal(t, "ses_1: -", turns[0], "the first turn carries the check's pong line and no card")
		assert.Contains(t, texts[0], "Run this now, exactly as written, then read on: "+pongCommand("chk1")+"\n")
		assert.Equal(t, "ses_1: c1", turns[1], "the card goes in once the session answered")
		assert.Equal(t, PushUnproven, before.Push)
		assert.Equal(t, "chk1", before.PushNonce)
		assert.Equal(t, PushProved, r.last().Push)
		records := strings.Join(r.records, "\n")
		assert.Contains(t, records, `subject="push check chk1" messages=0`)
		assert.Equal(t, []string{"pong chk1: pong chk1"}, r.adaGot(t), "the only pong on the bus is the session's")
	})
}

// The reversed witness of the proof: a session that never runs the check's line leaves the
// push unproven, and no card goes in however long the daemon runs; the daemon never
// answers its own check (no pong of its own on the bus), and hands the line again only on
// the check's cadence, never once a step.
func TestNoPongFromTheSessionLeavesThePushUnprovenAndNoCardRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}}
		r, _ := laneRig(t, h, 1)
		r.d.PongCommand = func(nonce string) string { return "/opt/nova/bin/nova-friend pong --as bob --nonce " + nonce }
		r.d.Proof = func() (bool, string) { return false, "chk1" }
		r.run(t, 40)
		turns, _, _ := h.got()
		assert.Equal(t, []string{"ses_1: -"}, turns, "one message turn with the check's line, no card, no second hand within the cadence: %v", turns)
		assert.Equal(t, PushUnproven, r.last().Push)
		assert.Empty(t, r.adaGot(t), "the daemon writes no pong of its own")
		assert.NotContains(t, strings.Join(r.records, "\n"), "card=done")
	})
}

// Every lane whose turn ended refills on the step after it, all of them together:
// four lanes held in their first cards and released at once each take their next card
// on one step, never one lane a step (the loop took one result a step; the reversed
// witness tla/MCFriendLanesBrokenOneResultPerStep.cfg is that loop, and this test is its
// trace: four turns end in one step, and a step later every lane is busy again).
func TestEveryFreedLaneRefillsOnTheSameStep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ids := []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8"}
		var tasks [][2]string
		finish := map[string]bool{}
		for _, id := range ids {
			tasks = append(tasks, [2]string{id, "queued"})
			finish[id] = true
		}
		dir := cardDirFixture(t, tasks, ids, nil)
		h := &lanesHarness{dir: dir, finish: finish, active: map[string]int{}, block: make(chan struct{})}
		r, _ := laneRig(t, h, 4)
		var held, refilled Status
		r.at[9] = func() { held = r.last() }
		r.at[10] = func() { close(h.block) }      // every first card ends on the same step
		r.at[12] = func() { refilled = r.last() } // the status the step after the release wrote
		r.run(t, 20)
		turns, _, _ := h.got()
		require.Len(t, turns, 8, "every card ran: %v", turns)
		assert.Equal(t, "1:ses_1:c1/1 2:ses_2:c2/1 3:ses_3:c3/1 4:ses_4:c4/1", held.Lanes, "the four lanes took their first cards together")
		assert.Equal(t, "1:ses_1:c5/1 2:ses_2:c6/1 3:ses_3:c7/1 4:ses_4:c8/1", refilled.Lanes, "and their next cards on the one step after their turns ended, not one lane a step")
		assert.Equal(t, 1, h.maxBusy, "a lane never runs two turns at once")
	})
}

// The width is the friend row's as the beat answers it, read every step: lowered
// from three to one, the lanes beyond it finish the turn under way and take no
// other (said retired on the status, their cards handed back); raised again, they
// refill on the next step. The status names the row's width and its source.
func TestAWidthChangeOnTheBeatAnswerIsTheLaneLimitOnTheNextStep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ids := []string{"c1", "c2", "c3", "c4", "c5", "c6"}
		var tasks [][2]string
		finish := map[string]bool{}
		for _, id := range ids {
			tasks = append(tasks, [2]string{id, "queued"})
			finish[id] = true
		}
		dir := cardDirFixture(t, tasks, ids, nil)
		h := &lanesHarness{dir: dir, finish: finish, active: map[string]int{}, block: make(chan struct{})}
		r, _ := laneRig(t, h, 3)
		var mu sync.Mutex
		width := 3
		r.d.Row = func() (string, int) { mu.Lock(); defer mu.Unlock(); return ModeOneShot, width }
		var lowered, narrowed, raised Status
		release := func() { // every turn under way ends; the turns after it hold again
			h.mu.Lock()
			old := h.block
			h.block = make(chan struct{})
			h.mu.Unlock()
			close(old)
		}
		r.at[8] = func() { mu.Lock(); width = 1; mu.Unlock() }
		r.at[11] = func() { lowered = r.last() }
		r.at[12] = release // the three first cards end; only lane 1 is within the width
		r.at[18] = func() { narrowed = r.last(); mu.Lock(); width = 3; mu.Unlock() }
		r.at[20] = func() { raised = r.last() } // the status the step after the raise wrote
		r.at[22] = release
		r.run(t, 26)
		turns, _, _ := h.got()
		require.Len(t, turns, 6, "every card ran in the end: %v", turns)
		assert.Equal(t, 1, lowered.Width)
		assert.Equal(t, 1, lowered.RowWidth)
		assert.Equal(t, WidthFromRow, lowered.WidthSource)
		assert.Equal(t, "1:ses_1:c1/1 2:ses_2:c2/1:retired 3:ses_3:c3/1:retired", lowered.Lanes, "lanes 2 and 3 finish the turn under way, beyond the width")
		assert.Equal(t, "ses_1: c4", turns[3], "while the width is one, only lane 1 takes a card: %v", turns)
		assert.Equal(t, "1:ses_1:c4/1 2:ses_2:-:retired 3:ses_3:-:retired", narrowed.Lanes, "the lanes beyond the width hold no card")
		assert.Equal(t, "1:ses_1:c4/1 2:ses_2:c5/1 3:ses_3:c6/1", raised.Lanes, "raised to three, lanes 2 and 3 refill on the step after the beat that carried it")
		assert.Equal(t, 3, r.last().Width)
		assert.Equal(t, 3, r.last().RowWidth)
	})
}

// A daemon whose beat answer carries no row width runs at the flag's, and the status
// says so (width_source=flag, row_width=0), so a row the beat never carried is read off
// the file and never mistaken for the row's.
func TestStatusNamesTheWidthSource(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Row = nil
	r.run(t, 3)
	assert.Equal(t, 4, r.last().Width, "the flag's width")
	assert.Equal(t, 0, r.last().RowWidth)
	assert.Equal(t, WidthFromFlag, r.last().WidthSource)
	r2 := newRig(t)
	r2.d.Row = func() (string, int) { return ModeBatch, 7 }
	r2.run(t, 3)
	assert.Equal(t, 7, r2.last().Width, "the row's width")
	assert.Equal(t, 7, r2.last().RowWidth)
	assert.Equal(t, WidthFromRow, r2.last().WidthSource)
}

// One lane waits for its card's turn to end before the next card: with the
// turn held, the second card is not handed; released, it is.
func TestALaneHandsItsNextCardOnlyWhenTheTurnEnds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true, "c2": true}, active: map[string]int{}, block: make(chan struct{})}
		r, _ := laneRig(t, h, 1)
		var held []string
		r.at[40] = func() {
			held, _, _ = h.got()
			close(h.block)
		}
		r.run(t, 50)
		assert.Equal(t, []string{"ses_1: c1"}, held, "forty seconds into the first card, the second waits")
		turns, _, _ := h.got()
		assert.Equal(t, []string{"ses_1: c1", "ses_1: c2"}, turns)
	})
}

// A lane restarted keeps its session (no new one opened) and does not hand a
// card it set aside before; the mode follows the row, changing only when the
// other mode's turns are over.
func TestALaneKeepsItsSessionAcrossARestartAndTheModeFollowsTheRow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c2": true}, active: map[string]int{}}
		r, state := laneRig(t, h, 1)
		*state = LaneState{Sessions: map[int]string{1: "ses_old"}, GivenUp: []string{"c1"}}
		var mu sync.Mutex
		mode := ModeBatch
		r.d.Row = func() (string, int) { mu.Lock(); defer mu.Unlock(); return mode, 1 }
		r.send(t, "ada", "first", "in batch")
		r.at[5] = func() { mu.Lock(); mode = ModeOneShot; mu.Unlock() }
		r.run(t, 12)
		turns, _, seeds := h.got()
		assert.Empty(t, seeds, "the lane's session is kept")
		assert.Equal(t, []string{"ses_old: c2"}, turns, "c1 was set aside before the restart")
		assert.Contains(t, strings.Join(r.records, "\n"), "mode: one-shot, from batch (the friend row)")
		assert.Equal(t, ModeOneShot, r.last().Mode)
	})
}

// A harness that cannot open a session per lane delivers in batch whatever
// the row says, and says why once.
func TestOneShotOnAHarnessWithoutSessionsDeliversInBatch(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Row = func() (string, int) { return ModeOneShot, 3 }
	r.send(t, "ada", "hello", "x")
	r.run(t, 4)
	require.Len(t, r.delivered, 1, "the batch turn")
	assert.Equal(t, 1, strings.Count(strings.Join(r.records, "\n"), "cannot open a session per lane; delivering in batch"))
	assert.Equal(t, ModeBatch, r.last().Mode)
	assert.Equal(t, 3, r.last().Width, "the row's width all the same")
}

// NextCard reads the queue file in order: queued and delivered, not done,
// not skipped; a card delivered at two epochs is its latest.
func TestNextCardIsTheFirstQueuedDeliveredUnfinishedCard(t *testing.T) {
	t.Parallel()
	dir := cardDirFixture(t, [][2]string{{"a", "done"}, {"b", "queued"}, {"c", "queued"}}, []string{"a", "b", "c"}, []string{"b"})
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", "c~9"), 0o755))
	c, found, err := NextCard(dir, func(Card) bool { return false })
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, Card{ID: "c", Brief: filepath.Join(dir, "inbox", "c~15", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c~15")}, c, "epoch 15 over 9")
	_, found, err = NextCard(dir, func(c Card) bool { return c.ID == "c" })
	require.NoError(t, err)
	assert.False(t, found)
}

// docs/FRIENDS.md: a redealt job belongs to its generation; progress names only its epoch.
func TestLanesRunACardDealtAgainAtItsGeneration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, task, want string
		doneOld          bool
	}{
		{"redealt", `{"id":"c","state":"queued","gen":2,"job":"c~15.g2"}`, "c~15.g2", true},
		{"generation without job", `{"id":"c","state":"queued","gen":2}`, "c~15.g2", true},
		{"legacy", `{"id":"c","state":"queued"}`, "c~15", false},
		{"missing generation", `{"id":"c","state":"queued","gen":3}`, "", false},
		{"mismatched job", `{"id":"c","state":"queued","gen":2,"job":"c~15"}`, "", false},
		{"unsafe job", `{"id":"c","state":"queued","gen":2,"job":"../c~15.g2"}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := cardDirFixture(t, nil, []string{"c"}, nil)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[`+tc.task+`]}`), 0o600))
			job := filepath.Join(dir, "inbox", "c~15.g2")
			require.NoError(t, os.MkdirAll(job, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(job, "BRIEF.md"), []byte("new generation"), 0o600))
			if tc.doneOld {
				old := filepath.Join(dir, "outbox", "c~15")
				require.NoError(t, os.MkdirAll(old, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(old, "RESULT.md"), []byte("done"), 0o600))
			}
			c, found, err := NextCard(dir, func(Card) bool { return false })
			require.NoError(t, err)
			require.Equal(t, tc.want != "", found)
			if !found {
				return
			}
			assert.Equal(t, filepath.Join(dir, "inbox", tc.want, "BRIEF.md"), c.Brief)
			assert.Equal(t, filepath.Join(dir, "outbox", tc.want), c.Outbox)
			assert.Equal(t, "15", c.Epoch())
			assert.Equal(t, [][]string{{"progress", "--as", "friend.friend-a", "c", "--epoch", "15"}}, ProgressArgv("friend-a", []Card{c}))
		})
	}
}

// docs/FRIENDS.md: before the first clear, StoredID omits the zero epoch.
func TestLanesUseGenerationJobsBeforeTheFirstClear(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := filepath.Join(dir, "inbox", "c.w1.g2")
	require.NoError(t, os.MkdirAll(job, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(job, "BRIEF.md"), []byte("work"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"c.w1","gen":2,"job":"c.w1.g2"}]}`), 0o600))
	c, found, err := NextCard(dir, func(Card) bool { return false })
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, filepath.Join(job, "BRIEF.md"), c.Brief)
	assert.Equal(t, "0", c.Epoch())
	assert.Equal(t, [][]string{{"progress", "--as", "friend.friend-a", "c.w1", "--epoch", "0"}}, ProgressArgv("friend-a", []Card{c}))
}

// The queue's recorded job wins over newer-looking stale directories. A missing
// brief for that assignment is never repaired by taking another generation or epoch.
func TestNextCardReadsCardAtHerGeneration(t *testing.T) {
	t.Parallel()
	dir := cardDirFixture(t, nil, []string{"c"}, nil)
	queue := filepath.Join(dir, "inbox", "QUEUE.json")
	require.NoError(t, os.WriteFile(queue, []byte(`{"tasks":[{"id":"c","state":"queued","gen":2,"job":"c~15.g2"}]}`), 0o600))
	for _, job := range []string{"c~15.g2", "c~16.g2", "c~15.g3"} {
		in := filepath.Join(dir, "inbox", job)
		require.NoError(t, os.MkdirAll(in, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(in, "BRIEF.md"), []byte(job), 0o600))
	}
	c, found, err := NextCard(dir, func(Card) bool { return false })
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, Card{ID: "c", Brief: filepath.Join(dir, "inbox", "c~15.g2", "BRIEF.md"), Outbox: filepath.Join(dir, "outbox", "c~15.g2")}, c)
	assert.Equal(t, "15", c.Epoch())
	assert.Equal(t, [][]string{{"progress", "--as", "friend.friend-a", "c", "--epoch", "15"}}, ProgressArgv("friend-a", []Card{c}))
	require.NoError(t, os.Remove(c.Brief))
	_, found, err = NextCard(dir, func(Card) bool { return false })
	require.NoError(t, err)
	assert.False(t, found, "the missing assignment cannot fall back to generation 1, generation 3 or epoch 16")

	// Older producers also wrote a bare task list without generation or job.
	require.NoError(t, os.WriteFile(queue, []byte(`[{"id":"c","state":"queued"}]`), 0o600))
	c, found, err = NextCard(dir, func(Card) bool { return false })
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, filepath.Join(dir, "inbox", "c~15", "BRIEF.md"), c.Brief)
	assert.Equal(t, filepath.Join(dir, "outbox", "c~15"), c.Outbox)
}

// docs/FRIENDS.md: giving up an older assignment never suppresses a new generation.
func TestLanesRetryANewGenerationAfterGivingUpTheOldJob(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		job := filepath.Join(dir, "inbox", "c1~15.g2")
		require.NoError(t, os.MkdirAll(job, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(job, "BRIEF.md"), []byte("work"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"c1","state":"queued","gen":2,"job":"c1~15.g2"}]}`), 0o600))
		// each turn ends in a refused permission, an attempt that counts (a run that exits 0 with no
		// report is a harness fault and counts none: lane_path_test.go)
		h := &lanesHarness{dir: dir, active: map[string]int{}, reject: map[string]string{"c1": "Permission to read /elsewhere was auto-rejected"}}
		r, state := laneRig(t, h, 2)
		*state = LaneState{GivenUp: []string{"c1", "c1~15"}}
		r.run(t, 12)
		turns, _, _ := h.got()
		assert.Len(t, turns, CardTurns, "a fresh assignment is tried, by only one lane")
		assert.Contains(t, state.GivenUp, "c1~15.g2", "the new set-aside record names the job")
	})
}

// claudeRig is the daemon rig over a claude harness in one-shot mode at
// width 1, its runs answered by run (no process), its lane state in memory.
func claudeRig(t *testing.T, dir, configDir string, run Exec) (*rig, *LaneState) {
	r := newRig(t)
	state := &LaneState{}
	r.d.Deliver = &Claude{Stub: Stub{Harness: "claude"}, Friend: "bob", Dir: dir, Run: run, ConfigDir: func() string { return configDir }}
	r.d.Harness, r.passive, r.d.Dir = "claude", true, dir
	r.d.Pause = func(context.Context, time.Duration) { synctest.Wait() }
	r.d.Row = func() (string, int) { return ModeOneShot, 1 }
	r.d.Coordinator = "ada"
	r.d.LoadLanes = func() (LaneState, error) { return *state, nil }
	r.d.SaveLanes = func(s LaneState) error { *state = s; return nil }
	return r, state
}

// A claude lane is a process per card: no session opened, the brief the
// prompt, the result read from the outbox. A card whose run wrote REPORT.md
// and RESULT.md is done; one whose run exited 0 and wrote nothing is a
// harness fault, never a failed attempt: kept in the lane's hand and run
// again, never set aside, and the third alike marks her row down once with
// one judgment to the seat (the-lane-hands-the-brief-by-absolute-path-bb;
// lane_path_test.go). A bus message rides with no card: it waits, pending.
func TestAClaudeLaneRunsEachCardAsAProcessAndReadsItsOutbox(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}, {"c2", "queued"}}, []string{"c1", "c2"}, nil)
		var mu sync.Mutex
		var prompts []string
		run := func(_ context.Context, _, _ string, args []string, _ string) (string, int, error) {
			mu.Lock()
			defer mu.Unlock()
			prompts = append(prompts, args[3])
			if args[3] == "RESULT: c1\n" {
				out := filepath.Join(dir, "outbox", "c1~15")
				if err := os.MkdirAll(out, 0o755); err != nil {
					return "", 0, err
				}
				for f, text := range map[string]string{"REPORT.md": "Verdict: LAND\n", "RESULT.md": "RESULT: c1\n"} {
					if err := os.WriteFile(filepath.Join(out, f), []byte(text), 0o644); err != nil {
						return "", 0, err
					}
				}
			}
			return "the card is done", 0, nil
		}
		r, state := claudeRig(t, dir, "/accounts/heavy-a", run)
		r.send(t, "ada", "hello", "no card carries this")
		r.run(t, 20)

		mu.Lock()
		assert.Equal(t, []string{"RESULT: c1\n", "RESULT: c2\n", "RESULT: c2\n", "RESULT: c2\n"}, prompts, "one run per card turn, the brief its prompt; the third fault holds the lane")
		mu.Unlock()
		assert.Empty(t, state.Sessions, "no session is opened")
		assert.Empty(t, state.GivenUp, "a harness fault never sets the card aside")
		records := strings.Join(r.records, "\n")
		assert.Equal(t, 1, strings.Count(records, " card=done"), records)
		assert.Contains(t, records, `error="claude -p exited 0 and `+filepath.Join(dir, "outbox", "c2~15")+` holds no REPORT.md and no RESULT.md" card=kept turn=0/2 reason="harness-fault: no report; first error: the harness printed no error line"`)
		assert.NotContains(t, records, "card=again")
		assert.NotContains(t, records, "card=set_aside")
		got := r.adaGot(t)
		require.Len(t, got, 1, "one judgment, not one per card")
		assert.True(t, strings.HasPrefix(got[0], "friend bob down until "), got[0])
		pending, fresh, err := r.bus.Peek(context.Background(), "bob")
		require.NoError(t, err)
		assert.Empty(t, pending)
		assert.Len(t, fresh, 1, "the message is never taken")
		assert.Equal(t, ModeOneShot, r.last().Mode)
	})
}

// A claude lane's card run is a lane's: its context is marked (LaneContext),
// so the harness's process runs inside the lane wall (Wall.Exec) and never
// outside it.
func TestAClaudeLaneCardRunsUnderALaneContext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		var mu sync.Mutex
		var inLane []bool
		run := func(ctx context.Context, _, _ string, _ []string, _ string) (string, int, error) {
			mu.Lock()
			defer mu.Unlock()
			inLane = append(inLane, InLane(ctx))
			out := filepath.Join(dir, "outbox", "c1~15")
			if err := os.MkdirAll(out, 0o755); err != nil {
				return "", 0, err
			}
			for _, f := range []string{"REPORT.md", "RESULT.md"} {
				if err := os.WriteFile(filepath.Join(out, f), []byte("done\n"), 0o644); err != nil {
					return "", 0, err
				}
			}
			return "", 0, nil
		}
		r, _ := claudeRig(t, dir, "/accounts/heavy-a", run)
		r.run(t, 10)
		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, []bool{true}, inLane, "the one card run was a lane's")
	})
}

// A claude row in one-shot mode with no config_dir is refused: the refusal
// and its remedy said once on the record, no card run, the daemon in batch
// (passive: nothing delivered).
func TestAClaudeRowInOneShotModeWithoutConfigDirIsRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		ran := 0
		r, _ := claudeRig(t, dir, "", func(context.Context, string, string, []string, string) (string, int, error) {
			ran++
			return "", 0, nil
		})
		r.run(t, 6)
		assert.Zero(t, ran, "no card runs")
		records := strings.Join(r.records, "\n")
		assert.Equal(t, 1, strings.Count(records, "mode: one-shot REFUSED: friend bob is a claude friend in one-shot mode with no config_dir"), records)
		assert.Contains(t, records, "run: nova-config friend set bob --config_dir <her account's absolute config directory>")
		assert.Equal(t, ModeBatch, r.last().Mode)
	})
}

// RowConfigDir reads row_config_dir= off the beat's answer, empty when absent.
func TestRowConfigDirIsReadOffTheBeat(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/accounts/heavy-a", RowConfigDir("FRIEND-BEAT OK bob at=x row_mode=one-shot row_width=8 row_config_dir=/accounts/heavy-a"))
	assert.Empty(t, RowConfigDir("FRIEND-BEAT OK bob at=x row_mode=one-shot row_width=8"))
}

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
	block   chan struct{} // when set, a card turn waits for it
}

var cardOfText = regexp.MustCompile(`one card this turn, ([^ ]+)\. Do exactly`)
var laneOfSeed = regexp.MustCompile(`this is lane (\d+),`)

func (h *lanesHarness) Deliver(context.Context, string) (int, error) { return 0, nil }

func (h *lanesHarness) OpenSession(_ context.Context, seed string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seeds = append(h.seeds, seed)
	return "ses_" + laneOfSeed.FindStringSubmatch(seed)[1], nil // named for its lane: the opens race, the names do not
}

func (h *lanesHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	id := cardOfText.FindStringSubmatch(text)[1]
	h.mu.Lock()
	h.turns = append(h.turns, session+": "+id)
	h.texts = append(h.texts, text)
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
			byLane[ses] = append(byLane[ses], card)
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
		assert.NotContains(t, strings.Join(texts, "\n"), "after every card", "a message with no card to ride with waits")
		pending, fresh := r.pending(t)
		assert.Len(t, pending, 1, "the late message waits in hand, pending")
		assert.Equal(t, "later", pending[0].Message().Subject)
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
		s := r.last()
		assert.Equal(t, ModeOneShot, s.Mode)
		assert.Equal(t, "1:ses_1:- 2:ses_2:-", s.Lanes)
		assert.Equal(t, 2, s.Width)
	})
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
		h := &lanesHarness{dir: dir, active: map[string]int{}}
		r, state := laneRig(t, h, 2)
		*state = LaneState{GivenUp: []string{"c1", "c1~15"}}
		r.run(t, 12)
		turns, _, _ := h.got()
		assert.Len(t, turns, CardTurns, "a fresh assignment is tried, by only one lane")
		assert.Contains(t, state.GivenUp, "c1~15.g2", "the new set-aside record names the job")
	})
}

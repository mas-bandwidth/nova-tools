package friend

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contractSession is a harness's deliver command as the session check reaches it: every text it
// is handed is kept, and while noMonitor is set it defers as the grok adapter does when the
// open window runs no monitor over its wake file.
type contractSession struct {
	mu        sync.Mutex
	texts     []string
	noMonitor string // the wake file no monitor tails; "" delivers
}

func (f *contractSession) Deliver(_ context.Context, text string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	if f.noMonitor != "" {
		return 0, Deferred{Reason: "the grok session in /w/bob runs no monitor over " + f.noMonitor + "; in that session: " + GrokMonitorLine(f.noMonitor)}
	}
	return 0, nil
}

func (f *contractSession) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

// posts is the fake push path into the session: each message the daemon pushes is kept, and
// err (nil: delivered) is the answer the harness's deliver command gives, a grok session
// running no monitor over its wake file answering Deferred among them.
type posts struct {
	mu   sync.Mutex
	msgs [][2]string // subject, body
	err  error
}

func (p *posts) post(_ context.Context, subject, body string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, [2]string{subject, body})
	return p.err
}

func (p *posts) got() [][2]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][2]string(nil), p.msgs...)
}

// contractRig is one SessionCheck over bus's Fake with the contract wired as the run verb
// wires it (cmd/nova-friend/daemon.go): its checks' text through the teller, its record lines
// read by the teller and the monitor watch. The clock is the test's.
type contractRig struct {
	store   *bustest.Fake
	session *contractSession
	sc      *SessionCheck
	teller  *ContractTeller
	watch   *MonitorWatch
	pushed  *posts
	direct  *bus.Bus
	now     time.Time
	nonces  int
	records []string
	written []string
}

func bobContract(wake, epoch string) Contract {
	return Contract{Friend: "bob", Harness: "grok", Wake: wake, Monitor: GrokMonitorLine(wake),
		Pong: "nova-friend pong --as bob --nonce <nonce>", Server: "sprint.test:6390", Epoch: epoch, Reports: "/w/bob/outbox"}
}

func newContractRig(t *testing.T, c Contract, prior string) *contractRig {
	t.Helper()
	r := &contractRig{store: bustest.NewFake(t0, "ada", "bob"), session: &contractSession{}, pushed: &posts{}, now: t0}
	clock := func() time.Time { return r.now }
	record := func(line string) { r.records = append(r.records, line) }
	r.teller = &ContractTeller{Push: r.pushed.post, Write: func(text string) error { r.written = append(r.written, text); return nil }, Record: record, Now: clock}
	r.watch = &MonitorWatch{Post: r.pushed.post, Pong: func(nonce string) string { return "nova-friend pong --as bob --nonce " + nonce }, Contract: func() string { return r.teller.Contract().Text() }, Record: record, Now: clock}
	r.sc = &SessionCheck{
		Friend: "bob", Store: r.store, Now: clock,
		Nonce: func() string { r.nonces++; return "n" + string(rune('0'+r.nonces)) },
		Text: func(nonce string) string {
			return r.teller.CheckText(SessionCheckText(nonce, "nova-friend pong --as bob --nonce "+nonce, "ada"))
		},
		Record: func(line string) {
			record(line)
			if IsSessionAnswer(line) {
				r.teller.Answered()
			}
			r.watch.Line(context.Background(), line)
			r.sc.NoteNoMonitor(line) // the run verb's record does this too (cmd/nova-friend/main.go)
		},
		Go: func(f func()) { f() },
	}
	r.sc.Deliver = r.sc.Gate(r.session)
	r.direct = &bus.Bus{Store: r.store}
	r.teller.Start(context.Background(), c, prior)
	return r
}

func (r *contractRig) step(d time.Duration) {
	r.now = r.now.Add(d)
	r.sc.Step(context.Background())
}

func (r *contractRig) answer(t *testing.T, nonce string) {
	t.Helper()
	_, err := r.direct.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: PongSubject, Body: PongLine(nonce, 0, 0, 4) + "\n"})
	require.NoError(t, err)
}

// pushedChecks is how many pushed messages are deferred session checks.
func pushedChecks(msgs [][2]string) int {
	n := 0
	for _, m := range msgs {
		if strings.HasPrefix(m[0], SessionCheckPrefix) {
			n++
		}
	}
	return n
}

// carrying is how many of texts carry the contract.
func carrying(texts []string) int {
	n := 0
	for _, text := range texts {
		if strings.Contains(text, ContractTitle+" (nova-friend, bob)") {
			n++
		}
	}
	return n
}

// The daemon's start pushes one message titled "your contract", body the contract, on the same
// path a card takes, and the first session check carries it too, with every line the machine
// expects (the wake file and the exact monitor line, the pong line, the start stamp, the finish
// form). CONTRACT.md is written, and once the session answers no check carries it again. A
// reinstall that moved the wake file is a start that says so, and pushes the new contract once
// in the same way (docs/SPEC-FRIEND.md, the session contract).
func TestStartAndReinstallPushTheContractOnce(t *testing.T) {
	t.Parallel()
	first := bobContract("/h/bob.wake", "")
	r := newContractRig(t, first, "")
	require.Len(t, r.written, 1, "CONTRACT.md is written at the start")
	assert.Contains(t, r.records[0], "contract: start: owed to the session")

	r.step(BeatEvery)
	texts := r.session.got()
	require.Len(t, texts, 1)
	assert.True(t, strings.HasPrefix(texts[0], SessionCheckPrefix+"n1\n"), "the check's first line stays the check: %q", texts[0])
	for _, want := range []string{
		"Wake file: /h/bob.wake",
		"Monitor: monitor `tail -n 0 -F /h/bob.wake`",
		"Pong: answer a SESSION CHECK <nonce> at once",
		"nova-friend pong --as bob --nonce <nonce>",
		"Start: when you or a child begin a card, stamp it started before anything else: NOVA_SPRINT_SERVER=sprint.test:6390 nova-sprint progress --as friend.bob <card>@<gen> --epoch <n>",
		"Finish: write /w/bob/outbox/<job>/REPORT.md: first line exactly `Verdict: LAND|HOLD|FAIL`, second line exactly `Head: <40-hex>`",
		"Read it again: nova-friend contract --as bob",
	} {
		assert.Contains(t, texts[0], want)
	}

	r.answer(t, "n1")
	r.step(BeatEvery)
	assert.False(t, r.teller.Owed(), "the session answered the check that carried it")
	r.step(ProveEvery)
	texts = r.session.got()
	require.Len(t, texts, 2, "the next check went in")
	assert.Equal(t, 1, carrying(texts), "the contract went in once, on the check")
	pushed := r.pushed.got()
	require.Len(t, pushed, 1, "the start pushes the contract once, beside the check")
	assert.Equal(t, ContractTitle, pushed[0][0])
	assert.Equal(t, first.Text(), pushed[0][1])
	assert.Contains(t, pushed[0][1], "Wake file: /h/bob.wake")
	assert.Contains(t, pushed[0][1], "Monitor: monitor `tail -n 0 -F /h/bob.wake`")
	assert.Contains(t, pushed[0][1], "nova-sprint progress --as friend.bob <card>@<gen> --epoch <n>")
	assert.Contains(t, pushed[0][1], "Finish: write /w/bob/outbox/<job>/REPORT.md")

	// a reinstall: the next run starts with the last run's CONTRACT.md and a new wake file
	second := bobContract("/h/bob-2.wake", "")
	r2 := newContractRig(t, second, r.written[0])
	assert.Contains(t, r2.records[0], "contract: reinstall: the wake path (/h/bob.wake -> /h/bob-2.wake) since the last run")
	require.Len(t, r2.pushed.got(), 1, "a reinstall pushes the new contract once")
	assert.Equal(t, ContractTitle, r2.pushed.got()[0][0])
	assert.Equal(t, second.Text(), r2.pushed.got()[0][1])
	r2.step(BeatEvery)
	r2.answer(t, "n1")
	r2.step(BeatEvery)
	r2.step(ProveEvery)
	texts = r2.session.got()
	require.Len(t, texts, 2)
	assert.Equal(t, 1, carrying(texts), "the reinstall tells the new contract once")
	assert.Contains(t, texts[0], "Wake file: /h/bob-2.wake")
	assert.Equal(t, ContractKeyOf(second.Text()), second.Key(), "CONTRACT.md reads back to the contract's key")
}

// A change of the wake path, the server or the epoch while the daemon runs pushes the contract
// again as one message, and owes it to the next check; the same contract again pushes nothing.
// The epoch first said by the server is a change, empty to a number, and is pushed the same
// way a later epoch change is.
func TestAWakePathChangePushesTheContractAgain(t *testing.T) {
	t.Parallel()
	r := newContractRig(t, bobContract("/h/bob.wake", ""), "")
	r.step(BeatEvery)
	r.answer(t, "n1")
	r.step(BeatEvery)
	require.False(t, r.teller.Owed())
	require.Len(t, r.pushed.got(), 1, "the start already pushed the contract once")

	assert.True(t, r.teller.Update(context.Background(), bobContract("/h/bob.wake", "15")), "the first epoch pushes the contract")
	assert.True(t, r.teller.Owed(), "the next check carries the epoch")
	assert.False(t, r.teller.Update(context.Background(), bobContract("/h/bob.wake", "15")), "nothing changed")
	require.Len(t, r.pushed.got(), 2)
	assert.Equal(t, ContractTitle, r.pushed.got()[1][0])
	assert.Contains(t, r.pushed.got()[1][1], "Epoch: 15")

	assert.True(t, r.teller.Update(context.Background(), bobContract("/h/bob-2.wake", "15")), "the wake path changed")
	got := r.pushed.got()
	require.Len(t, got, 3)
	assert.Equal(t, ContractTitle, got[2][0])
	assert.Contains(t, got[2][1], "Wake file: /h/bob-2.wake")
	assert.Contains(t, got[2][1], "Epoch: 15")
	assert.True(t, r.teller.Owed(), "the next check carries it too")
	assert.Contains(t, strings.Join(r.records, "\n"), "contract: the wake path (/h/bob.wake -> /h/bob-2.wake) changed; pushed into the session as a message")

	assert.True(t, r.teller.Update(context.Background(), bobContract("/h/bob-2.wake", "16")), "the epoch changed")
	require.Len(t, r.pushed.got(), 4)
	assert.Contains(t, r.pushed.got()[3][1], "nova-sprint progress --as friend.bob <card>@<gen> --epoch 16")
	assert.Len(t, r.written, 4, "CONTRACT.md follows every change: the start, the epoch, the wake path, the epoch")
}

// A session check deferred because the session runs no monitor over its wake file is pushed as
// a message into the session, "answer <nonce>: <pong line>", and that message still carries the
// contract (the wake file, the monitor command, the start stamp, the finish form). A grok
// deliver with no monitor writes nothing, so the check's own text never reaches the session.
// A check deferred for any other reason is not pushed.
func TestADeferredCheckIsPushedAsAMessage(t *testing.T) {
	t.Parallel()
	r := newContractRig(t, bobContract("/h/bob.wake", ""), "")
	r.session.noMonitor = "/h/bob.wake"
	r.step(BeatEvery)
	got := r.pushed.got()
	require.Len(t, got, 2, "the start's contract, then the deferred check")
	assert.Equal(t, ContractTitle, got[0][0])
	assert.Equal(t, SessionCheckPrefix+"n1", got[1][0])
	assert.Contains(t, got[1][1], "answer n1: nova-friend pong --as bob --nonce n1\n")
	for _, want := range []string{
		"Wake file: /h/bob.wake",
		"Monitor: monitor `tail -n 0 -F /h/bob.wake`",
		"nova-sprint progress --as friend.bob <card>@<gen> --epoch <n>",
		"Finish: write /w/bob/outbox/<job>/REPORT.md",
	} {
		assert.Contains(t, got[1][1], want, "the deferred message carries the contract")
	}
	assert.Contains(t, strings.Join(r.records, "\n"), "presence: session check n1 deferred: the session runs no monitor over /h/bob.wake (1 of 3 unanswered); the answer was pushed into the session as a message")

	other := &MonitorWatch{Post: r.pushed.post}
	other.Line(context.Background(), "2026-10-07T23:53:00Z presence: session check n2 deferred: deferred: a turn runs in friend-bob: its prompt is not shown; the bound runs")
	assert.Len(t, r.pushed.got(), 2, "a deferral for another reason is not a missing monitor")
	nonce, file, ok := DeferredCheckOf("2026-10-07T23:53:00Z presence: session check n7 deferred: deferred: the grok session in /w/bob runs no monitor over /h/bob.wake; in that session: monitor `tail -n 0 -F /h/bob.wake`; the bound runs")
	require.True(t, ok)
	assert.Equal(t, "n7", nonce)
	assert.Equal(t, "/h/bob.wake", file)
}

// A push the harness's deliver command defers (a grok session running no monitor over its wake
// file) is recorded as deferred, never as delivered: the contract's start and the deferred
// check's answer alike say "the push was deferred", nothing claims the session received it, and
// the contract stays owed to the next session check (docs/SPEC-FRIEND.md, the session contract).
func TestADeferredPushIsRecordedAsDeferredNotDelivered(t *testing.T) {
	t.Parallel()
	reason := "the grok session in /w/bob runs no monitor over /h/bob.wake; in that session: monitor `tail -n 0 -F /h/bob.wake`"
	run := func(t *testing.T, fn func(p *posts, record func(string))) []string {
		t.Helper()
		p := &posts{err: Deferred{Reason: reason}}
		var records []string
		fn(p, func(line string) { records = append(records, line) })
		return records
	}

	t.Run("the contract's start", func(t *testing.T) {
		records := run(t, func(p *posts, record func(string)) {
			teller := &ContractTeller{Push: p.post, Write: func(string) error { return nil }, Record: record}
			assert.False(t, teller.Start(context.Background(), bobContract("/h/bob.wake", ""), ""), "a deferred push is not a delivered contract")
			assert.True(t, teller.Owed(), "the next check still carries the contract")
		})
		joined := strings.Join(records, "\n")
		assert.Contains(t, joined, "contract: start: owed to the session")
		assert.Contains(t, joined, "the push was deferred: "+reason)
		assert.NotContains(t, joined, "pushed into the session as a message")
	})

	t.Run("the deferred check's answer", func(t *testing.T) {
		records := run(t, func(p *posts, record func(string)) {
			w := &MonitorWatch{Post: p.post, Pong: func(nonce string) string { return "nova-friend pong --as bob --nonce " + nonce }, Record: record}
			w.Deferred(context.Background(), "n1", "/h/bob.wake")
		})
		joined := strings.Join(records, "\n")
		assert.Contains(t, joined, "the answer's push was deferred: "+reason)
		assert.NotContains(t, joined, "pushed into the session")
	})
}

// Three session checks in a row deferred for no monitor, none answered, are the friend down
// with the reason "session runs no monitor over <file>", the exact words her beat and her
// presence file say; two are not, and any answer from the session starts the count again.
// A no-monitor deferral queues nothing, so the next check is owed on the down cadence
// (SessionQuiet from the ask), not after ReaskAfter.
func TestThreeUnansweredNoMonitorChecksAreDownWithTheExactReason(t *testing.T) {
	t.Parallel()
	r := newContractRig(t, bobContract("/h/bob.wake", ""), "")
	r.session.noMonitor = "/h/bob.wake"
	r.step(BeatEvery) // the first check, at the start
	down, _ := r.watch.Down()
	assert.False(t, down)
	r.step(SessionBound) // the bound ends inside SessionQuiet: not asked again yet
	assert.Equal(t, 1, pushedChecks(r.pushed.got()), "a no-monitor check is not re-asked before the check cadence, and not held for ReaskAfter")
	r.step(SessionQuiet - SessionBound) // SessionQuiet from the ask: the down cadence
	down, _ = r.watch.Down()
	assert.False(t, down, "two unanswered are not yet down")
	r.step(SessionQuiet)
	down, why := r.watch.Down()
	require.True(t, down, "three unanswered on the check cadence, not after ReaskAfter: %v", r.records)
	assert.Equal(t, "session runs no monitor over /h/bob.wake", why)
	var checks int
	for _, m := range r.pushed.got() {
		if strings.HasPrefix(m[0], SessionCheckPrefix) {
			checks++
			assert.Contains(t, m[1], "Wake file: /h/bob.wake", "each deferred message carries the contract")
			assert.Contains(t, m[1], "Monitor: monitor `tail -n 0 -F /h/bob.wake`")
		}
	}
	assert.Equal(t, 3, checks, "each deferred check was pushed as a message")
	assert.Contains(t, strings.Join(r.records, "\n"), "presence: down: session runs no monitor over /h/bob.wake")
	assert.Equal(t, 3, carrying(r.session.got()), "every unanswered check carried the contract, its monitor line among it")

	r.answer(t, "n1") // the session answers the check it was asked (the nonce is kept while unproved)
	r.step(BeatEvery)
	down, _ = r.watch.Down()
	assert.False(t, down, "an answer clears it")
}

// The inbox pass stamps a work card started when its job directory shows work begun: nothing
// while it shows none, one stamp when it gains a branch push (<card>@<gen>, her row as the
// holder), never a second while it stands, again after StartRetry when the server refused it,
// and a read or a card off her row never.
func TestAJobDirectoryGainingABranchPushStampsTheStart(t *testing.T) {
	t.Parallel()
	signs := map[string]JobSigns{}
	w := &StartWatch{Friend: "bob", Look: func(h HeldCard) JobSigns { return signs[h.Job] }}
	cards := []HeldCard{
		{Card: "c1", Job: "c1~15.g3", Col: "working", Gen: 3, Epoch: 15},
		{Card: "r1", Job: "r1~15", Col: "working", Kind: "read", Epoch: 15},
	}
	assert.Empty(t, w.Pass(t0, cards), "no worktree and no push: not started")

	signs["c1~15.g3"] = JobSigns{Pushed: true}
	signs["r1~15"] = JobSigns{Pushed: true}
	owed := w.Pass(t0.Add(BeatEvery), cards)
	require.Len(t, owed, 1)
	assert.Equal(t, []string{"progress", "--as", "friend.bob", "c1@3", "--epoch", "15"}, owed[0].Argv)
	assert.Equal(t, "its job c1~15.g3 shows a branch push", owed[0].Why)
	assert.Empty(t, w.Pass(t0.Add(2*BeatEvery), cards), "stamped once")

	w.Failed("c1~15.g3", t0.Add(2*BeatEvery))
	assert.Empty(t, w.Pass(t0.Add(3*BeatEvery), cards), "a refused stamp waits StartRetry")
	require.Len(t, w.Pass(t0.Add(2*BeatEvery+StartRetry), cards), 1, "then it is sent again")

	assert.Empty(t, w.Pass(t0.Add(time.Hour), nil), "the card left her row")
	require.Len(t, w.Pass(t0.Add(2*time.Hour), cards), 1, "dealt again, it is a card of this run's again")
	assert.Equal(t, "15", RowEpoch(cards))
	assert.Empty(t, RowEpoch(nil))
	assert.Equal(t, "NOVA_SPRINT_SERVER=s:1 nova-sprint progress --as friend.bob c1@3 --epoch 15", StartStamp("bob", "s:1", "c1", 3, "15"))
}

// A lane the daemon starts is stamped started at its start, before its turn prints anything:
// the daemon can see the start, so the card counts working from its first second.
func TestALaneItStartedIsStampedAtStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c1", "queued"}}, []string{"c1"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c1": true}, active: map[string]int{}, block: make(chan struct{})}
		r, _ := laneRig(t, h, 1)
		var mu sync.Mutex
		var stamped [][]Card
		r.d.Progress = func(_ context.Context, cs []Card) error {
			mu.Lock()
			defer mu.Unlock()
			stamped = append(stamped, cs)
			return nil
		}
		r.at[20] = func() { close(h.block) } // the turn never printed
		r.run(t, 30)
		mu.Lock()
		defer mu.Unlock()
		require.Len(t, stamped, 1, "stamped at its start, and nothing more while it printed nothing")
		require.Len(t, stamped[0], 1)
		assert.Equal(t, "c1", stamped[0][0].ID)
		assert.Equal(t, [][]string{{"progress", "--as", "friend.bob", "c1", "--epoch", "15"}}, ProgressArgv("bob", stamped[0]))
	})
}

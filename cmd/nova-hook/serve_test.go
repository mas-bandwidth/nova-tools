package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// msg is one message on a stream: its id, kind and subject.
type msg struct{ id, kind, subject string }

// stream is every message on name's bus stream, oldest first.
func (r *rig) stream(t *testing.T, name string) []msg {
	t.Helper()
	es, err := r.h.Client.XRange(context.Background(), bus.StreamOf(name), "-", "+").Result()
	require.NoError(t, err)
	var out []msg
	for _, e := range es {
		v := func(k string) string { s, _ := e.Values[k].(string); return s } // ignored: a missing field reads empty
		out = append(out, msg{v("id"), v("kind"), v("subject")})
	}
	return out
}

// count is how many messages on name's stream have a subject starting with prefix.
func (r *rig) count(t *testing.T, name, prefix string) int {
	t.Helper()
	n := 0
	for _, m := range r.stream(t, name) {
		if strings.HasPrefix(m.subject, prefix) {
			n++
		}
	}
	return n
}

// challenge is the nonce of the newest heartbeat challenge on name's stream.
func (r *rig) challenge(t *testing.T, name string) string {
	t.Helper()
	ms := r.stream(t, name)
	for i := len(ms) - 1; i >= 0; i-- {
		if n, ok := strings.CutPrefix(ms[i].subject, "HOOK CHALLENGE nonce="); ok {
			return n
		}
	}
	t.Fatalf("no HOOK CHALLENGE on %s's stream", name)
	return ""
}

// stepDownAfter is the --down-after of step's servers: an interval and a
// half, so the telling tests see a name silent since its hooking in go down
// at the third pass (the first whose silence is older), as they always have;
// the default (hook.DownAfter) has its own tests below.
const stepDownAfter = "90s"

// step is one interval: each server in turn runs one pass (the first takes
// it), each name in answering proves its challenge, the store's clock moves
// an interval on, and the answering names' holders renew.
func (r *rig) step(t *testing.T, servers []string, answering ...string) string {
	t.Helper()
	cli := r.cli()
	var out string
	for _, sv := range servers {
		res := cli.Run("serve", "--as", sv, "--once", "--down-after", stepDownAfter)
		require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
		out += res.Stdout
	}
	for _, n := range answering {
		if r.h.Client.Exists(context.Background(), hook.ChallengeKey(n)).Val() > 0 {
			cli.Do(t, "prove", "--as", n, "--nonce", r.challenge(t, n)).Exit(0)
		}
	}
	r.h.Pass(hook.BeatEvery)
	for _, n := range answering {
		r.h.Hook(t, n, r.h.State(t, n).Role)
	}
	return out
}

// TestServeRefusesAServerThatIsNotHooked pins that the server cannot run deaf.
func TestServeRefusesAServerThatIsNotHooked(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	res := r.cli().Run("serve", "--as", "ada", "--once")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "the heartbeat server must itself be hooked in")
	assert.Contains(t, res.Stdout+res.Stderr, "run: nova-hook hook --as ada --role comms")
}

// TestAFriendDownIsOneJudgmentToTheCoordinator pins the coordinator's half
// (tla/HookBeat.tla, Tell and Judge), with two servers at every step: the
// challenge goes to the name's own stream and never on the server's lines; a
// silent friend makes one FRIEND DOWN judgment for the coordinator and one
// message for the others; it is pushed again as OVERDUE once past its due,
// never twice; an answer closes it; the friend's return is one FRIEND BACK.
func TestAFriendDownIsOneJudgmentToTheCoordinator(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	require.NoError(t, r.h.Client.SAdd(context.Background(), "friends", "carl").Err())
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	r.h.Hook(t, "carl", hook.Comms)
	servers := []string{"ada", "carl"}
	var out string
	for range 18 {
		out += r.step(t, servers, "ada", "carl")
	}
	assert.NotContains(t, out, "nonce", "the server never prints a nonce")
	assert.Equal(t, 18, strings.Count(out, "event=skip"), "the second server's pass shares the first's interval, every step")
	require.Equal(t, hook.StateUnhooked, r.h.State(t, "bob").State)
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND DOWN bob: no answer in 1m30s (1 challenges missed)"))
	assert.Equal(t, 1, r.count(t, "carl", "FRIEND DOWN bob: no answer in 1m30s (1 challenges missed)"))
	var judgment msg
	for _, m := range r.stream(t, "ada") {
		if strings.HasPrefix(m.subject, "FRIEND DOWN") {
			judgment = m
		}
	}
	assert.Equal(t, bus.KindJudgment, judgment.kind)

	for range 4 { // past --judge-within (2m): OVERDUE once, never again
		r.step(t, servers, "ada", "carl")
	}
	assert.Equal(t, 1, r.count(t, "ada", "OVERDUE FRIEND DOWN bob"))
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND DOWN bob"), "the judgment itself is not sent again")

	r.step(t, servers, "ada", "carl")
	open, err := r.h.Client.HLen(context.Background(), hook.JudgmentsKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), open, "the re-push answers nothing: the judgment stays open")
	b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
	_, err = b.Send(context.Background(), bus.Message{From: "ada", To: []string{"ada"}, Re: judgment.id, Subject: "wait", Body: "wait"})
	require.NoError(t, err)
	out = r.step(t, servers, "ada", "carl")
	assert.Contains(t, out, "event=answered id="+judgment.id)
	n, err := r.h.Client.HLen(context.Background(), hook.JudgmentsKey).Result()
	require.NoError(t, err)
	assert.Zero(t, n, "an answered judgment is closed")

	r.h.Hook(t, "bob", hook.Comms)
	for range 3 {
		r.step(t, servers, "ada", "bob", "carl")
	}
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND BACK bob"))
	assert.Equal(t, 1, r.count(t, "carl", "FRIEND BACK bob"))
	assert.Zero(t, r.count(t, "bob", "FRIEND BACK"), "the one who came back is not told of it")
}

// TestACoordinatorDownIsOneJudgmentToEveryFriend pins who takes over: the
// coordinator silent past --down-after makes one COORDINATOR DOWN judgment for every
// other live name, with two servers, never repeated.
func TestACoordinatorDownIsOneJudgmentToEveryFriend(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	require.NoError(t, r.h.Client.SAdd(context.Background(), "friends", "carl").Err())
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	r.h.Hook(t, "carl", hook.Comms)
	for range 38 {
		r.step(t, []string{"bob", "carl"}, "bob", "carl")
	}
	for _, n := range []string{"bob", "carl"} {
		assert.Equal(t, 1, r.count(t, n, "COORDINATOR DOWN ada: who takes over?"), n)
		assert.Equal(t, 1, r.count(t, n, "COORDINATOR IS ada"), n)
	}
	for _, m := range r.stream(t, "bob") {
		if strings.HasPrefix(m.subject, "COORDINATOR DOWN") {
			assert.Equal(t, bus.KindJudgment, m.kind)
		}
	}
}

// TestFriendsJSONIsTheRecord pins nova-hook friends: every name from the hook
// record, by the store's time, the coordinator and the server's age.
func TestFriendsJSONIsTheRecord(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	r.step(t, []string{"ada"}, "ada")
	r.step(t, []string{"ada"}, "ada")
	for range 16 {
		r.step(t, []string{"ada"}, "ada")
	} // silence and answer deadline pass
	res := r.cli().Run("friends", "--json")
	require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
	var got struct {
		Facts map[string]any `json:"facts"`
		Items []struct {
			Fields map[string]any `json:"fields"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got))
	assert.Equal(t, "ada", got.Facts["coordinator"])
	assert.Equal(t, true, got.Facts["serve_alive"])
	assert.Equal(t, "1m0s", got.Facts["serve_age"])
	require.Len(t, got.Items, 2)
	ada, bob := got.Items[0].Fields, got.Items[1].Fields
	st := r.h.State(t, "ada")
	assert.Equal(t, map[string]any{"name": "ada", "role": "coordinator", "state": "up", "since": stamp(st.Armed), "reason": "",
		"last_challenge_at": stamp(st.BeatAt), "last_answer_at": stamp(st.AnswerAt), "misses": float64(0)}, ada)
	sb := r.h.State(t, "bob")
	assert.Equal(t, map[string]any{"name": "bob", "role": "comms", "state": "down", "since": stamp(sb.Unhooked), "reason": "no answer in 1m30s (1 challenges missed)",
		"last_challenge_at": stamp(sb.BeatAt), "last_answer_at": stamp(sb.AnswerAt), "misses": float64(1)}, bob)
}

// TestStatusSaysNoChallengeWhenTheServerIsGone pins the client's timeout on
// the store's clock: two intervals with no challenge and status --as fails.
func TestStatusSaysNoChallengeWhenTheServerIsGone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	cli := r.cli()
	r.h.Hook(t, "ada", hook.Comms)
	cli.Do(t, "status", "--as", "ada").Exit(0)
	cli.Do(t, "serve", "--as", "ada", "--once").Exit(0)
	if r.h.Client.Exists(context.Background(), hook.ChallengeKey("ada")).Val() > 0 {
		cli.Do(t, "prove", "--as", "ada", "--nonce", r.challenge(t, "ada")).Exit(0)
	}
	r.h.Pass(2 * hook.BeatEvery)
	r.h.Hook(t, "ada", hook.Comms)
	cli.Do(t, "status", "--as", "ada").Exit(0) // two intervals exactly: not yet
	r.h.Pass(time.Second)
	r.h.Hook(t, "ada", hook.Comms)
	res := cli.Run("status", "--as", "ada")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "ada is not hooked in: hook server stale")
}

// TestAClientThatLosesTheServerSaysItOnceAndReportsItOnce pins the hook
// client's own timer (tla/HookBeat.tla, ClientTimeout): no move of the
// server's row for two intervals by this client's clock is one NOT CONNECTED
// judgment into its session; the next move is one line back, and the episode
// is reported to the server once.
func TestAClientThatLosesTheServerSaysItOnceAndReportsItOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.Hook(t, "ada", hook.Comms)
	w := r.world()
	var out bytes.Buffer
	c := &tool.Call{Stdout: &out}
	s := &session{hooks: r.h.Store, as: "ada"}
	var lk link
	look := func() { w.watchServer(c, s, hook.Comms, &lk) }
	look() // no server ever passed: nothing to lose
	r.cli().Do(t, "serve", "--as", "ada", "--once").Exit(0)
	look()        // the first contact
	for range 8 { // the server stops; this client's clock runs on
		r.advance(30 * time.Second)
		look()
	}
	r.h.Pass(5 * time.Minute)
	r.h.Hook(t, "ada", hook.Comms)
	r.cli().Do(t, "serve", "--as", "ada", "--once").Exit(0)
	r.advance(30 * time.Second)
	look()
	look()
	assert.Equal(t, 1, strings.Count(out.String(), "state=not-connected"), out.String())
	assert.Contains(t, out.String(), "judgment: NOT CONNECTED TO THE SERVER since")
	assert.Equal(t, 1, strings.Count(out.String(), "state=connected"), out.String())
	es, err := r.h.Client.XRange(context.Background(), hook.EventsKey, "-", "+").Result()
	require.NoError(t, err)
	n := 0
	for _, e := range es {
		if e.Values["event"] == "not-connected" {
			n++
		}
	}
	assert.Equal(t, 1, n, "the episode is reported once")
}

// TestATickExpiresAnOverdueAskOnce pins the asks' clock: the server's pass
// expires an ask past its deadline, once however many passes run.
func TestATickExpiresAnOverdueAskOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	cli := r.cli()
	r.h.Hook(t, "ada", hook.Comms)
	b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
	_, err := b.Send(context.Background(), bus.Message{From: "ada", To: []string{"bob"}, Kind: bus.KindAsk, Wait: time.Minute, Subject: "are you there", Body: "answer by a minute"})
	require.NoError(t, err)
	r.h.Pass(time.Minute + time.Second)
	r.h.Hook(t, "ada", hook.Comms)
	var out string
	for range 3 {
		out += cli.Do(t, "serve", "--as", "ada", "--once").Exit(0).Stdout
	}
	r.h.Pass(hook.BeatEvery)
	r.h.Hook(t, "ada", hook.Comms)
	out += cli.Do(t, "serve", "--as", "ada", "--once").Exit(0).Stdout
	assert.Equal(t, 1, strings.Count(out, "event=expired"), out)
	assert.Equal(t, 1, r.count(t, "ada", "ASK TIMEOUT"), "one timeout reaches the sender")
}

// TestAFailedTellIsToldAgainOnceAtTheNextPass pins mark-after-delivery
// (tla/HookBeat.tla, the finding of the model run): a pass whose messages do
// not go out marks nothing told, the next pass tells the same change, and once
// delivered it is never told again.
func TestAFailedTellIsToldAgainOnceAtTheNextPass(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	require.NoError(t, r.h.Client.SAdd(context.Background(), "friends", "carl").Err())
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	r.h.Hook(t, "carl", hook.Comms)
	r.step(t, []string{"ada"}, "ada", "carl")
	r.step(t, []string{"ada"}, "ada", "carl")
	for range 15 {
		r.step(t, []string{"ada"}, "ada", "carl")
	}
	r.failTells.Store(true) // the pass that unhooks bob: its tells do not go out
	r.step(t, []string{"ada"}, "ada", "carl")
	require.Equal(t, hook.StateUnhooked, r.h.State(t, "bob").State)
	assert.Zero(t, r.count(t, "ada", "FRIEND DOWN bob"))
	r.failTells.Store(false)
	for range 3 {
		r.step(t, []string{"ada"}, "ada", "carl")
	}
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND DOWN bob"), "told at the next pass, once")
	assert.Equal(t, 1, r.count(t, "carl", "FRIEND DOWN bob"), "told at the next pass, once")
}

// TestAStaleServerReadsEveryoneDown pins that no reader trusts the last
// written state once the heartbeat server is gone (the finding of the model
// run): hook:serve older than two intervals by the store's TIME, read at the
// query, and every name reads down (hook server stale).
func TestAStaleServerReadsEveryoneDown(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.Hook(t, "ada", hook.Coordinator)
	r.step(t, []string{"ada"}, "ada") // the server passed a minute ago
	r.h.Pass(hook.BeatEvery)
	r.h.Hook(t, "ada", hook.Coordinator)
	res := r.cli().Run("friends")
	require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
	assert.Contains(t, res.Stdout, "state=up", "two intervals exactly: not yet stale")
	r.h.Pass(time.Second)
	r.h.Hook(t, "ada", hook.Coordinator)
	res = r.cli().Run("friends")
	require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
	assert.Contains(t, res.Stdout, "serve_alive=false")
	assert.Contains(t, res.Stdout, `name=ada role=coordinator state=down`)
	assert.Contains(t, res.Stdout, `reason="hook server stale"`)
	res = r.cli().Run("status")
	assert.Contains(t, res.Stdout, "state=down", res.Stdout)
	res = r.cli().Run("status", "--as", "ada")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "hook server stale")
}

// TestALostAnswerIsNeverToldTwice pins the message and its told mark as one
// write (tla/HookBeat.tla, NeverToldTwice; the model's BadMarkAfterSend): a
// send whose reply is lost after the store took it is found sent by its
// once-key, and no later pass tells it again; the OVERDUE re-push the same.
func TestALostAnswerIsNeverToldTwice(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	require.NoError(t, r.h.Client.SAdd(context.Background(), "friends", "carl").Err())
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	r.h.Hook(t, "carl", hook.Comms)
	r.loseAnswers.Store(true)
	for range 23 { // bob down after its quiet and answer windows; the judgment overdue past two minutes
		r.step(t, []string{"ada"}, "ada", "carl")
	}
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND DOWN bob"))
	assert.Equal(t, 1, r.count(t, "carl", "FRIEND DOWN bob"))
}

// renew is a name's holder renewing its bus lease at the record's epoch, as
// its hook loop does every RenewEvery, with no answer: the answer is the
// model's alone (nova-hook prove).
func (r *rig) renew(t *testing.T, name string) {
	t.Helper()
	st := r.h.State(t, name)
	require.NoError(t, r.h.Store.Renew(context.Background(), name, st.Role, hook.Bus, st.Epoch, "test"))
}

// beats runs n intervals of one server, ada, which answers every one; bob
// proves his challenge at the intervals answer says, and is renewed by his
// holder throughout. It answers bob's status after each interval's pass, and
// whether he read down (hook.Served.Down) just before it.
func (r *rig) beats(t *testing.T, n int, answer func(i int) bool, serve ...string) (after []hook.Status, downs []string) {
	t.Helper()
	cli := r.cli()
	for i := range n {
		downs = append(downs, r.h.State(t, "bob").Down)
		cli.Do(t, append([]string{"serve", "--as", "ada", "--once"}, serve...)...).Exit(0)
		st := r.h.State(t, "bob")
		after = append(after, st)
		if r.h.Client.Exists(context.Background(), hook.ChallengeKey("ada")).Val() > 0 {
			cli.Do(t, "prove", "--as", "ada", "--nonce", r.challenge(t, "ada")).Exit(0)
		}
		if st.State != hook.StateUnhooked && answer(i) && r.h.Client.Exists(context.Background(), hook.ChallengeKey("bob")).Val() > 0 {
			cli.Do(t, "prove", "--as", "bob", "--nonce", r.challenge(t, "bob")).Exit(0)
		}
		r.h.Pass(hook.BeatEvery)
		r.h.Hook(t, "ada", hook.Coordinator)
		if st.State != hook.StateUnhooked {
			r.renew(t, "bob")
		}
	}
	return after, downs
}

// TestAFriendAnsweringEveryFourMinutesStaysUp pins the down-after rule of
// 2026-10-14 (tla/HookBeat.tla, NoDownBeforeDownAfter): a harness that cannot
// answer mid-turn answers between turns, every four minutes here; under the
// default --down-after (5m) it is challenged every minute, misses three in a
// row each time, and is never unhooked, never told down, and never reads down.
func TestAFriendAnsweringEveryFourMinutesStaysUp(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	after, downs := r.beats(t, 13, func(i int) bool { return i%4 == 3 })
	most := 0
	for i, st := range after {
		assert.NotEqual(t, hook.StateUnhooked, st.State, "interval %d", i)
		most = max(most, st.Misses)
	}
	for i, d := range downs {
		assert.Empty(t, d, "interval %d: bob read down", i)
	}
	assert.Zero(t, r.count(t, "ada", "FRIEND DOWN"))
	assert.Zero(t, most, "one pending nonce is answered before its deadline")
}

// TestASilentFriendGoesDownPastFiveMinutesNotBefore pins the default: a
// friend silent from the moment it hooked in stays hooked through the pass
// five minutes in (not older than 5m) and is unhooked at the next, with the reason.
func TestASilentFriendGoesDownPastFiveMinutesNotBefore(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	after, _ := r.beats(t, 19, func(int) bool { return false })
	assert.Len(t, upTo(after), 17, "quiet for 10m, challenge at 11m, full 5m answer window")
	assert.Equal(t, hook.StateUnhooked, after[17].State, "unhooked at the first pass past five minutes")
	st := r.h.State(t, "bob")
	assert.Equal(t, "no answer in 5m0s (1 challenges missed)", st.Reason)
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND DOWN bob: no answer in 5m0s (1 challenges missed)"))
}

// TestDownAfterIsHonoured pins --down-after: at 3m a silent friend is unhooked
// at the first pass past three minutes, not at two; one under --every is refused.
func TestDownAfterIsHonoured(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	after, _ := r.beats(t, 19, func(int) bool { return false }, "--down-after", "3m")
	assert.Len(t, upTo(after), 17, "the answer deadline still applies with a shorter down-after")
	assert.Equal(t, hook.StateUnhooked, after[17].State)
	assert.Equal(t, "no answer in 3m0s (1 challenges missed)", r.h.State(t, "bob").Reason)
	res := r.cli().Run("serve", "--as", "ada", "--once", "--down-after", "30s")
	assert.Equal(t, 2, res.Code, res.Stdout+res.Stderr)
	assert.Contains(t, res.Stdout+res.Stderr, "--down-after 30s is under --every 1m0s")
}

// upTo is the minute of each pass before the first that unhooked, as "<i>m".
func upTo(after []hook.Status) []string {
	var out []string
	for i, st := range after {
		if st.State == hook.StateUnhooked {
			break
		}
		out = append(out, fmt.Sprintf("%dm", i))
	}
	return out
}

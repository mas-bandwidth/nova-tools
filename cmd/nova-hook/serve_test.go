package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
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

// hasChallenge is whether a challenge message for name is on its stream.
func (r *rig) hasChallenge(t *testing.T, name string) bool {
	for _, m := range r.stream(t, name) {
		if strings.HasPrefix(m.subject, "HOOK CHALLENGE nonce=") {
			return true
		}
	}
	return false
}

// stepDownAfter is the --down-after of step's servers: an interval and a
// half, so the telling tests see a name silent since its hooking in go down
// at the third pass (the first whose silence is older), as they always have;
// the default (hook.DownAfter) has its own tests below.
const stepDownAfter = "90s"

// step is one interval: each server in turn runs one pass (the first takes
// it), each name in answering proves its challenge, the store's clock moves
// an interval on, the answering names' holders renew, and each name in r.held
// that is still hooked has its bus lease renewed with no answer (its service
// runs, its model is silent).
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
		if r.h.Client.Exists(context.Background(), hook.ChallengeKey(n)).Val() > 0 && r.hasChallenge(t, n) { // a challenge whose send failed has no nonce to answer
			cli.Do(t, "prove", "--as", n, "--nonce", r.challenge(t, n)).Exit(0)
		}
	}
	r.h.Pass(hook.BeatEvery)
	for _, n := range answering {
		r.h.Hook(t, n, r.h.State(t, n).Role)
	}
	for _, n := range r.held {
		if !slices.Contains(answering, n) && r.h.State(t, n).State != hook.StateUnhooked {
			r.renew(t, n)
		}
	}
	return out
}

// TestServeNeedsOnlyItsStore pins that no daemon gates on another's liveness
// or a model's proof: a serve pass runs, and the heartbeat row is written,
// whatever the server name's own hook record is: absent, partial (armed and
// never proven) or unhooked.
func TestServeNeedsOnlyItsStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, r *rig)
	}{
		{"absent", func(t *testing.T, r *rig) {}},
		{"partial", func(t *testing.T, r *rig) {
			_, _, err := r.h.Store.HookAs(ctx, "ada", hook.Comms, "ada") // armed, no proof, no push live
			require.NoError(t, err)
		}},
		{"unhooked", func(t *testing.T, r *rig) {
			r.h.Hook(t, "ada", hook.Comms)
			r.h.Unhook(t, "ada")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t)
			tc.setup(t, r)
			res := r.cli().Run("serve", "--as", "ada", "--once")
			require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
			assert.NotContains(t, res.Stdout+res.Stderr, "must itself be hooked in")
			at, err := r.h.Client.HGet(ctx, hook.ServeKey, "at").Result()
			require.NoError(t, err)
			assert.NotEmpty(t, at, "the pass ran and stamped the heartbeat row")
		})
	}
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
	r.held = []string{"bob"} // its service renews its lease; its model goes silent
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
	r.held = []string{"ada"} // its service renews its lease; its model goes silent
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
	r.held = []string{"bob"} // its service renews its lease; its model goes silent
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

// TestServeDryRunPlansAPassAndWritesNothing pins --dry-run: the plan names the
// challenges, the unhook and the telling the next pass would carry out, from
// the same script, and writes nothing: no challenge reaches a stream, the
// interval stays free for the real pass, and a name the plan unhooks is still
// hooked until that pass. A challenge is due only past QuietAfter of silence
// and a name goes down only past the answer bound (tla/HookBeat.tla,
// NoChallengeWhileTalking, Pass), so the test runs the clock to each.
func TestServeDryRunPlansAPassAndWritesNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.held = []string{"bob"} // its service renews its lease; its model goes silent
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	cli := r.cli()

	quiet := int(hook.QuietAfter/hook.BeatEvery) + 1 // passes, one interval each, until bob's silence is past QuietAfter
	for range quiet {
		r.step(t, []string{"ada"}, "ada")
	}
	asked := r.h.MR.Dump()
	cli.Do(t, "serve", "--as", "ada", "--dry-run", "--down-after", stepDownAfter).Exit(0).
		Out("SERVE OK", "taken=true", "dry_run=true", "SERVE CHALLENGE name=bob").NotOut("nonce", "SERVE CHALLENGE name=ada")
	assert.Zero(t, r.count(t, "bob", "HOOK CHALLENGE"), "a dry run sends no challenge")
	assert.Equal(t, asked, r.h.MR.Dump(), "the plan of a challenge writes nothing (no nonce, pending deadline or delivery mark)")
	r.step(t, []string{"ada"}, "ada")
	assert.Equal(t, 1, r.count(t, "bob", "HOOK CHALLENGE"), "the real pass after a dry run is taken")
	for range int(hook.AnswerBound / hook.BeatEvery) { // the challenge stands unanswered past the answer bound
		r.step(t, []string{"ada"}, "ada")
	}

	before := r.h.MR.Dump()
	cli.Do(t, "serve", "--as", "ada", "--dry-run", "--down-after", stepDownAfter).Exit(0).
		Out("SERVE UNHOOK name=bob misses=1", "SERVE TELL audience=coordinator down=bob back=-").NotOut("SERVE CHALLENGE name=bob")
	assert.Equal(t, before, r.h.MR.Dump(), "the plan of an expired challenge and an unhook writes nothing (its miss, latch and delivery marks included)")
	require.NotEqual(t, hook.StateUnhooked, r.h.State(t, "bob").State, "the plan unhooks no one")
	assert.Zero(t, r.count(t, "ada", "FRIEND DOWN bob"), "the plan tells no one")
	r.step(t, []string{"ada"}, "ada")
	require.Equal(t, hook.StateUnhooked, r.h.State(t, "bob").State, "the real pass does what the plan said")
}

// TestAnArmedFriendIsBackOnlyWhenItsModelProves pins the ruling of
// 2026-10-16 (tla/HookBeat.tla, NoBackWithoutProof and NoFlapWithoutProof):
// a friend is up only while its model has proven a challenge. Down once, its
// supervised hook service re-arms it and renews its lease with no model
// behind it: no FRIEND BACK, to the coordinator or anyone; its unanswered
// challenge lapses and it is unhooked again: no second FRIEND DOWN; armed
// again and proven by its model: one FRIEND BACK.
func TestAnArmedFriendIsBackOnlyWhenItsModelProves(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newRig(t)
	require.NoError(t, r.h.Client.SAdd(ctx, "friends", "carl").Err())
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	r.h.Hook(t, "carl", hook.Comms)
	r.held = []string{"bob"}
	servers := []string{"ada"}
	until := func(what string, done func() bool, answering ...string) {
		t.Helper()
		for i := 0; !done(); i++ {
			require.Less(t, i, 40, what)
			r.step(t, servers, answering...)
		}
	}
	unhooked := func() bool { return r.h.State(t, "bob").State == hook.StateUnhooked }
	until("bob's silence unhooks him", unhooked, "ada", "carl")
	r.step(t, servers, "ada", "carl")
	require.Equal(t, 1, r.count(t, "ada", "FRIEND DOWN bob"))
	require.Equal(t, 1, r.count(t, "carl", "FRIEND DOWN bob"))

	arm := func() { // the service, not the model: the identity's word, then its holder's lease
		_, _, err := r.h.Store.HookAs(ctx, "bob", hook.Comms, "service")
		require.NoError(t, err)
		require.NoError(t, (&hook.Holder{Store: r.h.Store, Name: "bob", Role: hook.Comms, Pushes: hook.PushesOf(hook.Comms), By: "service"}).Start(ctx))
		require.Equal(t, hook.StateLive, r.h.State(t, "bob").State)
	}
	arm()
	r.step(t, servers, "ada", "carl")
	assert.True(t, r.hasChallenge(t, "bob"), "armed: challenged at once")
	until("bob's unanswered challenge unhooks him again", unhooked, "ada", "carl")
	r.step(t, servers, "ada", "carl")
	assert.Zero(t, r.count(t, "ada", "FRIEND BACK bob"), "armed by its service, never proven: not back")
	assert.Zero(t, r.count(t, "carl", "FRIEND BACK bob"))
	assert.Zero(t, r.count(t, "bob", "FRIEND DOWN"), "a down friend hears no broadcast")
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND DOWN bob"), "a lapse from live to unhooked while down is no second DOWN")
	assert.Equal(t, 1, r.count(t, "carl", "FRIEND DOWN bob"))

	arm()
	r.step(t, servers, "ada", "carl")
	assert.Zero(t, r.count(t, "ada", "FRIEND BACK bob"), "armed again: still down")
	r.step(t, servers, "ada", "bob", "carl") // the model proves
	r.step(t, servers, "ada", "bob", "carl")
	r.step(t, servers, "ada", "bob", "carl")
	assert.Equal(t, hook.StateProven, r.h.State(t, "bob").State)
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND BACK bob"), "proven: back, once")
	assert.Equal(t, 1, r.count(t, "carl", "FRIEND BACK bob"))
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND DOWN bob"))
}

// TestAFriendWhoseServiceStopsIsDownAtItsLeaseLapse pins the other way out of
// up (hook.Passed.Unhooked): a friend still hooked whose hook service stopped
// renewing has no live lease, so no longer reads proven, and is told down
// once with that reason, before any silence would unhook it.
func TestAFriendWhoseServiceStopsIsDownAtItsLeaseLapse(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	for range 3 {
		r.step(t, []string{"ada"}, "ada")
	}
	assert.Equal(t, hook.StateExpired, r.h.State(t, "bob").State)
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND DOWN bob: hooked, not proven (expired)"))
}

// TestAHookedCoordinatorNotProvenIsCoordinatorDown pins the coordinator's own
// down (tla/HookBeat.tla, CoordOf: the first coordinator up): a coordinator
// still hooked whose hook service stopped is not proven, so she is down; the
// other live names are asked who takes over, once, and she is sent no
// FRIEND DOWN about herself.
func TestAHookedCoordinatorNotProvenIsCoordinatorDown(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	require.NoError(t, r.h.Client.SAdd(context.Background(), "friends", "carl").Err())
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	r.h.Hook(t, "carl", hook.Comms)
	for range 5 {
		r.step(t, []string{"bob"}, "bob", "carl")
	}
	require.Equal(t, hook.StateExpired, r.h.State(t, "ada").State, "still hooked")
	for _, n := range []string{"bob", "carl"} {
		assert.Equal(t, 1, r.count(t, n, "COORDINATOR DOWN ada: who takes over?"), n)
	}
	assert.Zero(t, r.count(t, "ada", "FRIEND DOWN ada"), "no judgment about herself")
}

// TestADownWhileNoCoordinatorIsUpIsJudgedToTheNextOne pins that no change is
// dropped for want of a coordinator (tla/HookBeat.tla, JudgeCoord and
// DownJudgedToCoordinator): ada, the coordinator, reads down (her service
// stopped), then carl does; nobody is up to judge it, so the change waits,
// and when ada is back she gets the FRIEND DOWN carl judgment, once, and no
// FRIEND BACK about herself.
func TestADownWhileNoCoordinatorIsUpIsJudgedToTheNextOne(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	require.NoError(t, r.h.Client.SAdd(context.Background(), "friends", "carl").Err())
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	r.h.Hook(t, "carl", hook.Comms)
	servers := []string{"bob"}
	for range 3 { // ada's lease lapses: still hooked, not proven, down
		r.step(t, servers, "bob", "carl")
	}
	require.Equal(t, hook.StateExpired, r.h.State(t, "ada").State)
	require.Equal(t, 1, r.count(t, "carl", "COORDINATOR DOWN ada"))
	for range 3 { // then carl's
		r.step(t, servers, "bob")
	}
	require.Equal(t, hook.StateExpired, r.h.State(t, "carl").State)
	assert.Zero(t, r.count(t, "ada", "FRIEND DOWN carl"), "no coordinator up: nothing judged yet")
	r.h.Hook(t, "ada", hook.Coordinator) // ada is back
	for range 2 {
		r.step(t, servers, "ada", "bob")
	}
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND DOWN carl"), "the waiting change is judged to the coordinator up again")
	var j msg
	for _, m := range r.stream(t, "ada") {
		if strings.Contains(m.subject, "FRIEND DOWN carl") {
			j = m
		}
	}
	assert.Equal(t, bus.KindJudgment, j.kind)
	assert.Zero(t, r.count(t, "ada", "FRIEND BACK ada"), "her own return is no judgment for her")
}

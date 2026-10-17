package main

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeNow is the fake store's TIME: the instant a pass run now stamps its transitions with.
func (r *rig) storeNow(t *testing.T) string {
	t.Helper()
	now, err := r.h.Client.Time(context.Background()).Result()
	require.NoError(t, err)
	return now.UTC().Format(time.RFC3339)
}

// TestEveryFriendTransitionIsPushedToTheCoordinator pins the coordinator's
// push of every friend status change (tla/HookBeat.tla, TransitionPushedToCoordinator,
// NoFold and NeverPushedTwice) with the heartbeat server and the coordinator's
// hook under ONE name, as in production. Over a fake store, bus and clock, the
// coordinator's hook runs its real loop and the passes run inside it, one
// stage at a time, each after the lines of the one before arrived: every
// transition of every friend (proven to unhooked, back, live, proven, and a
// lapse to expired and back) reaches the loop as its own HOOK PUSH line naming
// the friend, the old state, the new state and the store's TIME of the pass
// that saw it; two friends that change in one pass are two lines; a send that
// fails is sent at the next pass and once; and what the coordinator sent itself
// with nova-bus send is not echoed.
func TestEveryFriendTransitionIsPushedToTheCoordinator(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ctx := context.Background()
	require.NoError(t, r.h.Client.SAdd(ctx, "friends", "carl").Err())
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Comms)
	r.h.Hook(t, "carl", hook.Comms)
	servers := []string{"ada"} // the server runs as the coordinator
	b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
	var want []string
	expect := func(name, old, new, at string) {
		want = append(want, "FRIEND STATE "+name+" "+old+"->"+new+" at="+at)
	}
	pass := func(answering ...string) string {
		at := r.storeNow(t)
		r.step(t, servers, answering...)
		return at
	}
	stages := []func(){
		func() { // the first pass writes the baseline: nothing is pushed for the states a fresh row finds
			pass("ada", "bob", "carl")
			pass("ada", "bob", "carl")
			assert.Empty(t, r.stateLines(t))
			// silent friends are challenged only past their quiet window (tla/HookBeat.tla,
			// NoChallengeWhileTalking): their leases lapse first, then the unanswered challenge unhooks them
			var expired, down string
			for i := 0; i < 25 && down == ""; i++ {
				at := pass("ada")
				switch len(r.stateLines(t)) {
				case 2:
					if expired == "" {
						expired = at
					}
				case 4:
					down = at
				}
			}
			require.NotEmpty(t, down, "both silent friends go down")
			expect("bob", "proven", "expired", expired)
			expect("carl", "proven", "expired", expired)
			expect("bob", "expired", "unhooked", down)
			expect("carl", "expired", "unhooked", down)
		},
		func() { // a send that fails is not sent, and is sent at the next pass, once
			require.Equal(t, hook.StateUnhooked, r.h.State(t, "bob").State)
			_, _, err := r.h.Store.HookAs(ctx, "bob", hook.Comms, "hooktest") // bob is back, not yet proven: live
			require.NoError(t, err)
			require.NoError(t, (&hook.Holder{Store: r.h.Store, Name: "bob", Role: hook.Comms, Pushes: hook.PushesOf(hook.Comms), By: "hooktest"}).Start(ctx))
			require.Equal(t, hook.StateLive, r.h.State(t, "bob").State)
			r.failTells.Store(true)
			pass("ada")
			assert.Len(t, r.stateLines(t), 4, "the failed send put nothing on the stream")
			r.failTells.Store(false)
			// each pass here is a new server process (serve --once), which holds no transition over: the retry
			// finds bob's change again against the last pushed mark, and stamps it with its own pass's time
			at := pass("ada", "bob") // the retry sees bob live; its answer proves it for the next pass
			expect("bob", "unhooked", "live", at)
		},
		func() { // bob proves, carl hooks in: two friends change in one pass
			r.h.Hook(t, "carl", hook.Comms)
			at := pass("ada", "bob")
			expect("bob", "live", "proven", at)
			expect("carl", "unhooked", "proven", at)
		},
		func() { // a lapse to expired and the recovery are two lines each
			r.h.Pass(hook.LeaseTTL + time.Second) // every lease lapses; the coordinator's own is held again
			r.h.Hook(t, "ada", hook.Coordinator)
			r.h.Evidence(t, "bob")
			r.h.Evidence(t, "carl")
			at := pass("ada")
			expect("bob", "proven", "expired", at)
			expect("carl", "proven", "expired", at)
		},
		func() {
			r.h.Hook(t, "bob", hook.Comms)
			r.h.Hook(t, "carl", hook.Comms)
			at := pass("ada", "bob", "carl")
			expect("bob", "expired", "proven", at)
			expect("carl", "expired", "proven", at)
		},
		func() { // the coordinator's own note to itself is not echoed; a message from a friend is
			_, err := b.Send(ctx, bus.Message{From: "ada", To: []string{"ada"}, Kind: bus.KindStatus, Subject: "note to self", Body: "x"})
			require.NoError(t, err)
			_, err = b.Send(ctx, bus.Message{From: "bob", To: []string{"ada"}, Subject: "hello", Body: "x"})
			require.NoError(t, err)
		},
	}
	var got, pushes []string
	next, helloOwed := 0, false
	out := &streamOut{}
	out.each = func(l string) {
		if !strings.HasPrefix(l, "HOOK PUSH") {
			if strings.HasPrefix(l, "HOOK ARMED") && next == 0 {
				stages[0]()
				next = 1
			}
			return
		}
		pushes = append(pushes, l)
		if strings.Contains(l, "FRIEND STATE ") {
			got = append(got, l)
		}
		if next < len(stages) && len(got) == len(want) {
			stages[next]()
			next++
			helloOwed = next == len(stages)
		}
		if strings.Contains(l, `subject="hello"`) || (next == len(stages) && !helloOwed && len(got) == len(want)) {
			r.cancel()
		}
	}
	code := run([]string{"hook", "--as", "ada", "--role", "coordinator"}, strings.NewReader(""), out, io.Discard, r.world())
	assert.Equal(t, 0, code, out.all.String())
	require.Len(t, got, len(want), strings.Join(pushes, "\n"))
	for i, w := range want {
		assert.Contains(t, got[i], "from=ada subject=\""+w+"\"", "line %d", i)
		assert.Contains(t, got[i], "push=bus", "line %d", i)
	}
	for _, l := range pushes {
		assert.NotContains(t, l, "note to self", "what the coordinator sent itself is not echoed")
	}
	assert.True(t, slices.ContainsFunc(pushes, func(l string) bool { return strings.Contains(l, `subject="hello"`) }), "a friend's message is shown")
	for _, m := range r.stream(t, "ada") {
		if strings.HasPrefix(m.subject, "FRIEND STATE ") {
			assert.Equal(t, bus.KindStatus, m.kind, "a status line, never a judgment: nothing answers it and nothing is OVERDUE")
		}
		assert.NotContains(t, m.subject, "OVERDUE FRIEND STATE")
	}
}

// stateLines is the FRIEND STATE subjects now on the coordinator's stream, oldest first.
func (r *rig) stateLines(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, m := range r.stream(t, "ada") {
		if strings.HasPrefix(m.subject, "FRIEND STATE ") {
			out = append(out, m.subject)
		}
	}
	return out
}

// TestAMessageFromTheHooksOwnNameIsShownOnlyWhenTheServerSentIt pins the loop's
// rule for every route that filters (the stream loop, the Codex backlog; Grok
// takes the stream loop's): by kind and subject, the heartbeat server's
// challenge, friend-status line, judgment and OVERDUE are its own lines under
// the coordinator's name, and anything else the session sent itself is not.
func TestAMessageFromTheHooksOwnNameIsShownOnlyWhenTheServerSentIt(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		m    bus.Message
		want bool
	}{
		{bus.Message{From: "ada", Subject: "HOOK CHALLENGE nonce=x"}, true},
		{bus.Message{From: "ada", Kind: bus.KindStatus, Subject: "HOOK CHALLENGE nonce=x"}, true}, // as the bus stores it
		{bus.Message{From: "ada", Kind: bus.KindStatus, Subject: "FRIEND STATE bob proven->unhooked at=2026-10-15T14:00:00Z"}, true},
		{bus.Message{From: "ada", Kind: bus.KindJudgment, Subject: "FRIEND DOWN bob: no answer"}, true},
		{bus.Message{From: "ada", Kind: bus.KindJudgment, Subject: "FRIEND BACK bob"}, true},
		{bus.Message{From: "ada", Kind: bus.KindJudgment, Subject: "OVERDUE FRIEND BACK bob"}, true},
		{bus.Message{From: "ada", Kind: bus.KindJudgment, Subject: "COORDINATOR DOWN ada: who takes over?"}, true},
		{bus.Message{From: "ada", Kind: bus.KindStatus, Subject: "FRIEND BACK bob"}, true},            // a kind-less judgment, as the bus stores it
		{bus.Message{From: "ada", Kind: bus.KindStatus, Subject: "FRIEND DOWN bob: no answer"}, true}, // the fleet's v1.4.5 wire form, 2026-10-16
		{bus.Message{From: "ada", Kind: bus.KindStatus, Subject: "note to self"}, false},
		{bus.Message{From: "ada", Subject: "hello"}, false},
		{bus.Message{From: "ada", Kind: bus.KindJudgment, Subject: "my own judgment"}, false},
		{bus.Message{From: "bob", Kind: bus.KindStatus, Subject: "FRIEND STATE bob proven->unhooked at=x"}, false},
	} {
		assert.Equal(t, c.want, serverSent(c.m, "ada"), "%s %q", c.m.Kind, c.m.Subject)
	}
}

// The heartbeat server's challenge, as the bus stores it, passes the loop's
// own-name filter. A send with no kind is stored as kind status (bus.Bus.check),
// so the server's kind-less challenge reaches every stream as status, and a
// filter that took challenges only when kind-less passed none of them.
// Dogfood 2026-10-16: the claude route never saw the server's challenge
// 01M52YTW63V92PTE140CEKHBJF (kind=status on the wire); only its own
// bootstraps, which never cross the bus, became notices.
func TestTheServersChallengeAsStoredIsShownUnderTheOwnName(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.world()
	b := &bus.Bus{Store: bus.Redis{C: r.h.Client}}
	// the message pass sends (serve.go), word for word
	m := bus.Message{From: "ada", To: []string{"ada"}, Subject: "HOOK CHALLENGE nonce=e9c904dbf7557346",
		Body: notice.ChallengeText("ada", "e9c904dbf7557346", hook.AnswerBound), Once: hook.DeliveryOnce("ada", 1, "e9c904dbf7557346")}
	_, err := w.deliver(b, m)
	require.NoError(t, err)
	es, err := r.h.Client.XRange(context.Background(), bus.StreamOf("ada"), "-", "+").Result()
	require.NoError(t, err)
	require.Len(t, es, 1)
	fields := map[string]string{}
	for k, v := range es[0].Values {
		fields[k], _ = v.(string)
	}
	got := bus.Parse(fields)
	require.Equal(t, bus.KindStatus, got.Kind, "the bus stores a kind-less send as status")
	assert.True(t, serverSent(got, "ada"), "the loop shows the server's challenge to the Core")
	nonce, ok := notice.Canonical(got, "ada")
	assert.True(t, ok, "and it is canonical, so the Core can hold it")
	assert.Equal(t, "e9c904dbf7557346", nonce)
}

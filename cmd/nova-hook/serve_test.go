package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subjects is every subject on name's bus stream, oldest first.
func (r *rig) subjects(t *testing.T, name string) []string {
	t.Helper()
	es, err := r.h.Client.XRange(context.Background(), bus.StreamOf(name), "-", "+").Result()
	require.NoError(t, err)
	var out []string
	for _, e := range es {
		v, _ := e.Values["subject"].(string) // ignored: an entry with no subject is listed empty
		out = append(out, v)
	}
	return out
}

// challenge is the nonce of the newest heartbeat challenge on name's stream.
func (r *rig) challenge(t *testing.T, name string) string {
	t.Helper()
	ss := r.subjects(t, name)
	for i := len(ss) - 1; i >= 0; i-- {
		if n, ok := strings.CutPrefix(ss[i], "HOOK CHALLENGE nonce="); ok {
			return n
		}
	}
	t.Fatalf("no HOOK CHALLENGE on %s's stream: %v", name, ss)
	return ""
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

// TestServeChallengesOnTheBusAndUnhooksTheSilent pins the server's pass
// end to end (tla/HookBeat.tla): the challenge goes to the name's own bus
// stream (never on the server's lines), a session that proves with it stays
// hooked, one that stays silent is unhooked at the third pass, and the
// server's notify name is told the unhooked set once.
func TestServeChallengesOnTheBusAndUnhooksTheSilent(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	cli := r.cli()
	r.h.Hook(t, "ada", hook.Comms)
	r.h.Hook(t, "bob", hook.Comms)
	var out string
	for i := range 3 {
		res := cli.Run("serve", "--as", "ada", "--once")
		require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
		out += res.Stdout
		cli.Do(t, "prove", "--as", "ada", "--nonce", r.challenge(t, "ada")).Exit(0)
		r.h.Pass(hook.BeatEvery)
		r.h.Hook(t, "ada", hook.Comms) // its holder's renewal
		if i < 2 {
			assert.NotContains(t, res.Stdout, "nonce", "the server never prints a nonce")
		}
	}
	assert.Contains(t, out, "SERVE CHALLENGE name=bob epoch=1 misses=1")
	assert.Contains(t, out, `SERVE UNHOOKED name=bob reason="missed 2 challenges"`)
	assert.Equal(t, 1, strings.Count(out, "SERVE TOLD to=ada unhooked=bob"))
	assert.Equal(t, hook.StateUnhooked, r.h.State(t, "bob").State)
	assert.Equal(t, hook.StateProven, r.h.State(t, "ada").State)
	assert.Equal(t, 1, strings.Count(strings.Join(r.subjects(t, "ada"), "\n"), "HOOK UNHOOKED bob"))

	cli.Do(t, "serve", "--as", "ada", "--once").Exit(0)
	res := cli.Run("serve", "--as", "ada", "--once")
	assert.Contains(t, res.Stdout, "SERVE SKIP", "a second pass inside the interval is not taken")
}

// TestStatusSaysNoChallengeWhenTheServerIsGone pins the client's timeout
// (tla/HookBeat.tla, ClientTimeout): two intervals with no challenge and
// status --as fails, naming the age.
func TestStatusSaysNoChallengeWhenTheServerIsGone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	cli := r.cli()
	r.h.Hook(t, "ada", hook.Comms)
	cli.Do(t, "status", "--as", "ada").Exit(0)
	cli.Do(t, "serve", "--as", "ada", "--once").Exit(0)
	cli.Do(t, "prove", "--as", "ada", "--nonce", r.challenge(t, "ada")).Exit(0)
	r.h.Pass(2 * hook.BeatEvery)
	r.h.Hook(t, "ada", hook.Comms)
	cli.Do(t, "status", "--as", "ada").Exit(0) // two intervals exactly: not yet
	r.h.Pass(time.Second)
	r.h.Hook(t, "ada", hook.Comms)
	res := cli.Run("status", "--as", "ada")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "NO CHALLENGE FOR 2m1s: the heartbeat server last passed 2m1s ago")
	cli.Do(t, "status").Exit(0).Out("serve_age=2m1s")
}

// TestATickExpiresAnOverdueAskOnce pins the asks' clock: the server's pass
// expires an ask past its deadline, and a second server's pass, or the next
// one, never delivers that timeout again.
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
		res := cli.Run("serve", "--as", "ada", "--once")
		require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
		out += res.Stdout
	}
	r.h.Pass(hook.BeatEvery)
	r.h.Hook(t, "ada", hook.Comms)
	out += cli.Do(t, "serve", "--as", "ada", "--once").Exit(0).Stdout
	assert.Equal(t, 1, strings.Count(out, "SERVE EXPIRED"), out)
	n := 0
	for _, s := range r.subjects(t, "ada") {
		if strings.HasPrefix(s, "ASK TIMEOUT") {
			n++
		}
	}
	assert.Equal(t, 1, n, "one timeout reaches the sender")
}

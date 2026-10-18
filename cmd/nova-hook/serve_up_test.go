package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/hook/hooktest"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upStart is where the FRIEND UP tests start the clock: at 1000, never 0, so
// a time left zero reads as long past at once.
var upStart = time.Unix(1000, 0).UTC()

// newUpRig is newRig on a clock that starts at upStart: a fake store over
// net.Pipe, no socket and no real time.
func newUpRig(t *testing.T) *rig {
	t.Helper()
	h := hooktest.NewAt(t, upStart)
	require.NoError(t, h.Client.SAdd(context.Background(), "friends", "ada", "bob").Err())
	return &rig{h: h, home: t.TempDir(), clock: upStart}
}

// arm hooks name in as role with its proof not yet in: live, as nova-hook
// hook leaves a friend's record the moment she arms.
func arm(t *testing.T, r *rig, name string, role hook.Role) {
	t.Helper()
	ctx := context.Background()
	_, _, err := r.h.Store.HookAs(ctx, name, role, "hooktest")
	require.NoError(t, err)
	require.NoError(t, (&hook.Holder{Store: r.h.Store, Name: name, Role: role, Pushes: hook.PushesOf(role), By: "hooktest"}).Start(ctx))
	require.Equal(t, hook.StateLive, r.h.State(t, name).State)
}

// serveOnce runs one pass of the heartbeat server as ada, with the sprint's
// answers to friend cards from rows (a name rows lacks is refused).
func serveOnce(t *testing.T, r *rig, rows map[string]string, asked *[][]string) string {
	t.Helper()
	w := r.world()
	w.sprintBatch = func(_ context.Context, _ string, argv ...[]string) ([]sprintwire.Result, error) {
		out := make([]sprintwire.Result, len(argv))
		for i, a := range argv {
			*asked = append(*asked, a)
			name := a[2]
			if ans, ok := rows[name]; ok {
				out[i] = sprintwire.Result{Stdout: ans}
			} else {
				out[i] = sprintwire.Result{Code: 1, Stdout: "no friend " + name}
			}
		}
		return out, nil
	}
	cli := testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
	res := cli.Run("serve", "--as", "ada", "--once", "--down-after", stepDownAfter)
	require.Equal(t, 0, res.Code, res.Stdout+res.Stderr)
	r.h.Pass(hook.BeatEvery)
	r.h.Hook(t, "ada", hook.Coordinator)
	return res.Stdout
}

// upMsg is one FRIEND UP message on a stream: its kind, subject and body.
type upMsg struct{ kind, subject, body string }

// upMessages is every FRIEND UP message on ada's stream, oldest first.
func upMessages(t *testing.T, r *rig) []upMsg {
	t.Helper()
	es, err := r.h.Client.XRange(context.Background(), bus.StreamOf("ada"), "-", "+").Result()
	require.NoError(t, err)
	var out []upMsg
	for _, e := range es {
		v := func(k string) string { s, _ := e.Values[k].(string); return s } // ignored: a missing field reads empty
		if strings.HasPrefix(v("subject"), upSubject) {
			out = append(out, upMsg{v("kind"), v("subject"), v("body")})
		}
	}
	return out
}

// TestAFriendComingUpIsPushedAtOnceWithHerRow pins the owner's ask of 2026-10-18
// (tla/HookBeat.tla, ArmingPushedAtPass): a friend armed at 15:57:25 and the
// coordinator heard FRIEND STATE unhooked->live, then FRIEND BACK only at her
// proof a minute later, and nothing said her sprint row was held. A friend
// coming up is one FRIEND UP push at the pass that finds her armed, carrying
// her sprint row (held or unheld, width, ready and working cards) and the
// hold's reason in its body; her proof is FRIEND STATE live->proven and the
// FRIEND BACK judgment, as before; and one message per change, never twice.
func TestAFriendComingUpIsPushedAtOnceWithHerRow(t *testing.T) {
	t.Parallel()
	r := newUpRig(t)
	r.h.Hook(t, "ada", hook.Coordinator)
	r.h.Hook(t, "bob", hook.Friend)
	var asked [][]string
	held := `{"friend":"bob","cards":[{"card":"c1","col":"working"},{"card":"c2","col":"working"},{"card":"c3","col":"ready"}],"row":{"held":true,"reason":"allowance out","width":32},"outcome":"accepted","changed":"no"}`
	rows := map[string]string{"bob": held}
	serveOnce(t, r, rows, &asked)
	serveOnce(t, r, rows, &asked) // the baseline: bob proven, nothing pushed
	r.h.Unhook(t, "bob")
	serveOnce(t, r, rows, &asked)
	require.Equal(t, 1, r.count(t, "ada", "FRIEND STATE bob proven->down"))
	require.Empty(t, upMessages(t, r))

	asked = nil
	arm(t, r, "bob", hook.Friend)
	at := r.storeNow(t)
	out := serveOnce(t, r, rows, &asked)
	ups := upMessages(t, r)
	require.Len(t, ups, 1, out)
	assert.Equal(t, "FRIEND UP bob live held width=32 ready=1 working=2 at="+at, ups[0].subject)
	assert.Equal(t, "status", ups[0].kind, "a status line: nothing answers it")
	assert.Contains(t, ups[0].body, "The sprint holds bob's row: allowance out; nova-sprint friend up bob releases it.")
	assert.Contains(t, ups[0].body, "bob is armed; FRIEND BACK follows the proof")
	// the busy set reads her cards every pass she is hooked; the push adds one read, in the same verb, on the pass she comes up
	assert.Equal(t, 2, strings.Count(strings.Join(argvs(asked), "\n"), "friend cards bob --json"), "the busy set's read and the push's")
	assert.Zero(t, r.count(t, "ada", "FRIEND BACK "), "no FRIEND BACK before her proof")

	r.h.Hook(t, "bob", hook.Friend) // her proof
	serveOnce(t, r, rows, &asked)
	assert.Equal(t, 1, r.count(t, "ada", "FRIEND STATE bob live->proven"))
	assert.Len(t, upMessages(t, r), 1, "up is pushed once")
	serveOnce(t, r, rows, &asked)
	assert.Len(t, upMessages(t, r), 1, "and never again while she stays up")
}

// TestFriendUpSaysWhenHerRowIsUnheldOrUnread pins the other answers: an
// unheld row says unheld; a sprint server older than the row field, or one
// that refuses or does not answer, still pushes FRIEND UP at once, with
// row=unread and why in its body (accepting read: the push never waits on
// the sprint); a name that is no friend (comms) carries no row.
func TestFriendUpSaysWhenHerRowIsUnheldOrUnread(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, answer, subject, body string
	}{
		{"unheld", `{"friend":"bob","cards":[],"row":{"held":false,"width":8}}`, "FRIEND UP bob live unheld width=8 ready=0 working=0 at=", ""},
		{"old server", `{"friend":"bob","cards":[]}`, "FRIEND UP bob live row=unread at=", "the sprint server sends no row"},
		{"refused", "", "FRIEND UP bob live row=unread at=", "the sprint refused friend cards bob"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newUpRig(t)
			r.h.Hook(t, "ada", hook.Coordinator)
			r.h.Hook(t, "bob", hook.Friend)
			rows := map[string]string{}
			if tc.answer != "" {
				rows["bob"] = tc.answer
			}
			var asked [][]string
			serveOnce(t, r, rows, &asked)
			serveOnce(t, r, rows, &asked)
			r.h.Unhook(t, "bob")
			serveOnce(t, r, rows, &asked)
			arm(t, r, "bob", hook.Friend)
			at := r.storeNow(t)
			serveOnce(t, r, rows, &asked)
			ups := upMessages(t, r)
			require.Len(t, ups, 1)
			assert.Equal(t, tc.subject+at, ups[0].subject)
			assert.Contains(t, ups[0].body, tc.body)
			assert.NotContains(t, ups[0].body, "The sprint holds")
		})
	}
	t.Run("comms", func(t *testing.T) {
		t.Parallel()
		r := newUpRig(t)
		r.h.Hook(t, "ada", hook.Coordinator)
		r.h.Hook(t, "bob", hook.Comms)
		var asked [][]string
		serveOnce(t, r, nil, &asked)
		serveOnce(t, r, nil, &asked)
		r.h.Unhook(t, "bob")
		serveOnce(t, r, nil, &asked)
		asked = nil
		arm(t, r, "bob", hook.Comms)
		at := r.storeNow(t)
		serveOnce(t, r, nil, &asked)
		ups := upMessages(t, r)
		require.Len(t, ups, 1)
		assert.Equal(t, "FRIEND UP bob live at="+at, ups[0].subject)
		assert.Empty(t, asked, "no sprint row is read for a name that is no friend")
	})
}

// TestPushLevelReadsEveryStateAndOldMark pins pushLevel (tla/HookBeat.tla,
// PS and LastC): proven; live and partial live; every other state, and an
// old mark of one, down.
func TestPushLevelReadsEveryStateAndOldMark(t *testing.T) {
	t.Parallel()
	for state, want := range map[string]string{
		hook.StateProven: hook.StateProven, hook.StateLive: hook.StateLive, hook.StatePartial: hook.StateLive,
		hook.StateExpired: hook.StateDown, hook.StateUnhooked: hook.StateDown, hook.StateNone: hook.StateDown,
		hook.StateDown: hook.StateDown, "": hook.StateDown,
	} {
		assert.Equal(t, want, pushLevel(state), state)
	}
}

func argvs(asked [][]string) []string {
	out := make([]string, len(asked))
	for i, a := range asked {
		out[i] = strings.Join(a, " ")
	}
	return out
}

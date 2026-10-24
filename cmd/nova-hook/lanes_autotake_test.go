package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// A friend's hook takes her ready cards on its own (2026-10-24: the friend daemons that took
// cards were retired on 10-17, and a friend hooked in the foreground took
// nothing, since only the supervised service carried lanes). The model is
// tla/FriendAutoTake.tla: TakesWithinWidth, NeverOthersCard, DealOrder, TakenWithinInterval.
// Every test here runs on fakes: a fake sprint server, a fake nova-config row, the fake clock
// of the hook rig (miniredis over net.Pipe); no socket, no real time, no network.

// laneClientFor answers whether a hook of role, run as a service or in the foreground, with
// args carries a lane client.
func laneClientFor(t *testing.T, service bool, role hook.Role, args ...string) bool {
	t.Helper()
	w := world{
		getenv: func(k string) string {
			if k == serviceRunEnv && service {
				return "1"
			}
			return ""
		},
		sprint: func(context.Context, string, []string) (string, error) { return "", nil },
	}
	got := false
	tl := &tool.Tool{Name: "nova-hook", What: "test", Verbs: []tool.Verb{{
		Name: "hook", Effect: tool.Inspection,
		Flags: func(f *tool.Flags) { f.Bool("once", false, "once"); laneFlags(w, f) },
		Run: func(c *tool.Call) *tool.Out {
			got = w.laneClient(c, "amy", role) != nil
			return tool.Done()
		},
	}}}
	var out, errb strings.Builder
	require.Equal(t, 0, tl.Run(append([]string{"hook"}, args...), strings.NewReader(""), &out, &errb), errb.String())
	return got
}

// A friend's foreground hook carries the lanes as her service does; a coordinator's
// foreground hook does not (her service does); --once and --lanes 0 carry none.
func TestAFriendsForegroundHookCarriesTheLanes(t *testing.T) {
	t.Parallel()
	assert.True(t, laneClientFor(t, false, hook.Friend), "the session's own hook takes her cards")
	assert.True(t, laneClientFor(t, true, hook.Friend))
	assert.False(t, laneClientFor(t, false, hook.Coordinator), "the coordinator deals; her session's hook takes nothing")
	assert.True(t, laneClientFor(t, true, hook.Coordinator))
	assert.False(t, laneClientFor(t, false, hook.Friend, "--once"))
	assert.False(t, laneClientFor(t, false, hook.Friend, "--lanes", "0"))
	assert.False(t, laneClientFor(t, false, hook.Comms))
}

// The rounds run every 30 to 60 s, so a free lane with a ready card is taken within one
// interval (TakenWithinInterval), and still renew well inside the server's lease.
func TestTheLanesAskEveryThirtyToSixtySeconds(t *testing.T) {
	t.Parallel()
	assert.GreaterOrEqual(t, LaneEvery, 30*time.Second)
	assert.LessOrEqual(t, LaneEvery, 60*time.Second)
	assert.Less(t, 4*(LaneEvery+laneRoundWait), ServerLaneLease, "three late or failed rounds in a row still renew inside the lease")
}

// With no harness route (send nil) a round's new holds are one HOOK TAKEN line each, told
// once, and no bus message is sent; an unproven model's round asks nothing and says nothing.
func TestAForegroundRoundSaysOneTakenLinePerNewCard(t *testing.T) {
	t.Parallel()
	asks := 0
	lc := &friend.Client{Name: "amy", Ask: laneServer(&asks), Dir: t.TempDir(), Fetch: widthRow(2, nil)}
	lc.Apply(context.Background(), time.Unix(1000, 0))

	assert.Empty(t, lanePass(context.Background(), lc, false, nil))
	assert.Zero(t, asks)

	out := lanePass(context.Background(), lc, true, nil)
	require.Len(t, out, 1, "one card taken, one line")
	var b strings.Builder
	line(&tool.Call{Stdout: &b}, out[0])
	text := b.String()
	assert.True(t, strings.HasPrefix(text, "HOOK TAKEN card=s1-1.w1@1 brief="), text)
	assert.Contains(t, text, "/s1-1.w1~2/BRIEF.md lane=1 epoch=2 as=friend.amy\n")
	assert.Contains(t, text, "HOOK NOTE work it in a child agent (the hook does not); finish: nova-sprint finish --as friend.amy s1-1.w1@1 --epoch 2 --lane 1")

	assert.Empty(t, lanePass(context.Background(), lc, true, nil), "told once: the next round says nothing")
}

// A hook with a harness route still sends the one bus notice, and says a TAKEN line per card
// only once the notice is sent.
func TestARoutedRoundSaysTakenOnlyOnceTheNoticeIsSent(t *testing.T) {
	t.Parallel()
	asks := 0
	lc := &friend.Client{Name: "amy", Ask: laneServer(&asks), Dir: t.TempDir(), Fetch: widthRow(1, nil)}
	lc.Apply(context.Background(), time.Unix(1000, 0))
	var sent []bus.Message
	out := lanePass(context.Background(), lc, true, func(_ context.Context, m bus.Message) error { sent = append(sent, m); return nil })
	require.Len(t, sent, 1)
	require.Len(t, out, 2)
	assert.Equal(t, "TAKEN", out[0].Word)
	assert.Equal(t, "lanes-taken", stateFact(out[1]))
}

// A friend's hook with no harness route says its route at arming, and warns when no session
// can be reading its stdout, naming --harness.
func TestTheRouteLinesNameTheHarnessFlag(t *testing.T) {
	t.Parallel()
	render := func(os []*tool.Out) string {
		var b strings.Builder
		for _, o := range os {
			line(&tool.Call{Stdout: &b}, o)
		}
		return b.String()
	}
	read := render(world{stdoutUnread: func() string { return "" }}.routeLines())
	assert.Contains(t, read, "HOOK STATE state=route-stdout\n")
	assert.Contains(t, read, "--harness <grok|muse|claude|deepseek|antigravity|codex>")
	assert.NotContains(t, read, "HOOK WARN")

	unread := render(world{stdoutUnread: func() string { return "this hook's stdout is a regular file, which no session reads" }}.routeLines())
	assert.Contains(t, unread, "HOOK WARN route=unread\n")
	assert.Contains(t, unread, "HOOK NOTE this hook's stdout is a regular file, which no session reads: challenges will not reach the model")
	assert.Contains(t, unread, "hook again under the session's Monitor, or with --harness <grok|muse|claude|deepseek|antigravity|codex>")
}

// The whole loop on the fake clock: ada hooks as a friend in the foreground (no service, no
// harness), proves her challenge, and the next round (within LaneEvery and a tick) takes the
// card the fake server deals her and prints one HOOK TAKEN line; no bus notice is sent. The
// fake server holds the lane state: its friend cards answer is what the next round asks by.
func TestAForegroundFriendHookTakesHerReadyCardWithinOneInterval(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.MR.SetTime(r.now())
	w := r.world()
	w.tick = Tick
	w.home = func() (string, error) { return r.home, nil }
	w.stdoutUnread = func() string { return "" }
	var mu sync.Mutex
	held := false // the fake server's lane 1
	var takes int
	w.sprint = func(_ context.Context, _ string, argv []string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if argv[0] == "friend" {
			if held {
				return `{"friend":"ada","cards":[{"card":"c7","col":"working","lane":1}]}`, nil
			}
			return `{"friend":"ada","cards":[]}`, nil
		}
		require.Equal(t, []string{"take", "--as", "friend.ada", "--lane"}, argv[:4], "the lane take of her own row")
		takes++
		if argv[4] == "1" {
			held = true
			return `{"says":["LANE friend.ada lane=1 epoch=9 holds=c7@1 kind=work job=c7~9"],"packets":[{"card":"c7","brief":"do c7"}]}`, nil
		}
		return `{"says":["LANE friend.ada lane=` + argv[4] + ` epoch=9 holds=-"]}`, nil
	}
	w.novaConfig = func(context.Context, []string) (string, error) {
		return `{"result":{"status":"ok"},"items":[{"kind":"friend","fields":{"name":"ada","width":"1"}}]}`, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	start := r.now()
	var nonce string
	var provedAt, takenAt time.Time
	var taken []string
	cs := &clockStore{r: r}
	cs.event = func(now time.Time) {
		// the heartbeat server's row, fresh on the fake clock: the evidence a proven record
		// needs (hook.Served.Down) for its lanes to ask
		require.NoError(t, r.h.Client.HSet(context.Background(), hook.ServeKey, "at", now.UnixMilli(), "every", hook.BeatEvery.Milliseconds(), "by", "test").Err())
		if provedAt.IsZero() && nonce != "" && now.Sub(start) >= 20*time.Second {
			r.cli().Do(t, "prove", "--as", "ada", "--nonce", nonce).Exit(0)
			provedAt = now
		}
		if now.Sub(start) >= 4*time.Minute {
			cancel()
		}
	}
	w.waitOn = func(wt bus.Waiter) bus.Waiter { cs.Waiter = wt; return cs }
	out := &streamOut{each: func(l string) {
		if m := nonceRe.FindStringSubmatch(l); m != nil && strings.HasPrefix(l, "HOOK CHALLENGE") {
			nonce = m[1]
		}
		if strings.HasPrefix(l, "HOOK TAKEN") {
			taken = append(taken, l)
			if takenAt.IsZero() {
				takenAt = r.now()
			}
		}
	}}
	var errb strings.Builder
	code := run([]string{"hook", "--as", "ada", "--role", "friend"}, strings.NewReader(""), out, &errb, w)
	require.Zero(t, code, out.all.String()+errb.String())
	all := out.all.String()
	assert.Contains(t, all, "HOOK STATE state=route-stdout", "the route is said at arming")
	require.False(t, provedAt.IsZero(), all)
	require.Len(t, taken, 1, "one card, one line, told once: %s", all)
	assert.Contains(t, taken[0], "HOOK TAKEN card=c7@1 brief="+r.home+"/.local/state/nova-hook/ada/briefs/c7~9/BRIEF.md lane=1 epoch=9 as=friend.ada")
	assert.LessOrEqual(t, takenAt.Sub(provedAt), LaneEvery+Tick, "taken within one interval of the proof")
	assert.NotContains(t, all, "SPRINT CARDS", "no bus notice: the stream is the route")
	mu.Lock()
	defer mu.Unlock()
	assert.True(t, held)
}

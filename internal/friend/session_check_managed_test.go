package friend

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedSessionCheckNeverStartsStandaloneDelivery(t *testing.T) {
	t.Parallel()
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "batch", true: "one-shot"}[managed], func(t *testing.T) {
			t.Parallel()
			r := newPresenceRig(t)
			r.sc.Managed = func() bool { return managed }
			r.step(t, BeatEvery)
			assert.Len(t, r.app.got(), map[bool]int{false: 1, true: 0}[managed])
			assert.Equal(t, "n1", r.sc.PendingCheck())
			up, _ := r.present(t)
			assert.False(t, up, "publishing a challenge is no session proof")
			r.send(t, r.daemon, "bob", PongSubject, PongLine("n1", 0, 0, 0))
			r.step(t, BeatEvery)
			up, _ = r.present(t)
			assert.False(t, up, "the daemon cannot answer its own challenge")
			r.send(t, r.direct, "bob", PongSubject, PongLine("wrong", 0, 0, 0))
			r.step(t, BeatEvery)
			assert.Equal(t, "n1", r.sc.PendingCheck())
			r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 0))
			r.step(t, BeatEvery)
			up, _ = r.present(t)
			assert.True(t, up)
			assert.Empty(t, r.sc.PendingCheck())
			r.step(t, ProveEvery)
			assert.Equal(t, "n2", r.sc.PendingCheck(), "presence still needs fresh checks after initial proof")
			assert.Len(t, r.app.got(), map[bool]int{false: 2, true: 0}[managed])
			assert.Equal(t, 0, r.store.Len(bus.StreamOf("bob")), "managed check is not queued as ordinary work")
		})
	}
}

type managedCheckHarness struct {
	*lanesHarness
	t *testing.T
}

func (h *managedCheckHarness) OpenSession(ctx context.Context, seed string) (string, error) {
	assert.True(h.t, InLane(ctx))
	assert.NotNil(h.t, processStarted(ctx))
	assert.NotEmpty(h.t, processGroupAnchor(ctx))
	return h.lanesHarness.OpenSession(ctx, seed)
}
func (h *managedCheckHarness) DeliverTo(ctx context.Context, session, text string) (LaneTurn, error) {
	assert.True(h.t, InLane(ctx), "only an owned lane can deliver the check")
	assert.NotNil(h.t, processStarted(ctx))
	assert.NotEmpty(h.t, processGroupAnchor(ctx))
	// Exercise the real Wall routing without executing a provider.
	wall := Wall{Self: []string{"/tools/nova-friend"}, Dir: h.dir}
	_, _, err := wall.Exec(func(_ context.Context, _ string, program string, args []string, _ string) (string, int, error) {
		assert.Equal(h.t, wall.Self[0], program)
		require.NotEmpty(h.t, args)
		assert.Equal(h.t, "wall", args[0])
		return "", 0, nil
	})(ctx, h.dir, "opencode", []string{"run", text}, "")
	require.NoError(h.t, err)
	return h.lanesHarness.DeliverTo(ctx, session, text)
}

func TestManagedPresenceChecksUseOwnedWallLaneAfterProofAndBeforeNewCards(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, nil, nil, nil)
		h := &lanesHarness{dir: dir, active: map[string]int{}}
		r, _ := laneRig(t, h, 1)
		r.d.Deliver = &managedCheckHarness{lanesHarness: h, t: t}
		sc := &SessionCheck{Friend: "bob", Store: r.store, Now: func() time.Time { return r.now }, Nonce: func() string { return "check1" }, Managed: func() bool { return true }}
		// A standalone delivery would be a second, unowned provider path.
		sc.Deliver = &closedApp{}
		r.d.Proof = sc.Proof
		r.d.ControlCheck = sc.PendingCheck
		r.d.PongCommand = func(n string) string { return "nova-friend pong --as bob --nonce " + n }
		beat := r.d.Beat
		r.d.Beat = func(ctx context.Context, at time.Time) error { sc.Step(ctx); return beat(ctx, at) }
		h.onPong = func(n string) {
			_, err := r.bus.Send(context.Background(), bus.Message{From: "bob", To: []string{"ada"}, Subject: PongSubject, Body: PongLine(n, 0, 0, 0)})
			require.NoError(t, err)
		}
		r.at[5] = func() {
			proven, _ := sc.Proof()
			require.True(t, proven)
			sc.mu.Lock()
			sc.m.Ask(r.now, "check2")
			sc.mu.Unlock()
			c := filepath.Join(dir, "inbox", "c1~15")
			require.NoError(t, os.MkdirAll(c, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(c, "BRIEF.md"), []byte("RESULT: c1\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"c1","state":"queued"}]}`), 0o644))
			h.finish = map[string]bool{"c1": true}
		}
		r.run(t, 14)
		_, texts, _ := h.got()
		require.Len(t, texts, 3, "initial check, later check, then card")
		assert.Contains(t, texts[0], "--nonce check1")
		assert.Contains(t, texts[1], "--nonce check2")
		assert.Contains(t, texts[2], "one card this turn, c1")
		assert.Empty(t, sc.Deliver.(*closedApp).got(), "no raw provider check")
	})
}

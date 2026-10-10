package store

import (
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestConcurrentSeatPushMergesPreserveEveryWatchAndTheJudgmentsRecord(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	name := h.st.Actor
	require.NoError(t, h.m.SetCoordinator(h.ctx, name))
	rec := sprint.PushRecord{Name: name, Harness: "codex", Target: "job", Session: "session-a", Nonce: "n1", PongOf: "n1", Proven: h.st.Now()}
	require.NoError(t, h.st.PutSeatPushes(h.ctx, sprint.SeatPushSet{PushRecord: rec}))
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for _, source := range []string{"bus", "friends", "transitions"} {
		wg.Add(1)
		go func(source string) { defer wg.Done(); errs <- h.st.BeatSeatPush(h.ctx, name, source, "") }(source)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- h.st.UpdateSeatPushes(h.ctx, name, func(set *sprint.SeatPushSet) error { set.Nonce = "n2"; return nil })
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	set, ok, err := h.st.SeatPushes(h.ctx, name)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Len(t, set.Watches, 3)
	assert.Equal(t, "n2", set.Nonce, "a watch cannot overwrite a newer judgments nonce")
	assert.Equal(t, "session-a", set.Session)
	require.NoError(t, h.st.BeatSeatPush(h.ctx, name, "bus", "read failed"))
	set, _, err = h.st.SeatPushes(h.ctx, name)
	require.NoError(t, err)
	assert.Equal(t, "read failed", set.Watches["bus"].Failed)
	assert.Len(t, set.Watches, 3)
}

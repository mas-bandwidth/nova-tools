package friend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A session that starts is handed one present and never the backlog that
// accumulated on its stream (docs/SPEC-FRIEND.md, The present).
func TestAStartedSessionGetsThePresentAndNeverTheBacklog(t *testing.T) {
	t.Parallel()
	t.Run("new session", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		dir := r.d.Dir
		writePresent(t, dir, presentMemo{Session: "ses_old", Launch: "run_old", At: t0})
		r.d.Session, r.d.Launch = "ses_new", "run_old"
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"c1","state":"queued","job":"c1~15"},{"id":"c2","state":"working","job":"c2~15"}]}`), 0o644))
		r.send(t, "ada", "card c1 dealt", "old deal")
		r.send(t, "ada", "PING n1", PingText("ada", t0, "n1"))
		r.send(t, "ada", "morning note", "obsolete morning")
		r.send(t, "ada", "evening note", "second and newest")

		r.run(t, 4)

		require.Len(t, r.delivered, 1, "one present, not the backlog: %v", r.delivered)
		text := r.delivered[0]
		assert.NotContains(t, text, "old deal")
		assert.NotContains(t, text, "obsolete morning")
		assert.NotContains(t, text, "Answer first")
		assert.Contains(t, text, "seat: ada")
		assert.Contains(t, text, "c1 queued inbox/c1~15/BRIEF.md")
		assert.Contains(t, text, "c2 working inbox/c2~15/BRIEF.md")
		assert.Contains(t, text, "newest note from ada subject=\"evening note\"")
		assert.Contains(t, text, "second and newest")
		assert.Contains(t, text, "1 deals, 1 pings, 2 notes, all superseded")
		pending, fresh, err := r.bus.Peek(context.Background(), "bob")
		require.NoError(t, err)
		assert.Empty(t, pending)
		assert.Empty(t, fresh)
		reason := presentReasonIn(t, r)
		assert.Contains(t, reason, "superseded by the present at ")
		assert.NotContains(t, strings.Join(r.adaGot(t), "\n"), "daemon-pong")

		r.send(t, "ada", "now", "current")
		r.run(t, r.beats+4) // beats is not reset; the turn needs a step after the one that starts it
		require.Len(t, r.delivered, 2, "a later message is itself: %v", r.delivered)
		assert.Contains(t, r.delivered[1], "current")
		assert.NotContains(t, r.delivered[1], "all superseded")
	})

	t.Run("gap", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		writePresent(t, r.d.Dir, presentMemo{Session: "ses", Launch: "run", At: t0.Add(-PresentStale - time.Second)})
		r.d.Session, r.d.Launch = "ses", "run"
		r.send(t, "ada", "card c9 dealt", "stale deal")
		r.run(t, 4)
		require.Len(t, r.delivered, 1, "%v", r.delivered)
		assert.NotContains(t, r.delivered[0], "stale deal")
		assert.Contains(t, r.delivered[0], "1 deals, 0 pings, 0 notes, all superseded")
		assert.Contains(t, presentReasonIn(t, r), "superseded by the present at ")
	})

	t.Run("her present request", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		writePresent(t, r.d.Dir, presentMemo{Session: "ses", Launch: "run", At: t0})
		r.d.Session, r.d.Launch = "ses", "run"
		r.send(t, "ada", "card c3 dealt", "old deal")
		r.send(t, "bob", "present", "present\n")
		r.run(t, 4)
		require.Len(t, r.delivered, 1, "%v", r.delivered)
		assert.NotContains(t, r.delivered[0], "old deal")
		assert.Contains(t, r.delivered[0], "1 deals, 0 pings, 0 notes, all superseded")
		pending, _, err := r.bus.Peek(context.Background(), "bob")
		require.NoError(t, err)
		assert.Empty(t, pending)
	})

	t.Run("stale nonce", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		r.send(t, "ada", "PING n9", PingText("ada", t0, "n9"))
		r.mu.Lock()
		r.now = r.now.Add(Window)
		r.mu.Unlock()
		r.run(t, 3)
		assert.Empty(t, r.delivered)
		assert.NotContains(t, strings.Join(r.adaGot(t), "\n"), "daemon-pong")
		assert.Contains(t, strings.Join(r.records, "\n"), "nonce dropped")
		pending, fresh, err := r.bus.Peek(context.Background(), "bob")
		require.NoError(t, err)
		assert.Empty(t, pending)
		assert.Empty(t, fresh)
	})

	t.Run("finish names the holder", func(t *testing.T) {
		t.Parallel()
		r := newRig(t)
		dir := r.d.Dir
		row := &twinRow{}
		r.d.Held = row.held
		f := &finishes{}
		r.d.Finish = f.finish
		r.d.Running = func() map[string]string { return map[string]string{"gone.w1": "cy", "gone.w1~15": "cy"} }
		outboxReport(t, dir, "gone.w1~15", "the card left her row\n")
		r.run(t, 3)
		assert.Contains(t, strings.Join(r.records, "\n"), "card gone.w1 is no longer hers; cy holds it now")
		assert.Empty(t, f.got())
	})
}

func writePresent(t *testing.T, dir string, m presentMemo) {
	t.Helper()
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(StateDirIn(dir), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(StateDirIn(dir), PresentFile), append(raw, '\n'), 0o644))
}

func presentReasonIn(t *testing.T, r *rig) string {
	t.Helper()
	log, err := r.store.Range(context.Background(), bus.LogKey, "-", "+", 100)
	require.NoError(t, err)
	var b strings.Builder
	for _, e := range log {
		b.WriteString(e.Fields["body"])
		b.WriteString("\n")
	}
	b.WriteString(strings.Join(r.records, "\n"))
	return b.String()
}

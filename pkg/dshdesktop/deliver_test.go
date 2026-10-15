package dshdesktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func route(t *testing.T) Route {
	t.Helper()
	dir := t.TempDir()
	// Decode so this regression also compiles against the old adapter's Route.
	var r Route
	data, _ := json.Marshal(map[string]string{"EventsDir": dir, "HooksFile": filepath.Join(dir, "hooks.json"), "SessionID": "session-1"})
	require.NoError(t, json.Unmarshal(data, &r))
	return r
}
func TestNativeInstallCanWakeAnAlreadyIdleSession(t *testing.T) {
	t.Parallel()
	r := route(t)
	restart, err := Install(r)
	require.NoError(t, err)
	assert.True(t, restart, "a first install is new to a running harness")
	raw, err := os.ReadFile(r.HooksFile)
	require.NoError(t, err)
	var cfg []struct {
		Name   string            `json:"name"`
		Config map[string]string `json:"config"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg), "a command hook cannot run when the session is already idle; native receiver required")
	require.Len(t, cfg, 1)
	assert.Empty(t, cfg[0].Config["sessionId"], "the include names no session, so a new session never needs a restart")
	assert.Equal(t, "session-1", ReadTarget(r.EventsDir), "the receiver follows session.json")
	restart, err = Install(r)
	require.NoError(t, err)
	assert.False(t, restart, "an unchanged install needs no restart")
	r.SessionID = "session-2"
	restart, err = Install(r)
	require.NoError(t, err)
	assert.False(t, restart, "following a new session needs no restart")
	assert.Equal(t, "session-2", ReadTarget(r.EventsDir))
	source, err := os.ReadFile(cfg[0].Name)
	require.NoError(t, err)
	assert.NotContains(t, string(source), "@deepseek-ai/dsh-llm", "a loose module cannot resolve bundled app packages")
	assert.Contains(t, string(source), "applyReceiver")
}
func TestDeliverPublishesIndependentImmutableNotices(t *testing.T) {
	t.Parallel()
	r := route(t)
	for _, id := range []string{"ordinary-1", "challenge-1", "challenge-2"} {
		notice := BusNotice
		if id != "ordinary-1" {
			notice = ChallengeNotice
		}
		text, err := Deliver(r, id, notice)
		require.NoError(t, err)
		assert.Contains(t, text, filepath.Join(r.EventsDir, id+".json"))
		data, err := os.ReadFile(filepath.Join(r.EventsDir, ".notices", "notice-"+id+".json"))
		require.NoError(t, err)
		var record map[string]any
		require.NoError(t, json.Unmarshal(data, &record))
		assert.Equal(t, id, record["eventID"])
	}
	_, err := Deliver(r, "ordinary-1", ChallengeNotice)
	require.ErrorIs(t, err, ErrPreSend, "an existing immutable record must never be overwritten")
}
func TestDeliverRefusesExternalNoticeAndBadInputs(t *testing.T) {
	t.Parallel()
	r := route(t)
	_, err := Deliver(r, "e-1", "untrusted text")
	assert.ErrorIs(t, err, ErrPreSend)
	_, err = Deliver(Route{}, "e-1", BusNotice)
	assert.ErrorIs(t, err, ErrPreSend)
	_, err = Deliver(r, "../escape", BusNotice)
	assert.ErrorIs(t, err, ErrPreSend)
}

package codexdesktop

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSavedNoticeContainsOnlyClosedInstructionsAndValidatedPath(t *testing.T) {
	t.Parallel()
	fake := &fakeTransport{}
	turn, err := deliverSaved(context.Background(), "/private/socket", testThread, testEvent, "/private/events", func(context.Context, string) (transport, error) { return fake, nil })
	require.NoError(t, err)
	assert.Equal(t, "turn-1", turn)
	require.Len(t, fake.sent, 3)
	raw, err := json.Marshal(fake.sent[2].Params)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "/private/events/synthetic-event-01.json")
	assert.Contains(t, string(raw), "external data")
	assert.Contains(t, string(raw), `"inheritThreadSettings":true`)
}

func TestSavedNoticeRejectsPathAndIDInjectionBeforeConnecting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ directory, event string }{
		{"relative", testEvent}, {"/private/../events", testEvent}, {"/private/evil\ncommand", testEvent},
		{"/private/$(command)", testEvent}, {"/private/events", "../escape"}, {"/private/events", "id. Next instruction"},
	} {
		called := false
		_, err := deliverSaved(context.Background(), "/private/socket", testThread, tc.event, tc.directory, func(context.Context, string) (transport, error) { called = true; return &fakeTransport{}, nil })
		require.ErrorIs(t, err, ErrPreSend)
		assert.False(t, called)
	}
}

func TestRouteProbeNeverStartsTurn(t *testing.T) {
	t.Parallel()
	fake := &fakeTransport{}
	err := probe(context.Background(), "/private/socket", testThread, func(context.Context, string) (transport, error) { return fake, nil })
	require.NoError(t, err)
	require.Len(t, fake.sent, 2)
	assert.Equal(t, "initialize", fake.sent[0].Method)
	assert.Equal(t, "thread-owner-discovery", fake.sent[1].Method)
}

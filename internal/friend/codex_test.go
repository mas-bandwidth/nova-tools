package friend

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/friendbus"
)

func TestCodexHookRegistersOnlyNativeSessionStart(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input string
		accepted    bool
	}{
		{"startup", `{"hook_event_name":"SessionStart","session_id":"session-1","cwd":"/work","source":"startup","future_field":true}`, true},
		{"resume", `{"hook_event_name":"SessionStart","session_id":"session-1","cwd":"/work","source":"resume"}`, true},
		{"other hook", `{"hook_event_name":"SessionEnd","session_id":"session-1","cwd":"/work","source":"startup"}`, false},
		{"missing identity", `{"hook_event_name":"SessionStart","cwd":"/work","source":"startup"}`, false},
		{"unknown source", `{"hook_event_name":"SessionStart","session_id":"session-1","cwd":"/work","source":"other"}`, false},
		{"trailing value", `{"hook_event_name":"SessionStart","session_id":"session-1","cwd":"/work","source":"startup"} {}`, false},
		{"oversized", strings.Repeat(" ", (64<<10)+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			event, err := ReadCodexSessionStart(strings.NewReader(tc.input))
			if tc.accepted {
				require.NoError(t, err)
				assert.Equal(t, "session-1", event.SessionID)
			} else {
				require.Error(t, err)
			}
		})
	}
}

type codexBridgeStub struct {
	session friendbus.Session
	receipt friendbus.Acceptance
	calls   int
}

func (b *codexBridgeStub) Register(context.Context, string, friendbus.Session) (friendbus.Session, error) {
	b.calls++
	return b.session, nil
}
func (b *codexBridgeStub) Wake(context.Context, friendbus.Delivery, friendbus.Session) (friendbus.Acceptance, error) {
	b.calls++
	return b.receipt, nil
}

func TestCodexWakeRequiresNativeBridgeAndBoundDurableReceipt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := (CodexAdapter{}).Register(ctx, "friend", friendbus.Session{ID: "session-1"})
	require.ErrorContains(t, err, "no registered native bridge")
	session := friendbus.Session{ID: "session-1", Adapter: "codex", Revision: "registration-1", Capabilities: []string{"durable-delivery-id", "durable-receipt"}}
	for _, tc := range []struct {
		name     string
		receipt  friendbus.Acceptance
		accepted bool
	}{
		{"bound durable", friendbus.Acceptance{ReceiptID: "receipt-1", Session: "session-1", Adapter: "codex", Revision: "registration-1", Durable: true}, true},
		{"wrong session", friendbus.Acceptance{ReceiptID: "receipt-1", Session: "session-2", Adapter: "codex", Revision: "registration-1", Durable: true}, false},
		{"wrong adapter", friendbus.Acceptance{ReceiptID: "receipt-1", Session: "session-1", Adapter: "other", Durable: true}, false},
		{"wrong revision", friendbus.Acceptance{ReceiptID: "receipt-1", Session: "session-1", Adapter: "codex", Revision: "registration-2", Durable: true}, false},
		{"not durable", friendbus.Acceptance{ReceiptID: "receipt-1", Session: "session-1", Adapter: "codex"}, false},
		{"no receipt", friendbus.Acceptance{Session: "session-1", Adapter: "codex", Revision: "registration-1", Durable: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bridge := &codexBridgeStub{session: session, receipt: tc.receipt}
			adapter := CodexAdapter{Bridge: bridge}
			registered, err := adapter.Register(ctx, "friend", session)
			require.NoError(t, err)
			_, err = adapter.Wake(ctx, friendbus.Delivery{}, registered)
			if tc.accepted {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	bridge := &codexBridgeStub{session: session}
	adapter := CodexAdapter{Bridge: bridge, SessionID: "session-1"}
	seeded, err := adapter.Register(ctx, "friend", friendbus.Session{})
	require.NoError(t, err)
	assert.Equal(t, session.ID, seeded.ID)
	bridge.calls = 0
	unknown := session
	unknown.Capabilities = nil
	_, err = adapter.Wake(ctx, friendbus.Delivery{}, unknown)
	require.ErrorContains(t, err, "does not confirm durable-delivery-id")
	assert.Zero(t, bridge.calls, "an incapable registration must not launch a wake")
	bridge.session.ID = "session-2"
	_, err = adapter.Register(ctx, "friend", session)
	require.ErrorContains(t, err, "another session")
}

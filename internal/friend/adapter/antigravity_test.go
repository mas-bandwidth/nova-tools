package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/friendbus"
)

type mockTransport struct {
	sendFunc func(ctx context.Context, recipientID, title, body string) error
	calls    int
	lastRecv struct {
		recipientID string
		title       string
		body        string
	}
}

func (m *mockTransport) SendMessage(ctx context.Context, recipientID, title, body string) error {
	m.calls++
	m.lastRecv.recipientID = recipientID
	m.lastRecv.title = title
	m.lastRecv.body = body
	if m.sendFunc != nil {
		return m.sendFunc(ctx, recipientID, title, body)
	}
	return nil
}

type mockBridge struct {
	registerFunc func(ctx context.Context, friendName string, s friendbus.Session) (friendbus.Session, error)
	wakeFunc     func(ctx context.Context, d friendbus.Delivery, s friendbus.Session) (friendbus.Acceptance, error)
	regCalls     int
	wakeCalls    int
}

func (b *mockBridge) Register(ctx context.Context, friendName string, s friendbus.Session) (friendbus.Session, error) {
	b.regCalls++
	if b.registerFunc != nil {
		return b.registerFunc(ctx, friendName, s)
	}
	return s, nil
}

func (b *mockBridge) Wake(ctx context.Context, d friendbus.Delivery, s friendbus.Session) (friendbus.Acceptance, error) {
	b.wakeCalls++
	if b.wakeFunc != nil {
		return b.wakeFunc(ctx, d, s)
	}
	return friendbus.Acceptance{
		ReceiptID: "bridge-receipt-1",
		Adapter:   "antigravity",
		Session:   s.ID,
		Revision:  s.Revision,
		Durable:   true,
	}, nil
}

func TestAntigravityRegister(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("valid registration with explicit capabilities", func(t *testing.T) {
		t.Parallel()
		a := &AntigravityAdapter{SessionID: "sess-123"}
		prev := friendbus.Session{
			ID:           "sess-123",
			Capabilities: []string{"durable-delivery-id", "durable-receipt"},
		}
		s, err := a.Register(ctx, "emma", prev)
		require.NoError(t, err)
		assert.Equal(t, "sess-123", s.ID)
		assert.Equal(t, "antigravity", s.Adapter)
		assert.NotEmpty(t, s.Revision)
		assert.Contains(t, s.Capabilities, "durable-delivery-id")
		assert.Contains(t, s.Capabilities, "durable-receipt")
	})

	t.Run("valid registration defaults required capabilities", func(t *testing.T) {
		t.Parallel()
		a := &AntigravityAdapter{SessionID: "sess-123"}
		s, err := a.Register(ctx, "emma", friendbus.Session{})
		require.NoError(t, err)
		assert.Equal(t, "sess-123", s.ID)
		assert.Equal(t, "antigravity", s.Adapter)
		assert.ElementsMatch(t, []string{"durable-delivery-id", "durable-receipt"}, s.Capabilities)
	})

	t.Run("registration with configured revision", func(t *testing.T) {
		t.Parallel()
		a := &AntigravityAdapter{SessionID: "sess-123", Revision: "rev-custom-1"}
		s, err := a.Register(ctx, "emma", friendbus.Session{})
		require.NoError(t, err)
		assert.Equal(t, "rev-custom-1", s.Revision)
	})

	t.Run("registration inherits previous session id when adapter session id unset", func(t *testing.T) {
		t.Parallel()
		a := &AntigravityAdapter{}
		prev := friendbus.Session{ID: "sess-from-previous"}
		s, err := a.Register(ctx, "emma", prev)
		require.NoError(t, err)
		assert.Equal(t, "sess-from-previous", s.ID)
	})

	t.Run("registration refuses missing session id", func(t *testing.T) {
		t.Parallel()
		a := &AntigravityAdapter{}
		_, err := a.Register(ctx, "emma", friendbus.Session{})
		require.ErrorContains(t, err, "requires a non-empty session ID")
	})

	t.Run("registration refuses mismatched adapter", func(t *testing.T) {
		t.Parallel()
		a := &AntigravityAdapter{SessionID: "sess-123"}
		_, err := a.Register(ctx, "emma", friendbus.Session{Adapter: "codex"})
		require.ErrorContains(t, err, "requires adapter \"antigravity\", got \"codex\"")
	})

	t.Run("registration refuses missing durable-delivery-id capability", func(t *testing.T) {
		t.Parallel()
		a := &AntigravityAdapter{SessionID: "sess-123"}
		prev := friendbus.Session{
			Capabilities: []string{"durable-receipt"},
		}
		_, err := a.Register(ctx, "emma", prev)
		require.ErrorContains(t, err, "does not confirm durable-delivery-id")
	})

	t.Run("registration refuses missing durable-receipt capability", func(t *testing.T) {
		t.Parallel()
		a := &AntigravityAdapter{SessionID: "sess-123"}
		prev := friendbus.Session{
			Capabilities: []string{"durable-delivery-id"},
		}
		_, err := a.Register(ctx, "emma", prev)
		require.ErrorContains(t, err, "does not confirm durable-receipt")
	})

	t.Run("registration refuses invalid adapter configured capabilities", func(t *testing.T) {
		t.Parallel()
		a := &AntigravityAdapter{
			SessionID:    "sess-123",
			Capabilities: []string{"ephemeral-only"},
		}
		_, err := a.Register(ctx, "emma", friendbus.Session{})
		require.ErrorContains(t, err, "does not confirm durable-delivery-id")
	})

	t.Run("registration delegates to wrapped bridge", func(t *testing.T) {
		t.Parallel()
		bridge := &mockBridge{}
		a := &AntigravityAdapter{SessionID: "sess-123", Bridge: bridge}
		s, err := a.Register(ctx, "emma", friendbus.Session{})
		require.NoError(t, err)
		assert.Equal(t, 1, bridge.regCalls)
		assert.Equal(t, "sess-123", s.ID)
	})
}

func TestAntigravityWakeDeliveringAndBoundReceipt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	validSession := friendbus.Session{
		ID:           "sess-ag-1",
		Adapter:      "antigravity",
		Revision:     "rev-1",
		Capabilities: []string{"durable-delivery-id", "durable-receipt"},
	}

	validDelivery := friendbus.Delivery{
		Key:       "msg-123/emma",
		Recipient: "emma",
		From:      "stella",
		Re:        "E12 Task Update",
		Body:      []byte("hello antigravity"),
	}

	t.Run("wake delivers message and binds durable receipt", func(t *testing.T) {
		t.Parallel()
		transport := &mockTransport{}
		a := NewAntigravityAdapter("sess-ag-1", "", transport)

		acc, err := a.Wake(ctx, validDelivery, validSession)
		require.NoError(t, err)

		assert.Equal(t, 1, transport.calls)
		assert.Equal(t, "sess-ag-1", transport.lastRecv.recipientID)
		assert.Equal(t, "E12 Task Update", transport.lastRecv.title)
		assert.Equal(t, "hello antigravity", transport.lastRecv.body)

		assert.True(t, acc.Durable)
		assert.Equal(t, "antigravity", acc.Adapter)
		assert.Equal(t, "sess-ag-1", acc.Session)
		assert.Equal(t, "rev-1", acc.Revision)
		expectedReceiptID := GenerateReceiptID("sess-ag-1", "rev-1", "msg-123/emma")
		assert.Equal(t, expectedReceiptID, acc.ReceiptID)
	})

	t.Run("wake default title uses from address when Re is empty", func(t *testing.T) {
		t.Parallel()
		transport := &mockTransport{}
		a := NewAntigravityAdapter("sess-ag-1", "", transport)
		d := validDelivery
		d.Re = ""
		d.From = "stella"

		_, err := a.Wake(ctx, d, validSession)
		require.NoError(t, err)
		assert.Equal(t, "nova-bus message from stella", transport.lastRecv.title)
	})

	t.Run("wake validates delivery key", func(t *testing.T) {
		t.Parallel()
		a := NewAntigravityAdapter("sess-ag-1", "", &mockTransport{})
		d := validDelivery
		d.Key = ""
		_, err := a.Wake(ctx, d, validSession)
		require.ErrorContains(t, err, "requires a delivery key")
	})

	t.Run("wake validates session id", func(t *testing.T) {
		t.Parallel()
		a := NewAntigravityAdapter("", "", &mockTransport{})
		s := validSession
		s.ID = ""
		_, err := a.Wake(ctx, validDelivery, s)
		require.ErrorContains(t, err, "requires a session ID")
	})

	t.Run("wake validates adapter identity", func(t *testing.T) {
		t.Parallel()
		a := NewAntigravityAdapter("sess-ag-1", "", &mockTransport{})
		s := validSession
		s.Adapter = "codex"
		_, err := a.Wake(ctx, validDelivery, s)
		require.ErrorContains(t, err, "wants adapter \"antigravity\", got \"codex\"")
	})

	t.Run("wake validates capabilities", func(t *testing.T) {
		t.Parallel()
		a := NewAntigravityAdapter("sess-ag-1", "", &mockTransport{})
		s := validSession
		s.Capabilities = []string{"notifications-only"}
		_, err := a.Wake(ctx, validDelivery, s)
		require.ErrorContains(t, err, "does not confirm durable-delivery-id")
	})

	t.Run("wake delegates to wrapped bridge when bridge is provided", func(t *testing.T) {
		t.Parallel()
		bridge := &mockBridge{}
		a := &AntigravityAdapter{Bridge: bridge}
		acc, err := a.Wake(ctx, validDelivery, validSession)
		require.NoError(t, err)
		assert.Equal(t, 1, bridge.wakeCalls)
		assert.Equal(t, "bridge-receipt-1", acc.ReceiptID)
	})
}

func TestAntigravityDeduplication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	session := friendbus.Session{
		ID:           "sess-dedup",
		Adapter:      "antigravity",
		Revision:     "rev-dedup",
		Capabilities: []string{"durable-delivery-id", "durable-receipt"},
	}

	delivery := friendbus.Delivery{
		Key:       "stable-key-001",
		Recipient: "emma",
		Body:      []byte("payload"),
	}

	t.Run("in-memory deduplication prevents re-injection", func(t *testing.T) {
		t.Parallel()
		transport := &mockTransport{}
		a := NewAntigravityAdapter("sess-dedup", "", transport)

		acc1, err := a.Wake(ctx, delivery, session)
		require.NoError(t, err)
		assert.Equal(t, 1, transport.calls)

		// Second delivery with same Key must return same acceptance without calling transport
		acc2, err := a.Wake(ctx, delivery, session)
		require.NoError(t, err)
		assert.Equal(t, 1, transport.calls, "transport must not be called again for duplicated delivery key")
		assert.Equal(t, acc1, acc2)

		// Different delivery key invokes transport
		delivery2 := delivery
		delivery2.Key = "stable-key-002"
		acc3, err := a.Wake(ctx, delivery2, session)
		require.NoError(t, err)
		assert.Equal(t, 2, transport.calls)
		assert.NotEqual(t, acc1.ReceiptID, acc3.ReceiptID)
	})

	t.Run("durable deduplication survives process restart via StatePath", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		statePath := filepath.Join(tempDir, "antigravity-state.json")

		transport1 := &mockTransport{}
		a1 := NewAntigravityAdapter("sess-dedup", statePath, transport1)

		acc1, err := a1.Wake(ctx, delivery, session)
		require.NoError(t, err)
		assert.Equal(t, 1, transport1.calls)

		// Verify state file exists on disk
		_, err = os.Stat(statePath)
		require.NoError(t, err, "state file must be written to disk")

		// Simulate process restart: instantiate brand new adapter with same StatePath
		transport2 := &mockTransport{}
		a2 := NewAntigravityAdapter("sess-dedup", statePath, transport2)

		acc2, err := a2.Wake(ctx, delivery, session)
		require.NoError(t, err)
		assert.Zero(t, transport2.calls, "transport must NOT be called on replay after restart")
		assert.Equal(t, acc1, acc2, "prior durable acceptance must be returned identically")
	})
}

func TestAntigravityTransportErrorHandling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	session := friendbus.Session{
		ID:           "sess-err",
		Adapter:      "antigravity",
		Revision:     "rev-err",
		Capabilities: []string{"durable-delivery-id", "durable-receipt"},
	}

	delivery := friendbus.Delivery{
		Key:       "failing-msg-key",
		Recipient: "emma",
		Body:      []byte("failing payload"),
	}

	transport := &mockTransport{
		sendFunc: func(ctx context.Context, recipientID, title, body string) error {
			return errors.New("agentapi connection failed: conversation not found")
		},
	}

	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "err-state.json")
	a := NewAntigravityAdapter("sess-err", statePath, transport)

	acc, err := a.Wake(ctx, delivery, session)
	require.ErrorContains(t, err, "agentapi connection failed: conversation not found")
	assert.Empty(t, acc.ReceiptID)

	// Verify the failing delivery key was NOT stored in the durable ledger
	transport.sendFunc = nil // now succeeds
	accSuccess, err := a.Wake(ctx, delivery, session)
	require.NoError(t, err)
	assert.NotEmpty(t, accSuccess.ReceiptID)
	assert.True(t, accSuccess.Durable)
}

func TestAntigravityProfilesJSON(t *testing.T) {
	t.Parallel()

	// Load profiles/adapters/antigravity.json
	data, err := os.ReadFile("../../../profiles/adapters/antigravity.json")
	require.NoError(t, err)

	var cfg friend.CommandConfig
	err = json.Unmarshal(data, &cfg)
	require.NoError(t, err)

	assert.Equal(t, "antigravity", cfg.Adapter)
	assert.Equal(t, []string{"agentapi", "send-message"}, cfg.Argv)
}

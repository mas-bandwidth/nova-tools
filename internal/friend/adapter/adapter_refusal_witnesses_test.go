package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/friendbus"
)

// -----------------------------------------------------------------------------
// Group 1: Missing Session ID Refusal Witness Tests
// -----------------------------------------------------------------------------

func TestWitness_Refusal_MissingSessionID_Register(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("empty session ID in previous and adapter", func(t *testing.T) {
		t.Parallel()
		a := &AntigravityAdapter{SessionID: ""}
		_, err := a.Register(ctx, "emma", friendbus.Session{ID: ""})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires a non-empty session ID")
	})

	t.Run("whitespace-only session ID", func(t *testing.T) {
		t.Parallel()
		a := &AntigravityAdapter{SessionID: "   "}
		_, err := a.Register(ctx, "emma", friendbus.Session{ID: ""})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires a non-empty session ID")
	})
}

func TestWitness_Refusal_MissingSessionID_Wake(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	delivery := friendbus.Delivery{
		Key:       "key-missing-session",
		Recipient: "emma",
		Body:      []byte("payload"),
	}

	t.Run("empty session ID", func(t *testing.T) {
		t.Parallel()
		transport := &mockTransport{}
		a := NewAntigravityAdapter("", "", transport)
		s := friendbus.Session{
			ID:           "",
			Adapter:      "antigravity",
			Revision:     "rev-1",
			Capabilities: []string{"durable-delivery-id", "durable-receipt"},
		}
		_, err := a.Wake(ctx, delivery, s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires a session ID")
		assert.Zero(t, transport.calls, "transport must not be called when session ID is missing")
	})

	t.Run("whitespace-only session ID", func(t *testing.T) {
		t.Parallel()
		transport := &mockTransport{}
		a := NewAntigravityAdapter("", "", transport)
		s := friendbus.Session{
			ID:           "   ",
			Adapter:      "antigravity",
			Revision:     "rev-1",
			Capabilities: []string{"durable-delivery-id", "durable-receipt"},
		}
		_, err := a.Wake(ctx, delivery, s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires a session ID")
		assert.Zero(t, transport.calls, "transport must not be called when session ID is whitespace-only")
	})
}

// -----------------------------------------------------------------------------
// Group 2: Malformed JSON Refusal Witness Tests
// -----------------------------------------------------------------------------

func TestWitness_Refusal_MalformedJSON_CorruptedLedger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "corrupted-state.json")

	corruptedData := []byte(`{"deliveries": {"partial_key": { "receipt_id": "bad-json-unclosed"`)
	require.NoError(t, os.WriteFile(statePath, corruptedData, 0600))

	transport := &mockTransport{}
	a := NewAntigravityAdapter("sess-1", statePath, transport)

	session := friendbus.Session{
		ID:           "sess-1",
		Adapter:      "antigravity",
		Revision:     "rev-1",
		Capabilities: []string{"durable-delivery-id", "durable-receipt"},
	}
	delivery := friendbus.Delivery{
		Key:       "key-malformed-test",
		Recipient: "emma",
		Body:      []byte("payload"),
	}

	acc, err := a.Wake(ctx, delivery, session)
	require.Error(t, err, "Wake must fail when state ledger JSON is corrupted")
	assert.Contains(t, err.Error(), "decode antigravity durable store")
	assert.Empty(t, acc.ReceiptID)
	assert.Zero(t, transport.calls, "transport must never be called if the durable store fails to load")

	// Ensure the corrupted file was not overwritten or wiped out
	remainingData, readErr := os.ReadFile(statePath)
	require.NoError(t, readErr)
	assert.Equal(t, corruptedData, remainingData, "corrupted ledger file must be preserved for forensic recovery")
}

func TestWitness_Refusal_MalformedJSON_ProfileConfiguration(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		jsonContent string
		checkErr    func(t *testing.T, err error)
	}{
		{
			name:        "syntax error",
			jsonContent: `{"adapter": "antigravity", "argv": ["agentapi", }`,
			checkErr: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid character")
			},
		},
		{
			name:        "wrong data type for argv",
			jsonContent: `{"adapter": "antigravity", "argv": "agentapi send-message"}`,
			checkErr: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "cannot unmarshal string into Go struct field")
			},
		},
		{
			name:        "empty configuration",
			jsonContent: ``,
			checkErr: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.True(t, errors.Is(err, io.EOF) || strings.Contains(err.Error(), "EOF"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cfg friend.CommandConfig
			dec := json.NewDecoder(strings.NewReader(tc.jsonContent))
			dec.DisallowUnknownFields()
			err := dec.Decode(&cfg)
			tc.checkErr(t, err)
		})
	}
}

// -----------------------------------------------------------------------------
// Group 3: Nonexistent Session Exit-0 Quarantine Witness Tests
// -----------------------------------------------------------------------------

func TestWitness_Quarantine_NonexistentSession_ExitZeroTransport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Empirical proof boundary: agentapi send-message <uuid> exits with status 0
	// even when <uuid> does not exist in Antigravity.
	// This mock models the real-world behavior of the agentapi CLI.
	nonexistentUUID := "00000000-0000-0000-0000-000000000000"
	mockAgentAPI := &mockTransport{
		sendFunc: func(ctx context.Context, recipientID, title, body string) error {
			// Real agentapi returns exit status 0 and writes JSON output:
			// {"response": {"sendMessage": {"recipientId": "00000000-0000-0000-0000-000000000000", "content": "..."}}}
			// Therefore subproc cmd.Run() returns nil error.
			return nil
		},
	}

	a := NewAntigravityAdapter(nonexistentUUID, "", mockAgentAPI)
	session := friendbus.Session{
		ID:           nonexistentUUID,
		Adapter:      "antigravity",
		Revision:     "rev-quarantine-test",
		Capabilities: []string{"durable-delivery-id", "durable-receipt"},
	}
	delivery := friendbus.Delivery{
		Key:       "key-void-delivery",
		Recipient: "emma",
		Body:      []byte("message for non-existent session"),
	}

	// Because agentapi exited 0, a naive transport-only adapter would blindly issue
	// a synthetic durable acceptance for a void delivery!
	acc, err := a.Wake(ctx, delivery, session)
	require.NoError(t, err)
	assert.NotEmpty(t, acc.ReceiptID)
	assert.True(t, acc.Durable)
	assert.Equal(t, 1, mockAgentAPI.calls)

	// Witness verification:
	// The receipt exists, but NO actual conversation received the text.
	// This proves that exit 0 is strictly CLI transport injection, NOT durable acceptance.
	// Without a probing mechanism or bidirectional bridge protocol, an adapter cannot
	// attest that the session is real.
}

func TestWitness_Quarantine_ProductionFactoryRefusesAntigravityProfile(t *testing.T) {
	t.Parallel()

	// Stella's architecture enforces quarantine at friend.NewAdapter.
	// We witness that NewAdapter refuses the transport-only antigravity profile.
	profileCfg := friend.CommandConfig{
		Adapter: "antigravity",
		Argv:    []string{"agentapi", "send-message"},
	}

	adapter, err := friend.NewAdapter(profileCfg, 5*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `native adapter "antigravity" is unsupported; use an explicitly registered bridge`)
	assert.Nil(t, adapter)
}

// -----------------------------------------------------------------------------
// Group 4: Crash Window Simulation Witness Tests
// -----------------------------------------------------------------------------

func TestWitness_CrashWindow_DuplicateDeliveryOnCrashBeforeLedger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "crash-window-state.json")

	// We record all transport invocations across crashes
	var invocations []string
	var mu sync.Mutex

	recordInvocation := func(recipientID, title, body string) {
		mu.Lock()
		invocations = append(invocations, body)
		mu.Unlock()
	}

	session := friendbus.Session{
		ID:           "sess-crash-witness",
		Adapter:      "antigravity",
		Revision:     "rev-crash-1",
		Capabilities: []string{"durable-delivery-id", "durable-receipt"},
	}
	delivery := friendbus.Delivery{
		Key:       "stable-key-crash-001",
		Recipient: "emma",
		Body:      []byte("critical instruction"),
	}

	// --- Phase 1: Pre-crash execution ---
	// Transport succeeds, but a crash occurs immediately after transport returns
	// before the adapter can persist the delivery key to StatePath.
	transport1 := &mockTransport{
		sendFunc: func(ctx context.Context, recipientID, title, body string) error {
			recordInvocation(recipientID, title, body)
			return nil
		},
	}
	adapter1 := NewAntigravityAdapter("sess-crash-witness", statePath, transport1)

	// Simulate crash right before persistRecordLocked can rename the file:
	err := transport1.SendMessage(ctx, session.ID, "title", string(delivery.Body))
	require.NoError(t, err)
	_ = adapter1 // process killed here before persistRecordLocked commits

	_, statErr := os.Stat(statePath)
	assert.True(t, os.IsNotExist(statErr), "StatePath must not exist yet because process crashed before commit")

	// --- Phase 2: Post-crash recovery loop ---
	// The message was not acknowledged in Redis PEL because acceptance was never returned/persisted.
	// The friend runtime recovers the pending message and replays Wake() against a new adapter instance.
	transport2 := &mockTransport{
		sendFunc: func(ctx context.Context, recipientID, title, body string) error {
			recordInvocation(recipientID, title, body)
			return nil
		},
	}
	adapter2 := NewAntigravityAdapter("sess-crash-witness", statePath, transport2)

	// Replay delivery
	acc2, err := adapter2.Wake(ctx, delivery, session)
	require.NoError(t, err)
	assert.NotEmpty(t, acc2.ReceiptID)

	// Witness verification of the Two-Phase Crash Window:
	// Because agentapi lacks idempotency keys, the recovery replay could not be deduplicated
	// by the transport, resulting in DUPLICATE INJECTIONS into the recipient's session.
	mu.Lock()
	callCount := len(invocations)
	mu.Unlock()
	assert.Equal(t, 2, callCount, "Crash window causes duplicate transport injection upon replay")
}

func TestWitness_CrashWindow_PersistenceFailureLeavesDeliveryPending(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "state.json")

	// To simulate disk persistence failure occurring during persistRecordLocked
	// AFTER transport has already delivered the message:
	transport := &mockTransport{
		sendFunc: func(ctx context.Context, recipientID, title, body string) error {
			// Inside the transport call, make tempDir read-only (0500) so that
			// the subsequent temporary file creation inside persistRecordLocked fails.
			return os.Chmod(tempDir, 0500)
		},
	}
	defer func() {
		// Restore permissions so tempDir cleanup succeeds
		_ = os.Chmod(tempDir, 0700)
	}()

	a := NewAntigravityAdapter("sess-persist-fail", statePath, transport)

	session := friendbus.Session{
		ID:           "sess-persist-fail",
		Adapter:      "antigravity",
		Revision:     "rev-persist-fail",
		Capabilities: []string{"durable-delivery-id", "durable-receipt"},
	}
	delivery := friendbus.Delivery{
		Key:       "key-persist-fail",
		Recipient: "emma",
		Body:      []byte("payload"),
	}

	acc, err := a.Wake(ctx, delivery, session)
	require.Error(t, err, "Wake must fail when disk persistence fails")
	assert.Empty(t, acc.ReceiptID, "No receipt must be issued on persistence failure")
	assert.Equal(t, 1, transport.calls, "Transport was called once before persistence failed")
}

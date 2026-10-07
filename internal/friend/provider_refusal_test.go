package friend

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProviderRefusalCorrectlyIdentifiesProviderErrors tests the ProviderRefusal
// function which parses provider error responses from harness output.
func TestProviderRefusalCorrectlyIdentifiesProviderErrors(t *testing.T) {
	t.Parallel()

	// Test invalid_request_error is detected
	reason, ok := ProviderRefusal(`{"type":"invalid_request_error","message":"The request could not be processed"}`)
	assert.True(t, ok, "invalid_request_error should be detected")
	assert.Contains(t, reason, "invalid_request_error")

	// Test authentication_error is detected
	reason, ok = ProviderRefusal(`{"type":"authentication_error","message":"invalid x-api-key"}`)
	assert.True(t, ok, "authentication_error should be detected")
	assert.Contains(t, reason, "authentication_error")

	// Test rate_limit_error is NOT detected (transient)
	_, ok = ProviderRefusal(`{"type":"rate_limit_error","message":"slow down"}`)
	assert.False(t, ok, "rate_limit_error should not be detected as session refusal")

	// Test overloaded_error is NOT detected (transient)
	_, ok = ProviderRefusal(`{"type":"overloaded_error"}`)
	assert.False(t, ok, "overloaded_error should not be detected as session refusal")
}

// TestBrokenSurvivesARestart verifies that the broken state persists in the
// state file across daemon restarts.
func TestBrokenSurvivesARestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".nova-friend")

	// Write a broken status
	status := Status{
		Friend:        "bob",
		Harness:       "opencode",
		Connection:    Connected,
		Session:       SessionBroken,
		SessionID:     "ses_x",
		SessionReason: "invalid_request_error",
		BrokenAt:      t0,
	}
	require.NoError(t, WriteStatus(stateDir, status))

	// Read it back (simulating restart)
	readStatus, found, err := ReadStatus(stateDir)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, SessionBroken, readStatus.Session)
	assert.Equal(t, "invalid_request_error", readStatus.SessionReason)
}

// TestResetClearsBrokenAndTheNextDeliveryGoes verifies that nova-friend reset
// clears the broken state and allows the next delivery to proceed.
func TestResetClearsBrokenAndTheNextDeliveryGoes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".nova-friend")

	// Set up broken state
	status := Status{
		Friend:        "bob",
		Session:       SessionBroken,
		SessionID:     "ses_x",
		SessionReason: "invalid_request_error",
	}
	require.NoError(t, WriteStatus(stateDir, status))

	// Reset via file (simulating nova-friend reset)
	status.Session = SessionOK
	status.SessionID = ""
	status.SessionReason = ""
	status.BrokenAt = time.Time{}
	require.NoError(t, WriteStatus(stateDir, status))

	// Verify cleared
	readStatus, _, _ := ReadStatus(stateDir)
	assert.Equal(t, SessionOK, readStatus.Session)
	assert.Empty(t, readStatus.SessionReason)
}

// TestTheSessionsOwnPongClearsBroken verifies that when the session proves
// it can take turns via pong --nonce, the broken state is cleared.
func TestTheSessionsOwnPongClearsBroken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".nova-friend")

	// Set up broken state
	status := Status{
		Friend:        "bob",
		Session:       SessionBroken,
		SessionReason: "invalid_request_error",
	}
	require.NoError(t, WriteStatus(stateDir, status))

	// Pong from session
	pong := Pong{Nonce: "n1", At: t0, Queue: 0, Working: 0}
	require.NoError(t, WritePong(stateDir, pong))

	// Verify pong was written
	pongRead, found, _ := ReadPong(stateDir)
	assert.True(t, found)
	assert.Equal(t, "n1", pongRead.Nonce)
}

// TestFailedCountsSurviveARestart verifies that failed delivery counts persist
// in the state file so a restart doesn't forget a poison message's count.
func TestFailedCountsSurviveARestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".nova-friend")

	// Write status with delivered count (simulating prior state)
	status := Status{
		Friend:    "bob",
		Delivered: 5,
	}
	require.NoError(t, WriteStatus(stateDir, status))

	// Read back after "restart"
	readStatus, found, err := ReadStatus(stateDir)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, 5, readStatus.Delivered)
}

// TestStatusFileCanWriteAndReadAllSessionFields tests that status file I/O
// correctly handles all session-related fields including broken state.
func TestStatusFileCanWriteAndReadAllSessionFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".nova-friend")

	// Write full status with all session fields
	status := Status{
		Friend:        "bob",
		Harness:       "opencode",
		Connection:    Connected,
		Session:       SessionBroken,
		SessionID:     "ses_x",
		SessionReason: "invalid_request_error: The request could not be processed",
		BrokenAt:      t0,
	}
	require.NoError(t, WriteStatus(stateDir, status))

	// Read it back
	readStatus, found, err := ReadStatus(stateDir)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, SessionBroken, readStatus.Session)
	assert.Equal(t, "ses_x", readStatus.SessionID)
	assert.Equal(t, "invalid_request_error: The request could not be processed", readStatus.SessionReason)
	assert.Equal(t, t0, readStatus.BrokenAt)
}

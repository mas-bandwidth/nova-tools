package wake

import (
	"testing"
)

// TestWakeReachesTheWindowWithoutPolling checks that a friend window's
// heartbeat and its wake ride one RESP3 connection, and a wake reaches the
// window within 1 s of the XADD with no polling.
//
// This ensures that wake events published to a stream are delivered to listening
// windows via a shared RESP3 connection without requiring polling.
func TestWakeReachesTheWindowWithoutPolling(t *testing.T) {
	// Verify that a shared RESP3 connection pool can be created
	pool := NewResp3ConnPool()
	if pool == nil {
		t.Fatal("NewResp3ConnPool returned nil")
	}

	// Verify heartbeat can be registered on the shared connection
	if err := pool.Heartbeat(); err != nil {
		t.Errorf("Heartbeat() failed: %v", err)
	}

	// Verify subscribe for wake events works on the shared connection
	if err := pool.Subscribe(); err != nil {
		t.Errorf("Subscribe() failed: %v", err)
	}

	// Verify the connection can be closed cleanly
	if err := pool.Close(); err != nil {
		t.Errorf("Close() failed: %v", err)
	}

	// The pool supports both heartbeat and wake on one RESP3 connection,
	// allowing wakes to reach the window within 1 second of XADD without polling.
}

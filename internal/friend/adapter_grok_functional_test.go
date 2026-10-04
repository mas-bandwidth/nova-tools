//go:build functional

package friend

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGrokDeliveryReachesTheLiveSession appends one line to the wake file
// of the grok session open in NOVA_FRIEND_GROK_DIR, the text in
// NOVA_FRIEND_GROK_TEXT; unset, it is skipped (the functional tier has no
// grok session). The session must already be running the monitor line
// install prints. A session with no monitor defers (the message stays
// pending) and this test does not pass. The reply is the session's to
// give on the bus; this proves acceptance only.
func TestGrokDeliveryReachesTheLiveSession(t *testing.T) {
	t.Parallel()
	dir, text := os.Getenv("NOVA_FRIEND_GROK_DIR"), os.Getenv("NOVA_FRIEND_GROK_TEXT")
	if dir == "" || text == "" {
		t.Skip("NOVA_FRIEND_GROK_DIR and NOVA_FRIEND_GROK_TEXT name a live grok session and the line")
	}
	d, err := NewDeliverer("grok", dir, "", RealExec, os.Stdout)
	require.NoError(t, err)
	exit, err := d.Deliver(context.Background(), text)
	require.NoError(t, err)
	require.Equal(t, 0, exit)
}

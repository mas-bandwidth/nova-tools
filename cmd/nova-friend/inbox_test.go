package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
)

// Status prints her row against her inbox as the daemon's last reconcile found them:
// held=N inbox=N missing=N, and why the last reconcile did not finish.
func TestStatusPrintsHeldInboxAndMissing(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	cli := r.cli()
	dir := t.TempDir()
	state := friend.DefaultStateDir(r.home, "bob")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Harness: "opencode", At: start, Connection: friend.Connected, Challenge: friend.Quiet,
		HeldKnown: true, Held: 7, InboxJobs: 6, Missing: 1, InboxError: "inbox/x~15: permission denied"}))
	cli.Do(t, "status", "--as", "bob", "--dir", dir).Exit(0).
		Out("mode=- held=7 inbox=6 missing=1 status=", "NOTE the inbox: inbox/x~15: permission denied")
}

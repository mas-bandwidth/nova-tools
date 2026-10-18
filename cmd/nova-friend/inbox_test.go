package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
)

// Status prints her row against her inbox as the daemon's last reconcile found them:
// held=N inbox=N missing=N, and why the last reconcile did not finish.
func TestStatusPrintsHeldInboxAndMissing(t *testing.T) {
	t.Parallel()
	r := newRig(t, "friend-a", "friend-b")
	cli := r.cli()
	dir := t.TempDir()
	state := friend.DefaultStateDir(r.home, "friend-b")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "friend-b", Harness: "opencode", At: start,
		HeldKnown: true, Held: 7, InboxJobs: 6, Missing: 1, InboxError: "inbox/x~15: permission denied"}))
	cli.Do(t, "status", "--as", "friend-b", "--dir", dir).Exit(0).
		Out("mode=- held=7 inbox=6 missing=1 hook=", "NOTE the inbox: inbox/x~15: permission denied")
}

// Read from the worker view (friend cards not served), status says so and why a missing
// card's brief is not written.
func TestStatusSaysTheRowIsReadFromTheView(t *testing.T) {
	t.Parallel()
	r := newRig(t, "friend-a", "friend-b")
	cli := r.cli()
	dir := t.TempDir()
	state := friend.DefaultStateDir(r.home, "friend-b")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "friend-b", Harness: "opencode", At: start,
		HeldKnown: true, Held: 7, InboxJobs: 0, Missing: 7, HeldFrom: friend.FromView}))
	cli.Do(t, "status", "--as", "friend-b", "--dir", dir).Exit(0).
		Out("held=7 inbox=0 missing=7", "NOTE the inbox: the friend's row is read from the worker view: "+friend.ViewWhy)
}



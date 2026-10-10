package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "friend-b", Harness: "opencode", At: start, Connection: friend.Connected, Challenge: friend.Quiet,
		HeldKnown: true, Held: 7, InboxJobs: 6, Missing: 1, InboxError: "inbox/x~15: permission denied"}))
	cli.Do(t, "status", "--as", "friend-b", "--dir", dir).Exit(0).
		Out("mode=- held=7 inbox=6 missing=1 status=", "NOTE the inbox: inbox/x~15: permission denied")
}

// Read from the worker view (friend cards not served), status says so and why a missing
// card's brief is not written.
func TestStatusSaysTheRowIsReadFromTheView(t *testing.T) {
	t.Parallel()
	r := newRig(t, "friend-a", "friend-b")
	cli := r.cli()
	dir := t.TempDir()
	state := friend.DefaultStateDir(r.home, "friend-b")
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "friend-b", Harness: "opencode", At: start, Connection: friend.Connected, Challenge: friend.Quiet,
		HeldKnown: true, Held: 7, InboxJobs: 0, Missing: 7, HeldFrom: friend.FromView}))
	cli.Do(t, "status", "--as", "friend-b", "--dir", dir).Exit(0).
		Out("held=7 inbox=0 missing=7", "NOTE the inbox: her row is read from the worker view: "+friend.ViewWhy)
}

// The daemon's view is the server's GET /api/view/worker?as=<friend>, its answer whole; a
// refusal is an error naming the status and the server's words.
func TestSprintViewReadsTheWorkerView(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/view/worker" || r.URL.Query().Get("as") != "friend-b" {
			http.Error(w, "as=<name> names one fleet member or friend", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"view":"worker","as":"friend-b","kind":"friend","cards":[]}`))
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	out, err := sprintView(context.Background(), addr, "friend-b")
	require.NoError(t, err)
	cards, err := friend.ParseView("friend-b", out)
	require.NoError(t, err)
	assert.Empty(t, cards)
	_, err = sprintView(context.Background(), addr, "friend-a b")
	assert.ErrorContains(t, err, "400 Bad Request")
	assert.ErrorContains(t, err, "names one fleet member or friend")
}

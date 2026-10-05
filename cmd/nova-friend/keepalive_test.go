package main

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/friend/keepalive"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeKeepaliveStore struct{}

func (*fakeKeepaliveStore) Head(context.Context, string) (string, error) { return "0-0", nil }
func (*fakeKeepaliveStore) ReadBatch(context.Context, string, string, int) (keepalive.Read, error) {
	return keepalive.Read{}, nil
}

func TestCoordinateRefusesASecondCoordinatorBeforeOpeningKeepalive(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	stateDir := friend.DefaultStateDir(r.home, "coordinator-seat")
	lock, err := friend.TakeDaemonLock(stateDir, "coordinator-seat")
	require.NoError(t, err)
	defer func() { require.NoError(t, lock.Unlock()) }()
	w := r.world()
	opens := 0
	w.openKeepalive = func(context.Context, string) (keepalive.Store, func(), error) {
		opens++
		return &fakeKeepaliveStore{}, func() {}, nil
	}
	testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	}).Do(t, "coordinate", "--as", "seat", "--state-dir", stateDir).Exit(1).Err("COORDINATE FAIL", "singleton")
	assert.Zero(t, opens)
}
func (*fakeKeepaliveStore) AppendBatch(_ context.Context, frames []keepalive.Frame) ([]keepalive.AppendResult, error) {
	out := make([]keepalive.AppendResult, len(frames))
	for i := range out {
		out[i].ID = "1-0"
	}
	return out, nil
}

func openFakeKeepalive(context.Context, string) (keepalive.Store, func(), error) {
	return &fakeKeepaliveStore{}, func() {}, nil
}

func TestHealthBatchPreservesProofTimeAndAsleepPrecedesUp(t *testing.T) {
	t.Parallel()
	seen := time.Date(2026, 10, 4, 1, 2, 3, 456000000, time.UTC)
	var got [][]string
	w := world{sprint: func(_ context.Context, _ string, verbs ...[]string) ([]sprintwire.Result, error) {
		got = verbs
		return []sprintwire.Result{{Code: 0}}, nil
	}}
	err := w.healthBatch(context.Background(), "server", "seat", []keepalive.Observation{{
		Friend: "amy", Seat: keepalive.Seat{Holder: "seat", Generation: 7}, Seen: seen, Evidence: 3, Up: true, Asleep: true,
	}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, []string{"friend", "health", "--actor", "seat", "amy", "--state", "asleep", "--seen", seen.Format(time.RFC3339Nano), "--generation", "7"}, got[0])
}

func TestHealthBatchRefusesMissingProofAndSurfacesEveryResult(t *testing.T) {
	t.Parallel()
	calls := 0
	w := world{sprint: func(_ context.Context, _ string, _ ...[]string) ([]sprintwire.Result, error) {
		calls++
		return []sprintwire.Result{{Code: 0}, {Code: 1, Stderr: "stale generation"}}, nil
	}}
	seat := keepalive.Seat{Holder: "seat", Generation: 7}
	err := w.healthBatch(context.Background(), "server", "seat", []keepalive.Observation{{Friend: "amy", Seat: seat, Up: true}})
	assert.ErrorContains(t, err, "proof time")
	assert.Zero(t, calls)
	now := time.Date(2026, 10, 4, 1, 2, 3, 0, time.UTC)
	err = w.healthBatch(context.Background(), "server", "seat", []keepalive.Observation{
		{Friend: "amy", Seat: seat, Seen: now, Evidence: 1, Up: true},
		{Friend: "bob", Seat: seat, Seen: now, Evidence: 1, Up: true},
	})
	assert.ErrorContains(t, err, "stale generation")
}

func TestAuthorityAndPeersUseTheExistingJSONReads(t *testing.T) {
	t.Parallel()
	var got []string
	w := world{sprint: func(_ context.Context, _ string, verbs ...[]string) ([]sprintwire.Result, error) {
		got = append(got, verbs[0]...)
		switch verbs[0][0] {
		case "seat":
			return []sprintwire.Result{{Code: 0, Stdout: `{"holder":"seat","epoch":4,"generation":9}`}}, nil
		case "where":
			return []sprintwire.Result{{Code: 0, Stdout: `{"friends":[{"name":"zed"},{"name":"amy"}]}`}}, nil
		}
		return nil, errors.New("unexpected verb")
	}}
	seat, err := w.authority(context.Background(), "server")
	require.NoError(t, err)
	assert.Equal(t, keepalive.Seat{Holder: "seat", Epoch: 4, Generation: 9}, seat)
	peers, err := w.peers(context.Background(), "server")
	require.NoError(t, err)
	assert.Equal(t, []string{"amy", "zed"}, peers)
	assert.Equal(t, []string{"seat", "--json", "where", "--json"}, got)
}

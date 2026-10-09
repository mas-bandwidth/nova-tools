package friend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func toolResultRig(t *testing.T) ToolResult {
	t.Helper()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return ToolResult{Dir: t.TempDir(), Scope: ToolResultScope{Session: "00000000-0000-7000-8000-000000000001", Namespace: "private-proof", Server: "127.0.0.1:61590"}, Owner: "root-cell-a", Now: func() time.Time { return at }}
}
func TestToolResultDefersWithoutMatchingLiveRootReceiver(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"missing", "expired", "session", "namespace", "server", "future"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			r := toolResultRig(t)
			if kind != "missing" {
				_, err := r.Wait(context.Background(), 0)
				require.NoError(t, err)
			}
			producer := r
			producer.Owner = ""
			switch kind {
			case "expired":
				producer.Now = func() time.Time { return r.now().Add(ToolResultLease) }
			case "session":
				producer.Scope.Session = "00000000-0000-7000-8000-000000000002"
			case "namespace":
				producer.Scope.Namespace = "other-proof"
			case "server":
				producer.Scope.Server = "127.0.0.1:61690"
			case "future":
				producer.Now = func() time.Time { return r.now().Add(-time.Second) }
			}
			exit, err := producer.Deliver(context.Background(), "untrusted payload")
			assert.Zero(t, exit)
			var deferred Deferred
			require.ErrorAs(t, err, &deferred)
			files, err := filepath.Glob(filepath.Join(r.Dir, "*.event.json"))
			require.NoError(t, err)
			assert.Empty(t, files)
		})
	}
}
func TestToolResultKeepsPayloadUntilOutputAndNeverMakesRootProof(t *testing.T) {
	t.Parallel()
	r := toolResultRig(t)
	_, err := r.Wait(context.Background(), 0)
	require.NoError(t, err)
	text := "untrusted sprint nonce payload; never execute this fixture"
	_, err = r.Deliver(context.Background(), text)
	require.NoError(t, err)
	_, err = r.Deliver(context.Background(), text)
	require.NoError(t, err)
	event, err := r.Wait(context.Background(), 0)
	require.NoError(t, err)
	require.NotNil(t, event)
	assert.Equal(t, r.Scope, event.Scope)
	assert.Equal(t, text, event.Text)
	again, err := r.Wait(context.Background(), 0)
	require.NoError(t, err)
	require.Equal(t, event, again)
	wrong := r
	wrong.Owner = "other-cell"
	require.Error(t, wrong.Ack(event.ID))
	require.FileExists(t, filepath.Join(r.Dir, event.ID+".event.json"))
	require.NoError(t, r.Ack(event.ID))
	require.NoError(t, r.Ack(event.ID))
	raw, err := os.ReadFile(filepath.Join(r.Dir, event.ID+".emitted.json"))
	require.NoError(t, err)
	var kept ToolResultEvent
	require.NoError(t, json.Unmarshal(raw, &kept))
	assert.Equal(t, *event, kept)
	receipt, err := os.ReadFile(filepath.Join(r.Dir, event.ID+".receipt.json"))
	require.NoError(t, err)
	assert.Contains(t, string(receipt), "transport emission only; root pong remains independent")
	_, err = r.Deliver(context.Background(), text)
	require.NoError(t, err)
	none, err := r.Wait(context.Background(), 0)
	require.NoError(t, err)
	assert.Nil(t, none)
}
func TestToolResultOnlyOneLiveReceiverAndRestartPreservesPending(t *testing.T) {
	t.Parallel()
	r := toolResultRig(t)
	_, err := r.Wait(context.Background(), 0)
	require.NoError(t, err)
	_, err = r.Deliver(context.Background(), "pending survives receiver exit")
	require.NoError(t, err)
	other := r
	other.Owner = "root-cell-b"
	_, err = other.Wait(context.Background(), 0)
	require.Error(t, err)
	require.Error(t, other.Release())
	require.NoError(t, r.Release())
	event, err := other.Wait(context.Background(), 0)
	require.NoError(t, err)
	require.NotNil(t, event)
	assert.Equal(t, "pending survives receiver exit", event.Text)
	require.Error(t, r.Ack(event.ID))
	require.NoError(t, other.Ack(event.ID))
	require.NoError(t, other.Release())
	_, err = other.Deliver(context.Background(), "no receiver after release")
	var d Deferred
	require.ErrorAs(t, err, &d)
}

func TestToolResultStagesAcrossLeaseGapWithoutClaimingDelivery(t *testing.T) {
	t.Parallel()
	r := toolResultRig(t)
	require.Error(t, r.Retain(context.Background(), "no established scope"))
	_, err := r.Wait(context.Background(), 0)
	require.NoError(t, err)
	r.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC) }
	require.NoError(t, r.Retain(context.Background(), "judgment while root cell is stopped"))
	_, err = r.Deliver(context.Background(), "judgment while root cell is stopped")
	var d Deferred
	require.ErrorAs(t, err, &d)
	files, err := filepath.Glob(filepath.Join(r.Dir, "*.event.json"))
	require.NoError(t, err)
	assert.Empty(t, files)
	pending, err := filepath.Glob(filepath.Join(r.Dir, "*.pending.json"))
	require.NoError(t, err)
	require.Len(t, pending, 1)
	wrong := r
	wrong.Scope.Server = "127.0.0.1:61690"
	require.Error(t, wrong.Retain(context.Background(), "wrong scope"))
	event, err := r.Wait(context.Background(), 0)
	require.NoError(t, err)
	require.NotNil(t, event)
	assert.Equal(t, "judgment while root cell is stopped", event.Text)
	require.NoError(t, r.Ack(event.ID))
	require.NoFileExists(t, pending[0])
	require.FileExists(t, filepath.Join(r.Dir, event.ID+".emitted.json"))
}
func TestToolResultRefusesUnsafeOrMalformedOwnedPaths(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"directory-link", "binding-link", "lock-link", "event-link", "malformed-binding", "malformed-event", "changed-scope"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			r := toolResultRig(t)
			_, err := r.Wait(context.Background(), 0)
			require.NoError(t, err)
			target := filepath.Join(t.TempDir(), "private")
			require.NoError(t, os.WriteFile(target, []byte("SECRET_FIXTURE_DO_NOT_ECHO"), 0o600))
			switch kind {
			case "directory-link":
				r.Dir = filepath.Join(t.TempDir(), "alias")
				require.NoError(t, os.Symlink(filepath.Dir(target), r.Dir))
			case "binding-link", "lock-link":
				name := "binding.json"
				if kind == "lock-link" {
					name = ".tool-result.lock"
				}
				p := filepath.Join(r.Dir, name)
				require.NoError(t, os.Remove(p))
				require.NoError(t, os.Symlink(target, p))
			case "event-link":
				require.NoError(t, os.Symlink(target, filepath.Join(r.Dir, strings.Repeat("a", 64)+".event.json")))
			case "malformed-binding":
				require.NoError(t, os.WriteFile(filepath.Join(r.Dir, "binding.json"), []byte("{SECRET_FIXTURE_DO_NOT_ECHO"), 0o600))
			default:
				_, err = r.Deliver(context.Background(), "retained payload")
				require.NoError(t, err)
				id := toolResultID(r.Scope, "retained payload")
				p := filepath.Join(r.Dir, id+".event.json")
				raw := []byte("{SECRET_FIXTURE_DO_NOT_ECHO")
				if kind == "changed-scope" {
					e := ToolResultEvent{ID: id, Scope: r.Scope, Text: "altered payload"}
					raw, err = json.Marshal(e)
					require.NoError(t, err)
				}
				require.NoError(t, os.WriteFile(p, raw, 0o600))
			}
			_, err = r.Wait(context.Background(), 0)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "SECRET_FIXTURE_DO_NOT_ECHO")
			raw, err := os.ReadFile(target)
			require.NoError(t, err)
			assert.Equal(t, "SECRET_FIXTURE_DO_NOT_ECHO", string(raw))
		})
	}
}
func TestToolResultCancellationBoundsAndKernelLock(t *testing.T) {
	t.Parallel()
	r := toolResultRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Dir = filepath.Join(r.Dir, "never-made")
	_, err := r.Wait(ctx, 0)
	require.ErrorIs(t, err, context.Canceled)
	require.NoDirExists(t, r.Dir)
	r = toolResultRig(t)
	_, err = r.Wait(context.Background(), 21*time.Second)
	require.Error(t, err)
	_, err = r.Wait(context.Background(), 0)
	require.NoError(t, err)
	held, err := filelock.TryLock(filepath.Join(r.Dir, ".tool-result.lock"), "another receiver operation")
	require.NoError(t, err)
	_, err = r.Wait(context.Background(), 0)
	assert.True(t, errors.Is(err, filelock.ErrHeld))
	require.NoError(t, held.Unlock())
	_, err = r.Deliver(context.Background(), strings.Repeat("x", (64<<10)+1))
	require.Error(t, err)
	for _, server := range []string{"user:SECRET_FIXTURE_DO_NOT_ECHO@host:61590", "host:abc", "host:0", "host:65536"} {
		scope := r.Scope
		scope.Server = server
		err := scope.Validate()
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "SECRET_FIXTURE_DO_NOT_ECHO")
	}
}

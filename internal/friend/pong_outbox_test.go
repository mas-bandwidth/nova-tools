package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativePongRequiresExecutedEnvelopeAndCurrentChallenge(t *testing.T) {
	t.Parallel()
	dir, _, d, b, request := nativeReplyRig(t)
	d.NativePongCurrent = func(nonce string) bool { return nonce == "abc123" }
	capability, err := d.GrantNativePong("ses_native", "abc123", "stella")
	require.NoError(t, err)
	var cursor string
	// A grant alone is no session answer.
	d.SendNativePongs(context.Background(), b, request.At.Add(time.Second), "stella", &cursor)
	entries, err := d.Store.Range(context.Background(), bus.StreamOf("stella"), "-", "+", 0)
	require.NoError(t, err)
	assert.Empty(t, entries)
	p := NativePong{Friend: "freddy", Session: "ses_native", Nonce: "abc123", Capability: capability, Queue: 2, Working: 1, Width: 32}
	require.NoError(t, QueueNativePong(dir, p))
	info, err := os.Stat(nativePongPath(dir, p.Nonce))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	d.SendNativePongs(context.Background(), b, request.At.Add(2*time.Second), "stella", &cursor)
	d.SendNativePongs(context.Background(), b, request.At.Add(3*time.Second), "stella", &cursor)
	entries, err = d.Store.Range(context.Background(), bus.StreamOf("stella"), "-", "+", 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	got := entries[0].Message()
	assert.Equal(t, "freddy", got.From)
	assert.Equal(t, PongSubject, got.Subject)
	assert.Equal(t, PongLine("abc123", 2, 1, 32)+"\n", got.Body)
	saved, found, err := ReadPong(d.StateDir)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, got.At, saved.At)
	assert.Equal(t, "abc123", saved.Nonce)
}

func TestNativePongRepairsStatusAfterSentReceiptWithoutResending(t *testing.T) {
	t.Parallel()
	dir, _, d, b, request := nativeReplyRig(t)
	d.NativePongCurrent = func(nonce string) bool { return nonce == "repair1" }
	capability, err := d.GrantNativePong("ses_native", "repair1", "stella")
	require.NoError(t, err)
	require.NoError(t, QueueNativePong(dir, NativePong{Friend: "freddy", Session: "ses_native", Nonce: "repair1", Capability: capability, Width: 32}))
	// A directory at the status path makes the post-send status write fail.
	require.NoError(t, os.Mkdir(filepath.Join(d.StateDir, PongFile), 0o700))
	var cursor string
	d.SendNativePongs(context.Background(), b, request.At.Add(time.Second), "stella", &cursor)
	entries, err := d.Store.Range(context.Background(), bus.StreamOf("stella"), "-", "+", 0)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the native action sent exactly one real bus pong")
	var receipt nativeReplyReceipt
	found, err := read(nativePongReceiptPath(d.NativeBrokerDir, "repair1"), &receipt)
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, receipt.Sent)
	assert.False(t, receipt.SentAt.IsZero())
	require.NoError(t, os.Remove(filepath.Join(d.StateDir, PongFile)))
	d.NativePongCurrent = func(string) bool { return false } // bus read may have cleared the challenge
	d.SendNativePongs(context.Background(), b, request.At.Add(2*time.Second), "stella", &cursor)
	entries, err = d.Store.Range(context.Background(), bus.StreamOf("stella"), "-", "+", 0)
	require.NoError(t, err)
	require.Len(t, entries, 1, "repair never sends a second bus pong")
	status, found, err := ReadPong(d.StateDir)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "repair1", status.Nonce)
	assert.Equal(t, receipt.SentAt, status.At)
}

func TestNativePongRejectsForgedCapabilityAndStaleChallengeWithoutLeak(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*NativePong, *Daemon)
	}{
		{"wrong capability", func(p *NativePong, _ *Daemon) { p.Capability = strings.Repeat("0", 64) }},
		{"wrong session", func(p *NativePong, _ *Daemon) { p.Session = "ses_elsewhere" }},
		{"stale nonce", func(_ *NativePong, d *Daemon) { d.NativePongCurrent = func(string) bool { return false } }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, _, d, b, request := nativeReplyRig(t)
			d.NativePongCurrent = func(nonce string) bool { return nonce == "abc123" }
			capability, err := d.GrantNativePong("ses_native", "abc123", "stella")
			require.NoError(t, err)
			p := NativePong{Friend: "freddy", Session: "ses_native", Nonce: "abc123", Capability: capability}
			tc.mutate(&p, d)
			require.NoError(t, QueueNativePong(dir, p))
			var logs []string
			d.Record = func(s string) { logs = append(logs, s) }
			var cursor string
			d.SendNativePongs(context.Background(), b, request.At.Add(time.Second), "stella", &cursor)
			entries, err := d.Store.Range(context.Background(), bus.StreamOf("stella"), "-", "+", 0)
			require.NoError(t, err)
			assert.Empty(t, entries)
			assert.NotContains(t, strings.Join(logs, "\n"), capability)
			_, err = os.Stat(filepath.Join(d.NativeBrokerDir, "native-pong-receipts", p.Nonce+".json"))
			assert.True(t, os.IsNotExist(err), "refusal has no send marker")
		})
	}
}

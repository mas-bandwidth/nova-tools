package notice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAntigravityTokenNeverReachesAnErrorOrANotice(t *testing.T) {
	t.Parallel()
	const token = "csrf-fake-token-7f3a"
	base := errors.New("agentapi refused")
	a := &Antigravity{Directory: "/private/events", Token: func() string { return token }, Deliver: func(context.Context, string, string) (string, error) {
		return "", errors.Join(base, errors.New("port 61101: "+token+" rejected"))
	}}
	n := Notice{ID: "n001", Path: "/private/events/n001.json"}
	_, err := a.Push(context.Background(), n)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), token)
	assert.ErrorIs(t, err, base, "redaction keeps what the error wraps")
	assert.NotContains(t, n.Text(), token)
}

func TestMailboxConfirmsOnlyThisNoticesID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "msg-001.json"), []byte("old"), 0o600))
	before := MailboxSnapshot(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "msg-002.json"), []byte("another sender"), 0o600))
	assert.Empty(t, MailboxConfirm(dir, before, "n007"), "a file another sender wrote never confirms")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "msg-003.json"), []byte("nova-hook saved event n007"), 0o600))
	assert.Equal(t, "msg-003.json", MailboxConfirm(dir, before, "n007"))
}

package notice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/dshdesktop"
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

func TestAntigravityPrunesResolvedNoticesAndKeepsPending(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := &Antigravity{
		Directory: dir,
		Deliver: func(_ context.Context, id, _ string) (string, error) {
			return "msg-" + id, nil
		},
	}
	for i := 1; i <= 30; i++ {
		id := fmt.Sprintf("notice-%04d", i)
		path := filepath.Join(dir, id+".json")
		require.NoError(t, os.WriteFile(path, []byte(`{"messages":0}`), 0o600))
		n := Notice{ID: id, Path: path}
		_, err := a.Push(context.Background(), n)
		require.NoError(t, err)
		require.NoError(t, a.Clear(context.Background(), n))
	}
	pendingID := "notice-0031"
	pendingPath := filepath.Join(dir, pendingID+".json")
	require.NoError(t, os.WriteFile(pendingPath, []byte(`{"messages":1}`), 0o600))
	_, err := a.Push(context.Background(), Notice{ID: pendingID, Path: pendingPath})
	require.NoError(t, err)

	files, err := dshdesktop.ListedNoticeFiles(dir)
	require.NoError(t, err)
	assert.Len(t, files, dshdesktop.MaxResolvedNotices+1)
	assert.Contains(t, files, "notice-0031.json")
	assert.NotContains(t, files, "notice-0001.json")
}

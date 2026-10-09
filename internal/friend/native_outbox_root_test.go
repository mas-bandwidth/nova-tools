package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreshOpenCodeSetupPreparesNativeOutboxWithoutAProviderTurn(t *testing.T) {
	t.Parallel()
	fs := NewMemFS()
	require.NoError(t, fs.MkdirAll("/friends/one", 0o700))
	opts := SetupOptions{Harness: "opencode", Dir: "/friends/one", Home: "/profiles/one",
		ConfigDir: "/profiles/one/opencode-extra", Program: "/opt/opencode/bin/opencode", ProfileOnly: true}
	plan, err := PlanOpenCodeSetup(opts, fs)
	require.NoError(t, err)
	require.Equal(t, "native-outbox", plan.Steps[0].Name)
	entry, err := fs.Lstat("/friends/one/outbox")
	require.NoError(t, err)
	assert.Equal(t, KindMissing, entry.Kind, "planning does not create the handoff")
	_, err = plan.Steps[0].Apply()
	require.NoError(t, err)
	entry, err = fs.Lstat("/friends/one/outbox")
	require.NoError(t, err)
	assert.Equal(t, KindDir, entry.Kind)
	assert.Equal(t, os.FileMode(0o700), entry.Mode.Perm())
	again, err := PlanOpenCodeSetup(opts, fs)
	require.NoError(t, err)
	assert.NotEqual(t, "native-outbox", again.Steps[0].Name, "existing real outbox is retained")
}

func TestFreshOpenCodeDaemonPreparesBothNativeHandoffsBeforeFirstTurn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := bustest.NewFake(time.Now(), "worker", "seat")
	d := &Daemon{Friend: "worker", Harness: "opencode", Dir: dir, NativeReplyStore: store,
		Store: store, Now: time.Now}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // no card, row or provider turn is needed to initialize the work root
	require.NoError(t, d.Run(ctx))
	info, err := os.Lstat(filepath.Join(dir, "outbox"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	capability := strings.Repeat("a", 64)
	require.NoError(t, QueueNativeReply(dir, NativeReply{Friend: "worker", Session: "ses_native", Re: "01M4F39QAT9N7621G6XKC3R9AA",
		Capability: capability, Subject: "reply", Body: "native answer"}))
	require.NoError(t, QueueNativePong(dir, NativePong{Friend: "worker", Session: "ses_native", Nonce: "abc123",
		Capability: capability, Width: 1}))
	assert.FileExists(t, nativeReplyPath(dir, "01M4F39QAT9N7621G6XKC3R9AA"))
	assert.FileExists(t, nativePongPath(dir, "abc123"))
}

func TestNativeOutboxBootstrapRefusesLinkedAndMalformedWorkPaths(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"outbox symlink", "outbox file", "work symlink"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			dir := filepath.Join(parent, "work")
			require.NoError(t, os.Mkdir(dir, 0o700))
			switch kind {
			case "outbox symlink":
				require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(dir, "outbox")))
			case "outbox file":
				require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox"), []byte("keep"), 0o600))
			case "work symlink":
				linked := filepath.Join(parent, "linked")
				require.NoError(t, os.Symlink(dir, linked))
				dir = linked
			}
			err := ensureNativeOutbox(OSFS{}, dir)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "keep")
			if kind == "outbox file" {
				raw, readErr := os.ReadFile(filepath.Join(dir, "outbox"))
				require.NoError(t, readErr)
				assert.Equal(t, "keep", string(raw))
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// The friends table's roster (docs/SPEC-SPRINT.md section 1) comes from the
// selected config store, with the same typed width and mode for a local file.
func TestFriendSyncReadsTheTypedRosterFromAStoreFile(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "existing")
	ta.ok("friend sync")
	path, _ := friendSyncStoreFile(t)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, ta.ok("friend sync --file "+path), "added=worker-a removed=existing")
	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(t, err)
	spec, err := st.FriendSpecOf(context.Background(), "worker-a")
	require.NoError(t, err)
	assert.Equal(t, 3, spec.Width)
	assert.Equal(t, "one-shot", spec.Mode)
	assert.Equal(t, map[string]string{"worker-a": "down"}, ta.friendStatus(), "sync adds a roster row without fabricating session evidence")
	assert.Contains(t, ta.ok("friend sync --file "+path), "nothing to do")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "sync reads the config file without rewriting it")
}

// A file source is reopened on every pass of the existing friend sync loop
// (docs/FRIENDS.md, "The friend sync loop"), never cached in the app.
func TestFriendSyncFileLoopRereadsTheRoster(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t)
	path, cfg := friendSyncStoreFile(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ta.a.notify = func(context.Context) (context.Context, context.CancelFunc) { return ctx, cancel }
	passes := 0
	ta.a.after = func(d time.Duration) <-chan time.Time {
		passes++
		if passes == 1 {
			_, _, err := cfg.Update(context.Background(), config.KindFriend, "worker-a", map[string]string{"width": "5", "mode": "batch"}, "test")
			require.NoError(t, err)
		} else {
			cancel()
		}
		fired := make(chan time.Time, 1)
		fired <- ta.a.now().Add(d)
		return fired
	}
	var out, errs bytes.Buffer
	code := ta.a.run([]string{"friend", "sync", "--file", path, "--every", "1s", "--root", t.TempDir()}, &out, &errs)
	require.Equal(t, 0, code, errs.String())
	assert.Empty(t, errs.String())
	assert.Contains(t, out.String(), "added=worker-a")
	assert.Contains(t, out.String(), "updated=worker-a")
	assert.Contains(t, out.String(), "STOP interrupted")
	st, err := ta.a.store(common{redis: "mem:0", actor: "coordinator"})
	require.NoError(t, err)
	spec, err := st.FriendSpecOf(context.Background(), "worker-a")
	require.NoError(t, err)
	assert.Equal(t, 5, spec.Width)
	assert.Equal(t, "batch", spec.Mode)
}

// Refused source selections and strict config-file decoding leave the existing
// friends table intact (docs/SPEC-SPRINT.md section 1).
func TestFriendSyncFileRefusalsPreserveTheRoster(t *testing.T) {
	t.Parallel()
	path, _ := friendSyncStoreFile(t)
	dir := t.TempDir()
	for _, tc := range []struct {
		name, args, body, says string
		code                   int
	}{
		{"exclusive", "--pg postgres://u@h/db --file " + path, "", "exclusive", 2},
		{"exclusive-empty-pg", "--pg= --file " + path, "", "exclusive", 2},
		{"empty-file", "--file=", "", "wants the path", 2},
		{"unknown-flag", "--files " + path, "", "flag", 2},
		{"missing", "--file " + filepath.Join(dir, "missing.json"), "", "not there yet", exitCannotRead},
		{"malformed", "", "{", "not a nova-config store file", exitCannotRead},
		{"unknown-field", "", `{"schema":1,"rows":{},"unexpected":true}`, "unknown field", exitCannotRead},
		{"unknown-kind", "", `{"schema":1,"rows":{"unknown":{}}}`, "does not know", exitCannotRead},
		{"empty-roster", "", `{"schema":1,"rows":{}}`, "holds no friend row", exitCannotRead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta, _ := friendApp(t, "existing")
			ta.ok("friend sync")
			args := tc.args
			if tc.body != "" {
				bad := filepath.Join(t.TempDir(), "config.json")
				require.NoError(t, os.WriteFile(bad, []byte(tc.body), 0o600))
				args = "--file " + bad
			}
			code, out, errs := ta.do("friend sync " + args)
			assert.Equal(t, tc.code, code, errs)
			assert.Empty(t, out)
			assert.Contains(t, errs, tc.says)
			assert.Equal(t, map[string]string{"existing": "down"}, ta.friendStatus())
		})
	}
}

func friendSyncStoreFile(t *testing.T) (string, *config.FileStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	st, err := config.OpenFile(path)
	require.NoError(t, err)
	_, _, _, err = st.Migrate(context.Background())
	require.NoError(t, err)
	_, err = st.Insert(context.Background(), config.KindFriend, config.Row{Name: "worker-a", Fields: map[string]string{"slots": "1", "tiers": "flash", "width": "3", "mode": "one-shot"}}, "test")
	require.NoError(t, err)
	return path, st
}

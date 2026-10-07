package friend

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestAProvenSessionSurvivesTheInstalledArguments(t *testing.T) {
	t.Parallel()
	a := Agent{Friend: "f", Harness: "codex", Dir: "/work", Session: "old", Home: t.TempDir(), Binary: "/bin/nova-friend", Redis: "store:1", Server: "sprint:2", Coordinator: "c", Width: 2}
	path := filepath.Join(t.TempDir(), "friend.plist")
	require.NoError(t, os.WriteFile(path, []byte(a.Plist()), 0o600))
	require.NoError(t, RenewSessionPlist(path, "new&session"))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	want := a.Args()
	for i, arg := range want {
		if arg == "--session" {
			want[i+1] = "new&session"
		}
	}
	assert.Equal(t, want, PlistArgs(string(raw)))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

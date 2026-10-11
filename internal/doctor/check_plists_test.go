package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorPlistsNamesAPlistPlutilRefuses(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "home", "Library", "LaunchAgents")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "com.nova.friend-stopgap.plist")
	require.NoError(t, os.WriteFile(path, []byte("<plist>bad &</plist>"), 0o644))

	fake := fakeEnv{env: map[string]string{"HOME": "home"}, root: root,
		exec: func(name string, _ ...string) (string, error) {
			if name == "plutil" {
				return path + ": Encountered unknown tag on line 1\n", errors.New("exit 1")
			}
			return "", errors.New("unexpected " + name)
		}}

	r := checkPlists(context.Background(), fake)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "unknown tag")
	assert.Contains(t, r.Fix, "remove a hand-written stopgap")

	fake.exec = func(string, ...string) (string, error) { return path + ": OK\n", nil }
	r = checkPlists(context.Background(), fake)
	assert.Equal(t, OK, r.Status)
}

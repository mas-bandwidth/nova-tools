package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorHarnessCheckNamesAFriendWhoseHarnessIsMissing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	friends := filepath.Join(root, "friends")
	friend := filepath.Join(friends, "ada")
	bin := filepath.Join(root, "bin")
	config := filepath.Join(root, "claude-config")
	for _, dir := range []string{friend, bin, config} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(friend, "beat"), []byte("FRIEND-BEAT OK ada row_harness=claude row_mode=one-shot row_config_dir="+config), 0o644))

	version := "claude 1.2.0"
	fake := fakeEnv{
		env:  map[string]string{"NOVA_FRIEND_HOME": friends, "PATH": bin},
		root: root,
		exec: func(_ string, _ ...string) (string, error) { return version, nil },
	}
	result := checkHarness(context.Background(), fake)
	assert.Equal(t, Fail, result.Status)
	assert.Contains(t, result.Evidence, "claude is not on PATH")
	assert.Contains(t, result.Fix, "nova-friend install")

	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte("fake"), 0o755))
	version = "claude unknown"
	result = checkHarness(context.Background(), fake)
	assert.Equal(t, Fail, result.Status)
	assert.Contains(t, result.Evidence, "supported version")

	version = "claude 1.2.0"
	result = checkHarness(context.Background(), fake)
	assert.Equal(t, OK, result.Status)
}

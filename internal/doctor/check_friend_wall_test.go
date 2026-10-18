package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoctorFriendWallNamesADirInsideTheDenySelf(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFriendPlist(t, root, "ada", "--as", "ada", "--harness", "claude",
		"--coordinator", "lead", "--dir", "home/ai/lead/working/ada")
	fake := friendEnv{goos: "darwin", fakeEnv: fakeEnv{env: map[string]string{"HOME": "home"}, root: root}}

	r := checkFriendWall(context.Background(), fake)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "home/ai/lead/working/ada")
	assert.Contains(t, r.Evidence, "home/ai/lead/working")
	assert.Contains(t, r.Fix, "sibling")
	assert.NotContains(t, r.Fix, "nova-friend install", "the friend daemons are retired: the remedy never reinstalls one")

	writeFriendPlist(t, root, "ada", "--as", "ada", "--harness", "claude",
		"--coordinator", "lead", "--dir", "home/ada-lane")
	r = checkFriendWall(context.Background(), fake)
	assert.Equal(t, OK, r.Status)

	// A --dir that is a symlink into the deny-self is caught: the paths are
	// resolved through symlinks before the containment test.
	real := t.TempDir()
	self := filepath.Join(real, "lead-working")
	require.NoError(t, os.MkdirAll(self, 0o755))
	link := filepath.Join(real, "ada-lane")
	require.NoError(t, os.Symlink(self, link))
	writeFriendPlist(t, root, "ada", "--as", "ada", "--harness", "claude",
		"--dir", link, "--deny-self", self)
	r = checkFriendWall(context.Background(), fake)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "inside the deny-self")
}

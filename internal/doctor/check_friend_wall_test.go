package doctor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDoctorFriendWallNamesADirInsideTheDenySelf(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFriendPlist(t, root, "ada", "--as", "ada", "--harness", "claude",
		"--coordinator", "lead", "--dir", "home/lead-working/ada")
	fake := fakeEnv{env: map[string]string{"HOME": "home"}, root: root}

	r := checkFriendWall(context.Background(), fake)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "home/lead-working/ada")
	assert.Contains(t, r.Evidence, "home/lead-working")
	assert.Contains(t, r.Fix, "sibling")

	writeFriendPlist(t, root, "ada", "--as", "ada", "--harness", "claude",
		"--coordinator", "lead", "--dir", "home/ada-lane")
	r = checkFriendWall(context.Background(), fake)
	assert.Equal(t, OK, r.Status)
}

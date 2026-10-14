package swarm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeMirror makes a fake bench mirror at dir: a directory holding a HEAD file and, when
// origin is not "", a config recording it as remote "origin".
func writeMirror(t *testing.T, dir, origin string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	if origin == "" {
		return
	}
	config := "[core]\n\tbare = true\n[remote \"origin\"]\n\turl = " + origin + "\n\tfetch = +refs/heads/*:refs/heads/*\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config"), []byte(config), 0o644))
}

// TestFindBenchMirrorKeysOnOwnerAndName holds that a bench mirror is the card's repository,
// not just a repository of the card's name: two owners' repositories of one name are told
// apart by the origin a mirror records, and a mirror of another owner's repository of the
// same name is refused by name rather than handed to the card (docs/SPEC-SWARM.md, the
// clone; tla/CardContract.tla, StagedFromOrigin).
func TestFindBenchMirrorKeysOnOwnerAndName(t *testing.T) {
	t.Parallel()

	t.Run("two owners of one name are told apart by the mirror's origin", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		flat := filepath.Join(home, "nova-bench", "mirror", "serialize.git")
		owner := filepath.Join(home, "nova-bench", "mirror", "owner-one", "serialize.git")
		writeMirror(t, flat, "https://example.com/owner-two/serialize.git")
		writeMirror(t, owner, "https://example.com/owner-one/serialize.git")

		got, err := FindBenchMirror(home, "https://example.com/owner-one/serialize.git")
		require.NoError(t, err)
		assert.Equal(t, owner, got, "the owner-keyed mirror is the card's")

		got, err = FindBenchMirror(home, "https://example.com/owner-two/serialize.git")
		require.NoError(t, err)
		assert.Equal(t, flat, got, "the flat mirror's recorded origin names the card's owner/name")
	})

	t.Run("only another owner's mirror of the name exists: staging refuses by name", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		flat := filepath.Join(home, "nova-bench", "mirror", "serialize.git")
		writeMirror(t, flat, "https://example.com/owner-two/serialize.git")

		got, err := FindBenchMirror(home, "owner-one/serialize")
		require.Error(t, err, "a mirror of another owner's repository of the same name is refused")
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "owner-two/serialize")
		assert.Contains(t, err.Error(), "owner-one/serialize")
	})

	t.Run("a mirror with no recorded origin is found by name as before", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		flat := filepath.Join(home, "nova-bench", "mirror", "serialize.git")
		writeMirror(t, flat, "")

		got, err := FindBenchMirror(home, "https://example.com/owner-one/serialize.git")
		require.NoError(t, err)
		assert.Equal(t, flat, got)
	})
}
